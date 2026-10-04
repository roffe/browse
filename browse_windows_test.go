package browse

import (
	"errors"
	"os"
	"path/filepath"
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
	start, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mode   mode
		file   string
		button uintptr
		want   string
	}{
		{"open", modeOpen, "in.bin", idOK, in},
		{"open multiple", modeOpenMultiple, "in.bin", idOK, in},
		{"save", modeSave, "out", idOK, filepath.Join(dir, "out.bin")},
		{"folder", modeFolder, "sub", idOK, sub},
		{"cancel", modeOpen, "", idCancel, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title := "browse test " + tt.name
			stop := make(chan struct{})
			seen := make(chan string, 1)
			go press(title, tt.button, stop, start, seen)
			got, err := show(tt.mode, Options{Title: title, Dir: dir, Name: tt.file, Filters: filters})
			close(stop)

			// The dialog shows dir, the process has to stay where it was.
			if during := <-seen; during != start {
				t.Errorf("working directory was %q while the dialog was open, want %q", during, start)
			}
			if after, _ := os.Getwd(); after != start {
				t.Errorf("working directory is %q after the dialog, want %q", after, start)
			}

			if tt.want == "" {
				if !errors.Is(err, ErrCancelled) {
					t.Fatalf("show = %q, %v, want ErrCancelled", got, err)
				}
				return
			}
			if err != nil || len(got) != 1 || !strings.EqualFold(got[0], tt.want) {
				t.Fatalf("show = %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}
