//go:build windows

package tray

import (
	_ "embed"
	"runtime"

	"github.com/energye/systray"
)

//go:embed icon.ico
var icon []byte

type Options struct {
	Tooltip   string
	OnShow    func()
	OnHide    func()
	OnBrowser func()
	OnQuit    func()
}

type Tray struct{ hide func() }

func Start(o Options) *Tray {
	if o.Tooltip == "" {
		o.Tooltip = "Tandem - shared cockpit ground"
	}
	t := &Tray{}
	go func() {
		runtime.LockOSThread()
		systray.Run(func() {
			systray.SetIcon(icon)
			systray.SetTooltip(o.Tooltip)
			systray.SetOnClick(func(menu systray.IMenu) {
				if menu != nil {
					_ = menu.ShowMenu()
				}
			})
			systray.SetOnRClick(func(menu systray.IMenu) {
				if menu != nil {
					_ = menu.ShowMenu()
				}
			})
			show := systray.AddMenuItem("Show Tandem", "open or restore the window")
			hide := systray.AddMenuItem("Hide window", "keep syncing, stop taking screen")
			systray.AddSeparator()
			web := systray.AddMenuItem("Open in browser", "if the window ever fails to appear")
			quit := systray.AddMenuItem("Quit Tandem", "sessions on both sides drop")
			show.Click(func() {
				if o.OnShow != nil {
					o.OnShow()
				}
			})
			hide.Click(func() {
				if o.OnHide != nil {
					o.OnHide()
				}
			})
			web.Click(func() {
				if o.OnBrowser != nil {
					o.OnBrowser()
				}
			})
			quit.Click(func() {
				if o.OnQuit != nil {
					o.OnQuit()
				}
				systray.Quit()
			})
		}, nil)
	}()
	return t
}

func (t *Tray) Stop() {}
