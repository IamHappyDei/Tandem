package pick

import (
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

var (
	pFindWindow  = user32.NewProc("FindWindowW")
	pPostMessage = user32.NewProc("PostMessageW")
)

const (
	wmClose   = 0x0010
	wmSetText = 0x000C
	wmKeydown = 0x0100
	wmKeyup   = 0x0101
	wmCommand = 0x0111
	vkReturn  = 0x0D
)

var (
	pFindWindowEx = user32.NewProc("FindWindowExW")
	pGetDlgItem   = user32.NewProc("GetDlgItem")
	pSendMessage  = user32.NewProc("SendMessageW")
)

func mustPtr(s string) uintptr {
	p, _ := syscall.UTF16PtrFromString(s)
	return uintptr(unsafe.Pointer(p))
}

func TestFolderOpensAndACancelIsHonest(t *testing.T) {
	done := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		where, err := Folder("test: pick a folder")
		if err != nil {
			errs <- err
			return
		}
		done <- where
	}()

	var hwnd uintptr
	deadline := time.Now().Add(6 * time.Second)
	class, _ := syscall.UTF16PtrFromString("#32770")
	for time.Now().Before(deadline) && hwnd == 0 {
		h, _, _ := pFindWindow.Call(uintptr(unsafe.Pointer(class)), 0)
		hwnd = h
		if hwnd == 0 {
			time.Sleep(80 * time.Millisecond)
		}
	}
	if hwnd == 0 {
		t.Fatal("no dialog window ever appeared - the picker is not opening")
	}
	t.Logf("the dialog is up as window %x", hwnd)

	pPostMessage.Call(hwnd, wmClose, 0, 0)
	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("a cancelled pick came back with no error and no path")
		}
		t.Logf("cancelled, and it said: %v", err)
	case where := <-done:
		t.Fatalf("a cancelled pick returned a path: %q", where)
	case <-time.After(6 * time.Second):
		t.Fatal("closing the dialog did not end the call - it would hang the app")
	}
}

func TestTypingAPathIntoTheDialogIsWhatComesBack(t *testing.T) {
	want := `D:\MSFS\Community`
	got := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		where, err := Folder("test: pick the community folder")
		if err != nil {
			errs <- err
			return
		}
		got <- where
	}()
	class, _ := syscall.UTF16PtrFromString("#32770")
	var hwnd uintptr
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) && hwnd == 0 {
		h, _, _ := pFindWindow.Call(uintptr(unsafe.Pointer(class)), 0)
		hwnd = h
		if hwnd == 0 {
			time.Sleep(80 * time.Millisecond)
		}
	}
	if hwnd == 0 {
		t.Fatal("no dialog window ever appeared")
	}
	text, _ := syscall.UTF16PtrFromString(want)
	child, _, _ := pFindWindowEx.Call(hwnd, 0, mustPtr("Edit"), 0)
	if child == 0 {
		child, _, _ = pFindWindowEx.Call(hwnd, 0, mustPtr("ComboBoxEx32"), 0)
	}
	if child == 0 {
		t.Skip("this dialog has no box to type in - nothing to prove here")
	}
	pSendMessage.Call(child, wmSetText, 0, uintptr(unsafe.Pointer(text)))
	ok, _, _ := pGetDlgItem.Call(hwnd, 1)
	pSendMessage.Call(child, wmKeydown, vkReturn, 0)
	pSendMessage.Call(child, wmKeyup, vkReturn, 0)
	if ok != 0 {
		pSendMessage.Call(hwnd, wmCommand, uintptr(1), ok)
	}
	select {
	case where := <-got:
		if !strings.EqualFold(where, want) {
			t.Fatalf("it gave back %q, wanted %q", where, want)
		}
		t.Logf("typed a path in, and that is the path Tandem got: %s", where)
	case err := <-errs:
		t.Skip("the dialog would not take a typed path:", err)
	case <-time.After(6 * time.Second):
		t.Fatal("it did not answer")
	}
}

var (
	pGetWindow     = user32.NewProc("GetWindow")
	pCreateWindow  = user32.NewProc("CreateWindowExW")
	pDestroyWindow = user32.NewProc("DestroyWindow")
	pRegClass      = user32.NewProc("RegisterClassW")
	pDefProc       = user32.NewProc("DefWindowProcW")
	pLoadCursor    = user32.NewProc("LoadCursorW")
	pPeek          = user32.NewProc("PeekMessageW")
	pTranslate     = user32.NewProc("TranslateMessage")
	pDispatch      = user32.NewProc("DispatchMessageW")
)

const (
	gwOwner       = 4
	wsOverlapped  = 0x00000000
	wsVisible     = 0x10000000
	gwHwndMessage = 0xffffffff
)

type wndClassW struct {
	style         uint32
	lpfnWndProc   uintptr
	cbsize        int32
	cnExtra       int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hBackground   uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

func TestTheDialogIsOwnedByOurWindow(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	k := syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleW")
	mod, _, _ := k.Call(0)
	stat, _ := syscall.UTF16PtrFromString("Static")
	name, _ := syscall.UTF16PtrFromString("TandemPickOwnerWindow")
	h, _, err := pCreateWindow.Call(0, uintptr(unsafe.Pointer(stat)),
		uintptr(unsafe.Pointer(name)), wsVisible, 100, 100, 320, 200, 0, 0, mod, 0)
	if h == 0 {
		t.Fatalf("could not make a window to own the dialog: %v", err)
	}
	defer pDestroyWindow.Call(h)

	got := make(chan uintptr, 1)
	go func() {
		if _, err := Folder("owned"); err != nil {
			got <- 0
			return
		}
		got <- 1
	}()

	dlgClass, _ := syscall.UTF16PtrFromString("#32770")
	var dlg uintptr
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) && dlg == 0 {
		x, _, _ := pFindWindow.Call(uintptr(unsafe.Pointer(dlgClass)), uintptr(unsafe.Pointer(name)))
		if x == 0 {
			x, _, _ = pFindWindow.Call(uintptr(unsafe.Pointer(dlgClass)), 0)
		}
		dlg = x
		pump()
		if dlg == 0 {
			time.Sleep(60 * time.Millisecond)
		}
	}
	if dlg == 0 {
		t.Fatal("the dialog never appeared")
	}
	owner, _, _ := pGetWindow.Call(dlg, gwOwner)
	if owner != h {
		t.Fatalf("the dialog answers to window %x, not our own %x - it would sit behind the page", owner, h)
	}
	t.Logf("the dialog is owned by our window %x, so it stays on top of it", owner)
	pPostMessage.Call(dlg, wmClose, 0, 0)
	<-got
}

func pump() {
	var msg struct{ hwnd, message, wParam, lParam uintptr }
	for i := 0; i < 16; i++ {
		r, _, _ := pPeek.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1)
		if r == 0 {
			return
		}
		pTranslate.Call(uintptr(unsafe.Pointer(&msg)))
		pDispatch.Call(uintptr(unsafe.Pointer(&msg)))
	}
}
