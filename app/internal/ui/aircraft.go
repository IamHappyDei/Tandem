package ui

import (
	"errors"
	"net/http"
	"strings"

	"tandem/internal/aircraft"
	"tandem/internal/conf"
)

func gsxShared(cfg *conf.Config, acs *aircraft.Store) bool {
	if !cfg.Sync.GsxSync {
		return false
	}
	if key, _ := acs.ActiveKey(); key != "" {
		return acs.Profile(key).GsxSync
	}
	return true
}

func (a *App) applyAircraft() {
	a.engine.SetAllowed(a.activeServices())
	a.engine.SetGsxSync(gsxShared(a.cfg, a.ac))
}

func (a *App) activeServices() []string {
	key, _ := a.ac.ActiveKey()
	if key == "" {
		return nil
	}
	return a.ac.Profile(key).Services
}

func (a *App) aircraftBrief() map[string]any {
	key, why := a.ac.ActiveKey()
	label := ""
	if key != "" {
		for _, p := range a.ac.List() {
			if p.Key() == key {
				label = p.Label()
			}
		}
	}
	return map[string]any{"active": key, "label": label, "why": why, "community": a.ac.CommunityDir()}
}

func (a *App) hAircraft(r *http.Request, body map[string]any) (any, error) {
	changed := false
	save := func() error {
		if changed {
			changed = false
			return a.ac.Save()
		}
		return nil
	}
	if v, ok := body["community"].(string); ok && strings.TrimSpace(v) != "" {
		a.ac.SetCommunity(v)
		a.log.Info("looking for aircraft in %s", a.ac.CommunityDir())
		changed = true
	}
	if _, ok := body["rescan"].(bool); ok {
		a.ac.SetCommunity(a.ac.CommunityDir())
		changed = true
	}
	if v, ok := body["autoDetect"].(bool); ok {
		a.ac.SetAuto(v)
		a.log.Info("aircraft auto-detect %v", v)
		changed = true
	}
	if _, ok := body["detect"].(bool); ok {
		if p, why, ok := aircraft.Best(aircraft.Aircraft(a.ac.List()), a.gsx.State()); ok {
			a.ac.SetActive(p.Key(), why)
			a.applyAircraft()
			a.log.Info("aircraft: %s (%s)", p.Label(), why)
			changed = true
		} else {
			if err := save(); err != nil {
				return nil, err
			}
			return map[string]any{"error": "nothing in what GSX reports matches the folder - pick it below"}, nil
		}
	}
	if v, ok := body["active"].(string); ok {
		why := "you picked it"
		if v == "" {
			why = ""
		}
		a.ac.SetActivePinned(v, why, v != "")
		a.applyAircraft()
		changed = true
	}
	if raw, ok := body["profile"].(map[string]any); ok {
		key, _ := raw["key"].(string)
		if key == "" {
			return nil, errors.New("no aircraft named")
		}
		label, _ := raw["label"].(string)
		on := true
		if v, ok := raw["gsxSync"].(bool); ok {
			on = v
		}
		var services []string
		if v, ok := raw["services"].([]any); ok {
			for _, x := range v {
				if s, ok := x.(string); ok {
					services = append(services, s)
				}
			}
		}
		a.ac.Set(key, label, on, services)
		a.applyAircraft()
		changed = true
	}
	if err := save(); err != nil {
		return nil, err
	}
	return a.ac.Summary(), nil
}

func (a *App) autoAircraft() {
	if !a.ac.Auto() || a.ac.IsPinned() {
		return
	}
	p, why, ok := a.simDetect(a.ac.List())
	if !ok {
		p, why, ok = aircraft.Best(aircraft.Aircraft(a.ac.List()), a.gsx.State())
	}
	if !ok {
		return
	}
	if cur, _ := a.ac.ActiveKey(); cur == p.Key() {
		return
	}
	a.ac.SetActive(p.Key(), why)
	a.applyAircraft()
	if err := a.ac.Save(); err != nil {
		a.log.Debug("aircraft: could not be written down: %v", err)
	}
	a.log.Info("flying %s (%s)", p.Label(), why)
}
