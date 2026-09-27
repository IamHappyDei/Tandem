package syn

import (
	"crypto/rand"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tandem/internal/gsx"
	"tandem/internal/logx"
	"tandem/internal/room"
	"tandem/internal/wire"
)

const maxSeen = 800

type EngineConfig struct {
	Role             string
	EchoMs           int
	DebounceMs       int
	MirrorMenuPicks  bool
	AutoReconcile    bool
	ReconcileSeconds int
	ConfirmDigests   int
	GraceSeconds     int
	MaxReplays       int
	AutoStop         bool
	CaptureState     bool
	GsxSyncDisabled  bool
	StartPaused      bool
	Name             string
	Allowed          []string
}

type Counters struct {
	LocalIntents    int `json:"localIntents"`
	Broadcast       int `json:"broadcast"`
	RxActions       int `json:"rxActions"`
	AppliedRemote   int `json:"appliedRemote"`
	SuppressedEcho  int `json:"suppressedEcho"`
	DroppedDupe     int `json:"droppedDupe"`
	ReconcileFixes  int `json:"reconcileFixes"`
	ReconcileSkiped int `json:"reconcileSkipped"`
	Filtered        int `json:"filtered"`
	Muted           int `json:"muted"`
	Failed          int `json:"failed"`
}

type Recent struct {
	TS   int64  `json:"ts"`
	Src  string `json:"src"`
	Why  string `json:"why"`
	Kind string `json:"kind"`
}

type Engine struct {
	cfg  EngineConfig
	gsx  *gsx.Client
	link *room.Link
	log  *logx.Log

	id       string
	mu       sync.Mutex
	lamport  int
	seen     map[string]bool
	seenOrde []string
	suppress map[string]int64
	pending  []pendingPick
	touched  map[string]int64
	drift    map[string]int
	replays  map[string]int
	digests  map[string]*room.Msg
	counters Counters
	allow    atomic.Value
	gsxOn    atomic.Bool
	paused   atomic.Bool
	recent   []Recent
	stop     chan struct{}
	tickers  []*time.Ticker
}

type pendingPick struct {
	label   string
	intent  *Intent
	expires int64
}

func NewEngine(cfg EngineConfig, g *gsx.Client, l *room.Link, log *logx.Log) *Engine {
	if cfg.ReconcileSeconds < 1 {
		cfg.ReconcileSeconds = 5
	}
	if cfg.ConfirmDigests < 1 {
		cfg.ConfirmDigests = 1
	}
	e := &Engine{
		cfg: cfg, gsx: g, link: l, log: log, id: randHex(4),
		seen: map[string]bool{}, suppress: map[string]int64{},
		touched: map[string]int64{}, drift: map[string]int{}, replays: map[string]int{},
		digests: map[string]*room.Msg{}, stop: make(chan struct{}),
	}
	e.allow.Store(allowSet(cfg.Allowed))
	e.gsxOn.Store(!cfg.GsxSyncDisabled)
	e.paused.Store(cfg.StartPaused)
	return e
}

func (e *Engine) live() bool { return !e.paused.Load() }

func (e *Engine) syncing() bool { return e.live() && e.gsxOn.Load() }

func (e *Engine) Start() {
	e.link.H.OnAction = e.onPeerAction
	e.link.H.OnDigest = e.onPeerDigest
	e.link.H.OnNote = e.onPeerNote
	e.link.H.OnJoin = func(p *room.Peer) {
		e.log.Info("in room with %s", p.Name)
		e.askAndPublish()
	}
	e.link.H.OnLeave = func(p *room.Peer) {
		e.mu.Lock()
		delete(e.digests, p.Origin)
		e.mu.Unlock()
		e.log.Warn("%s left the room", p.Name)
	}
	e.gsx.OnChange(e.onLocalChange)
	e.gsx.OnLink(func(up bool) {
		if up {
			e.log.Info("GSX is talking to us again")
		}
	})

	rec := time.Duration(e.cfg.ReconcileSeconds) * time.Second
	e.tickers = append(e.tickers, e.every(rec, func() { e.publishDigest() }))
	e.tickers = append(e.tickers, e.every(time.Second, func() { e.sweep() }))
	e.log.Info("engine up: role=%s id=%s reconcile=%s gsxSync=%v paused=%v", e.cfg.Role, e.id, rec, e.gsxOn.Load(), e.paused.Load())
}

func (e *Engine) Stop() {
	close(e.stop)
	for _, t := range e.tickers {
		t.Stop()
	}
}

func (e *Engine) every(d time.Duration, f func()) *time.Ticker {
	t := time.NewTicker(d)
	go func() {
		for {
			select {
			case <-t.C:
				f()
			case <-e.stop:
				t.Stop()
				return
			}
		}
	}()
	return t
}

func (e *Engine) onLocalChange(c Change) {
	if !e.syncing() {
		e.countMuted()
		return
	}
	intents := Classify(c)
	if len(intents) == 0 {
		return
	}
	now := time.Now().UnixMilli()
	phaseMoved := false
	for _, in := range intents {
		if in.Kind == "service" {
			e.mu.Lock()
			e.touched[in.Key] = now
			e.mu.Unlock()
			phaseMoved = true
		}
		if in.Replay == "none" {
			e.log.Debug("%s", in.Why)
			continue
		}
		if !e.allows(in) {
			e.filtered(in)
			continue
		}
		sig := signature(in)
		if e.suppressed(sig) {
			e.mu.Lock()
			e.counters.SuppressedEcho++
			e.mu.Unlock()
			e.log.Debug("echo dropped: %s", in.Why)
			continue
		}
		e.note(in, "local")
		if e.cfg.Role == "copilot" {
			e.log.Debug("copilot role: not broadcasting %s", in.Why)
			continue
		}
		e.mu.Lock()
		e.counters.LocalIntents++
		e.lamport++
		l := e.lamport
		e.mu.Unlock()
		msg := &room.Msg{
			T: "action", ID: e.id + "-" + itoa(l), Lamport: l, TS: now,
			Intent: slim(in),
		}
		n := e.link.Broadcast(msg)
		e.mu.Lock()
		e.counters.Broadcast += n
		e.mu.Unlock()
		e.log.Info("-> peers(%d) %s", n, in.Why)
	}
	if phaseMoved {
		e.digestSoon()
	}
}

func (e *Engine) onPeerAction(p *room.Peer, m *room.Msg) {
	if m.Origin == e.link.Origin() {
		return
	}
	e.mu.Lock()
	if m.ID != "" && e.seen[m.ID] {
		e.counters.DroppedDupe++
		e.mu.Unlock()
		return
	}
	e.remember(m.ID)
	e.counters.RxActions++
	e.mu.Unlock()

	if e.cfg.Role == "driver" {
		e.log.Debug("driver role: ignoring peer action %s", intentWhy(m.Intent))
		return
	}
	in := m.Intent
	if in == nil {
		return
	}
	e.note(in, "remote:"+orEmpty(m.Who, p.Name))
	if !e.allows(in) {
		e.filtered(in)
		return
	}
	if !e.syncing() {
		e.countMuted()
		e.log.Debug("muted (%s): peer order %s was noted and not applied", e.muteWhy(), intentWhy(in))
		return
	}
	e.suppressFor(in)
	ok := e.apply(in)
	e.mu.Lock()
	if ok {
		e.counters.AppliedRemote++
	} else {
		e.counters.Failed++
	}
	e.mu.Unlock()
	if ok {
		e.log.Info("<- peer %s applied: %s", orEmpty(m.Who, p.Name), intentWhy(in))
	} else {
		e.log.Warn("<- peer %s could NOT be applied here: %s (see status)", orEmpty(m.Who, p.Name), intentWhy(in))
	}
}

func (e *Engine) apply(in *Intent) bool {
	switch in.Kind {
	case "service":
		if in.Name != "" && verbKnown(in.Name) && in.Replay == "service" {
			if ok, _ := e.gsx.TriggerService(in.Name); ok {
				return true
			}
			e.log.Debug("service.trigger(%s) rejected in every shape", in.Name)
		}
		return e.viaMenu(in)
	case "menu":
		if in.Replay == "none" {
			return false
		}
		return e.viaMenu(in)
	}
	return false
}

func (e *Engine) viaMenu(in *Intent) bool {
	label := orEmpty(in.Picked, orEmpty(in.Label, in.Name))
	if label == "" || !e.cfg.MirrorMenuPicks {
		return false
	}
	if idx := e.indexOfLabel(label); idx >= 0 {
		if err := e.gsx.PickMenu(idx); err != nil {
			e.log.Debug("menu.pick %d rejected: %s", idx, err)
			return false
		}
		return true
	}
	e.mu.Lock()
	for _, q := range e.pending {
		if q.label == label {
			e.mu.Unlock()
			return false
		}
	}
	e.pending = append(e.pending, pendingPick{label: label, intent: in, expires: time.Now().Add(12 * time.Second).UnixMilli()})
	e.mu.Unlock()
	e.log.Info("waiting for a menu page with %q (peer wanted %s)", label, intentWhy(in))
	return false
}

func (e *Engine) indexOfLabel(label string) int {
	m := menuOf(e.gsx)
	if m == nil {
		return -1
	}
	want := norm(label)
	for i, entry := range m.Entries {
		if i < len(m.Disabled) && m.Disabled[i] {
			continue
		}
		if norm(entry) == want {
			return i
		}
	}
	for i, entry := range m.Entries {
		e := norm(entry)
		if e != "" && want != "" && (strings.Contains(e, want) || strings.Contains(want, e)) {
			return i
		}
	}
	return -1
}

func (e *Engine) publishDigest() {
	m := menuOf(e.gsx)
	mb := &room.MenuBrief{}
	if m != nil {
		mb.Shown, mb.Title = m.Shown, m.Title
		labels := labelsOf(e.gsx)
		if len(labels) > 24 {
			labels = labels[:24]
		}
		mb.Labels = labels
	}
	digest := &room.Msg{
		T: "digest", Phases: phasesOf(e.gsx), StateHash: stateHashOf(e.gsx),
		Menu: mb, SimReady: boolPtr(e.gsx.SimReady()),
	}
	e.link.Broadcast(e.link.Envelope(digest))
	e.compare(digest.Origin, digest, false)
}

func (e *Engine) askAndPublish() {
	e.publishDigest()
	e.link.Broadcast(e.link.Envelope(&room.Msg{T: "ask"}))
}

var digestTimerMu sync.Mutex
var digestAt *time.Timer

func (e *Engine) digestSoon() {
	digestTimerMu.Lock()
	defer digestTimerMu.Unlock()
	if digestAt != nil {
		return
	}
	digestAt = time.AfterFunc(250*time.Millisecond, func() {
		digestTimerMu.Lock()
		digestAt = nil
		digestTimerMu.Unlock()
		e.publishDigest()
	})
}

func (e *Engine) onPeerDigest(p *room.Peer, m *room.Msg) {
	if m.T == "ask" {

		e.link.SendTo(p.Origin, e.link.Envelope(e.digestMessage()))
		return
	}
	if m.Origin == "" || m.Origin == e.link.Origin() {
		return
	}
	e.mu.Lock()
	e.digests[m.Origin] = m
	e.mu.Unlock()
	e.compare(m.Origin, m, true)
}

func (e *Engine) digestMessage() *room.Msg {
	m := menuOf(e.gsx)
	mb := &room.MenuBrief{Shown: m != nil && m.Shown}
	if m != nil {
		mb.Title = m.Title
	}
	return &room.Msg{T: "digest", Phases: phasesOf(e.gsx), StateHash: stateHashOf(e.gsx), Menu: mb, SimReady: boolPtr(e.gsx.SimReady())}
}

func (e *Engine) compare(origin string, d *room.Msg, isPeer bool) {
	if !isPeer || d == nil {
		return
	}
	mine := phasesOf(e.gsx)
	theirs := d.Phases
	for _, key := range unionKeys(mine, theirs) {
		a, hasA := mine[key]
		b, hasB := theirs[key]
		if !hasA || !hasB {
			continue
		}
		if a.State == b.State && a.PhaseHash == b.PhaseHash {
			e.mu.Lock()
			delete(e.drift, key)
			delete(e.replays, key)
			e.mu.Unlock()
			continue
		}

		wantStart := b.State == "active" && (a.State == "idle" || a.State == "absent")
		wantStop := b.State == "idle" && a.State == "active"
		if !wantStart && !wantStop {
			e.log.Debug("%s: detail differs (here=%s there=%s) - not actionable", key, a.State, b.State)
			continue
		}
		e.mu.Lock()
		e.drift[key] = e.drift[key] + 1
		n := e.drift[key]
		e.mu.Unlock()
		if n < e.cfg.ConfirmDigests {
			continue
		}
		e.mu.Lock()
		last := e.touched[key]
		e.mu.Unlock()
		if time.Now().UnixMilli()-last < int64(e.cfg.GraceSeconds)*1000 {
			e.mu.Lock()
			e.counters.ReconcileSkiped++
			e.mu.Unlock()
			continue
		}
		if wantStop && !e.cfg.AutoStop {
			e.log.Warn("DRIFT %s: we are RUNNING, %s is idle - not cancelling a live service (auto-stop is off)", key, orEmpty(d.Who, origin))
			continue
		}
		e.reconcile(key, a, b, wantStart, orEmpty(d.Who, origin))
	}
}

func (e *Engine) reconcile(key string, a, b Phase, wantStart bool, who string) {
	if !e.syncing() {
		e.countMuted()
		e.log.Warn("DRIFT %s: here=%s there=%s (%s) - %s, so nothing was ordered", key, a.State, b.State, who, e.muteWhy())
		return
	}
	if !e.allows(&Intent{Name: orEmpty(a.Canonical, b.Canonical), Picked: orEmpty(a.Label, b.Label), Key: key}) {
		e.log.Debug("DRIFT %s: here=%s there=%s - outside this aircraft's profile, left alone", key, a.State, b.State)
		return
	}
	why := "peer stopped it"
	if wantStart {
		why = "peer started it"
	}
	if !e.cfg.AutoReconcile {
		e.log.Warn("DRIFT %s: here=%s there=%s (%s) - auto-reconcile off", key, a.State, b.State, why)
		return
	}
	e.mu.Lock()
	e.replays[key] = e.replays[key] + 1
	n := e.replays[key]
	e.touched[key] = time.Now().UnixMilli()
	e.mu.Unlock()
	if n > e.cfg.MaxReplays {
		e.log.Warn("DRIFT %s: here=%s there=%s - giving up after %d replays, do it by hand", key, a.State, b.State, e.cfg.MaxReplays)
		return
	}
	in := &Intent{Kind: "service", Name: orEmpty(a.Canonical, b.Canonical), Key: key, Label: orEmpty(a.Label, b.Label),
		From: a.State, To: b.State, Replay: "service", Why: "reconcile " + key + " (" + why + ")"}
	e.suppressFor(in)
	ok := e.apply(in)
	e.mu.Lock()
	if ok {
		e.counters.ReconcileFixes++
	}
	e.mu.Unlock()
	if ok {
		e.log.Info("reconcile %s (%s) -> replayed", key, why)
	} else {
		e.log.Warn("reconcile %s needs a human: no verb and no matching menu label", key)
	}
}

func (e *Engine) onPeerNote(p *room.Peer, m *room.Msg) {
	e.log.Info("[%s] %s", orEmpty(m.Who, p.Name), m.Text)
}

func (e *Engine) Note(text string) {
	e.link.Broadcast(e.link.Envelope(&room.Msg{T: "note", Text: text}))
}

func signature(in *Intent) string {
	switch in.Kind {
	case "service":
		return "svc:" + orEmpty(in.Name, in.Key) + ":" + orEmpty(in.To, in.From)
	case "menu":

		if in.Picked != "" {
			return "menu:" + norm(in.Picked)
		}
		return "menu:page:" + norm(in.To)
	}
	return in.Kind + ":" + in.Key
}

func (e *Engine) suppressed(sig string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	until, ok := e.suppress[sig]
	if !ok {
		return false
	}
	if time.Now().UnixMilli() > until {
		delete(e.suppress, sig)
		return false
	}
	return true
}

func (e *Engine) suppressFor(in *Intent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	until := time.Now().Add(time.Duration(e.cfg.EchoMs) * time.Millisecond).UnixMilli()
	e.suppress[signature(in)] = until
	if in.Kind == "service" {
		base := "svc:" + orEmpty(in.Name, in.Key) + ":"

		for _, s := range []string{"idle", "active", "done", "absent"} {
			e.suppress[base+s] = time.Now().Add(time.Duration(e.cfg.EchoMs/2) * time.Millisecond).UnixMilli()
		}
	}
}

func (e *Engine) remember(id string) {
	if id == "" {
		return
	}
	e.seen[id] = true
	e.seenOrde = append(e.seenOrde, id)
	for len(e.seenOrde) > maxSeen {
		delete(e.seen, e.seenOrde[0])
		e.seenOrde = e.seenOrde[1:]
	}
}

func (e *Engine) note(in *Intent, src string) {
	e.mu.Lock()
	e.recent = append(e.recent, Recent{TS: time.Now().UnixMilli(), Src: src, Why: intentWhy(in), Kind: in.Kind})
	if len(e.recent) > 60 {
		e.recent = e.recent[len(e.recent)-60:]
	}
	e.mu.Unlock()
}

func (e *Engine) sweep() {
	if !e.syncing() {
		e.mu.Lock()
		if len(e.pending) > 0 {
			e.counters.Muted += len(e.pending)
			e.pending = nil
		}
		e.mu.Unlock()
		return
	}
	now := time.Now().UnixMilli()
	e.mu.Lock()
	list := append([]pendingPick{}, e.pending...)
	e.mu.Unlock()
	keep := list[:0]
	for _, q := range list {
		if q.expires < now {
			e.log.Warn("peer pick %q never appeared locally - skipped", q.label)
			continue
		}
		if idx := e.indexOfLabel(q.label); idx >= 0 {
			if err := e.gsx.PickMenu(idx); err == nil {
				e.log.Info("picked %q as the peer wanted", q.label)
			}
			continue
		}
		keep = append(keep, q)
	}
	e.mu.Lock()
	e.pending = keep
	for sig, until := range e.suppress {
		if until < now {
			delete(e.suppress, sig)
		}
	}
	e.mu.Unlock()
}

type PeerStatus struct {
	Name      string           `json:"name"`
	Origin    string           `json:"origin"`
	Phases    map[string]Phase `json:"phases"`
	StateHash string           `json:"stateHash"`
	Menu      *room.MenuBrief  `json:"menu"`
	SimReady  bool             `json:"simReady"`
	TS        int64            `json:"ts"`
	Paths     []string         `json:"paths"`
}

type Status struct {
	TS   int64 `json:"ts"`
	Self struct {
		Name string `json:"name"`
		ID   string `json:"id"`
		Role string `json:"role"`
	} `json:"self"`
	GSX struct {
		URL           string            `json:"url"`
		Connected     bool              `json:"connected"`
		SimReady      bool              `json:"simReady"`
		StateHash     string            `json:"stateHash"`
		Phases        map[string]Phase  `json:"phases"`
		TopKeys       []string          `json:"topKeys"`
		Stats         gsx.Stats         `json:"stats"`
		Shapes        map[string]string `json:"shapes"`
		Menu          *wire.MenuInfo    `json:"menu"`
		UnknownStates []UnknownState    `json:"unknownStates"`
	} `json:"gsx"`
	Link struct {
		Room  string          `json:"room"`
		Peers []room.PeerInfo `json:"peers"`
		Stats room.Stats      `json:"stats"`
	} `json:"link"`
	Sync struct {
		GsxSync   bool     `json:"gsxSync"`
		Paused    bool     `json:"paused"`
		Mode      string   `json:"mode"`
		Allowed   []string `json:"allowed,omitempty"`
		Counters  Counters `json:"counters"`
		Reconcile string   `json:"reconcile"`
		Pending   []string `json:"pendingMenu"`
		Recent    []Recent `json:"recent"`
	} `json:"sync"`
	Peers []PeerStatus `json:"peers"`
}

func phasesOf(g *gsx.Client) map[string]Phase { return ServicePhaseMap(g.ServicesRaw()) }
func stateHashOf(g *gsx.Client) string        { return Hash(phasesOf(g)) }

func menuOf(g *gsx.Client) *wire.MenuInfo {
	m := CheapMenu(g.MenuRaw())
	if m != nil {
		m.Shown = g.MenuShown()
	}
	return m
}

func labelsOf(g *gsx.Client) []string {
	m := menuOf(g)
	if m == nil {
		return nil
	}
	out := []string{}
	for _, s := range m.Entries {
		if s != "" && s != "[c]" {
			out = append(out, s)
		}
	}
	return out
}

func (e *Engine) Status() *Status {
	st := &Status{}
	st.TS = time.Now().UnixMilli()
	st.Self.Name = e.cfg.Name
	st.Self.ID = e.id
	st.Self.Role = e.cfg.Role
	st.GSX.URL = e.gsx.URL
	st.GSX.Connected = e.gsx.Connected()
	st.GSX.SimReady = e.gsx.SimReady()
	st.GSX.StateHash = stateHashOf(e.gsx)
	st.GSX.Phases = phasesOf(e.gsx)
	st.GSX.TopKeys = e.gsx.TopKeys()
	st.GSX.Stats = e.gsx.Stats()
	st.GSX.Shapes = e.gsx.Shapes()
	st.GSX.Menu = menuOf(e.gsx)
	st.GSX.UnknownStates = UnknownStates()
	st.Link.Room = e.link.RoomName()
	st.Link.Peers = e.link.Peers()
	st.Link.Stats = e.link.Stats()
	st.Sync.GsxSync, st.Sync.Paused = e.SyncFlags()
	st.Sync.Allowed = e.Allowed()
	switch {
	case st.Sync.Paused:
		st.Sync.Mode = "paused"
	case !st.Sync.GsxSync:
		st.Sync.Mode = "room only"
	default:
		st.Sync.Mode = "gsx + room"
	}
	e.mu.Lock()
	st.Sync.Counters = e.counters
	st.Sync.Reconcile = itoa(e.cfg.ReconcileSeconds) + "s"
	for _, q := range e.pending {
		st.Sync.Pending = append(st.Sync.Pending, q.label)
	}
	st.Sync.Recent = append([]Recent{}, e.recent...)
	digests := make([]*room.Msg, 0, len(e.digests))
	for _, d := range e.digests {
		digests = append(digests, d)
	}
	e.mu.Unlock()
	for _, d := range digests {
		who := ""
		paths := []string{}
		for _, p := range st.Link.Peers {
			if p.Origin == d.Origin {
				who, paths = p.Name, p.Paths
			}
		}
		sim := false
		if d.SimReady != nil {
			sim = *d.SimReady
		}
		st.Peers = append(st.Peers, PeerStatus{Name: orEmpty(d.Who, who), Origin: d.Origin, Phases: d.Phases,
			StateHash: d.StateHash, Menu: d.Menu, SimReady: sim, TS: d.TS, Paths: paths})
	}
	sort.Slice(st.Peers, func(i, j int) bool { return st.Peers[i].Name < st.Peers[j].Name })
	return st
}

var normRe = regexp.MustCompile(`[^a-z0-9]+`)

func norm(s string) string {
	return strings.TrimSpace(normRe.ReplaceAllString(strings.ToLower(s), " "))
}

func intentWhy(in *Intent) string {
	if in == nil {
		return "?"
	}
	if in.Why != "" {
		return in.Why
	}
	return in.Kind
}

func slim(in *Intent) *Intent {
	out := *in
	out.Entries = nil
	if in.Picked != "" {
		out.Picked = in.Picked
	}
	return &out
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func unionKeys2(a, b map[string]Phase) []string {
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func boolPtr(b bool) *bool { return &b }

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = byte(time.Now().UnixNano() >> (8 * uint(i)))
		}
	}
	const digits = "0123456789abcdef"
	out := make([]byte, 0, n*2)
	for _, x := range b {
		out = append(out, digits[x>>4], digits[x&15])
	}
	return string(out)
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func (e *Engine) SetName(n string) { e.mu.Lock(); e.cfg.Name = n; e.mu.Unlock() }
func (e *Engine) SetRole(r string) {
	e.mu.Lock()
	e.cfg.Role = r
	e.mu.Unlock()
	e.log.Info("role is now %s", r)
}
func (e *Engine) SetReconcile(s int) {
	if s < 1 {
		s = 1
	}
	e.mu.Lock()
	e.cfg.ReconcileSeconds = s
	e.mu.Unlock()
	e.log.Debug("reconcile every %ds", s)
}
func (e *Engine) SetAutoStop(v bool) { e.mu.Lock(); e.cfg.AutoStop = v; e.mu.Unlock() }

func (e *Engine) SetGsxSync(v bool) bool {
	old := e.gsxOn.Swap(v)
	if v != old {
		if v {
			e.log.Info("gsx sync back on - the room shares ground services again")
		} else {
			e.log.Warn("gsx sync off - nothing is ordered in either cockpit's GSX; the room stays connected")
		}
		e.digestSoon()
	}
	return v
}

func (e *Engine) SetPaused(v bool) bool {
	old := e.paused.Swap(v)
	if v != old {
		if v {
			e.log.Warn("paused - this cockpit does nothing on its own now, and remote orders are refused")
		} else {
			e.log.Info("resumed - the room is live again")
			e.askAndPublish()
		}
		e.digestSoon()
	}
	return v
}

func (e *Engine) SyncFlags() (gsxSync, paused bool) {
	return e.gsxOn.Load(), e.paused.Load()
}

func (e *Engine) muteWhy() string {
	if e.paused.Load() {
		return "paused"
	}
	return "gsx sync is off"
}

func (e *Engine) countMuted() {
	e.mu.Lock()
	e.counters.Muted++
	e.mu.Unlock()
}

func (e *Engine) Phases() map[string]Phase     { return phasesOf(e.gsx) }
func (e *Engine) StateHash() string            { return stateHashOf(e.gsx) }
func (e *Engine) Menu() *wire.MenuInfo         { return menuOf(e.gsx) }
func (e *Engine) GSXConnected() bool           { return e.gsx.Connected() }
func (e *Engine) GSXSimReady() bool            { return e.gsx.SimReady() }
func (e *Engine) GSXURL() string               { return e.gsx.URL }
func (e *Engine) GSXStats() gsx.Stats          { return e.gsx.Stats() }
func (e *Engine) GSXShapes() map[string]string { return e.gsx.Shapes() }
func (e *Engine) GSXTopKeys() []string         { return e.gsx.TopKeys() }
func (e *Engine) GSXState() map[string]any     { return e.gsx.State() }

func (e *Engine) PeersDigests() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.digests))
	for k := range e.digests {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (e *Engine) MenuLabels() []string { return labelsOf(e.gsx) }

func allowSet(names []string) *map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			m[norm(n)] = true
		}
	}
	return &m
}

func (e *Engine) SetAllowed(names []string) {
	m := map[string]bool{}
	list := []string{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !m[norm(n)] {
			list = append(list, n)
		}
		m[norm(n)] = true
	}
	e.allow.Store(&m)
	e.mu.Lock()
	e.cfg.Allowed = list
	e.mu.Unlock()
	if len(list) == 0 {
		e.log.Info("mirroring every service GSX knows")
	} else {
		e.log.Info("mirroring %d service(s) for this aircraft: %s", len(list), strings.Join(list, ", "))
	}
}

func (e *Engine) allows(in *Intent) bool {
	p := e.allow.Load()
	if p == nil {
		return true
	}
	m := *(p.(*map[string]bool))
	if len(m) == 0 {
		return true
	}
	for _, c := range []string{in.Name, in.Picked, in.Label, in.Key} {
		if c != "" && m[norm(c)] {
			return true
		}
	}
	return false
}

func (e *Engine) filtered(in *Intent) {
	e.mu.Lock()
	e.counters.Filtered++
	e.mu.Unlock()
	e.log.Debug("outside this aircraft's profile: %s", intentWhy(in))
}

func (e *Engine) Allowed() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.cfg.Allowed...)
}
