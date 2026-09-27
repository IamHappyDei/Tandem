package wire

type MenuInfo struct {
	Title      string   `json:"title,omitempty"`
	Entries    []string `json:"entries,omitempty"`
	Disabled   []bool   `json:"disabled,omitempty"`
	StateClass []string `json:"stateClass,omitempty"`
	Shown      bool     `json:"shown"`
}

type MenuBrief struct {
	Shown  bool     `json:"shown"`
	Title  string   `json:"title,omitempty"`
	Labels []string `json:"labels,omitempty"`
}

type Phase struct {
	Key       string  `json:"key"`
	Canonical string  `json:"canonical,omitempty"`
	KnownVerb bool    `json:"knownVerb"`
	State     string  `json:"state"`
	PhaseHash string  `json:"phaseHash"`
	Progress  float64 `json:"progress,omitempty"`
	Label     string  `json:"label,omitempty"`
	Token     string  `json:"token,omitempty"`
}

type Intent struct {
	Kind    string   `json:"kind"`
	Replay  string   `json:"replay"`
	Name    string   `json:"name,omitempty"`
	Key     string   `json:"key,omitempty"`
	From    string   `json:"from,omitempty"`
	To      string   `json:"to,omitempty"`
	Label   string   `json:"label,omitempty"`
	Picked  string   `json:"picked,omitempty"`
	Entries []string `json:"entries,omitempty"`
	Why     string   `json:"why"`
}

type PeerRef struct {
	Name   string `json:"name"`
	Origin string `json:"origin"`
}

type Msg struct {
	T         string           `json:"t"`
	Ver       int              `json:"ver,omitempty"`
	ID        string           `json:"id,omitempty"`
	Origin    string           `json:"origin,omitempty"`
	Who       string           `json:"who,omitempty"`
	Name      string           `json:"name,omitempty"`
	Room      string           `json:"room,omitempty"`
	Pass      string           `json:"pass,omitempty"`
	Lamport   int              `json:"lamport,omitempty"`
	TS        int64            `json:"ts,omitempty"`
	Intent    *Intent          `json:"intent,omitempty"`
	Phases    map[string]Phase `json:"phases,omitempty"`
	StateHash string           `json:"stateHash,omitempty"`
	Menu      *MenuBrief       `json:"menu,omitempty"`
	SimReady  *bool            `json:"simReady,omitempty"`
	Text      string           `json:"text,omitempty"`
	Reason    string           `json:"reason,omitempty"`
	Peers     []string         `json:"peers,omitempty"`
	Via       string           `json:"via,omitempty"`
	Cands     []string         `json:"cands,omitempty"`
	Vars      map[string]any   `json:"vars,omitempty"`
	From      *PeerRef         `json:"from,omitempty"`
}

const ProtoVersion = 1

type Change struct {
	Before map[string]any
	After  map[string]any
}
