// Command browse shows a native file dialog and prints the chosen paths, one
// per line. It exits with status 1 when the dialog is cancelled.
//
//	browse [flags] open|files|save|folder
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
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

// dialogs are the dialogs by the name they have on the command line.
var dialogs = map[string]func(browse.Options) ([]string, error){
	"open":   one(browse.OpenFile),
	"files":  browse.OpenFiles,
	"save":   one(browse.SaveFile),
	"folder": one(browse.OpenFolder),
}

// one gives a dialog returning a single path the shape of one returning
// several.
func one(dialog func(browse.Options) (string, error)) func(browse.Options) ([]string, error) {
	return func(o browse.Options) ([]string, error) {
		path, err := dialog(o)
		return []string{path}, err
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the program: it returns the exit status.
func run(args []string, stdout, stderr io.Writer) int {
	var o browse.Options
	flags := flag.NewFlagSet("browse", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&o.Title, "title", "", "dialog title")
	flags.StringVar(&o.Dir, "dir", "", "directory to start in")
	flags.StringVar(&o.Name, "name", "", "file name suggested when saving")
	flags.Func("filter", "file type as `name:ext,ext`, may be repeated", func(s string) error {
		name, exts, ok := strings.Cut(s, ":")
		if !ok {
			return errors.New("want name:ext,ext")
		}
		o.Filters = append(o.Filters, browse.Filter{Name: name, Extensions: strings.Split(exts, ",")})
		return nil
	})
	flags.Func("parent", "native `handle` of the window owning the dialog", func(s string) error {
		_, err := fmt.Sscan(s, &o.Parent)
		return err
	})
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: browse [flags] open|files|save|folder")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	dialog, ok := dialogs[flags.Arg(0)]
	if !ok {
		flags.Usage()
		return 2
	}

	paths, err := dialog(o)
	if errors.Is(err, browse.ErrCancelled) {
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	for _, p := range paths {
		fmt.Fprintln(stdout, p)
	}
	return 0
}
