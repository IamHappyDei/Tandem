package pick

import (
	"errors"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	shell32         = syscall.NewLazyDLL("shell32.dll")
	ole32           = syscall.NewLazyDLL("ole32.dll")
	user32          = syscall.NewLazyDLL("user32.dll")
	pBrowse         = shell32.NewProc("SHBrowseForFolderW")
	pPathFromID     = shell32.NewProc("SHGetPathFromIDListW")
	pCoTaskMemFree  = ole32.NewProc("CoTaskMemFree")
	pCoInitializeEx = ole32.NewProc("CoInitializeEx")
	pCoUninitialize = ole32.NewProc("CoUninitialize")
	pGetActiveWindw = user32.NewProc("GetActiveWindow")
)

const (
	bifReturnFSOrDOS  = 0x00001004
	bifNewDialogStyle = 0x00000040
	bifValidatePosFS  = 0x00000100
	bifEditBox        = 0x00000010
	bifStatusText     = 0x00000001
	maxPath           = 260
)

type browseInfo struct {
	hwndOwner      uintptr
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

func Folder(title string) (string, error) {
	if title == "" {
		title = `Pick the Community folder your aircraft live in`
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	pCoInitializeEx.Call(0, 2)
	defer pCoUninitialize.Call()

	hwnd, _, _ := pGetActiveWindw.Call()
	var nameBuf [maxPath]uint16
	info := browseInfo{
		hwndOwner:      hwnd,
		pszDisplayName: &nameBuf[0],
		lpszTitle:      syscall.StringToUTF16Ptr(title),
		ulFlags:        bifReturnFSOrDOS | bifNewDialogStyle | bifValidatePosFS | bifEditBox | bifStatusText,
	}
	pidl, _, _ := pBrowse.Call(uintptr(unsafe.Pointer(&info)))
	if pidl == 0 {
		return "", errors.New("no folder was picked")
	}
	defer pCoTaskMemFree.Call(pidl)

	var out [maxPath]uint16
	ok, _, _ := pPathFromID.Call(pidl, uintptr(unsafe.Pointer(&out[0])))
	if ok == 0 {
		return "", errors.New("that was not a folder on a disk")
	}
	return syscall.UTF16ToString(out[:]), nil
}
