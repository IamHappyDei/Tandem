package aircraft

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Package struct {
	Dir          string `json:"dir"`
	Name         string `json:"name"`
	Title        string `json:"title"`
	Creator      string `json:"creator"`
	Manufacturer string `json:"manufacturer"`
	Version      string `json:"version"`
	Kind         string `json:"kind"`
	TypeCode     string `json:"typeCode,omitempty"`
	found        int
	Families     []string `json:"families,omitempty"`
	SimObjects   int      `json:"simObjects,omitempty"`
}

func (p Package) Key() string {
	if p.Name != "" {
		return p.Name
	}
	return filepath.Base(p.Dir)
}

func (p Package) Label() string {
	t := p.Title
	if t == "" {
		t = p.Name
	}
	if t == "" {
		t = filepath.Base(p.Dir)
	}
	if p.TypeCode != "" && !strings.Contains(strings.ToUpper(t), p.TypeCode) {
		t += " · " + p.TypeCode
	}
	return t
}

type manifest struct {
	Title        string `json:"title"`
	Creator      string `json:"creator"`
	Manufacturer string `json:"manufacturer"`
	ContentType  string `json:"content_type"`
	Version      string `json:"package_version"`
}

var (
	icaoRe  = regexp.MustCompile(`(?im)^\s*(icao_type_code|icao_atc|atc_type|icao)\s*=\s*([A-Za-z0-9 _-]+)`)
	titleRe = regexp.MustCompile(`(?im)^\s*title\s*=\s*(.+)$`)
)

func Scan(root string) []Package {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := []Package{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		m, err := readManifest(dir)
		if err != nil {
			continue
		}
		p := Package{
			Dir: dir, Name: e.Name(), Title: strings.TrimSpace(m.Title),
			Creator: strings.TrimSpace(m.Creator), Manufacturer: strings.TrimSpace(m.Manufacturer),
			Version: strings.TrimSpace(m.Version), Kind: strings.ToUpper(strings.TrimSpace(m.ContentType)),
		}
		if p.Kind == "" {
			p.Kind = guessKind(dir, e.Name())
		}
		if p.Kind == "AIRCRAFT" {
			p.enrich()
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return strings.ToLower(out[i].Label()) < strings.ToLower(out[j].Label())
	})
	return out
}

func readManifest(dir string) (manifest, error) {
	var m manifest
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	return m, nil
}

func guessKind(dir, name string) string {
	if hasSimObjects(dir) {
		return "AIRCRAFT"
	}
	if strings.Contains(strings.ToLower(name), "liver") {
		return "LIVERY"
	}
	return "MISC"
}

func hasSimObjects(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && strings.EqualFold(e.Name(), "SimObjects") {
			return true
		}
	}
	return false
}

func (p *Package) enrich() {
	for _, dir := range simDirs(p.Dir) {
		_ = filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				if fi != nil && fi.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if fi.IsDir() {
				if depth(path, dir) > 3 {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.EqualFold(fi.Name(), "aircraft.cfg") {
				return nil
			}
			p.SimObjects++
			if p.found < 4 {
				p.found++
				if b, err := os.ReadFile(path); err == nil {
					p.readCfg(string(b))
				}
			}
			return nil
		})
		if p.Title != "" || p.TypeCode != "" {
			return
		}
	}
	if p.Title == "" {
		p.Title = p.Name
	}
}

func (p *Package) readCfg(s string) {
	if p.TypeCode == "" {
		if m := icaoRe.FindStringSubmatch(s); m != nil {
			code := strings.ToUpper(strings.TrimSpace(m[2]))
			if icaoOK(code) {
				p.TypeCode = code
			}
		}
	}
	if p.found == 1 && p.Title == "" {
		if m := titleRe.FindStringSubmatch(s); m != nil {
			p.Title = strings.TrimSpace(m[1])
		}
	}
}

func simDirs(root string) []string {
	out := []string{}
	if d := filepath.Join(root, "SimObjects"); isDir(d) {
		return []string{d}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if d := filepath.Join(root, e.Name(), "SimObjects"); isDir(d) {
			out = append(out, d)
		}
	}
	return out
}

var icaoShape = regexp.MustCompile(`^[A-Z][A-Z0-9]{2,4}$`)

func icaoOK(code string) bool {
	switch code {
	case "", "NONE", "GENERIC", "ATC", "TYPE", "UNKNOWN":
		return false
	}
	return icaoShape.MatchString(code) && !strings.Contains(code, "_")
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func depth(path, root string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return 99
	}
	if rel == "." {
		return 0
	}
	return strings.Count(rel, string(os.PathSeparator)) + 1
}

func Aircraft(pkgs []Package) []Package {
	out := []Package{}
	for _, p := range pkgs {
		if p.Kind == "AIRCRAFT" {
			out = append(out, p)
		}
	}
	return out
}

func SearchState(v any, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		for _, x := range t {
			SearchState(x, out)
		}
	case []any:
		for _, x := range t {
			SearchState(x, out)
		}
	case string:
		if x := strings.TrimSpace(t); x != "" && len(x) < 120 {
			*out = append(*out, x)
		}
	}
}

func Detect(pkgs []Package, state map[string]any) (Package, string, bool) {
	var names []string
	SearchState(state, &names)
	for _, n := range names {
		if p, ok := Guess(pkgs, n); ok {
			return p, "found in what GSX reports", true
		}
	}
	return Package{}, "", false
}

func Best(pkgs []Package, state map[string]any) (Package, string, bool) {
	if p, why, ok := Detect(pkgs, state); ok {
		return p, why, true
	}
	return FromFlightFiles(pkgs)
}
