package aircraft

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Profile struct {
	Key      string    `json:"key"`
	Label    string    `json:"label"`
	GsxSync  bool      `json:"gsxSync"`
	Services []string  `json:"services,omitempty"`
	Note     string    `json:"note,omitempty"`
	Updated  int64     `json:"updated"`
	Seen     time.Time `json:"-"`
}

type Store struct {
	path string

	mu        sync.Mutex
	Community string              `json:"community"`
	Active    string              `json:"active"`
	Detected  string              `json:"detectedFrom"`
	Profiles  map[string]*Profile `json:"profiles"`
	pkgs      []Package
	cached    time.Time
}

func defaultCommunity() string {
	if v := os.Getenv("MSFS_COMMUNITY"); v != "" {
		return v
	}
	best, bestScore := "", [2]int{}
	for _, c := range candidates() {
		if !isDir(c) {
			continue
		}
		pkgs := Scan(c)
		score := [2]int{len(Aircraft(pkgs)), len(pkgs)}
		if score[0] > bestScore[0] || (score[0] == bestScore[0] && score[1] > bestScore[1]) {
			best, bestScore = c, score
		}
	}
	return best
}

func candidates() []string {
	out := []string{}
	for _, drive := range []string{"C:", "D:", "E:", "F:"} {
		out = append(out,
			filepath.Join(drive, `\MSFS\Community`),
			filepath.Join(drive, `\Flight Simulator\Community`),
			filepath.Join(drive, `\Microsoft Flight Simulator\Community`),
			filepath.Join(drive, `\SteamLibrary\steamapps\common\MicrosoftFlightSimulator2024\Community`),
			filepath.Join(drive, `\SteamLibrary\steamapps\common\MSFS\Community`),
			filepath.Join(drive, `\Community`),
		)
	}
	if ld := os.Getenv("LOCALAPPDATA"); ld != "" {
		out = append(out, filepath.Join(ld, `Packages\Microsoft.Limitless_8wekyb3d8bbwe\LocalCache\Local\MSFS2024\Community`))
	}
	return out
}

func Open(path string) *Store {
	s := &Store{path: path, Profiles: map[string]*Profile{}}
	b, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(b, s)
	}
	if s.Profiles == nil {
		s.Profiles = map[string]*Profile{}
	}
	return s
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *Store) CommunityDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Community == "" {
		s.Community = defaultCommunity()
	}
	return s.Community
}

var cacheFor = 5 * time.Minute

func (s *Store) SetCommunity(dir string) {
	s.mu.Lock()
	s.Community = strings.TrimSpace(dir)
	s.pkgs, s.cached = nil, time.Time{}
	s.mu.Unlock()
}

func (s *Store) List() []Package {
	s.mu.Lock()
	fresh := time.Since(s.cached) < cacheFor && s.pkgs != nil
	s.mu.Unlock()
	if fresh {
		return s.pkgs
	}
	dir := s.CommunityDir()
	pkgs := Scan(dir)
	s.mu.Lock()
	s.pkgs, s.cached = pkgs, time.Now()
	s.mu.Unlock()
	return pkgs
}

func (s *Store) Profile(key string) *Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.Profiles[key]; ok {
		c := *p
		return &c
	}
	return &Profile{Key: key, GsxSync: true}
}

func (s *Store) Set(key, label string, gsxSync bool, services []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.Profiles[key]
	if !ok {
		p = &Profile{Key: key}
		s.Profiles[key] = p
	}
	p.Label, p.GsxSync, p.Services, p.Updated = label, gsxSync, services, time.Now().Unix()
}

func (s *Store) SetActive(key, why string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Active, s.Detected = key, why
}

func (s *Store) ActiveKey() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Active, s.Detected
}

func Guess(pkgs []Package, said string) (Package, bool) {
	want := normalize(said)
	if want == "" {
		return Package{}, false
	}
	for _, p := range pkgs {
		for _, c := range append([]string{p.Title, p.Name}, strings.Fields(p.Title)...) {
			n := normalize(c)
			if n == "" {
				continue
			}
			if n == want || strings.Contains(want, n) || strings.Contains(n, want) {
				return p, true
			}
		}
	}
	return Package{}, false
}

var normRe = regexp.MustCompile(`[^a-z0-9]+`)

func normalize(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "airbus ", "a")
	s = strings.ReplaceAll(s, "boeing ", "b")
	return strings.Trim(normRe.ReplaceAllString(s, " "), " ")
}

func (s *Store) Summary() map[string]any {
	pkgs := s.List()
	air := Aircraft(pkgs)
	type row struct {
		Key      string   `json:"key"`
		Label    string   `json:"label"`
		Kind     string   `json:"kind"`
		Creator  string   `json:"creator"`
		Version  string   `json:"version"`
		Objects  int      `json:"objects"`
		GsxSync  bool     `json:"gsxSync"`
		Services []string `json:"services,omitempty"`
	}
	rows := []row{}
	for _, p := range air {
		pr := s.Profile(p.Key())
		rows = append(rows, row{Key: p.Key(), Label: p.Label(), Kind: p.Kind, Creator: p.Creator,
			Version: p.Version, Objects: p.SimObjects, GsxSync: pr.GsxSync, Services: pr.Services})
	}
	other := []map[string]string{}
	for _, p := range pkgs {
		if p.Kind == "AIRCRAFT" {
			continue
		}
		other = append(other, map[string]string{"key": p.Key(), "label": p.Label(), "kind": p.Kind})
	}
	active, why := s.ActiveKey()
	keys := make([]string, 0, len(s.Profiles))
	s.mu.Lock()
	for k := range s.Profiles {
		keys = append(keys, k)
	}
	s.mu.Unlock()
	sort.Strings(keys)
	return map[string]any{
		"community": s.CommunityDir(),
		"found":     len(air),
		"other":     len(other),
		"aircraft":  rows,
		"packages":  other,
		"active":    active,
		"activeWhy": why,
		"profiles":  len(keys),
	}
}
