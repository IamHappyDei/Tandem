//go:build windows

package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const windowTitle = "Tandem"

const setupTitle = "Tandem setup"

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	pFindWindow      = user32.NewProc("FindWindowW")
	pShowWindow      = user32.NewProc("ShowWindow")
	pSetForeground   = user32.NewProc("SetForegroundWindow")
	pIsWindowVisible = user32.NewProc("IsWindowVisible")
	pPostClose       = user32.NewProc("PostMessageW")
)

const (
	swHide    = 0
	swShow    = 5
	swRestore = 9
	wmClose   = 0x0010
)

type Window struct {
	url     string
	title   string
	profile string
	hidden  bool

	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan struct{}
}

func NewWindow(url string) *Window { return &Window{url: url} }

func NewWindowAs(title, url string) *Window {
	return &Window{url: url, title: title, profile: strings.ToLower(strings.ReplaceAll(title, " ", "-"))}
}

func (w *Window) titleOf() string {
	if w.title != "" {
		return w.title
	}
	return windowTitle
}

func (w *Window) Gone() bool { return w.find() == 0 }

func (w *Window) Show() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd == nil {
		cmd, err := launchApp(w.url, w.titleOf(), w.profile)
		if err != nil {
			return err
		}
		w.cmd = cmd
		w.done = make(chan struct{})
		w.hidden = false
		go func(c *exec.Cmd, done chan struct{}) {
			_ = c.Wait()
			w.mu.Lock()
			if w.cmd == c {
				w.cmd, w.hidden = nil, false
			}
			w.mu.Unlock()
			close(done)
		}(cmd, w.done)

		go w.waitAndFocus(8 * time.Second)
		return nil
	}
	hwnd := w.find()
	if hwnd == 0 {
		return nil
	}
	show := uintptr(swRestore)
	if w.hidden {
		show = swShow
	}
	pShowWindow.Call(hwnd, show)
	pSetForeground.Call(hwnd)
	w.hidden = false
	return nil
}

func (w *Window) Hide() {
	w.mu.Lock()
	defer w.mu.Unlock()
	hwnd := w.find()
	if hwnd == 0 {
		return
	}
	pShowWindow.Call(hwnd, uintptr(swHide))
	w.hidden = true
}

func (w *Window) Hidden() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.hidden
}

func (w *Window) Up() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cmd != nil
}

func (w *Window) Close() {
	w.mu.Lock()
	cmd := w.cmd
	w.cmd = nil
	w.mu.Unlock()
	if hwnd := w.find(); hwnd != 0 {
		pPostClose.Call(hwnd, wmClose, 0, 0)
		time.Sleep(150 * time.Millisecond)
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (w *Window) waitAndFocus(d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if hwnd := w.find(); hwnd != 0 {
			pSetForeground.Call(hwnd)
			return
		}
		time.Sleep(120 * time.Millisecond)
	}
}

func (w *Window) find() uintptr { return findWindowByTitle(w.titleOf()) }

func FocusOther() bool {
	hwnd := findWindowByTitle(windowTitle)
	if hwnd == 0 {
		return false
	}
	pShowWindow.Call(hwnd, uintptr(swRestore))
	pSetForeground.Call(hwnd)
	return true
}

func findWindowByTitle(title string) uintptr {
	t, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return 0
	}
	h, _, _ := pFindWindow.Call(0, uintptr(unsafe.Pointer(t)))
	if h != 0 {
		return h
	}
	return findWindowBySweep(title)
}

var (
	pGetWindowText = user32.NewProc("GetWindowTextW")
	pEnumWindows   = user32.NewProc("EnumWindows")
)

func findWindowBySweep(title string) uintptr {
	var found uintptr
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		buf := make([]uint16, 512)
		n, _, _ := pGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n == 0 {
			return 1
		}

		t := syscall.UTF16ToString(buf[:n])
		if t == title || strings.HasPrefix(t, title+" —") || strings.HasPrefix(t, title+" -") || strings.HasPrefix(t, title+" ·") {
			found = hwnd
			return 0
		}
		return 1
	})
	pEnumWindows.Call(cb, 0)
	return found
}

func launchApp(url, name, profile string) (*exec.Cmd, error) {
	exe, err := appBrowser()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Tandem", "ui")
	if profile != "" {
		dir = filepath.Join(os.Getenv("LOCALAPPDATA"), "Tandem", "ui-"+profile)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	args := []string{
		"--app=" + url,
		"--user-data-dir=" + dir,
		"--window-name=" + name,
		"--window-size=1240,860",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-sync",
		"--disable-features=Translate,MediaRouter,msEdgeShoppingUI",
		"--metrics-recording-only",
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000010 | 0x08000000,
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cannot start the window: %w", err)
	}
	return cmd, nil
}

func appBrowser() (string, error) {
	candidates := []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Edge", "Application", "msedge.exe"),
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", errors.New("no Edge or Chrome found to draw the window - use --browser to open the dashboard in whatever you do have")
}

var (
	kernel32    = syscall.NewLazyDLL("kernel32.dll")
	pGetConsole = kernel32.NewProc("GetConsoleWindow")
)

func HideConsole() {
	h, _, _ := pGetConsole.Call()
	if h != 0 {
		pShowWindow.Call(h, uintptr(swHide))
	}
}

func KeepConsole() {
	h, _, _ := pGetConsole.Call()
	if h != 0 {
		pShowWindow.Call(h, uintptr(swShow))
	}
}

func WindowState() string {
	hwnd := findWindowByTitle(windowTitle)
	if hwnd == 0 {
		return "none"
	}
	if v, _, _ := pIsWindowVisible.Call(hwnd); v == 0 {
		return "hidden"
	}
	return "open"
}

func WindowShow() bool {
	hwnd := findWindowByTitle(windowTitle)
	if hwnd == 0 {
		return false
	}
	pShowWindow.Call(hwnd, uintptr(swRestore))
	pSetForeground.Call(hwnd)
	return true
}

func WindowHide() bool {
	hwnd := findWindowByTitle(windowTitle)
	if hwnd == 0 {
		return false
	}
	pShowWindow.Call(hwnd, uintptr(swHide))
	return true
}
