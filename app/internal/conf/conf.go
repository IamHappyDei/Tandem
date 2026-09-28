package conf

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Gsx struct {
	URL       string   `json:"url"`
	Channels  []string `json:"channels"`
	Reconnect bool     `json:"reconnect"`
}

type Net struct {
	Mode       string   `json:"mode"`
	Bind       string   `json:"bind"`
	Port       int      `json:"port"`
	UDPPort    int      `json:"udpPort"`
	Room       string   `json:"room"`
	Pass       string   `json:"pass"`
	Name       string   `json:"name"`
	RelayURLs  []string `json:"relayUrls"`
	Rendezvous string   `json:"rendezvous"`
	ID         string   `json:"id"`
}

type Link struct {
	Opaque  bool `json:"opaque"`
	Hide    bool `json:"hide"`
	WarnAck bool `json:"warnAck"`
}

type Sync struct {
	GsxSync          bool   `json:"gsxSync"`
	Paused           bool   `json:"paused"`
	Role             string `json:"role"`
	EchoMs           int    `json:"echoMs"`
	DebounceMs       int    `json:"debounceMs"`
	MirrorMenuPicks  bool   `json:"mirrorMenuPicks"`
	AutoReconcile    bool   `json:"autoReconcile"`
	ReconcileSeconds int    `json:"reconcileSeconds"`
	ConfirmDigests   int    `json:"confirmDigests"`
	AutoStop         bool   `json:"autoStop"`
	SimToPeer        *bool  `json:"simToPeer,omitempty"`
	PeerToSim        *bool  `json:"peerToSim,omitempty"`
	GraceSeconds     int    `json:"graceSeconds"`
	MaxReplays       int    `json:"maxReplaysPerService"`
	CaptureState     bool   `json:"captureState"`
}

type Config struct {
	Version int   `json:"version"`
	GSX     Gsx   `json:"gsx"`
	Net     Net   `json:"net"`
	Sync    Sync  `json:"sync"`
	Shared  *bool `json:"sharedCockpit,omitempty"`
	Sim     Sim   `json:"sim"`
	Link    Link  `json:"link"`
	UI      struct {
		Port          int  `json:"port"`
		AutoOpen      bool `json:"autoOpen"`
		NoUpdateCheck bool `json:"noUpdateCheck"`
	} `json:"ui"`
	Log struct {
		Level string `json:"level"`
	} `json:"log"`
	Peers []PeerHint `json:"peers,omitempty"`
}

type Sim struct {
	Enabled bool     `json:"enabled"`
	Port    int      `json:"port"`
	Watch   []string `json:"watch,omitempty"`
}

func (s Sim) SimToPeer() bool { return s.Enabled }

type PeerHint struct {
	Room  string `json:"room"`
	Addr  string `json:"addr,omitempty"`
	Where string `json:"where,omitempty"`
	Last  int64  `json:"last"`
}

func Defaults() *Config {
	c := &Config{Version: 1}
	c.GSX.URL = "ws://127.0.0.1:8744"
	c.GSX.Channels = []string{"state", "services", "menu", "prompts", "billing"}
	c.GSX.Reconnect = true
	c.Net.Mode = "solo"
	c.Net.Bind = "0.0.0.0"
	c.Net.Port = 8790
	c.Net.UDPPort = 8790
	c.Net.Room = ""
	c.Net.Pass = ""
	c.Net.RelayURLs = []string{}
	if r := RelayFromEnv(); r != "" {
		c.Net.RelayURLs = []string{r}
	}
	c.Shared = boolPtr(false)
	c.Sim.Enabled = false
	c.Sim.Port = 8796
	c.Sync.GsxSync = true
	c.Sync.Paused = false
	c.Sync.Role = "symmetric"
	c.Sync.EchoMs = 4000
	c.Sync.DebounceMs = 60
	c.Sync.MirrorMenuPicks = true
	c.Sync.AutoReconcile = true
	c.Sync.ReconcileSeconds = 5
	c.Sync.ConfirmDigests = 2
	c.Sync.AutoStop = false
	c.Sync.GraceSeconds = 12
	c.Sync.MaxReplays = 3
	c.Sync.CaptureState = true
	c.UI.Port = 8795
	c.UI.AutoOpen = true
	c.Log.Level = "info"
	c.Net.Name = Hostname()
	return c
}

func Dir() string {
	base := os.Getenv("APPDATA")
	if base == "" {
		base = filepath.Join(os.TempDir(), "Tandem")
		return base
	}
	d := filepath.Join(base, "Tandem")

	if _, err := os.Stat(d); err != nil {
		if prev := filepath.Join(base, "GSXSync"); dirExists(prev) {
			migrate(prev, d)
			return d
		}
	}
	_ = os.MkdirAll(d, 0o755)
	return d
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func migrate(from, to string) {
	if err := os.MkdirAll(to, 0o755); err != nil {
		return
	}
	fs, err := os.ReadDir(from)
	if err != nil {
		return
	}
	for _, f := range fs {
		if f.IsDir() || (!strings.HasSuffix(f.Name(), ".json") && !strings.HasSuffix(f.Name(), ".log")) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(from, f.Name()))
		if err == nil {
			_ = os.WriteFile(filepath.Join(to, f.Name()), b, 0o644)
		}
	}
}

var Path string

func ConfigPath() string {
	if Path != "" {
		return Path
	}
	return filepath.Join(Dir(), "config.json")
}
func LogPath() string { return filepath.Join(Dir(), "tandem.log") }

func Load() *Config {
	c := Defaults()
	merged := map[string]any{}
	for _, f := range []string{filepath.Join(Dir(), "config.json"), ConfigPath()} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		overlay(merged, m)
	}
	if len(merged) == 0 {
		return c
	}
	b, _ := json.Marshal(merged)
	_ = json.Unmarshal(b, c)
	if c.Net.Name == "" {
		c.Net.Name = Hostname()
	}
	if c.GSX.URL == "" {
		c.GSX.URL = Defaults().GSX.URL
	}
	return c
}

func (c *Config) Save() error {
	b, _ := json.MarshalIndent(c, "", "  ")
	where := ConfigPath()
	if err := os.WriteFile(where, b, 0o644); err != nil {
		return err
	}
	base := filepath.Join(Dir(), "config.json")
	if base != where {
		if err := os.WriteFile(base, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func overlay(dst, src map[string]any) {
	for k, v := range src {
		if sub, ok := v.(map[string]any); ok {
			if have, ok := dst[k].(map[string]any); ok {
				overlay(have, sub)
				continue
			}
		}
		dst[k] = v
	}
}

func Hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "cockpit"
	}
	return h
}

var slugBad = regexp.MustCompile(`[^a-z0-9._-]+`)

func Slug(name string) string {
	s := slugBad.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-.")
	if s == "" {
		return "cockpit"
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// RoomCode mints a room name. Eight characters to match Code() and isBareCode(),
// so a room code typed into the join box is recognised as a bare code, not a string.
// BuiltInRelay is the always-on server every cockpit dials out to, so nobody has to
// open a router port: the app connects through this instead of punching. TANDEM_RELAY overrides.
const BuiltInRelay = "wss://tandem-lioc.onrender.com"

func RelayFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("TANDEM_RELAY")); v != "" {
		return v
	}
	return BuiltInRelay
}

// RoomCode mints a room name. Eight characters, so it matches Code() and isBareCode()
// and a room code typed into the join box is taken as a bare code, not a long string.
func RoomCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, 8)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			out[i] = alphabet[(i*7+3)%len(alphabet)]
			continue
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out)
}

func LocalIPs() []string {
	out := []string{}
	for _, a := range mustAddrs() {
		out = append(out, a)
	}
	return out
}

func Code() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, 8)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			out[i] = alphabet[(i*7+3)%len(alphabet)]
			continue
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out)
}

func boolPtr(v bool) *bool { return &v }

func (c *Config) SimPort() int {
	if c.Sim.Port == 0 {
		return 8796
	}
	return c.Sim.Port
}

func (c *Config) SimToPeer() bool {
	if c.Sync.SimToPeer == nil {
		return c.Sim.Enabled
	}
	return *c.Sync.SimToPeer
}

func (c *Config) PeerToSim() bool {
	if c.Sync.PeerToSim == nil {
		return c.Sim.Enabled
	}
	return *c.Sync.PeerToSim
}

func (c *Config) SharedCockpit() bool {
	if c.Shared == nil {
		return true
	}
	return *c.Shared
}
