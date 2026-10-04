//go:build (linux && !android) || freebsd || netbsd || openbsd

package browse

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/godbus/dbus/v5"
)

const (
	portalName     = "org.freedesktop.portal.Desktop"
	portalPath     = "/org/freedesktop/portal/desktop"
	portalChooser  = "org.freedesktop.portal.FileChooser."
	portalRequest  = "org.freedesktop.portal.Request"
	portalResponse = portalRequest + ".Response"

	// Every dialog gets its own bus connection, so the token never has to
	// tell two requests of one connection apart.
	portalToken = "browse"
)

// portalFilter is one entry of the "filters" option, D-Bus type (sa(us)).
type portalFilter struct {
	Name  string
	Rules []portalRule
}

type portalRule struct {
	Type    uint32 // 0 for a glob pattern, 1 for a MIME type
	Pattern string
}

func show(m mode, o Options) ([]string, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer conn.Close()

	// The portal answers with a Response signal on a request object. Its path
	// is made up of our bus name and token, so subscribe before calling and
	// the answer can not be missed.
	sender := strings.ReplaceAll(strings.TrimPrefix(conn.Names()[0], ":"), ".", "_")
	request := dbus.ObjectPath(portalPath + "/request/" + sender + "/" + portalToken)
	watch := func(path dbus.ObjectPath) error {
		return conn.AddMatchSignal(
			dbus.WithMatchObjectPath(path),
			dbus.WithMatchInterface(portalRequest),
			dbus.WithMatchMember("Response"),
		)
	}
	if err := watch(request); err != nil {
		return nil, fmt.Errorf("browse: %w", err)
	}
	signals := make(chan *dbus.Signal, 4)
	conn.Signal(signals)

	method := "OpenFile"
	options := map[string]dbus.Variant{"handle_token": dbus.MakeVariant(portalToken)}
	switch m {
	case modeOpenMultiple:
		options["multiple"] = dbus.MakeVariant(true)
	case modeFolder:
		options["directory"] = dbus.MakeVariant(true)
	case modeSave:
		method = "SaveFile"
		if o.Name != "" {
			options["current_name"] = dbus.MakeVariant(o.Name)
		}
	}
	if o.Dir != "" {
		options["current_folder"] = dbus.MakeVariant(append([]byte(o.Dir), 0))
	}
	if len(o.Filters) > 0 {
		options["filters"] = dbus.MakeVariant(portalFilters(o.Filters))
	}
	parent := ""
	if o.Parent != 0 {
		parent = fmt.Sprintf("x11:%x", o.Parent)
	}

	var handle dbus.ObjectPath
	call := conn.Object(portalName, portalPath).Call(portalChooser+method, 0, parent, o.Title, options)
	if err := call.Store(&handle); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if handle != request {
		// Portals older than 0.9 choose the path themselves.
		if err := watch(handle); err != nil {
			return nil, fmt.Errorf("browse: %w", err)
		}
	}

	for sig := range signals {
		if sig.Path != handle || sig.Name != portalResponse {
			continue
		}
		var code uint32
		var results map[string]dbus.Variant
		if err := dbus.Store(sig.Body, &code, &results); err != nil {
			return nil, fmt.Errorf("browse: portal response: %w", err)
		}
		switch code {
		case 0:
			return portalPaths(results)
		case 1:
			return nil, ErrCancelled
		}
		return nil, errors.New("browse: the portal ended the dialog")
	}
	return nil, errors.New("browse: lost the session bus")
}

func portalPaths(results map[string]dbus.Variant) ([]string, error) {
	uris, ok := results["uris"].Value().([]string)
	if !ok {
		return nil, errors.New("browse: portal response has no list of files")
	}
	paths := make([]string, 0, len(uris))
	for _, uri := range uris {
		u, err := url.Parse(uri)
		if err != nil || u.Scheme != "file" {
			return nil, fmt.Errorf("browse: portal returned %q, not a local file", uri)
		}
		paths = append(paths, u.Path)
	}
	return paths, nil
}

func portalFilters(filters []Filter) []portalFilter {
	out := make([]portalFilter, len(filters))
	for i, f := range filters {
		out[i].Name = f.label()
		exts := f.extensions()
		if exts == nil {
			out[i].Rules = []portalRule{{Pattern: "*"}}
			continue
		}
		for _, ext := range exts {
			out[i].Rules = append(out[i].Rules, portalRule{Pattern: glob(ext)})
		}
	}
	return out
}

// glob returns the pattern matching ext in any letter case, "bin" becomes
// "*.[bB][iI][nN]". Portal backends match patterns case sensitively.
func glob(ext string) string {
	var b strings.Builder
	b.WriteString("*.")
	for _, r := range ext {
		if lo, up := unicode.ToLower(r), unicode.ToUpper(r); lo != up {
			b.WriteString("[" + string(lo) + string(up) + "]")
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
