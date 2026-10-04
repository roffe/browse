package browse

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	shell32              = windows.NewLazySystemDLL("shell32.dll")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procSHCreateItem     = shell32.NewProc("SHCreateItemFromParsingName")

	clsidFileOpenDialog = windows.GUID{Data1: 0xdc1c5a9c, Data2: 0xe88a, Data3: 0x4dde, Data4: [8]byte{0xa5, 0xa1, 0x60, 0xf8, 0x2a, 0x20, 0xae, 0xf7}}
	clsidFileSaveDialog = windows.GUID{Data1: 0xc0b4e2f3, Data2: 0xba21, Data3: 0x4773, Data4: [8]byte{0x8d, 0xba, 0x33, 0x5e, 0xc9, 0x46, 0xeb, 0x8b}}
	iidFileOpenDialog   = windows.GUID{Data1: 0xd57c7288, Data2: 0xd4ad, Data3: 0x4768, Data4: [8]byte{0xbe, 0x02, 0x9d, 0x96, 0x95, 0x32, 0xd9, 0x60}}
	iidFileSaveDialog   = windows.GUID{Data1: 0x84bccd23, Data2: 0x5fde, Data3: 0x4cdb, Data4: [8]byte{0xae, 0xa4, 0xaf, 0x64, 0xb8, 0x3d, 0x78, 0xab}}
	iidShellItem        = windows.GUID{Data1: 0x43826d1e, Data2: 0xe718, Data3: 0x42ee, Data4: [8]byte{0xbc, 0x55, 0xa1, 0xe2, 0x61, 0xc3, 0x7b, 0xfe}}
)

const (
	clsctxInprocServer = 0x1
	sigdnFileSysPath   = 0x80058000

	fosNoChangeDir      = 0x8
	fosPickFolders      = 0x20
	fosForceFileSystem  = 0x40
	fosAllowMultiSelect = 0x200

	sFalse          = syscall.Errno(1)
	rpcEChangedMode = syscall.Errno(0x80010106)
	errorCancelled  = syscall.Errno(0x800704c7) // HRESULT_FROM_WIN32(ERROR_CANCELLED)
)

// Slots in the virtual method tables of the interfaces used.
const (
	// IUnknown
	vtRelease = 2
	// IModalWindow
	vtShow = 3
	// IFileDialog
	vtSetFileTypes        = 4
	vtSetOptions          = 9
	vtGetOptions          = 10
	vtSetFolder           = 12
	vtSetFileName         = 15
	vtSetTitle            = 17
	vtGetResult           = 20
	vtSetDefaultExtension = 22
	// IFileOpenDialog
	vtGetResults = 27
	// IShellItem
	vtGetDisplayName = 5
	// IShellItemArray
	vtGetCount  = 7
	vtGetItemAt = 8
)

// object is a COM interface pointer.
type object struct {
	vtbl *[32]uintptr
}

// call invokes the method in the given slot of the virtual method table.
//
//go:uintptrescapes
func (o *object) call(method int, args ...uintptr) error {
	r, _, _ := syscall.SyscallN(o.vtbl[method], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return hresult(r)
}

func (o *object) release() {
	o.call(vtRelease)
}

// setString calls a method taking a single string, unless s is empty.
func (o *object) setString(method int, s string) error {
	if s == "" {
		return nil
	}
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return err
	}
	return o.call(method, uintptr(unsafe.Pointer(p)))
}

func hresult(r uintptr) error {
	if int32(r) < 0 {
		return syscall.Errno(uint32(r))
	}
	return nil
}

// comInit puts the calling thread in a single threaded apartment, which the
// dialogs need, and returns the function leaving it again.
func comInit() (func(), error) {
	runtime.LockOSThread()
	switch err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err {
	case nil, sFalse:
		return func() {
			windows.CoUninitialize()
			runtime.UnlockOSThread()
		}, nil
	case rpcEChangedMode:
		// Somebody made the thread multi threaded, that is not ours to undo.
		return runtime.UnlockOSThread, nil
	default:
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
}

func show(m mode, o Options) ([]string, error) {
	done, err := comInit()
	if err != nil {
		return nil, err
	}
	defer done()

	d, err := newDialog(m, o)
	if err != nil {
		return nil, err
	}
	defer d.release()

	if err := d.call(vtShow, o.Parent); err != nil {
		if err == errorCancelled {
			return nil, ErrCancelled
		}
		return nil, fmt.Errorf("browse: %w", err)
	}
	paths, err := d.results(m)
	if err != nil {
		return nil, fmt.Errorf("browse: %w", err)
	}
	return paths, nil
}

// newDialog creates the dialog and applies the options to it.
func newDialog(m mode, o Options) (*object, error) {
	clsid, iid := &clsidFileOpenDialog, &iidFileOpenDialog
	if m == modeSave {
		clsid, iid = &clsidFileSaveDialog, &iidFileSaveDialog
	}
	var d *object
	r, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(clsid)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&d)),
	)
	if err := hresult(r); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if err := d.configure(m, o); err != nil {
		d.release()
		return nil, fmt.Errorf("browse: %w", err)
	}
	return d, nil
}

func (d *object) configure(m mode, o Options) error {
	var flags uint32
	if err := d.call(vtGetOptions, uintptr(unsafe.Pointer(&flags))); err != nil {
		return err
	}
	flags |= fosForceFileSystem | fosNoChangeDir
	switch m {
	case modeOpenMultiple:
		flags |= fosAllowMultiSelect
	case modeFolder:
		flags |= fosPickFolders
	}
	if err := d.call(vtSetOptions, uintptr(flags)); err != nil {
		return err
	}
	if err := d.setString(vtSetTitle, o.Title); err != nil {
		return err
	}
	if err := d.setString(vtSetFileName, o.Name); err != nil {
		return err
	}
	if err := d.setFilters(m, o.Filters); err != nil {
		return err
	}
	d.setFolder(o.Dir)
	return nil
}

// filterSpec is a COMDLG_FILTERSPEC.
type filterSpec struct {
	name, spec *uint16
}

func (d *object) setFilters(m mode, filters []Filter) error {
	if len(filters) == 0 {
		return nil
	}
	specs := make([]filterSpec, len(filters))
	for i, f := range filters {
		spec := "*.*"
		if exts := f.extensions(); exts != nil {
			spec = "*." + strings.Join(exts, ";*.")
		}
		var err error
		if specs[i].name, err = windows.UTF16PtrFromString(f.label()); err != nil {
			return err
		}
		if specs[i].spec, err = windows.UTF16PtrFromString(spec); err != nil {
			return err
		}
	}
	if err := d.call(vtSetFileTypes, uintptr(len(specs)), uintptr(unsafe.Pointer(&specs[0]))); err != nil {
		return err
	}
	// With a default extension set the dialog appends the extension of the
	// selected file type to names typed without one.
	if exts := filters[0].extensions(); m == modeSave && exts != nil {
		return d.setString(vtSetDefaultExtension, exts[0])
	}
	return nil
}

// setFolder makes the dialog start in dir. A directory that can not be used is
// ignored, the dialog then starts where the system wants it to.
func (d *object) setFolder(dir string) {
	if dir == "" {
		return
	}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return
	}
	var item *object
	r, _, _ := procSHCreateItem.Call(
		uintptr(unsafe.Pointer(p)), 0,
		uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&item)),
	)
	if hresult(r) != nil {
		return
	}
	d.call(vtSetFolder, uintptr(unsafe.Pointer(item)))
	item.release()
}

func (d *object) results(m mode) ([]string, error) {
	if m != modeOpenMultiple {
		var item *object
		if err := d.call(vtGetResult, uintptr(unsafe.Pointer(&item))); err != nil {
			return nil, err
		}
		defer item.release()
		path, err := item.path()
		if err != nil {
			return nil, err
		}
		return []string{path}, nil
	}

	var items *object
	if err := d.call(vtGetResults, uintptr(unsafe.Pointer(&items))); err != nil {
		return nil, err
	}
	defer items.release()
	var count uint32
	if err := items.call(vtGetCount, uintptr(unsafe.Pointer(&count))); err != nil {
		return nil, err
	}
	paths := make([]string, 0, count)
	for i := range count {
		var item *object
		if err := items.call(vtGetItemAt, uintptr(i), uintptr(unsafe.Pointer(&item))); err != nil {
			return nil, err
		}
		path, err := item.path()
		item.release()
		if err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// path returns the file system path of an IShellItem.
func (item *object) path() (string, error) {
	var p *uint16
	if err := item.call(vtGetDisplayName, sigdnFileSysPath, uintptr(unsafe.Pointer(&p))); err != nil {
		return "", err
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(p))
	return windows.UTF16PtrToString(p), nil
}
