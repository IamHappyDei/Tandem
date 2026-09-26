//go:build !windows

package ui

func HideConsole()        {}
func KeepConsole()        {}
func FocusOther() bool    { return false }
func WindowState() string { return "none" }
func WindowShow() bool    { return false }
func WindowHide() bool    { return false }

type Window struct{ url string }

func NewWindow(url string) *Window { return &Window{url: url} }

func NewWindowAs(title, url string) *Window { return &Window{url: url} }

func (w *Window) Gone() bool   { return false }
func (w *Window) Show() error  { OpenBrowser(w.url); return nil }
func (w *Window) Hide()        {}
func (w *Window) Hidden() bool { return false }
func (w *Window) Up() bool     { return false }
func (w *Window) Close()       {}
