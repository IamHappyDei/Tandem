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
	a.engine.SetGsxSync(gsxShared(a.cfg, a.ac))
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
	if v, ok := body["community"].(string); ok && strings.TrimSpace(v) != "" {
		a.ac.SetCommunity(v)
		a.log.Info("looking for aircraft in %s", a.ac.CommunityDir())
		if err := a.ac.Save(); err != nil {
			return nil, err
		}
		return a.ac.Summary(), nil
	}
	if _, ok := body["rescan"].(bool); ok {
		a.ac.SetCommunity(a.ac.CommunityDir())
		return a.ac.Summary(), nil
	}
	if _, ok := body["detect"].(bool); ok {
		pkgs := a.ac.List()
		if p, why, ok := aircraft.Detect(pkgs, a.gsx.State()); ok {
			a.ac.SetActive(p.Key(), why)
			a.applyAircraft()
			_ = a.ac.Save()
			a.log.Info("aircraft: %s (%s)", p.Label(), why)
			return a.ac.Summary(), nil
		}
		return map[string]any{"error": "nothing in what GSX reports matches the folder - pick it below"}, nil
	}
	if v, ok := body["active"].(string); ok {
		why := "you picked it"
		if v == "" {
			why = ""
		}
		a.ac.SetActive(v, why)
		a.applyAircraft()
		if err := a.ac.Save(); err != nil {
			return nil, err
		}
		return a.ac.Summary(), nil
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
		if err := a.ac.Save(); err != nil {
			return nil, err
		}
		return a.ac.Summary(), nil
	}
	return a.ac.Summary(), nil
}
