package clip

import (
	"errors"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	pOpenClipboard   = user32.NewProc("OpenClipboard")
	pCloseClipboard  = user32.NewProc("CloseClipboard")
	pGetClipboard    = user32.NewProc("GetClipboardData")
	pGlobalLock      = kernel32.NewProc("GlobalLock")
	pGlobalUnlock    = kernel32.NewProc("GlobalUnlock")
	cfUnicodeText    = 13
	clipboardRetries = 8
)

func Text() (string, error) {
	for i := 0; i < clipboardRetries; i++ {
		r, _, err := pOpenClipboard.Call(0)
		if r != 0 {
			defer pCloseClipboard.Call()
			h, _, _ := pGetClipboard.Call(uintptr(cfUnicodeText))
			if h == 0 {
				return "", errors.New("the clipboard holds no text")
			}
			p, _, _ := pGlobalLock.Call(h)
			if p == 0 {
				return "", errors.New("the clipboard could not be read")
			}
			s := syscall.UTF16ToString((*[1 << 20]uint16)(unsafe.Pointer(p))[:])
			pGlobalUnlock.Call(h)
			return trimSpace(s), nil
		}
		if i == 0 && err != nil {
			_ = err
		}
		time.Sleep(40 * time.Millisecond)
	}
	return "", errors.New("something else is holding the clipboard - try again")
}

func trimSpace(s string) string {
	out := []rune(s)
	for len(out) > 0 && (out[0] == ' ' || out[0] == '\t' || out[0] == '\r' || out[0] == '\n') {
		out = out[1:]
	}
	for len(out) > 0 && (out[len(out)-1] == ' ' || out[len(out)-1] == '\t' || out[len(out)-1] == '\r' || out[len(out)-1] == '\n' || out[len(out)-1] == 0) {
		out = out[:len(out)-1]
	}
	return string(out)
}

var _ = runtime.GOOS
