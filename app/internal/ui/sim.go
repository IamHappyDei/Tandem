package ui

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"tandem/internal/aircraft"
	"tandem/internal/bridge"
)

func (a *App) startSim() {
	if !a.cfg.Sim.Enabled || a.sim != nil {
		return
	}
	a.sim = bridge.New(a.cfg.SimPort(), a.log.Child("[sim]"))
	if err := a.sim.Start(a.onSimVars); err != nil {
		a.log.Warn("the sim bridge could not take port %d (%s) - aircraft systems are left alone", a.cfg.Sim.Port, err)
		a.sim = nil
		return
	}
	a.sim.Watch(a.wantedVars())
	a.log.Info("the sim bridge is listening on %s", a.sim.URL())
	a.installBridge()
}

func (a *App) stopSim() {
	if a.sim != nil {
		a.sim.Stop()
		a.sim = nil
	}
}

func (a *App) onSimVars(changed map[string]any) {
	names := make([]string, 0, len(changed))
	for k := range changed {
		names = append(names, k)
	}
	sort.Strings(names)
	a.log.Debug("the sim reports %s", strings.Join(names, ", "))
	if a.cfg.SimToPeer() {
		a.engine.NoteSim(changed)
	}
}

func (a *App) fromPeerVars(vars map[string]any) {
	if a.sim == nil {
		return
	}
	if key, _ := a.ac.ActiveKey(); key != "" {
		if prof := a.ac.Profile(key); !prof.AllowsSimVars(keysOf(vars)) {
			a.log.Info("a coworker touched switches this aircraft profile keeps to itself - ignored")
			return
		}
	}
	for k, v := range vars {
		a.sim.Write(k, v)
	}
}

func (a *App) wantedVars() []string {
	set := map[string]bool{}
	for _, p := range a.cfg.Sim.Watch {
		set[p] = true
	}
	if key, _ := a.ac.ActiveKey(); key != "" {
		for _, p := range a.ac.Profile(key).SimVars {
			set[p] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (a *App) simBrief() map[string]any {
	b := map[string]any{"enabled": a.cfg.Sim.Enabled, "connected": false,
		"toPeers": a.cfg.SimToPeer(), "wanted": a.wantedVars(), "port": a.cfg.SimPort()}
	if a.sim == nil {
		return b
	}
	title, tail := a.sim.Aircraft()
	b["connected"] = a.sim.Connected()
	b["url"] = a.sim.URL()
	b["title"] = title
	b["tail"] = tail
	b["vars"] = a.sim.Vars()
	return b
}

func (a *App) hSim(r *http.Request, body map[string]any) (any, error) {
	if v, ok := body["enabled"].(bool); ok && v != a.cfg.Sim.Enabled {
		a.cfg.Sim.Enabled = v
		if v {
			a.startSim()
		} else {
			a.stopSim()
		}
		a.log.Info("the sim bridge is %s", map[bool]string{true: "on", false: "off"}[v])
		if err := a.cfg.Save(); err != nil {
			return nil, err
		}
	}
	if v, ok := body["simToPeer"].(bool); ok {
		a.cfg.Sync.SimToPeer = &v
		a.log.Info("what the sim reports %s reach your coworkers",
			map[bool]string{true: "now", false: "no longer"}[v])
		if err := a.cfg.Save(); err != nil {
			return nil, err
		}
	}
	if v, ok := body["watch"].([]any); ok {
		list := []string{}
		for _, x := range v {
			if s, ok := x.(string); ok {
				list = append(list, s)
			}
		}
		a.cfg.Sim.Watch = list
		if a.sim != nil {
			a.sim.Watch(a.wantedVars())
		}
		if err := a.cfg.Save(); err != nil {
			return nil, err
		}
	}
	if _, ok := body["uninstall"].(bool); ok {
		if err := a.uninstallBridge(); err != nil {
			return nil, err
		}
		a.log.Info("the bridge folder Tandem made in %s is gone", a.ac.CommunityDir())
	}
	if v, ok := body["installDir"].(string); ok && strings.TrimSpace(v) != "" {
		a.ac.SetCommunity(v)
		a.log.Info("looking for aircraft - and for the bridge - in %s", a.ac.CommunityDir())
		if err := a.ac.Save(); err != nil {
			return nil, err
		}
	}
	if raw, ok := body["profileSimVars"].(map[string]any); ok {
		key, _ := raw["key"].(string)
		if key == "" {
			return nil, errors.New("no aircraft named")
		}
		list := []string{}
		if v, ok := raw["vars"].([]any); ok {
			for _, x := range v {
				if s, ok := x.(string); ok {
					list = append(list, s)
				}
			}
		}
		a.ac.SetSimVars(key, list)
		if a.sim != nil {
			a.sim.Watch(a.wantedVars())
		}
		if err := a.ac.Save(); err != nil {
			return nil, err
		}
	}
	if v, ok := body["write"].(map[string]any); ok && len(v) > 0 {
		if a.sim == nil {
			return nil, errors.New("the sim bridge is not listening - turn it on in Settings first")
		}
		if key, _ := a.ac.ActiveKey(); key != "" {
			names := keysOf(v)
			if !a.ac.Profile(key).AllowsSimVars(names) {
				return nil, errors.New("this aircraft profile does not share those switches")
			}
		}
		for k, x := range v {
			a.sim.Write(k, x)
		}
		a.onSimVars(v)
	}
	if _, ok := body["detect"].(bool); ok {
		if p, why, ok := a.simDetect(a.ac.List()); ok {
			a.ac.SetActive(p.Key(), why)
			a.applyAircraft()
			a.log.Info("flying %s (%s)", p.Label(), why)
			if err := a.ac.Save(); err != nil {
				return nil, err
			}
		}
	}
	return map[string]any{"sim": a.simBrief(), "aircraft": a.aircraftBrief()}, nil
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (a *App) simDetect(pkgs []aircraft.Package) (aircraft.Package, string, bool) {
	if a.sim == nil || !a.sim.Connected() {
		return aircraft.Package{}, "", false
	}
	return a.sim.Detect(aircraft.Aircraft(pkgs))
}

func (a *App) installBridge() {
	dir := a.ac.CommunityDir()
	if dir == "" {
		a.log.Warn("the community folder is not known yet - Settings has a box for it")
		return
	}
	if bridge.InstalledIn(dir) {
		return
	}
	dest, err := a.sim.InstallInto(dir)
	if err != nil {
		a.log.Warn("the bridge could not be put in %s (%s)", dir, err)
		return
	}
	a.log.Info("the bridge is installed at %s - restart the sim if it is already open", dest)
}

func (a *App) uninstallBridge() error {
	return bridge.UninstallFrom(a.ac.CommunityDir())
}
