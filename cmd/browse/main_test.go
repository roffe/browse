package main

import (
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/roffe/browse"
)

func TestRun(t *testing.T) {
	real := dialogs
	t.Cleanup(func() { dialogs = real })

	// The dialogs are stood in for by one that records its options and
	// answers with what the test says.
	var asked browse.Options
	var paths []string
	var err error
	dialog := func(o browse.Options) ([]string, error) {
		asked = o
		return paths, err
	}
	dialogs = map[string]func(browse.Options) ([]string, error){"open": dialog, "files": dialog, "save": dialog}

	tests := []struct {
		name   string
		args   []string
		paths  []string
		err    error
		status int
		stdout string
		stderr string // has to appear on stderr
		asked  browse.Options
	}{
		{
			name:   "every flag",
			args:   []string{"-title", "Save log", "-dir", "logs", "-name", "a.csv", "-filter", "Logs:csv,log", "-filter", "All:*", "-parent", "0x1a2b", "save"},
			paths:  []string{"/logs/a.csv"},
			stdout: "/logs/a.csv\n",
			asked: browse.Options{
				Title: "Save log", Dir: "logs", Name: "a.csv", Parent: 0x1a2b,
				Filters: []browse.Filter{{Name: "Logs", Extensions: []string{"csv", "log"}}, {Name: "All", Extensions: []string{"*"}}},
			},
		},
		{name: "several files", args: []string{"files"}, paths: []string{"/a", "/b"}, stdout: "/a\n/b\n"},
		{name: "cancelled", args: []string{"open"}, err: browse.ErrCancelled, status: 1},
		{name: "failed", args: []string{"open"}, err: errors.New("no dialog today"), status: 2, stderr: "no dialog today"},
		{name: "unknown dialog", args: []string{"folder"}, status: 2, stderr: "usage:"},
		{name: "no dialog", status: 2, stderr: "usage:"},
		{name: "filter without extensions", args: []string{"-filter", "Logs", "open"}, status: 2, stderr: "name:ext,ext"},
		{name: "parent is not a number", args: []string{"-parent", "window", "open"}, status: 2, stderr: "usage:"},
		{name: "unknown flag", args: []string{"-colour", "red", "open"}, status: 2, stderr: "usage:"},
		{name: "help", args: []string{"-h"}, stderr: "usage:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asked, paths, err = browse.Options{}, tt.paths, tt.err
			var stdout, stderr strings.Builder
			if status := run(tt.args, &stdout, &stderr); status != tt.status {
				t.Errorf("status = %d, want %d", status, tt.status)
			}
			if stdout.String() != tt.stdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.stdout)
			}
			if !strings.Contains(stderr.String(), tt.stderr) || (tt.stderr == "" && stderr.Len() > 0) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.stderr)
			}
			if !reflect.DeepEqual(asked, tt.asked) {
				t.Errorf("dialog was asked for %+v, want %+v", asked, tt.asked)
			}
		})
	}
}

func TestOne(t *testing.T) {
	want := errors.New("nope")
	got, err := one(func(browse.Options) (string, error) { return "/a", want })(browse.Options{})
	if len(got) != 1 || got[0] != "/a" || err != want {
		t.Errorf("one = %q, %v", got, err)
	}
}

// TestDialogs runs the real dialogs where they can be relied on not to show
// up: on Linux without a session bus.
func TestDialogs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the dialogs would be shown")
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "missing"))
	for _, name := range []string{"open", "files", "save", "folder"} {
		var stdout, stderr strings.Builder
		if status := run([]string{name}, &stdout, &stderr); status != 2 || !strings.Contains(stderr.String(), browse.ErrUnavailable.Error()) {
			t.Errorf("%s: status = %d, stderr = %q, want the dialog to be unavailable", name, status, stderr.String())
		}
	}
}
