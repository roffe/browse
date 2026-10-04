//go:build !((linux && !android) || freebsd || netbsd || openbsd || windows || (darwin && !ios))

package browse

import (
	"errors"
	"testing"
)

func TestUnavailable(t *testing.T) {
	_, files := OpenFiles(Options{})
	_, file := OpenFile(Options{})
	_, save := SaveFile(Options{})
	_, folder := OpenFolder(Options{})
	for name, err := range map[string]error{"OpenFile": file, "OpenFiles": files, "SaveFile": save, "OpenFolder": folder} {
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s = %v, want ErrUnavailable", name, err)
		}
	}
}
