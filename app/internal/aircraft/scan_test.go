package aircraft

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	pkgs := map[string]map[string]any{
		"acme-a320neo": {
			"title": "Acme A320neo", "creator": "Acme", "content_type": "AIRCRAFT", "package_version": "1.2.3",
		},
		"acme-a320neo-liveries": {
			"title": "Acme A320neo Liveries", "content_type": "LIVERY",
		},
		"toolbox": {"title": "Toolbox", "content_type": "MISC"},
		"airport-rop": {
			"title": "Sibiu", "content_type": "SCENERY",
		},
		"loose-airplane": {"title": "Loose Plane", "creator": "Someone"},
	}
	for name, m := range pkgs {
		d := filepath.Join(root, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(m)
		if err := os.WriteFile(filepath.Join(d, "manifest.json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sim := filepath.Join(root, "acme-a320neo", "SimObjects", "Airplanes", "A32N")
	if err := os.MkdirAll(sim, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[fltsim.0]\ntitle = Acme A320neo LEAP\nmodel = LEAP\n[fltsim.1]\ntitle = Acme A320neo Shark\n"
	if err := os.WriteFile(filepath.Join(sim, "aircraft.cfg"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	loose := filepath.Join(root, "loose-airplane", "SimObjects", "Airplanes", "Loose")
	if err := os.MkdirAll(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loose, "aircraft.cfg"), []byte("[fltsim.0]\ntitle = Loose Plane\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestScanSortsIntoAircraftAndRest(t *testing.T) {
	pkgs := Scan(fixture(t))
	if len(pkgs) != 5 {
		t.Fatalf("found %d packages, want 5", len(pkgs))
	}
	air := Aircraft(pkgs)
	if len(air) != 2 {
		names := []string{}
		for _, p := range pkgs {
			names = append(names, p.Name+":"+p.Kind)
		}
		t.Fatalf("aircraft %d (%v), want the manifest one and the guessed one", len(air), names)
	}
	if air[0].SimObjects != 1 {
		t.Errorf("simobjects=%d, want 1", air[0].SimObjects)
	}
	if air[0].Label() != "Acme A320neo" {
		t.Errorf("label %q", air[0].Label())
	}
}

func TestGuessMatchesWhatTheSimSays(t *testing.T) {
	pkgs := Aircraft(Scan(fixture(t)))
	for _, said := range []string{"ACME A320NEO", "acme-a320neo", "Flying the Acme A320neo LEAP"} {
		if _, ok := Guess(pkgs, said); !ok {
			t.Errorf("%q did not match anything", said)
		}
	}
	if _, ok := Guess(pkgs, "Cessna 172"); ok {
		t.Errorf("a cessna matched an airbus")
	}
	if _, ok := Guess(pkgs, "   "); ok {
		t.Errorf("blank matched")
	}
}

func TestDetectReadsTheGsxState(t *testing.T) {
	pkgs := Aircraft(Scan(fixture(t)))
	state := map[string]any{
		"services": []any{map[string]any{"name": "Boarding"}},
		"aircraft": map[string]any{"title": "Acme A320neo LEAP", "tail": "YRABC"},
	}
	p, why, ok := Detect(pkgs, state)
	if !ok {
		t.Fatalf("nothing detected in a state that names the aircraft")
	}
	if p.Key() != "acme-a320neo" || why == "" {
		t.Errorf("detected %q from %q", p.Key(), why)
	}
	if _, _, ok := Detect(pkgs, map[string]any{"services": []any{}}); ok {
		t.Errorf("detected an aircraft out of nothing")
	}
}

func TestProfilesPersistAndDefaultToSharing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aircraft.json")
	s := Open(path)
	s.SetCommunity(fixture(t))
	if _, why := s.ActiveKey(); why != "" {
		t.Fatalf("a fresh store already claims an aircraft: %q", why)
	}
	if p := s.Profile("acme-a320neo"); !p.GsxSync {
		t.Errorf("a profile nobody touched says it is not sharing")
	}
	s.Set("acme-a320neo", "Acme A320neo", false, []string{"GPU"})
	s.SetActive("acme-a320neo", "you picked it")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	back := Open(path)
	if k, why := back.ActiveKey(); k != "acme-a320neo" || why != "you picked it" {
		t.Errorf("active lost: %q %q", k, why)
	}
	if p := back.Profile("acme-a320neo"); p.GsxSync || len(p.Services) != 1 {
		t.Errorf("profile did not survive: %+v", p)
	}
	sum := back.Summary()
	if sum["found"].(int) != 2 {
		t.Errorf("summary found %v", sum["found"])
	}
	row, _ := json.Marshal(sum["aircraft"])
	if !contains(string(row), `"gsxSync":false`) {
		t.Errorf("summary hides the switch state: %s", row)
	}
}

func contains(h, n string) bool { return strings.Contains(h, n) }
