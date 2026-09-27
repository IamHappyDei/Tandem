package ui

import (
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"tandem/internal/aircraft"
	"tandem/internal/bridge"
	"tandem/internal/conf"

	"tandem/internal/gsx"
	"tandem/internal/logx"
	"tandem/internal/room"
	"tandem/internal/syn"
)

//go:embed web
var webFS embed.FS

type App struct {
	cfg    *conf.Config
	log    *logx.Log
	gsx    *gsx.Client
	link   *room.Link
	engine *syn.Engine
	ac     *aircraft.Store

	mu        sync.Mutex
	joinedURL string
	lastDial  string
	public    []string
	upSince   time.Time
	subs      []chan logx.Rec
	srv       *http.Server
	up        Update
	sim       *bridge.Server
	upClient  *http.Client
	OnQuit    func()
}

func New(cfg *conf.Config, log *logx.Log) *App {
	glog := log.Child("[gsx]")
	llog := log.Child("[net]")
	elog := log.Child("[syn]")

	if cfg.Net.ID == "" {
		cfg.Net.ID = conf.Code()
	}
	l := room.NewLink(room.Config{
		Name: cfg.Net.Name, Room: cfg.Net.Room, Pass: cfg.Net.Pass, ID: cfg.Net.ID,
		Bind: cfg.Net.Bind, Port: cfg.Net.Port, UDPPort: cfg.Net.UDPPort, Log: llog,
	})
	g := gsx.New(cfg.GSX.URL, cfg.GSX.Channels, glog, cfg.Sync.DebounceMs)
	acs := aircraft.Open(filepath.Join(conf.Dir(), "aircraft.json"))
	e := syn.NewEngine(syn.EngineConfig{
		Role: cfg.Sync.Role, EchoMs: cfg.Sync.EchoMs, DebounceMs: cfg.Sync.DebounceMs,
		MirrorMenuPicks: cfg.Sync.MirrorMenuPicks, AutoReconcile: cfg.Sync.AutoReconcile,
		ReconcileSeconds: cfg.Sync.ReconcileSeconds, ConfirmDigests: cfg.Sync.ConfirmDigests,
		GraceSeconds: cfg.Sync.GraceSeconds, MaxReplays: cfg.Sync.MaxReplays,
		CaptureState: cfg.Sync.CaptureState, GsxSyncDisabled: !gsxShared(cfg, acs),
		StartPaused: cfg.Sync.Paused, Name: cfg.Net.Name,
	}, g, l, elog)
	a := &App{cfg: cfg, log: log, gsx: g, link: l, engine: e, upSince: time.Now(), ac: acs}
	e.OnSim(a.fromPeerVars)
	return a
}

func (a *App) Start() error {
	a.engine.Start()
	go func() {
		time.Sleep(2 * time.Second)
		a.autoAircraft()
		for range time.NewTicker(30 * time.Second).C {
			a.autoAircraft()
		}
	}()
	a.gsx.Start()
	a.startSim()
	go a.updateLoop()
	if !a.cfg.SharedCockpit() {
		a.log.Info("shared cockpit is switched off - nothing leaves this machine, no room is joined, no ports are open")
		return nil
	}
	if err := a.link.Start(); err != nil {
		return err
	}
	if a.cfg.Net.Room == "" {
		a.cfg.Net.Room = conf.RoomCode()
		_ = a.cfg.Save()
	}
	a.link.SetRoom(a.cfg.Net.Room, a.cfg.Net.Pass)
	go a.discoverLoop()
	a.ensureRendezvous()
	for _, u := range a.cfg.Net.RelayURLs {
		go func(u string) {
			time.Sleep(300 * time.Millisecond)
			if err := a.link.Relay(u, a.cfg.Net.Room, a.cfg.Net.Pass); err != nil {
				a.log.Warn("relay %s is not reachable (%s) - staying P2P only", u, err)
			} else {
				a.log.Info("attached to relay %s", u)
			}
		}(u)
	}
	return nil
}

func (a *App) Stop() {
	a.stopSim()
	a.gsx.Stop()
	a.link.Stop()
	a.engine.Stop()
}

func (a *App) discoverLoop() {
	for {
		pub := a.link.Discover()
		a.mu.Lock()
		a.public = pub
		a.mu.Unlock()
		if len(pub) > 0 {
			a.log.Debug("public endpoints: %s", strings.Join(pub, ", "))
		}
		time.Sleep(25 * time.Second)
	}
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/status", a.json(a.status))
	mux.HandleFunc("/api/log", a.json(a.logTail))
	mux.HandleFunc("/api/events", a.events)
	mux.HandleFunc("/api/connect", a.post(a.connect))
	mux.HandleFunc("/api/disconnect", a.post(a.disconnect))
	mux.HandleFunc("/api/invite", a.json(a.invite))
	mux.HandleFunc("/api/paste", a.post(a.paste))
	mux.HandleFunc("/api/note", a.post(a.note))
	mux.HandleFunc("/api/trigger", a.post(a.trigger))
	mux.HandleFunc("/api/config", a.cors(a.saveConfig))
	mux.HandleFunc("/api/state", a.json(a.dumpState))
	mux.HandleFunc("/api/probe", a.post(a.probeOnce))
	mux.HandleFunc("/api/quit", a.post(a.quit))
	mux.HandleFunc("/api/update", a.post(a.hUpdate))
	mux.HandleFunc("/api/update/apply", a.post(a.hApply))
	mux.HandleFunc("/api/aircraft", a.either(a.hAircraft))
	mux.HandleFunc("/api/sim", a.either(a.hSim))
	return mux
}

func (a *App) ServeWeb(port int) error {
	a.srv = &http.Server{Addr: "127.0.0.1:" + strconv.Itoa(port), Handler: a.Handler()}
	ln, err := net.Listen("tcp", a.srv.Addr)
	if err != nil {
		return err
	}
	go a.srv.Serve(ln)
	return nil
}

func (a *App) URL() string { return "http://" + a.srv.Addr }

func OpenBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

type jhandler func(w http.ResponseWriter, r *http.Request) (any, error)

func (a *App) json(h jhandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := h(w, r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			out = map[string]any{"error": err.Error()}
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(out)
	}
}

func (a *App) post(h func(r *http.Request, body map[string]any) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		body := map[string]any{}
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if len(b) > 0 {
			_ = json.Unmarshal(b, &body)
		}
		out, err := h(r, body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

func (a *App) cors(h func(r *http.Request, body map[string]any) (any, error)) http.HandlerFunc {
	return a.post(h)
}

func (a *App) either(h func(r *http.Request, body map[string]any) (any, error)) http.HandlerFunc {
	get := a.post(h)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			out, err := h(r, map[string]any{})
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		get(w, r)
	}
}

func (a *App) quit(r *http.Request, body map[string]any) (any, error) {
	a.log.Info("quit requested from the dashboard - closing shop")
	go func() {
		time.Sleep(300 * time.Millisecond)
		if a.OnQuit != nil {
			a.OnQuit()
		}
		a.Stop()
		os.Exit(0)
	}()
	return map[string]any{"ok": true}, nil
}

func (a *App) status(w http.ResponseWriter, r *http.Request) (any, error) {
	st := a.engine.Status()

	if r := a.link.RoomName(); r != "" && r != a.cfg.Net.Room {
		a.cfg.Net.Room = r
		_ = a.cfg.Save()
	}
	a.mu.Lock()
	pub := append([]string{}, a.public...)
	last := a.lastDial
	a.mu.Unlock()
	st.GSX.URL = a.cfg.GSX.URL
	extra := map[string]any{
		"version":       Version,
		"uptime":        int(time.Since(a.upSince).Seconds()),
		"public":        pub,
		"candidates":    a.link.Candidates(),
		"listening":     a.cfg.Net.Port,
		"udp":           a.cfg.Net.UDPPort,
		"lastDial":      last,
		"can":           a.capabilities(),
		"rdvAge":        a.link.RendezvousAge(),
		"room":          a.cfg.Net.Room,
		"code":          a.cfg.Net.ID,
		"rendezvous":    a.cfg.Net.Rendezvous,
		"viaRendezvous": a.link.HasRendezvous(),
		"relays":        strings.Join(a.cfg.Net.RelayURLs, " "),
		"pass":          a.cfg.Net.Pass != "",
		"mode":          modeOf(a.cfg),
		"name":          a.cfg.Net.Name,
		"uiPort":        a.cfg.UI.Port,
		"logFile":       filepath.Base(conf.LogPath()),
		"aircraft":      a.aircraftBrief(),
		"sim":           a.simBrief(),
		"gsxSync":       a.cfg.Sync.GsxSync,
		"paused":        a.cfg.Sync.Paused,
		"update":        a.updateInfo(),
		"checkUpdates":  !a.cfg.UI.NoUpdateCheck,
		"link": map[string]any{
			"opaque":  a.cfg.Link.Opaque,
			"hide":    a.cfg.Link.Hide,
			"warnAck": a.cfg.Link.WarnAck,
		},
	}
	return map[string]any{"status": st, "app": extra}, nil
}

func (a *App) logTail(w http.ResponseWriter, r *http.Request) (any, error) {
	n := 200
	if v := r.URL.Query().Get("n"); v != "" {
		n, _ = strconv.Atoi(v)
	}
	return a.log.Recent(n), nil
}

func (a *App) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no streaming", http.StatusInternalServerError)
		return
	}
	ch := make(chan logx.Rec, 64)
	a.mu.Lock()
	a.subs = append(a.subs, ch)
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		for i, s := range a.subs {
			if s == ch {
				a.subs = append(a.subs[:i], a.subs[i+1:]...)
				break
			}
		}
		a.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	ctx := r.Context()
	send := func(topic string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", topic, b)
		flusher.Flush()
	}
	send("status", mustJSON(a.mustStatus()))
	for {
		select {
		case <-ctx.Done():
			return
		case rec := <-ch:
			send("log", rec)
		case <-tick.C:
			send("status", mustJSON(a.mustStatus()))
		}
	}
}

func (a *App) ensureRendezvous() {
	addr := strings.TrimSpace(a.cfg.Net.Rendezvous)
	if addr == "" {
		a.link.StopRendezvous()
		return
	}
	a.link.SetRendezvousCode(a.cfg.Net.ID)
	if err := a.link.Rendezvous(addr, a.cfg.Net.ID); err != nil {
		a.log.Warn("rendezvous %s is not reachable (%s) - a bare code will not work, send the long string", addr, err)
	}
}

func (a *App) capabilities() map[string]any {
	pub := a.link.PublicAddresses()
	rdv := a.link.HasRendezvous()
	age := a.link.RendezvousAge()
	out := map[string]any{
		"gsx":    a.gsx.Connected(),
		"public": len(pub) > 0,
		"code":   rdv && age >= 0 && age < 70,
		"invite": len(pub) > 0,
		"lan":    len(conf.LocalIPs()) > 0,
		"relay":  len(a.cfg.Net.RelayURLs) > 0,
		"peers":  a.link.PeerCount(),
	}
	switch {
	case !a.gsx.Connected():
		out["hint"] = "nothing works until GSX answers on " + a.cfg.GSX.URL + " - enable the Remote control server"
	case out["code"] == true:
		out["hint"] = "codes work: give your co-pilot your code, or type theirs"
	case len(pub) > 0:
		out["hint"] = "no rendezvous configured, so share the room string instead of just your code"
	default:
		out["hint"] = "STUN got nothing back - your router is probably blocking UDP, so a code or a string alone will not connect you; use a relay"
	}
	if age >= 70 && rdv {
		out["hint"] = "the rendezvous has not confirmed your code for " + strconv.FormatInt(age, 10) + "s - is that server still running?"
	}
	return out
}

func (a *App) mustStatus() any {
	out, _ := a.status(nil, nil)
	return out
}

func (a *App) StatusSnapshot() any {
	out, _ := a.status(nil, nil)
	return out
}

func (a *App) connect(r *http.Request, body map[string]any) (any, error) {
	if err := a.shared(); err != nil {
		return nil, err
	}
	roomStr := strings.ToUpper(strings.TrimSpace(asString(body["room"])))
	pass := asString(body["pass"])
	target := strings.TrimSpace(asString(body["target"]))
	if roomStr == "" {

		roomStr = a.cfg.Net.Room
		if roomStr == "" {
			roomStr = conf.RoomCode()
		}
	}
	a.joinRoom(roomStr, pass, false)
	if v, ok := body["name"].(string); ok && v != "" {
		a.cfg.Net.Name = v
		a.link.SetName(v)
	}
	_ = a.cfg.Save()

	done := []string{}
	if target != "" {
		a.mu.Lock()
		a.lastDial = target
		a.mu.Unlock()
		go func(t string) {
			if _, err := a.link.Dial(t); err != nil {
				a.log.Warn("could not connect to %s: %s", t, err)
				a.log.Info("trying the room by hole punch instead")
				a.link.Punch(a.link.Candidates())
			} else {
				a.log.Info("in room %s with a peer over %s", roomStr, t)
			}
		}(target)
		done = append(done, "dialling "+target)
	}
	if len(a.link.Peers()) == 0 && target == "" {
		a.log.Info("room %s open on port %d (UDP %d). Now: click Make invite, send it, and WAIT - your co-pilot pastes it.", roomStr, a.cfg.Net.Port, a.cfg.Net.UDPPort)
		a.link.LANSweep()
	}
	return map[string]any{"room": roomStr, "peers": a.link.Peers(), "note": strings.Join(done, ", ")}, nil
}

func (a *App) joinRoom(room, pass string, dropPeers bool) {
	if room == a.cfg.Net.Room && pass == a.cfg.Net.Pass {
		return
	}
	a.cfg.Net.Room, a.cfg.Net.Pass = room, pass
	a.link.SetRoom(room, pass)
	if dropPeers {
		a.link.DropAll()
	}
	if room != "" {
		for _, u := range a.cfg.Net.RelayURLs {
			go func(u string) { _ = a.link.Relay(u, room, pass) }(u)
		}
	}
	_ = a.cfg.Save()
}

func (a *App) disconnect(r *http.Request, body map[string]any) (any, error) {
	if err := a.shared(); err != nil {
		return nil, err
	}
	from := a.cfg.Net.Room
	fresh := conf.RoomCode()
	a.link.DropAll()
	a.cfg.Net.Room, a.cfg.Net.Pass = fresh, ""
	a.link.SetRoom(fresh, "")
	for _, u := range a.cfg.Net.RelayURLs {
		go func(u string) { _ = a.link.Relay(u, fresh, "") }(u)
	}
	_ = a.cfg.Save()
	a.log.Info("left %s - you are back in a room of your own: %s", from, a.cfg.Net.Room)
	return map[string]any{"ok": true, "room": a.cfg.Net.Room}, nil
}

func (a *App) invite(w http.ResponseWriter, r *http.Request) (any, error) {
	if err := a.shared(); err != nil {
		return nil, err
	}
	if a.cfg.Net.Room == "" {
		a.cfg.Net.Room = conf.RoomCode()
		a.link.SetRoom(a.cfg.Net.Room, a.cfg.Net.Pass)
		_ = a.cfg.Save()
	}
	if len(a.link.PublicAddresses()) == 0 {

		go a.link.Discover()
		for i := 0; i < 12 && len(a.link.PublicAddresses()) == 0; i++ {
			time.Sleep(200 * time.Millisecond)
		}
		if len(a.link.PublicAddresses()) == 0 {
			a.log.Warn("no public address found (all STUN servers silent): your router may block it. Send your LAN address or run a relay - see the guide.")
		}
	}
	sh := &share{Room: a.cfg.Net.Room, Pass: a.cfg.Net.Pass, Name: a.cfg.Net.Name, By: a.link.Origin(),
		TCP: a.cfg.Net.Port, UDPPort: a.cfg.Net.UDPPort, Cands: a.link.Candidates()}
	pub := a.link.PublicAddresses()
	if len(pub) > 0 {

		host, port, err := net.SplitHostPort(pub[0])
		if err == nil {
			mapped, e := strconv.Atoi(port)
			if e != nil {
				mapped = sh.UDPPort
			}

			sh.Addr = host
			sh.Public = net.JoinHostPort(host, strconv.Itoa(sh.TCP))
			sh.UDPPort = mapped
			sh.Cands = append(sh.Cands, net.JoinHostPort(host, strconv.Itoa(mapped)))
		}
	}
	if os.Getenv("GSXTEST_LOCAL") != "" && a.cfg.Net.Port > 0 {

		sh.Addr, sh.Public = "127.0.0.1", "127.0.0.1:"+strconv.Itoa(a.cfg.Net.Port)
		sh.TCP, sh.UDPPort = a.cfg.Net.Port, a.cfg.Net.UDPPort
		sh.Cands = []string{sh.Public, net.JoinHostPort("127.0.0.1", strconv.Itoa(a.cfg.Net.UDPPort))}
	}
	body, _ := json.Marshal(map[string]any{"v": 1, "room": sh.Room, "pass": sh.Pass, "name": sh.Name,
		"by": sh.By, "cands": sh.Cands, "tcp": sh.TCP, "udp": sh.UDPPort, "ts": nowTS()})
	code, form := sh.short(), "plain"
	switch {
	case sh.Public == "":

		code, form = Prefix+":"+base64.RawURLEncoding.EncodeToString(body), "long"
	case a.cfg.Link.Opaque:
		if sc, err := sh.opaque(); err == nil {
			code, form = sc, "scrambled"
		} else {
			a.log.Warn("could not scramble the invite, sending the plain one: %s", err)
		}
	}
	return map[string]any{
		"code": code, "form": form, "room": sh.Room, "public": pub, "cands": sh.Cands,
		"long": Prefix + ":" + base64.RawURLEncoding.EncodeToString(body),
		"dial": sh.Public, "leaksAddress": form == "plain",
	}, nil
}

func (a *App) paste(r *http.Request, body map[string]any) (any, error) {
	if err := a.shared(); err != nil {
		return nil, err
	}
	in := strings.TrimSpace(asString(body["code"]))

	if isBareCode(in) {
		if !a.link.HasRendezvous() {
			return nil, fmt.Errorf("a code alone needs a rendezvous server - set one in Settings, or paste the longer tdm1/... string")
		}

		a.joinRoom("", "", true)
		a.log.Info("calling code %s through the rendezvous", in)
		a.link.Call(in)
		return map[string]any{"code": in, "via": "rendezvous"}, nil
	}
	sh, err := parseShare(in)
	if err != nil {
		return nil, err
	}
	if sh.By != "" && sh.By == a.link.Origin() {
		return nil, fmt.Errorf("that is your own room string - you made it. Send it to your co-pilot; they paste it, you keep this window open")
	}
	if sh.Room != "" && a.cfg.Net.Room != "" && a.cfg.Net.Room != sh.Room {
		a.log.Info("leaving room %s for %s", a.cfg.Net.Room, sh.Room)
	}
	if sh.Room != "" {

		a.joinRoom(sh.Room, sh.Pass, true)
	}

	ws, udp := sh.dialTargets()
	if sh.Local {
		a.log.Info("that string points at %s, a local address - fine on the same network, and from another city it cannot be the reason it works", sh.Addr)
	}
	a.link.LANSweep()
	a.log.Info("joining room %s - dial %s, punch %s", orDash(sh.Room), strings.Join(ws, ", "), strings.Join(udp, ", "))
	a.mu.Lock()
	a.lastDial = strings.Join(ws, " ")
	a.mu.Unlock()
	go func() {

		a.link.Punch(udp)
		for _, c := range ws {
			if _, err := a.link.Dial(c); err == nil {
				a.log.Info("connected over %s", c)
				return
			}
		}

		for i := 0; i < 12 && len(a.link.Peers()) == 0; i++ {
			time.Sleep(2 * time.Second)
			a.link.Punch(udp)
			for _, c := range ws {
				if _, err := a.link.Dial(c); err == nil {
					a.log.Info("connected over %s on attempt %d", c, i+2)
					return
				}
			}
			if i == 5 {
				a.log.Warn("still nobody in room %s - punching; if both routers block UDP, run a relay (guide)", sh.Room)
			}
		}
		if len(a.link.Peers()) == 0 {
			a.log.Warn("could not reach that room. Check the code is the CURRENT one (an old invite carries an old address), then try the relay path in the guide.")
		}
	}()
	return map[string]any{"room": sh.Room, "dial": ws, "punch": udp, "peers": a.link.Peers()}, nil
}

func isBareCode(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z') && !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func hostPort(ext string, fallbackPort int) string {
	h, p, err := net.SplitHostPort(ext)
	if err != nil {
		return ext
	}
	_ = p
	return net.JoinHostPort(h, strconv.Itoa(fallbackPort))
}

func orDash(s string) string {
	if s == "" {
		return "(no code)"
	}
	return s
}

func (a *App) note(r *http.Request, body map[string]any) (any, error) {
	if err := a.shared(); err != nil {
		return nil, err
	}
	text := asString(body["text"])
	if text == "" {
		return nil, fmt.Errorf("empty note")
	}
	a.engine.Note(text)
	a.log.Info("[out] %s", text)
	return map[string]any{"ok": true}, nil
}

func (a *App) trigger(r *http.Request, body map[string]any) (any, error) {
	name := asString(body["name"])
	if name == "" {
		return nil, fmt.Errorf("no service named")
	}
	ok, tried := a.gsx.TriggerService(name)
	if !ok {
		return nil, fmt.Errorf("GSX refused %s (%s)", name, strings.Join(tried, ", "))
	}
	return map[string]any{"ok": true, "name": name}, nil
}

func (a *App) dumpState(w http.ResponseWriter, r *http.Request) (any, error) {
	st := a.engine.GSXState()
	return map[string]any{
		"services":  st["services"],
		"menu":      st["menu"],
		"menuShown": st["menuShown"],
		"state":     st["state"],
		"stateText": st["stateText"],
		"airport":   st["airport"],
		"aircraft":  st["aircraft"],
		"parking":   st["parking"],
		"topKeys":   a.gsx.TopKeys(),
	}, nil
}

func (a *App) probeOnce(r *http.Request, body map[string]any) (any, error) {
	ph := a.engine.Phases()
	rows := []map[string]any{}
	names := []string{}
	for k := range ph {
		names = append(names, k)
	}
	sortStrings(names)
	for _, k := range names {
		p := ph[k]
		rows = append(rows, map[string]any{"key": k, "state": p.State, "canonical": p.Canonical,
			"verb": p.KnownVerb, "phase": p.PhaseHash, "label": p.Label})
	}
	return map[string]any{
		"connected": a.gsx.Connected(), "simReady": a.gsx.SimReady(), "hash": a.engine.StateHash(),
		"rows": rows, "unknown": syn.UnknownStates(), "topKeys": a.gsx.TopKeys(),
		"stats": a.gsx.Stats(), "shapes": a.gsx.Shapes(),
	}, nil
}

func (a *App) saveConfig(r *http.Request, body map[string]any) (any, error) {
	need := false
	if v, ok := body["gsxUrl"].(string); ok && v != "" && v != a.cfg.GSX.URL {
		a.cfg.GSX.URL = v
		a.gsx.SetURL(v)
		need = true
	}
	if v, ok := body["name"].(string); ok {
		a.cfg.Net.Name = v
		a.link.SetName(v)
		a.engine.SetName(v)
	}
	if v, ok := body["port"]; ok && int(asFloat(v)) != a.cfg.Net.Port {
		a.cfg.Net.Port = int(asFloat(v))
		need = true
	}
	if v, ok := body["udpPort"]; ok && int(asFloat(v)) != a.cfg.Net.UDPPort {
		a.cfg.Net.UDPPort = int(asFloat(v))
		need = true
	}
	if v, ok := body["role"].(string); ok {
		a.cfg.Sync.Role = v
		a.engine.SetRole(v)
	}
	if v, ok := body["reconcileSeconds"]; ok {
		a.cfg.Sync.ReconcileSeconds = int(asFloat(v))
		a.engine.SetReconcile(int(asFloat(v)))
	}
	if v, ok := body["gsxSync"].(bool); ok {
		a.cfg.Sync.GsxSync = v
		a.engine.SetGsxSync(v)
	}
	if v, ok := body["paused"].(bool); ok {
		a.cfg.Sync.Paused = v
		a.engine.SetPaused(v)
	}
	if v, ok := body["autoStop"]; ok {
		b, _ := v.(bool)
		a.cfg.Sync.AutoStop = b
		a.engine.SetAutoStop(b)
	}
	if v, ok := body["rendezvous"].(string); ok {
		a.cfg.Net.Rendezvous = strings.TrimSpace(v)
		go a.ensureRendezvous()
	}
	if v, ok := body["relay"].(string); ok {
		v = strings.TrimSpace(v)
		list := []string{}
		if v != "" {
			list = []string{v}
		}
		a.cfg.Net.RelayURLs = list
		for _, u := range list {
			go func(u string) {
				if err := a.link.Relay(u, a.cfg.Net.Room, a.cfg.Net.Pass); err != nil {
					a.log.Warn("relay %s refused us: %s", u, err)
				}
			}(u)
		}
	}
	if v, ok := body["level"].(string); ok {
		a.cfg.Log.Level = v
		a.log.SetLevel(v)
	}
	if v, ok := body["checkUpdates"].(bool); ok {
		a.cfg.UI.NoUpdateCheck = !v
		if v {
			go a.updateNow()
		}
	}

	if v, ok := body["opaque"].(bool); ok {
		a.cfg.Link.Opaque = v
	}
	if v, ok := body["hide"].(bool); ok {
		a.cfg.Link.Hide = v
	}
	if v, ok := body["warnAck"].(bool); ok && v {
		a.cfg.Link.WarnAck = true
	}
	_ = a.cfg.Save()
	return map[string]any{"ok": true, "restartNeeded": need}, nil
}

func mustJSON(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return buf.Bytes()
}

func decodeInvite(code string) (map[string]any, error) {
	code = strings.TrimSpace(code)
	raw := code
	if strings.HasPrefix(code, Prefix+":") || strings.HasPrefix(code, legacy+":") {
		raw = strings.TrimPrefix(normalize(code), Prefix+":")
	} else if i := strings.LastIndex(code, ":"); i > 0 && strings.Contains(code, ".") == false && !strings.Contains(code, "/") {
		raw = code[i+1:]
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {

		return map[string]any{"room": "", "cands": []any{code}}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("that is not a gsx-sync invite")
	}
	return out, nil
}

func withoutScheme(in []string) []string {
	out := []string{}
	for _, s := range in {
		s = strings.TrimPrefix(s, "ws://")
		s = strings.TrimPrefix(s, "wss://")
		if i := strings.LastIndex(s, "/"); i > 0 {
			s = s[:i]
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func ipPort(s string) bool {
	host, port, err := net.SplitHostPort(s)
	if err != nil || host == "" {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n < 65536
}

func asString(v any) string { s, _ := v.(string); return s }
func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	}
	return 0
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func modeOf(c *conf.Config) string {
	if c.Net.Room == "" {
		return "solo"
	}
	return "room"
}

const Version = "1.1.0"

func selfSent(cands []string, hosts, ports map[string]bool) string {
	for _, c := range cands {
		c = strings.TrimPrefix(strings.TrimPrefix(c, "ws://"), "wss://")
		host, port, err := net.SplitHostPort(c)
		if err != nil || port == "" {
			continue
		}
		if hosts[host] && ports[port] {
			return host + ":" + port
		}
	}
	return ""
}

func (a *App) shared() error {
	if a.cfg.SharedCockpit() {
		return nil
	}
	return errors.New("shared cockpit is switched off - turn it on in Settings to invite anyone")
}
