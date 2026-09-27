package ui

import (
	"time"

	"tandem/internal/conf"
)

func (a *App) applyShared(v bool) {
	if v == a.cfg.SharedCockpit() {
		return
	}
	a.cfg.Shared = &v
	if !v {
		a.link.Stop()
		a.log.Info("shared cockpit is off - the room is left, the ports are closed, nothing leaves this machine")
		return
	}
	a.log.Info("shared cockpit is on - joining the room")
	go func() {
		if err := a.link.Start(); err != nil {
			a.log.Warn("the room could not be started (%s)", err)
			return
		}
		if a.cfg.Net.Room == "" {
			a.cfg.Net.Room = conf.RoomCode()
			_ = a.cfg.Save()
		}
		a.link.SetRoom(a.cfg.Net.Room, a.cfg.Net.Pass)
		go a.discoverLoop()
		a.ensureRendezvous()
		for _, u := range a.cfg.Net.RelayURLs {
			go func(u string) {
				time.Sleep(300 * time.Millisecond)
				if err := a.link.Relay(u, a.cfg.Net.Room, a.cfg.Net.Pass); err != nil {
					a.log.Warn("relay %s is not reachable (%s) - staying P2P only", u, err)
				} else {
					a.log.Info("attached to relay %s", u)
				}
			}(u)
		}
	}()
}
