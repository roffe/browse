// Package browse shows the file dialogs of the operating system.
//
// Windows uses the IFileDialog COM interfaces, macOS uses NSOpenPanel and
// NSSavePanel, and Linux and the BSDs use the XDG Desktop Portal over D-Bus,
// which shows the dialog of the desktop the user is running. Nothing is linked
// against GTK or Qt and no helper programs are run.
//
// Every function blocks until the dialog is closed and may be called from any
// goroutine. Programs with a GUI event loop should call them from another
// goroutine than the one running the loop, the dialog then runs alongside it.
// On macOS a program without an event loop has to call from the main thread.
package browse

import (
	"errors"
	"path/filepath"
	"strings"
)

var (
	// ErrCancelled is returned when the user closes the dialog without
	// choosing anything.
	ErrCancelled = errors.New("browse: cancelled")

	// ErrUnavailable is returned, wrapped with the reason, when the system has
	// no native dialog to show. On Linux this means that no XDG Desktop Portal
	// is running in the session.
	ErrUnavailable = errors.New("browse: no native file dialog available")
)

// Filter is a named group of file types the user can limit the dialog to.
type Filter struct {
	// Name is shown to the user, for example "Images".
	Name string
	// Extensions are the file extensions in the group, with or without a
	// leading dot. Case is ignored. A filter without extensions, or with the
	// extension "*", matches every file.
	Extensions []string
}

// Options configures a dialog. The zero value shows every file and lets the
// system pick the title and starting directory.
type Options struct {
	// Title tells the user what they are choosing.
	Title string
	// Dir is the directory the dialog starts in.
	Dir string
	// Name is the file name suggested by SaveFile.
	Name string
	// Filters are the file types on offer, the first one is selected. macOS
	// has no type selector and accepts files matching any of them.
	Filters []Filter
	// Parent is the native handle of the window the dialog belongs to: an HWND
	// on Windows, an NSWindow pointer on macOS and an X11 window ID on Linux
	// and the BSDs. The dialog stays on top of that window and blocks input to
	// it. Zero shows a free-standing dialog.
	Parent uintptr
}

// OpenFile asks the user for an existing file.
func OpenFile(o Options) (string, error) {
	return first(run(modeOpen, o))
}

// OpenFiles asks the user for one or more existing files.
func OpenFiles(o Options) ([]string, error) {
	return run(modeOpenMultiple, o)
}

// SaveFile asks the user where to save a file. Overwriting an existing file is
// confirmed by the dialog.
//
// Windows and macOS add the first extension of the selected filter to a name
// typed without one. The portal dialogs on Linux return the name as typed.
func SaveFile(o Options) (string, error) {
	return first(run(modeSave, o))
}

// OpenFolder asks the user for an existing directory.
func OpenFolder(o Options) (string, error) {
	return first(run(modeFolder, o))
}

type mode int

const (
	modeOpen mode = iota
	modeOpenMultiple
	modeSave
	modeFolder
)

// run shows the dialog of the platform, which is implemented by show. It
// returns at least one path when the error is nil.
func run(m mode, o Options) ([]string, error) {
	if o.Dir != "" {
		if dir, err := filepath.Abs(o.Dir); err == nil {
			o.Dir = dir
		}
	}
	if m != modeSave {
		o.Name = ""
	}
	if m == modeFolder {
		o.Filters = nil
	}
	paths, err := show(m, o)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, ErrCancelled
	}
	return paths, nil
}

func first(paths []string, err error) (string, error) {
	if err != nil {
		return "", err
	}
	return paths[0], nil
}

// extensions returns the extensions of the filter without leading "*." or ".",
// or nil when the filter matches every file.
func (f Filter) extensions() []string {
	var exts []string
	for _, ext := range f.Extensions {
		ext = strings.TrimPrefix(strings.TrimPrefix(ext, "*"), ".")
		if ext == "" || ext == "*" {
			return nil
		}
		exts = append(exts, ext)
	}
	return exts
}

// label returns the name shown for the filter, falling back to its patterns.
func (f Filter) label() string {
	if f.Name != "" {
		return f.Name
	}
	if exts := f.extensions(); exts != nil {
		return "*." + strings.Join(exts, ", *.")
	}
	return "*"
}
