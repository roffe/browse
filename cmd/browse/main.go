// Command browse shows a native file dialog and prints the chosen paths, one
// per line. It exits with status 1 when the dialog is cancelled.
//
//	browse [flags] open|files|save|folder
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/roffe/browse"
)

// There is no event loop here, so on macOS the dialog has to run on the main
// thread.
func init() {
	runtime.LockOSThread()
}

func main() {
	var o browse.Options
	flag.StringVar(&o.Title, "title", "", "dialog title")
	flag.StringVar(&o.Dir, "dir", "", "directory to start in")
	flag.StringVar(&o.Name, "name", "", "file name suggested when saving")
	flag.Func("filter", "file type as `name:ext,ext`, may be repeated", func(s string) error {
		name, exts, ok := strings.Cut(s, ":")
		if !ok {
			return errors.New("want name:ext,ext")
		}
		o.Filters = append(o.Filters, browse.Filter{Name: name, Extensions: strings.Split(exts, ",")})
		return nil
	})
	flag.Func("parent", "native `handle` of the window owning the dialog", func(s string) error {
		_, err := fmt.Sscan(s, &o.Parent)
		return err
	})
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: browse [flags] open|files|save|folder")
		flag.PrintDefaults()
	}
	flag.Parse()

	var paths []string
	var path string
	var err error
	switch flag.Arg(0) {
	case "open":
		path, err = browse.OpenFile(o)
	case "files":
		paths, err = browse.OpenFiles(o)
	case "save":
		path, err = browse.SaveFile(o)
	case "folder":
		path, err = browse.OpenFolder(o)
	default:
		flag.Usage()
		os.Exit(2)
	}
	if errors.Is(err, browse.ErrCancelled) {
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if path != "" {
		paths = []string{path}
	}
	for _, p := range paths {
		fmt.Println(p)
	}
}
