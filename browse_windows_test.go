package browse

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32          = windows.NewLazySystemDLL("user32.dll")
	procFindWindow  = user32.NewProc("FindWindowW")
	procPostMessage = user32.NewProc("PostMessageW")
)

const (
	wmCommand = 0x0111
	idOK      = 1
	idCancel  = 2
)

// press pushes a button of the dialog with the given title until told to stop.
// It gives up and cancels the dialog after ten seconds. When stopped it sends
// the working directory the process had while the dialog was open, which is
// start unless the dialog moved it.
func press(title string, button uintptr, stop <-chan struct{}, start string, seen chan<- string) {
	name, _ := windows.UTF16PtrFromString(title)
	wd := ""
	for i := 0; ; i++ {
		select {
		case <-stop:
			seen <- wd
			return
		case <-time.After(100 * time.Millisecond):
		}
		if i > 100 {
			button = idCancel
		}
		if hwnd, _, _ := procFindWindow.Call(0, uintptr(unsafe.Pointer(name))); hwnd != 0 {
			if now, _ := os.Getwd(); wd == "" || now != start {
				wd = now
			}
			procPostMessage.Call(hwnd, wmCommand, button, 0)
		}
	}
}

// operate runs call, which has to show a dialog with the given title, and
// pushes one of its buttons. The test fails if the dialog moves the working
// directory of the process, or leaves the calling thread in a COM apartment.
func operate(t *testing.T, title string, button uintptr, call func() ([]string, error)) ([]string, error) {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	start, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	seen := make(chan string, 1)
	go press(title, button, stop, start, seen)
	got, err := call()
	close(stop)

	if during := <-seen; during != start {
		t.Errorf("working directory was %q while the dialog was open, want %q", during, start)
	}
	if after, _ := os.Getwd(); after != start {
		t.Errorf("working directory is %q after the dialog, want %q", after, start)
	}
	// A thread outside any apartment can enter the multi threaded one.
	if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil {
		t.Errorf("the dialog left the calling thread in a COM apartment: %v", err)
	} else {
		windows.CoUninitialize()
	}
	return got, err
}

// list gives the result of a function returning one path the shape of one
// returning several.
func list(path string, err error) ([]string, error) {
	return []string{path}, err
}

// onThread runs f on an operating system thread of its own. The thread is
// thrown away afterwards, so what f does to its COM state stays there.
func onThread(f func() error) error {
	done := make(chan error)
	go func() {
		runtime.LockOSThread() // never unlocked, the thread ends with the goroutine
		done <- f()
	}()
	return <-done
}

// TestShow opens real dialogs and operates them through their window.
func TestShow(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.bin")
	if err := os.WriteFile(in, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	filters := []Filter{{Name: "Binary", Extensions: []string{"bin"}}, {Name: "All"}}
	// The dialogs are told what to choose through the file name, which the
	// exported functions only pass on when saving, hence the calls to show.
	options := func(title, name string) Options {
		return Options{Title: title, Dir: dir, Name: name, Filters: filters}
	}

	tests := []struct {
		name   string
		call   func(title string) ([]string, error)
		button uintptr
		want   string // empty when the dialog is cancelled
	}{
		{"open", func(title string) ([]string, error) {
			return show(modeOpen, options(title, "in.bin"))
		}, idOK, in},
		{"open multiple", func(title string) ([]string, error) {
			return show(modeOpenMultiple, options(title, "in.bin"))
		}, idOK, in},
		{"save", func(title string) ([]string, error) {
			return list(SaveFile(options(title, "out")))
		}, idOK, filepath.Join(dir, "out.bin")},
		{"folder", func(title string) ([]string, error) {
			return show(modeFolder, options(title, "sub"))
		}, idOK, sub},
		{"cancel OpenFile", func(title string) ([]string, error) {
			return list(OpenFile(Options{Title: title}))
		}, idCancel, ""},
		{"cancel OpenFiles", func(title string) ([]string, error) {
			return OpenFiles(Options{Title: title})
		}, idCancel, ""},
		{"cancel SaveFile", func(title string) ([]string, error) {
			return list(SaveFile(Options{Title: title}))
		}, idCancel, ""},
		{"cancel OpenFolder", func(title string) ([]string, error) {
			return list(OpenFolder(Options{Title: title, Filters: filters}))
		}, idCancel, ""},
		{"start in a missing directory", func(title string) ([]string, error) {
			return show(modeOpen, Options{Title: title, Dir: filepath.Join(dir, "missing")})
		}, idCancel, ""},
		{"start in an unusable directory", func(title string) ([]string, error) {
			return show(modeOpen, Options{Title: title, Dir: "a\x00b"})
		}, idCancel, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title := "browse test " + tt.name
			got, err := operate(t, title, tt.button, func() ([]string, error) { return tt.call(title) })
			if tt.want == "" {
				if !errors.Is(err, ErrCancelled) {
					t.Fatalf("got %q, %v, want ErrCancelled", got, err)
				}
				return
			}
			if err != nil || len(got) != 1 || !strings.EqualFold(got[0], tt.want) {
				t.Fatalf("got %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}

// TestShowRejects gives the dialog text Windows can not take. No dialog may be
// shown, the test would wait for it forever.
func TestShowRejects(t *testing.T) {
	for name, o := range map[string]Options{
		"title":       {Title: "a\x00b"},
		"file name":   {Name: "a\x00b"},
		"filter name": {Filters: []Filter{{Name: "a\x00b", Extensions: []string{"bin"}}}},
		"extension":   {Filters: []Filter{{Name: "Binary", Extensions: []string{"a\x00b"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := show(modeSave, o)
			if err == nil || errors.Is(err, ErrCancelled) || errors.Is(err, ErrUnavailable) {
				t.Fatalf("show = %q, %v, want an error", got, err)
			}
		})
	}
}

// TestComInit enters the apartment from threads that are already in one.
func TestComInit(t *testing.T) {
	for name, model := range map[string]uint32{
		"single threaded": windows.COINIT_APARTMENTTHREADED,
		"multi threaded":  windows.COINIT_MULTITHREADED,
	} {
		t.Run(name, func(t *testing.T) {
			err := onThread(func() error {
				if err := windows.CoInitializeEx(0, model); err != nil {
					return fmt.Errorf("entering the apartment first: %w", err)
				}
				defer windows.CoUninitialize()
				leave, err := comInit()
				if err != nil {
					return fmt.Errorf("comInit: %w", err)
				}
				leave()
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNewDialogWithoutCOM(t *testing.T) {
	err := onThread(func() error {
		_, err := newDialog(modeOpen, Options{})
		return err
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("newDialog = %v, want ErrUnavailable", err)
	}
}

// TestDialogErrors asks a dialog for things it has to refuse.
func TestDialogErrors(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	leave, err := comInit()
	if err != nil {
		t.Fatal(err)
	}
	defer leave()
	filters := []Filter{{Name: "Binary", Extensions: []string{"bin"}}}
	d, err := newDialog(modeOpen, Options{Filters: filters})
	if err != nil {
		t.Fatal(err)
	}
	defer d.release()

	if err := d.setFilters(modeOpen, filters); err == nil {
		t.Error("the file types were set a second time")
	}
	for _, m := range []mode{modeOpen, modeOpenMultiple} {
		if got, err := d.results(m); err == nil {
			t.Errorf("results(%d) = %q from a dialog that was never shown", m, got)
		}
	}

	// "This PC" is a shell item without a place in the file system.
	name, _ := windows.UTF16PtrFromString("::{20D04FE0-3AEA-1069-A2D8-08002B30309D}")
	var item *object
	r, _, _ := procSHCreateItem.Call(
		uintptr(unsafe.Pointer(name)), 0,
		uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&item)),
	)
	if err := hresult(r); err != nil {
		t.Fatal(err)
	}
	defer item.release()
	if got, err := item.path(); err == nil {
		t.Errorf("path = %q for an item outside the file system", got)
	}
}
