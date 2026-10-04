package browse_test

import (
	"errors"
	"fmt"

	"github.com/roffe/browse"
)

func ExampleOpenFile() {
	path, err := browse.OpenFile(browse.Options{
		Title: "Open firmware",
		Filters: []browse.Filter{
			{Name: "Firmware", Extensions: []string{"bin", "hex"}},
			{Name: "All files"},
		},
	})
	switch {
	case errors.Is(err, browse.ErrCancelled):
		return
	case errors.Is(err, browse.ErrUnavailable):
		// No native dialog on this system, ask for the path some other way.
		return
	case err != nil:
		fmt.Println(err)
		return
	}
	fmt.Println(path)
}

func ExampleSaveFile() {
	path, err := browse.SaveFile(browse.Options{
		Title:   "Save log",
		Dir:     "logs",
		Name:    "session.csv",
		Filters: []browse.Filter{{Name: "CSV", Extensions: []string{"csv"}}},
	})
	if err != nil {
		return
	}
	fmt.Println(path)
}
