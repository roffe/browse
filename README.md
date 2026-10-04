# browse

Native file dialogs for Go. Open a file, open several, save a file or pick a
folder, each time with the dialog the operating system itself provides.

```go
path, err := browse.OpenFile(browse.Options{
	Title: "Open firmware",
	Filters: []browse.Filter{
		{Name: "Firmware", Extensions: []string{"bin", "hex"}},
		{Name: "All files"},
	},
})
if errors.Is(err, browse.ErrCancelled) {
	return
}
```

## What it uses

| Platform | Dialog | cgo |
|---|---|---|
| Windows | `IFileOpenDialog` / `IFileSaveDialog` | no |
| macOS | `NSOpenPanel` / `NSSavePanel` | yes |
| Linux, FreeBSD, NetBSD, OpenBSD | XDG Desktop Portal over D-Bus, which shows the dialog of the running desktop (GNOME, KDE, ...) | no |

Nothing is linked against GTK or Qt, and no helper programs such as `zenity`,
`kdialog` or `osascript` are run.

## Functions

```go
browse.OpenFile(o)   // one existing file
browse.OpenFiles(o)  // one or more existing files
browse.SaveFile(o)   // where to save, overwriting is confirmed by the dialog
browse.OpenFolder(o) // an existing directory
```

All of them take the same `Options`, and every field is optional:

| Field | Meaning |
|---|---|
| `Title` | What the user is asked to choose. |
| `Dir` | Directory the dialog starts in. |
| `Name` | File name suggested by `SaveFile`. |
| `Filters` | File types on offer. Extensions match in any letter case. A filter without extensions matches every file. |
| `Parent` | Native handle of the window that owns the dialog. |

Two errors are worth checking for:

- `ErrCancelled`: the user closed the dialog without choosing.
- `ErrUnavailable`: the system has no native dialog, for example a Linux
  session without a portal. Fall back to a dialog of your own.

## Threading

Every call blocks until the dialog is closed and can be made from any
goroutine. In a GUI program, call from a goroutine other than the one running
the event loop: the dialog then runs alongside the loop and your windows keep
painting.

On macOS a program without an event loop, such as a command line tool, has to
call from the main thread. Lock it in `init`:

```go
func init() { runtime.LockOSThread() }
```

## Parent windows

Set `Options.Parent` to attach the dialog to one of your windows. It then stays
on top of that window and blocks input to it. On macOS it is shown as a sheet.

| Platform | `Parent` is |
|---|---|
| Windows | the `HWND` |
| macOS | the `NSWindow` pointer |
| Linux and the BSDs | the X11 window ID |

Wayland windows can not be used as a parent yet, leave `Parent` zero there.

## Platform differences

- **Extension when saving.** Windows and macOS add the extension of the
  selected filter to a name typed without one. The portal dialogs on Linux
  return the name exactly as typed.
- **Filter selector.** macOS has none, so the dialog accepts files matching any
  of the filters.
- **Folders on old portals.** `OpenFolder` needs version 3 of the file chooser
  portal (xdg-desktop-portal 1.5.2 or newer).

## Command line

`cmd/browse` wraps the package, which is handy for scripts and for trying the
dialogs out:

```sh
go run github.com/roffe/browse/cmd/browse@latest -title "Pick a log" -filter "Logs:log,csv" open
```

It prints the chosen paths, one per line, and exits with status 1 when the
dialog is cancelled.

## Status

- **Linux:** tested against a stand-in portal in `go test`, and the calls are
  accepted by the real xdg-desktop-portal.
- **Windows:** the tests open real dialogs and operate them; so far they have
  only been run under Wine.
- **macOS:** not yet built or run on a Mac.
