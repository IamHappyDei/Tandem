package ui

import (
	"strings"
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
		a.rejoin()
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

func (a *App) rejoin() {
	a.mu.Lock()
	prev := a.lastDial
	a.mu.Unlock()
	if prev == "" {
		return
	}
	go func() {
		time.Sleep(700 * time.Millisecond)
		for _, t := range strings.Fields(prev) {
			if _, err := a.link.Dial(t); err != nil {
				a.log.Debug("back to %s did not work (%s)", t, err)
				continue
			}
			a.log.Info("back to %s - shared cockpit was turned on again", t)
		}
		a.link.Punch(a.link.Candidates())
	}()
}
