//go:build (linux && !android) || freebsd || netbsd || openbsd

package browse

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestGlob(t *testing.T) {
	for ext, want := range map[string]string{
		"bin":    "*.[bB][iI][nN]",
		"s19":    "*.[sS]19",
		"tar.gz": "*.[tT][aA][rR].[gG][zZ]",
	} {
		if got := glob(ext); got != want {
			t.Errorf("glob(%q) = %q, want %q", ext, got, want)
		}
	}
}

// startBus runs a session bus of our own and makes it the one show connects
// to. A connection may subscribe to matchRules signals. The bus is shut down
// by the function returned, or else when the test ends.
func startBus(t *testing.T, matchRules int) (stop func()) {
	t.Helper()
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon is not installed")
	}
	dir := t.TempDir()
	config := filepath.Join(dir, "bus.conf")
	err = os.WriteFile(config, fmt.Appendf(nil, `<busconfig>
	<type>session</type>
	<listen>unix:dir=%s</listen>
	<policy context="default">
		<allow send_destination="*" eavesdrop="true"/>
		<allow eavesdrop="true"/>
		<allow own="*"/>
	</policy>
	<limit name="max_match_rules_per_connection">%d</limit>
</busconfig>`, dir, matchRules), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(daemon, "--config-file="+config, "--nofork", "--print-address")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stop = func() {
		cmd.Process.Kill()
		cmd.Wait()
	}
	t.Cleanup(stop)
	address, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", strings.TrimSpace(address))
	return stop
}

// answer is what the fake portal does with a dialog.
type answer struct {
	code   uint32
	uris   []string
	body   []any // sent in place of code and uris when set
	legacy bool  // answer on a path of its own, as portals before 0.9 did
	silent bool  // do not answer at all
}

// question is what the fake portal was asked.
type question struct {
	method, parent, title string
	options               map[string]dbus.Variant
}

// option returns an option of the question, nil if it was left out.
func (q question) option(key string) any {
	if v, ok := q.options[key]; ok {
		return v.Value()
	}
	return nil
}

// fakePortal answers like the file chooser of the real portal: with a Response
// signal on the request object named after the caller and its token.
type fakePortal struct {
	conn    *dbus.Conn
	stopBus func()
	called  chan struct{} // gets a value whenever a dialog is asked for

	mu     sync.Mutex
	answer answer
	asked  question
}

// willAnswer sets what dialogs are answered with from now on.
func (p *fakePortal) willAnswer(a answer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answer = a
}

// lastAsked returns what the most recent dialog asked for.
func (p *fakePortal) lastAsked() question {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.asked
}

func (p *fakePortal) OpenFile(sender dbus.Sender, parent, title string, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	return p.respond("OpenFile", sender, parent, title, options)
}

func (p *fakePortal) SaveFile(sender dbus.Sender, parent, title string, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	return p.respond("SaveFile", sender, parent, title, options)
}

func (p *fakePortal) respond(method string, sender dbus.Sender, parent, title string, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	p.mu.Lock()
	p.asked = question{method, parent, title, options}
	a := p.answer
	p.mu.Unlock()
	select {
	case p.called <- struct{}{}:
	default:
	}

	name := strings.ReplaceAll(strings.TrimPrefix(string(sender), ":"), ".", "_")
	request := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + name + "/" + options["handle_token"].Value().(string))
	body := []any{a.code, map[string]dbus.Variant{"uris": dbus.MakeVariant(a.uris)}}
	if a.body != nil {
		body = a.body
	}
	switch {
	case a.silent:
	case a.legacy:
		legacy := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/legacy")
		go func() {
			// The caller only learns the path from our reply, give it time
			// to start listening there.
			time.Sleep(200 * time.Millisecond)
			// A response on the path the caller expected is not ours and has
			// to be ignored.
			p.conn.Emit(request, portalResponse, uint32(1), map[string]dbus.Variant{})
			p.conn.Emit(legacy, portalResponse, body...)
		}()
		return legacy, nil
	default:
		// Answering before the call has returned is the worst case for the caller.
		if err := p.conn.Emit(request, portalResponse, body...); err != nil {
			return "", dbus.MakeFailedError(err)
		}
	}
	return request, nil
}

// startPortal runs a fake portal on a session bus of its own.
func startPortal(t *testing.T, matchRules int) *fakePortal {
	t.Helper()
	stop := startBus(t, matchRules)
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	p := &fakePortal{conn: conn, stopBus: stop, called: make(chan struct{}, 1)}
	if err := conn.Export(p, portalPath, "org.freedesktop.portal.FileChooser"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.RequestName(portalName, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	return p
}

// failed reports whether err is an error other than the two the package
// defines.
func failed(err error) bool {
	return err != nil && !errors.Is(err, ErrCancelled) && !errors.Is(err, ErrUnavailable)
}

func TestPortal(t *testing.T) {
	p := startPortal(t, 100)
	filters := []Filter{{Name: "Binary", Extensions: []string{"bin"}}, {Name: "All"}}

	t.Run("open", func(t *testing.T) {
		p.willAnswer(answer{uris: []string{"file:///tmp/a%20b/in.bin"}})
		got, err := OpenFile(Options{Title: "Pick", Dir: "/tmp", Filters: filters, Parent: 0x1a2b})
		if err != nil || got != "/tmp/a b/in.bin" {
			t.Fatalf("OpenFile = %q, %v", got, err)
		}
		q := p.lastAsked()
		if q.method != "OpenFile" || q.parent != "x11:1a2b" || q.title != "Pick" {
			t.Errorf("portal got %s(%q, %q)", q.method, q.parent, q.title)
		}
		if sig := dbus.SignatureOf(portalFilters(filters)).String(); sig != "a(sa(us))" {
			t.Errorf("filters are sent as %q, the portal wants a(sa(us))", sig)
		}
		var sent []portalFilter
		if err := q.options["filters"].Store(&sent); err != nil {
			t.Fatal(err)
		}
		if len(sent) != 2 || sent[0].Name != "Binary" || sent[0].Rules[0] != (portalRule{0, "*.[bB][iI][nN]"}) || sent[1].Rules[0] != (portalRule{0, "*"}) {
			t.Errorf("filters = %+v", sent)
		}
		if folder, _ := q.option("current_folder").([]byte); string(folder) != "/tmp\x00" {
			t.Errorf("current_folder = %q", folder)
		}
		if q.option("multiple") != nil || q.option("directory") != nil {
			t.Errorf("multiple = %v, directory = %v, want neither", q.option("multiple"), q.option("directory"))
		}
	})

	t.Run("files", func(t *testing.T) {
		p.willAnswer(answer{uris: []string{"file:///a.bin", "file:///b.bin"}})
		got, err := OpenFiles(Options{})
		if err != nil || !slices.Equal(got, []string{"/a.bin", "/b.bin"}) {
			t.Fatalf("OpenFiles = %q, %v", got, err)
		}
		q := p.lastAsked()
		if q.option("multiple") != true || q.option("filters") != nil || q.parent != "" {
			t.Errorf("multiple = %v, filters = %v, parent = %q", q.option("multiple"), q.option("filters"), q.parent)
		}
	})

	t.Run("save", func(t *testing.T) {
		p.willAnswer(answer{uris: []string{"file:///tmp/out.bin"}})
		got, err := SaveFile(Options{Name: "out.bin", Filters: filters})
		q := p.lastAsked()
		if err != nil || got != "/tmp/out.bin" || q.method != "SaveFile" {
			t.Fatalf("SaveFile = %q, %v via %s", got, err, q.method)
		}
		if q.option("current_name") != "out.bin" || q.option("filters") == nil {
			t.Errorf("current_name = %v, filters = %v", q.option("current_name"), q.option("filters"))
		}
	})

	t.Run("folder", func(t *testing.T) {
		p.willAnswer(answer{uris: []string{"file:///tmp"}})
		got, err := OpenFolder(Options{Name: "ignored", Filters: filters})
		q := p.lastAsked()
		if err != nil || got != "/tmp" || q.method != "OpenFile" {
			t.Fatalf("OpenFolder = %q, %v via %s", got, err, q.method)
		}
		if q.option("directory") != true || q.option("filters") != nil || q.option("current_name") != nil {
			t.Errorf("directory = %v, filters = %v, current_name = %v", q.option("directory"), q.option("filters"), q.option("current_name"))
		}
	})

	t.Run("answer on a path of its own", func(t *testing.T) {
		p.willAnswer(answer{legacy: true, uris: []string{"file:///a.bin"}})
		if got, err := OpenFile(Options{}); err != nil || got != "/a.bin" {
			t.Fatalf("OpenFile = %q, %v", got, err)
		}
	})

	for name, a := range map[string]answer{
		"cancel":         {code: 1},
		"nothing chosen": {uris: []string{}},
	} {
		t.Run(name, func(t *testing.T) {
			p.willAnswer(a)
			if got, err := OpenFile(Options{}); !errors.Is(err, ErrCancelled) {
				t.Fatalf("OpenFile = %q, %v, want ErrCancelled", got, err)
			}
		})
	}

	for name, a := range map[string]answer{
		"dialog ended by the portal": {code: 2},
		"not a local file":           {uris: []string{"https://example.com/a.bin"}},
		"no uris":                    {body: []any{uint32(0), map[string]dbus.Variant{}}},
		"uris mistyped":              {body: []any{uint32(0), map[string]dbus.Variant{"uris": dbus.MakeVariant(7)}}},
		"body mistyped":              {body: []any{"zero", "nothing"}},
	} {
		t.Run(name, func(t *testing.T) {
			p.willAnswer(a)
			if got, err := OpenFile(Options{}); !failed(err) {
				t.Fatalf("OpenFile = %q, %v, want an error", got, err)
			}
		})
	}
}

// TestPortalLost takes the session bus away while a dialog is open.
func TestPortalLost(t *testing.T) {
	p := startPortal(t, 100)
	p.willAnswer(answer{silent: true})
	result := make(chan error, 1)
	go func() {
		_, err := OpenFile(Options{})
		result <- err
	}()
	<-p.called
	time.Sleep(200 * time.Millisecond) // let the dialog start waiting for its answer
	p.stopBus()
	select {
	case err := <-result:
		if err == nil || errors.Is(err, ErrCancelled) {
			t.Fatalf("OpenFile = %v, want an error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("OpenFile is still waiting for a bus that is gone")
	}
}

// TestPortalNoSignals runs on a bus that lets a connection subscribe to too
// few signals to hear the answer.
func TestPortalNoSignals(t *testing.T) {
	for name, tt := range map[string]struct {
		matchRules int
		answer     answer
	}{
		"none":                        {0, answer{}},
		"not the one the portal uses": {1, answer{legacy: true}},
	} {
		t.Run(name, func(t *testing.T) {
			p := startPortal(t, tt.matchRules)
			p.willAnswer(tt.answer)
			if got, err := OpenFile(Options{}); !failed(err) {
				t.Fatalf("OpenFile = %q, %v, want an error", got, err)
			}
		})
	}
}

func TestPortalMissing(t *testing.T) {
	startBus(t, 100)
	if got, err := OpenFile(Options{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("OpenFile = %q, %v, want ErrUnavailable", got, err)
	}
}

func TestNoSessionBus(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "missing"))
	if got, err := OpenFile(Options{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("OpenFile = %q, %v, want ErrUnavailable", got, err)
	}
}
