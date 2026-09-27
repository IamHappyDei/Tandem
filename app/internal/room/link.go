package room

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"tandem/internal/logx"
)

type Handlers struct {
	OnAction func(p *Peer, m *Msg)
	OnDigest func(p *Peer, m *Msg)
	OnNote   func(p *Peer, m *Msg)
	OnSim    func(p *Peer, m *Msg)
	OnJoin   func(p *Peer)
	OnLeave  func(p *Peer)

	OnIntro func(peerID, ext, lan string)
}

type Config struct {
	ID      string
	Name    string
	Room    string
	Pass    string
	Bind    string
	Port    int
	UDPPort int
	Log     *logx.Log
}

type Stats struct {
	Rx        int `json:"rx"`
	Tx        int `json:"tx"`
	Joins     int `json:"peersJoined"`
	Rejected  int `json:"rejected"`
	DupFrames int `json:"dupFrames"`
}

type transport struct {
	id     int
	kind   string
	desc   string
	send   func(*Msg) error
	close  func()
	carry  bool
	lastRX int64
}

type Peer struct {
	l       *Link
	mu      sync.Mutex
	Name    string
	Origin  string
	Lamport int
	Since   int64
	Tr      map[int]*transport
	rxID    map[string]int64
	closed  bool
}

type Link struct {
	cfg Config
	log *logx.Log
	H   Handlers

	origin  string
	mu      sync.Mutex
	peers   map[string]*Peer
	trs     map[int]*transport
	nextID  int
	stats   Stats
	udp     *udpSock
	tcpLn   *http.Server
	upgr    websocket.Upgrader
	stopped bool
	public  []string
	rel     *reliable
	relayMu sync.Mutex
	conduit *transport
	rdv     *net.UDPAddr
	rdvCode string
	rdvSeen int64
}

func NewLink(cfg Config) *Link {
	if cfg.Log == nil {
		cfg.Log = logx.New("info")
	}
	if cfg.Bind == "" {
		cfg.Bind = "0.0.0.0"
	}
	l := &Link{
		cfg:    cfg,
		log:    cfg.Log,
		origin: randHex(6),
		peers:  map[string]*Peer{},
		trs:    map[int]*transport{},
		upgr:   websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096},
	}
	l.rel = newReliable(l)
	return l
}

func (l *Link) RoomName() string     { return l.cfg.Room }
func (l *Link) Envelope(m *Msg) *Msg { return l.envelope(m) }

func (l *Link) adoptRoom(room string) bool {
	if room == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cfg.Room != "" {
		return false
	}
	l.cfg.Room = room
	return true
}

func (l *Link) SetRoom(room, pass string) {
	l.mu.Lock()
	l.cfg.Room, l.cfg.Pass = room, pass
	l.mu.Unlock()
}

func (l *Link) Origin() string { return l.origin }

func (l *Link) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stats
}

func (l *Link) Start() error {
	l.mu.Lock()
	l.stopped = false
	l.mu.Unlock()
	if err := l.startUDP(); err != nil {
		l.log.Warn("UDP unavailable (%s) - internet play will need a relay or a direct address", err)
	}
	go l.serveTCP()
	go l.keepalive()
	return nil
}

func (l *Link) Stop() {
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return
	}
	l.stopped = true
	trs := make([]*transport, 0, len(l.trs))
	for _, t := range l.trs {
		trs = append(trs, t)
	}
	l.mu.Unlock()
	for _, t := range trs {
		t.close()
	}
	l.stopUDP()
	if l.tcpLn != nil {
		l.tcpLn.Close()
	}
}

func (l *Link) serveTCP() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("tandem peer link " + l.origin + "\n"))
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := l.upgr.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		go l.acceptWS(conn, r.RemoteAddr)
	})
	srv := &http.Server{Addr: l.cfg.Bind + ":" + itoa(l.cfg.Port), Handler: mux}
	l.tcpLn = srv
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		l.log.Warn("cannot listen on %s:%d (%s) - other cockpits will have to dial out to you", l.cfg.Bind, l.cfg.Port, err)
	}
}

func (l *Link) Dial(rawurl string) (*Peer, error) {
	rawurl = strings.TrimSpace(rawurl)
	if rawurl == "" {
		return nil, errors.New("no address given")
	}
	if !strings.Contains(rawurl, "://") {

		if !strings.Contains(rawurl, ":") {
			rawurl += ":" + itoa(l.cfg.Port)
		}
		rawurl = "ws://" + rawurl + "/ws"
	}
	u, err := url.Parse(rawurl)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "http" {
		u.Scheme = "ws"
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/ws"
	}
	d := websocket.Dialer{HandshakeTimeout: 6 * time.Second}
	conn, _, err := d.Dial(u.String(), nil)
	if err != nil {
		return nil, err
	}
	tr := l.addTransport("ws", u.String(), func(m *Msg) error { return wsSend(conn, m) }, func() { conn.Close() }, false)
	hello := l.envelope(&Msg{T: "hello", Ver: ProtoVersion, Room: l.cfg.Room, Pass: l.cfg.Pass, Name: l.cfg.Name})
	if err := tr.send(hello); err != nil {
		l.dropTransport(tr)
		return nil, err
	}
	go l.readWS(conn, tr)
	l.log.Info("dialling %s (room %s)", u.String(), l.cfg.Room)

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if p := l.peerByTr(tr.id); p != nil {
			return p, nil
		}
		time.Sleep(60 * time.Millisecond)
	}
	l.dropTransport(tr)
	return nil, errors.New("no welcome from " + u.Host + " (wrong room code, or a firewall in the way?)")
}

func (l *Link) acceptWS(conn *websocket.Conn, addr string) {
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	_, raw, err := conn.ReadMessage()
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		conn.Close()
		return
	}
	var m Msg
	if err := json.Unmarshal(raw, &m); err != nil || m.T != "hello" {
		wsSend(conn, &Msg{T: "reject", Reason: "say hello first"})
		time.AfterFunc(100*time.Millisecond, func() { conn.Close() })
		return
	}
	if l.cfg.Room == "" && m.Room != "" {
		l.adoptRoom(m.Room)
		l.log.Info("joining their room %s", m.Room)
	}
	if l.cfg.Room != "" && m.Room != "" && m.Room != l.cfg.Room {
		wsSend(conn, &Msg{T: "reject", Reason: "wrong room (this box is " + l.cfg.Room + ")"})
		conn.Close()
		l.log.Warn("rejected peer: wrong room %q from %s", m.Room, addr)
		return
	}
	if l.cfg.Pass != "" && m.Pass != l.cfg.Pass {
		wsSend(conn, &Msg{T: "reject", Reason: "bad passphrase"})
		conn.Close()
		l.log.Warn("rejected peer: bad passphrase from %s", addr)
		return
	}
	tr := l.addTransport("ws", addr, func(mm *Msg) error { return wsSend(conn, mm) }, func() { conn.Close() }, false)
	p, created := l.attach(tr, orDefault(m.Origin, randHex(6)), orDefault(m.Name, "peer"))
	if p == nil {

		l.log.Debug("ignoring a connection from ourselves (%s)", addr)
		conn.Close()
		l.dropTransport(tr)
		return
	}
	wsSend(conn, l.envelope(&Msg{T: "welcome", Ver: ProtoVersion, Name: l.cfg.Name, Peers: l.peerNames(p.Origin)}))
	if created {
		l.notifyJoin(p)
	}
	go l.readWS(conn, tr)
}

func (l *Link) readWS(conn *websocket.Conn, tr *transport) {
	for {
		conn.SetReadDeadline(time.Now().Add(75 * time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var m Msg
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		l.handleFrame(tr, &m)
	}
	l.dropTransport(tr)
	conn.Close()
}

func (l *Link) handleFrame(tr *transport, m *Msg) {
	now := time.Now().UnixMilli()
	tr.lastRX = now
	l.mu.Lock()
	l.stats.Rx++
	l.mu.Unlock()

	switch m.T {
	case "ping":
		tr.send(&Msg{T: "pong", Origin: l.origin})
		return
	case "pong":
		return
	case "probe":

		l.considerProbe(tr, m)
		return
	case "ack":
		l.rel.ack(m.ID)
		return
	case "reject":
		l.log.Warn("the other side refused us: %s", m.Reason)
		l.dropTransport(tr)
		return
	case "hello":
		if tr.carry {
			return
		}

		p, created := l.attach(tr, orDefault(m.Origin, randHex(6)), orDefault(m.Name, "peer"))
		if p != nil {
			tr.send(l.envelope(&Msg{T: "welcome", Ver: ProtoVersion, Name: l.cfg.Name}))
			if created {
				l.notifyJoin(p)
			}
		}
		return
	case "welcome":
		if tr.carry {
			return
		}
		p, created := l.attach(tr, m.Origin, orDefault(m.Name, "host"))
		if p != nil && created {
			l.notifyJoin(p)
		}
		return
	}

	origin, name := m.Origin, m.Who
	if tr.carry && m.From != nil {
		origin, name = m.From.Origin, m.From.Name
	}
	if origin == "" || origin == l.origin {
		return
	}
	p, created := l.attach(tr, origin, name)
	if p == nil {
		return
	}
	if created {
		l.notifyJoin(p)
	}
	p.touch(m, name)

	switch m.T {
	case "action":
		if m.ID != "" && p.seenID(m.ID) {
			l.mu.Lock()
			l.stats.DupFrames++
			l.mu.Unlock()
			return
		}
		if m.ID != "" {
			tr.send(&Msg{T: "ack", ID: m.ID, Origin: l.origin})
		}
		if l.H.OnAction != nil {
			l.H.OnAction(p, m)
		}
	case "digest":
		if l.H.OnDigest != nil {
			l.H.OnDigest(p, m)
		}
	case "note":
		if l.H.OnNote != nil {
			l.H.OnNote(p, m)
		}
	case "sim":
		if l.H.OnSim != nil {
			l.H.OnSim(p, m)
		}
	case "ask":
		l.digestRequest(p)
	default:

	}
}

func (l *Link) digestRequest(p *Peer) {
	if l.H.OnDigest == nil {
		return
	}

	l.H.OnDigest(p, &Msg{T: "ask", Origin: p.Origin, Who: p.Name})
}

func (l *Link) attach(tr *transport, origin, name string) (*Peer, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if origin == "" || origin == l.origin {
		return nil, false
	}
	p, ok := l.peers[origin]
	created := false
	if !ok {
		p = &Peer{l: l, Origin: origin, Name: orDefault(name, "peer"), Since: time.Now().UnixMilli(),
			Tr: map[int]*transport{}, rxID: map[string]int64{}}
		l.peers[origin] = p
		l.stats.Joins++
		created = true
	}
	if _, has := p.Tr[tr.id]; !has {
		p.Tr[tr.id] = tr
		l.trs[tr.id] = tr
	}
	if name != "" {
		p.Name = name
	}
	return p, created
}

func (l *Link) peerByTr(id int) *Peer {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, p := range l.peers {
		if _, ok := p.Tr[id]; ok {
			return p
		}
	}
	return nil
}

func (l *Link) dropTransport(tr *transport) {
	l.mu.Lock()
	delete(l.trs, tr.id)
	var doomed []*Peer
	for origin, p := range l.peers {
		p.mu.Lock()
		delete(p.Tr, tr.id)
		empty := len(p.Tr) == 0
		p.mu.Unlock()
		if empty {
			delete(l.peers, origin)
			doomed = append(doomed, p)
		}
	}
	l.mu.Unlock()
	for _, p := range doomed {
		if l.H.OnLeave != nil {
			l.H.OnLeave(p)
		}
		l.log.Info("peer left: %s", p.Name)
	}
	tr.close()
}

func (l *Link) notifyJoin(p *Peer) {
	go func() {
		if l.H.OnJoin != nil {
			l.H.OnJoin(p)
		}

		l.SendTo(p.Origin, l.envelope(&Msg{T: "ask"}))
	}()
}

type PeerInfo struct {
	Name    string   `json:"name"`
	Origin  string   `json:"origin"`
	Since   int64    `json:"since"`
	Paths   []string `json:"paths"`
	Lamport int      `json:"lamport"`
}

func (l *Link) Peers() []PeerInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []PeerInfo{}
	for _, p := range l.peers {
		out = append(out, p.Info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (p *Peer) Info() PeerInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	paths := []string{}
	for _, t := range p.Tr {
		paths = append(paths, t.kind+":"+t.desc)
	}
	sort.Strings(paths)
	return PeerInfo{Name: p.Name, Origin: p.Origin, Since: p.Since, Paths: paths, Lamport: p.Lamport}
}

func (p *Peer) touch(m *Msg, name string) {
	p.mu.Lock()
	if name != "" {
		p.Name = name
	}
	if m.Lamport > p.Lamport {
		p.Lamport = m.Lamport
	}
	p.mu.Unlock()
}

func (p *Peer) seenID(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.rxID[id]; ok {
		return true
	}
	p.rxID[id] = time.Now().UnixMilli()
	if len(p.rxID) > 800 {

		now := time.Now().Add(-10 * time.Minute).UnixMilli()
		for k, v := range p.rxID {
			if v < now || len(p.rxID) > 700 {
				delete(p.rxID, k)
			}
		}
	}
	return false
}

func (p *Peer) send(m *Msg) {
	p.mu.Lock()
	trs := make([]*transport, 0, len(p.Tr))
	for _, t := range p.Tr {
		trs = append(trs, t)
	}
	p.mu.Unlock()
	for _, t := range trs {
		if err := t.send(m); err == nil {
			return
		}
	}
}

func (l *Link) Broadcast(m *Msg) int {
	l.mu.Lock()
	peers := make([]*Peer, 0, len(l.peers))
	for _, p := range l.peers {
		peers = append(peers, p)
	}
	l.mu.Unlock()

	m = l.envelope(m)
	l.mu.Lock()
	owned := map[int]bool{}
	out := make([]*transport, 0, len(l.trs))
	for _, p := range peers {
		for id, tr := range p.Tr {
			owned[id] = true
			if _, gone := l.trs[id]; !gone {
				continue
			}
			out = append(out, tr)
		}
	}

	for id, tr := range l.trs {
		if !owned[id] {
			out = append(out, tr)
		}
	}
	l.mu.Unlock()
	n := 0
	for _, tr := range out {
		t := tr
		if m.T == "action" {
			l.rel.sendRaw(t, m)
		} else {
			_ = t.send(m)
		}
		n++
	}
	l.mu.Lock()
	l.stats.Tx += n
	l.mu.Unlock()
	return n
}

func (l *Link) SendTo(origin string, m *Msg) {
	l.mu.Lock()
	p := l.peers[origin]
	l.mu.Unlock()
	if p != nil {
		p.send(m)
	}
}

func (l *Link) PeerCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.peers)
}

func (l *Link) peerNames(exclude string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []string{}
	for o, p := range l.peers {
		if o != exclude {
			out = append(out, p.Name)
		}
	}
	return out
}

func (l *Link) envelope(m *Msg) *Msg {
	if m.Origin == "" {
		m.Origin = l.origin
	}
	if m.Who == "" && m.T != "hello" && m.T != "welcome" {
		m.Who = l.cfg.Name
	}
	if m.Ver == 0 {
		m.Ver = ProtoVersion
	}
	if m.TS == 0 {
		m.TS = time.Now().UnixMilli()
	}
	return m
}

func (l *Link) keepalive() {
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for range tick.C {
		l.mu.Lock()
		trs := make([]*transport, 0, len(l.trs))
		for _, t := range l.trs {
			trs = append(trs, t)
		}
		l.mu.Unlock()
		for _, t := range trs {
			if err := t.send(&Msg{T: "ping", Origin: l.origin}); err != nil {
				l.dropTransport(t)
			}
		}
		l.punchBeacon()
	}
}

func wsSend(conn *websocket.Conn, m *Msg) error {
	conn.SetWriteDeadline(time.Now().Add(8 * time.Second))
	return conn.WriteJSON(m)
}

func (l *Link) addTransport(kind, desc string, send func(*Msg) error, close func(), carry bool) *transport {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.nextID++

	var wmu sync.Mutex
	guarded := func(m *Msg) error {
		wmu.Lock()
		defer wmu.Unlock()
		return send(m)
	}
	t := &transport{id: l.nextID, kind: kind, desc: desc, send: guarded, close: close, carry: carry, lastRX: time.Now().UnixMilli()}
	l.trs[t.id] = t
	return t
}

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

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func itoa(i int) string { return strconv.Itoa(i) }

func (l *Link) SetName(n string) {
	l.mu.Lock()
	l.cfg.Name = n
	l.mu.Unlock()
}

func (l *Link) DropAll() {
	l.mu.Lock()
	trs := make([]*transport, 0, len(l.trs))
	for _, t := range l.trs {
		trs = append(trs, t)
	}
	l.mu.Unlock()
	for _, t := range trs {
		l.dropTransport(t)
	}
}

func (l *Link) Relay(raw, roomName, pass string) error {
	u, err := url.Parse(raw)
	if err == nil {

		l.mu.Lock()
		for _, t := range l.trs {
			if t.kind == "relay" && t.desc == u.String() {
				l.mu.Unlock()
				return nil
			}
		}
		l.mu.Unlock()
	}
	if err != nil {
		return err
	}
	if u.Scheme == "http" {
		u.Scheme = "ws"
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/ws"
	}

	l.relayMu.Lock()
	defer l.relayMu.Unlock()
	for _, t := range l.snapshotTransports() {
		if t.kind == "relay" && t.desc == u.String() {
			return nil
		}
	}
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return err
	}
	tr := l.addTransport("relay", u.String(), func(m *Msg) error { return wsSend(conn, m) }, func() { conn.Close() }, true)
	wsSend(conn, l.envelope(&Msg{T: "hello", Ver: ProtoVersion, Name: l.cfg.Name, Room: orDefault(roomName, l.cfg.Room), Pass: orDefault(pass, l.cfg.Pass)}))
	l.mu.Lock()
	l.conduit = tr
	l.mu.Unlock()
	go l.readWS(conn, tr)
	l.log.Debug("attached to relay %s", u.String())
	return nil

}

func (l *Link) snapshotTransports() []*transport {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]*transport, 0, len(l.trs))
	for _, t := range l.trs {
		out = append(out, t)
	}
	return out
}

func (l *Link) PublicAddresses() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.public...)
}

func (l *Link) OwnEndpoints() (hosts map[string]bool, ports map[string]bool) {
	hosts, ports = map[string]bool{}, map[string]bool{}
	l.mu.Lock()
	pub := append([]string{}, l.public...)
	cfgPort, cfgUDP := itoa(l.cfg.Port), itoa(l.cfg.UDPPort)
	l.mu.Unlock()
	ports[cfgPort], ports[cfgUDP] = true, true
	for _, c := range append(pub, l.Candidates()...) {
		c = strings.TrimPrefix(strings.TrimPrefix(c, "ws://"), "wss://")
		if h, pt, err := net.SplitHostPort(c); err == nil {
			hosts[h] = true
			ports[pt] = true
		}
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				hosts[ipnet.IP.String()] = true
			}
		}
	}
	_ = cfgPort
	_ = cfgUDP
	return
}

func (l *Link) LocalEndpoints() []string {
	out := []string{}
	for _, ip := range localIPv4() {
		out = append(out, net.JoinHostPort(ip, itoa(l.cfg.UDPPort)))
	}
	return out
}

const rdvProto = "gsxd1"

func (l *Link) Rendezvous(server, code string) error {
	u, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.rdv, l.rdvCode = u, code
	l.mu.Unlock()
	l.log.Info("registering code %s with rendezvous %s", code, server)
	l.announceRDV()
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for range t.C {
			l.mu.Lock()
			dead := l.stopped
			l.mu.Unlock()
			if dead {
				return
			}
			l.announceRDV()
		}
	}()
	return nil
}

func (l *Link) StopRendezvous() {
	l.mu.Lock()
	l.rdv = nil
	l.mu.Unlock()
}

func (l *Link) announceRDV() {
	l.mu.Lock()
	u, code := l.rdv, l.rdvCode
	l.mu.Unlock()
	if u == nil || l.udp == nil {
		return
	}
	lan := strings.Join(l.LocalEndpoints(), ",")
	_ = l.udp.sendText(u, rdvProto+"|REG|"+code+"|"+lan)
}

func (l *Link) handleRendezvous(from net.Addr, text string) {
	ua, ok := from.(*net.UDPAddr)
	if !ok || ua == nil {
		return
	}
	_ = ua
	f := strings.Split(text, "|")
	if len(f) < 3 || f[0] != rdvProto {
		return
	}
	switch f[1] {
	case "REGOK":
		l.mu.Lock()
		l.rdvSeen = time.Now().UnixMilli()
		l.mu.Unlock()

		if len(f) > 3 && f[3] != "" {
			l.mu.Lock()
			fresh := true
			for _, p := range l.public {
				if p == f[3] {
					fresh = false
				}
			}
			if fresh {
				l.public = append(l.public, f[3])
			}
			l.mu.Unlock()
			if fresh {
				l.log.Info("the internet sees this cockpit at %s", f[3])
			}
		}
	case "INTRO":

		if len(f) < 6 {
			return
		}
		l.mu.Lock()
		mine := l.rdvCode
		l.mu.Unlock()
		if mine != "" && !strings.EqualFold(f[2], mine) {
			return
		}
		if strings.EqualFold(f[4], "") {
			return
		}
		l.log.Info("rendezvous introduced us to %s at %s", f[3], f[4])
		l.Punch([]string{f[4]})
		if f[5] != "" {
			l.Punch(strings.Split(f[5], ","))
		}
		go func(ext string) {
			for i := 0; i < 6 && l.PeerCount() == 0; i++ {
				time.Sleep(1200 * time.Millisecond)
				if _, err := l.Dial("ws://" + ext); err == nil {
					return
				}
				l.Punch([]string{ext})
			}
		}(f[4])
		if l.H.OnIntro != nil {
			l.H.OnIntro(f[3], f[4], f[5])
		}
	case "ERR":
		l.log.Warn("rendezvous refused us: %s", strings.Join(f[2:], " "))
	}
}

func (l *Link) Call(code string) bool {
	l.mu.Lock()
	u, mine := l.rdv, l.rdvCode
	l.mu.Unlock()
	if u == nil || l.udp == nil {
		return false
	}
	_ = l.udp.sendText(u, rdvProto+"|CALL|"+mine+"|"+strings.ToUpper(code))
	return true
}

func (l *Link) HasRendezvous() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rdv != nil
}

func (l *Link) SetRendezvousCode(code string) {
	l.mu.Lock()
	l.rdvCode = code
	l.mu.Unlock()
}

func (l *Link) considerProbe(tr *transport, m *Msg) {
	l.mu.Lock()
	room, origin := l.cfg.Room, l.origin
	l.mu.Unlock()
	if m.Origin == "" || m.Origin == origin {
		return
	}
	if room == "" && m.Room != "" {
		l.adoptRoom(m.Room)
		room = m.Room
		l.log.Info("taking room %s from the peer the phone book introduced", room)
	}
	if room == "" || (m.Room != "" && m.Room != room) {
		return
	}
	p, created := l.attach(tr, m.Origin, orDefault(m.Who, "peer"))
	if p == nil {
		return
	}
	if created {
		l.log.Info("found %s on this network (room %s)", p.Name, room)
		l.notifyJoin(p)
	}
	_ = tr.send(l.envelope(&Msg{T: "hello", Name: l.cfg.Name, Room: room}))
}

func (l *Link) LANSweep() {
	if l.udp == nil {
		return
	}
	l.mu.Lock()
	room := l.cfg.Room

	ports := []int{l.cfg.UDPPort}
	if l.cfg.UDPPort != 8790 {
		ports = append(ports, 8790)
	}
	l.mu.Unlock()
	if room == "" {
		return
	}
	go func() {
		n := 0
		for _, ip := range privateIPv4() {
			base := ip[:strings.LastIndex(ip, ".")+1]
			for _, last := range append(hosts254(), 0) {
				addr := base + itoa(last)
				if last == 0 {
					addr = base + "255"
				}
				for _, pt := range ports {
					l.probe(net.JoinHostPort(addr, itoa(pt)))
					n++
				}
				if n%32 == 0 {
					time.Sleep(20 * time.Millisecond)
				}
			}
		}
		l.log.Debug("swept %d local addresses for room %s", n, room)
	}()
}

func hosts254() []int {
	out := make([]int, 0, 254)
	for i := 1; i <= 254; i++ {
		out = append(out, i)
	}
	return out
}

func (l *Link) probe(addr string) {
	if l.udp == nil {
		return
	}
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return
	}
	l.mu.Lock()
	m := l.envelope(&Msg{T: "probe", Name: l.cfg.Name, Room: l.cfg.Room})
	l.mu.Unlock()
	_ = l.udp.sendOne(ua, m)
}

func (l *Link) RendezvousAge() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rdv == nil || l.rdvSeen == 0 {
		return -1
	}
	return (time.Now().UnixMilli() - l.rdvSeen) / 1000
}
