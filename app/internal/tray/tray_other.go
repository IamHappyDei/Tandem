//go:build !windows

package tray

type Options struct {
	Tooltip   string
	OnShow    func()
	OnHide    func()
	OnBrowser func()
	OnQuit    func()
}

type Tray struct{}

func Start(Options) *Tray { return &Tray{} }
func (t *Tray) Stop()     {}
