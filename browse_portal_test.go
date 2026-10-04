//go:build (linux && !android) || freebsd || netbsd || openbsd

package browse

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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

// startBus runs a session bus of our own and makes it the one show connects to.
func startBus(t *testing.T) {
	t.Helper()
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon is not installed")
	}
	dir := t.TempDir()
	config := filepath.Join(dir, "bus.conf")
	err = os.WriteFile(config, []byte(`<busconfig>
	<type>session</type>
	<listen>unix:dir=`+dir+`</listen>
	<policy context="default">
		<allow send_destination="*" eavesdrop="true"/>
		<allow eavesdrop="true"/>
		<allow own="*"/>
	</policy>
</busconfig>`), 0o600)
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
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	address, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", strings.TrimSpace(address))
}

// fakePortal answers like the file chooser of the real portal: with a Response
// signal on the request object named after the caller and its token.
type fakePortal struct {
	conn *dbus.Conn
	code uint32
	uris []string

	method, parent, title string
	options               map[string]dbus.Variant
}

func (p *fakePortal) OpenFile(sender dbus.Sender, parent, title string, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	return p.respond("OpenFile", sender, parent, title, options)
}

func (p *fakePortal) SaveFile(sender dbus.Sender, parent, title string, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	return p.respond("SaveFile", sender, parent, title, options)
}

func (p *fakePortal) respond(method string, sender dbus.Sender, parent, title string, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	p.method, p.parent, p.title, p.options = method, parent, title, options
	name := strings.ReplaceAll(strings.TrimPrefix(string(sender), ":"), ".", "_")
	request := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + name + "/" + options["handle_token"].Value().(string))
	// Answering before the call has returned is the worst case for the caller.
	results := map[string]dbus.Variant{"uris": dbus.MakeVariant(p.uris)}
	if err := p.conn.Emit(request, "org.freedesktop.portal.Request.Response", p.code, results); err != nil {
		return "", dbus.MakeFailedError(err)
	}
	return request, nil
}

func startPortal(t *testing.T) *fakePortal {
	t.Helper()
	startBus(t)
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	p := &fakePortal{conn: conn}
	if err := conn.Export(p, portalPath, "org.freedesktop.portal.FileChooser"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.RequestName(portalName, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPortal(t *testing.T) {
	p := startPortal(t)
	filters := []Filter{{Name: "Binary", Extensions: []string{"bin"}}, {Name: "All"}}

	// sent returns the option the portal was given, nil if it was left out.
	sent := func(key string) any {
		if v, ok := p.options[key]; ok {
			return v.Value()
		}
		return nil
	}

	t.Run("open", func(t *testing.T) {
		p.uris = []string{"file:///tmp/a%20b/in.bin"}
		got, err := OpenFile(Options{Title: "Pick", Dir: "/tmp", Filters: filters, Parent: 0x1a2b})
		if err != nil || got != "/tmp/a b/in.bin" {
			t.Fatalf("OpenFile = %q, %v", got, err)
		}
		if p.method != "OpenFile" || p.parent != "x11:1a2b" || p.title != "Pick" {
			t.Errorf("portal got %s(%q, %q)", p.method, p.parent, p.title)
		}
		if sig := dbus.SignatureOf(portalFilters(filters)).String(); sig != "a(sa(us))" {
			t.Errorf("filters are sent as %q, the portal wants a(sa(us))", sig)
		}
		var filtersSent []portalFilter
		if err := p.options["filters"].Store(&filtersSent); err != nil {
			t.Fatal(err)
		}
		if len(filtersSent) != 2 || filtersSent[0].Name != "Binary" || filtersSent[0].Rules[0] != (portalRule{0, "*.[bB][iI][nN]"}) || filtersSent[1].Rules[0] != (portalRule{0, "*"}) {
			t.Errorf("filters = %+v", filtersSent)
		}
		if folder, _ := sent("current_folder").([]byte); string(folder) != "/tmp\x00" {
			t.Errorf("current_folder = %q", folder)
		}
		if sent("multiple") != nil || sent("directory") != nil {
			t.Errorf("multiple = %v, directory = %v, want neither", sent("multiple"), sent("directory"))
		}
	})

	t.Run("files", func(t *testing.T) {
		p.uris = []string{"file:///a.bin", "file:///b.bin"}
		got, err := OpenFiles(Options{})
		if err != nil || !slices.Equal(got, []string{"/a.bin", "/b.bin"}) {
			t.Fatalf("OpenFiles = %q, %v", got, err)
		}
		if sent("multiple") != true || sent("filters") != nil || p.parent != "" {
			t.Errorf("multiple = %v, filters = %v, parent = %q", sent("multiple"), sent("filters"), p.parent)
		}
	})

	t.Run("save", func(t *testing.T) {
		p.uris = []string{"file:///tmp/out.bin"}
		got, err := SaveFile(Options{Name: "out.bin", Filters: filters})
		if err != nil || got != "/tmp/out.bin" || p.method != "SaveFile" {
			t.Fatalf("SaveFile = %q, %v via %s", got, err, p.method)
		}
		if sent("current_name") != "out.bin" || sent("filters") == nil {
			t.Errorf("current_name = %v, filters = %v", sent("current_name"), sent("filters"))
		}
	})

	t.Run("folder", func(t *testing.T) {
		p.uris = []string{"file:///tmp"}
		got, err := OpenFolder(Options{Name: "ignored", Filters: filters})
		if err != nil || got != "/tmp" || p.method != "OpenFile" {
			t.Fatalf("OpenFolder = %q, %v via %s", got, err, p.method)
		}
		if sent("directory") != true || sent("filters") != nil || sent("current_name") != nil {
			t.Errorf("directory = %v, filters = %v, current_name = %v", sent("directory"), sent("filters"), sent("current_name"))
		}
	})

	t.Run("cancel", func(t *testing.T) {
		p.code, p.uris = 1, nil
		if got, err := OpenFile(Options{}); !errors.Is(err, ErrCancelled) {
			t.Fatalf("OpenFile = %q, %v, want ErrCancelled", got, err)
		}
	})
}

func TestPortalMissing(t *testing.T) {
	startBus(t)
	if got, err := OpenFile(Options{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("OpenFile = %q, %v, want ErrUnavailable", got, err)
	}
}
