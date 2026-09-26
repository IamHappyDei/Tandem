package fake

import (
	"encoding/json"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	Idle   = "Not requested"
	Active = "In progress"
	Done   = "Completed"
)

var menuRows = []string{"Boarding", "Deboarding", "Refueling", "Catering", "GPU", "Operate Jetways", "Chocks On", "Pushback"}

var rowToService = map[string]string{
	"Boarding": "Boarding", "Deboarding": "Deboarding", "Refueling": "Refueling", "Catering": "Catering",
	"GPU": "GPU", "Operate Jetways": "Jetways", "Chocks On": "Chocks", "Pushback": "Pushback",
}

type Row struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	State       string `json:"state"`
	StateRaw    int    `json:"stateRaw"`
	StateText   string `json:"stateText"`
	CanTrigger  bool   `json:"canTrigger"`
	ProgressTxt string `json:"progressText"`
}

type Server struct {
	mu         sync.Mutex
	stateClass []string
	Addr       string
	port       int
	conns      map[*websocket.Conn]bool
	services   map[string]*Row
	menuOpen   bool
	entries    []string
	strict     bool
	cmds       []Cmd
	up         websocket.Upgrader
	srv        *http.Server
}

type Cmd struct {
	Verb string         `json:"verb"`
	Args map[string]any `json:"args"`
}

func New(port int, strict bool) *Server {
	s := &Server{
		port: port, strict: strict, conns: map[*websocket.Conn]bool{}, services: map[string]*Row{},
		entries: menuRows, stateClass: make([]string, len(menuRows)),
		up: websocket.Upgrader{},
	}
	for _, n := range []string{"Boarding", "Deboarding", "Refueling", "Catering", "GPU", "Jetways", "Chocks", "Pushback"} {
		s.services[n] = &Row{ID: n, DisplayName: n, State: "available", StateRaw: 1, StateText: n + " service can be requested", CanTrigger: true}
	}
	return s
}

func (s *Server) RealShape() {
	s.mu.Lock()
	defer s.mu.Unlock()

}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		c, err := s.up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns[c] = true
		s.mu.Unlock()
		go s.read(c)
	})
	s.srv = &http.Server{Addr: "127.0.0.1:" + itoa(s.port), Handler: mux}
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return err
	}
	s.Addr = ln.Addr().String()
	go s.srv.Serve(ln)
	return nil
}

func (s *Server) Stop() {
	s.mu.Lock()
	for c := range s.conns {
		c.Close()
	}
	s.conns = map[*websocket.Conn]bool{}
	s.mu.Unlock()
	if s.srv != nil {
		s.srv.Close()
	}
}

func (s *Server) Port() int { return s.port }

func (s *Server) read(c *websocket.Conn) {
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		c.Close()
	}()
	for {
		var m map[string]any
		if err := c.ReadJSON(&m); err != nil {
			return
		}
		switch m["type"] {
		case "subscribe":
			_ = c.WriteJSON(map[string]any{"type": "hello", "protocol": 1, "engine": "couatl", "sim": "MSFS",
				"gsxRunning": true, "authRequired": false,
				"capabilities": []string{"state", "services", "menu", "prompts", "billing"}})
			_ = c.WriteJSON(map[string]any{"ok": true, "type": "result"})
			s.mu.Lock()
			snap := map[string]any{"type": "snapshot", "v": 1, "ts": time.Now().UnixMilli()}
			snap["services"] = s.rowsLocked()
			snap["menuShown"] = s.menuOpen
			snap["menu"] = map[string]any{"title": "Ramp Manager", "entries": s.entries, "disabled": []bool{}, "stateClass": []string{}}
			snap["statusHtml"] = "<div>status</div>"
			snap["message"] = map[string]any{"visible": false, "text": ""}
			s.mu.Unlock()
			_ = c.WriteJSON(snap)
		case "command":
			s.command(c, m)
		default:
			_ = c.WriteJSON(map[string]any{"type": "result", "ok": false, "error": map[string]any{"code": "bad_message"}})
		}
	}
}

func (s *Server) command(c *websocket.Conn, m map[string]any) {
	verb, _ := m["verb"].(string)
	args, _ := m["args"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}
	s.mu.Lock()
	s.cmds = append(s.cmds, Cmd{Verb: verb, Args: args})
	s.mu.Unlock()
	ok := true
	var err map[string]any
	switch verb {
	case "service.trigger":
		given := ""
		if s.strict {
			given = asS(args["name"])
		} else {
			for _, k := range []string{"service", "name", "id", "command"} {
				if v := asS(args[k]); v != "" {
					given = v
					break
				}
			}
		}
		key := s.resolve(given)
		if key == "" {
			ok, err = false, map[string]any{"code": "unknown_service", "message": given}
		} else {
			s.Toggle(key)
		}
	case "menu.toggle":
		s.mu.Lock()
		s.menuOpen = !s.menuOpen
		open := s.menuOpen
		s.mu.Unlock()
		s.broadcast(map[string]any{"type": "patch", "path": "/menuShown", "value": open})
	case "menu.pick":
		idx := int(asN(args["index"]))
		s.mu.Lock()
		var label string
		if idx >= 0 && idx < len(s.entries) {
			label = s.entries[idx]
		}

		s.menuOpen = true
		open := s.menuOpen
		s.mu.Unlock()
		s.broadcast(map[string]any{"type": "patch", "path": "/menuShown", "value": open})
		key := rowToService[label]
		if key == "" {
			ok, err = false, map[string]any{"code": "bad_index"}
		} else {
			s.Toggle(key)
		}
	case "command.run", "input.submit", "invoice.seen":

	default:
		ok, err = false, map[string]any{"code": "unknown_verb", "message": verb}
	}
	_ = c.WriteJSON(map[string]any{"type": "result", "ok": ok, "error": err})
}

var loose = regexp.MustCompile(`[^a-z0-9]+`)

func (s *Server) resolve(given string) string {
	g := strings.ToLower(strings.TrimSpace(given))
	if g == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.services[given]; ok {
		return given
	}
	for label, key := range rowToService {
		if strings.ToLower(label) == g {
			return key
		}
	}
	for _, label := range s.entries {
		if strings.Contains(strings.ToLower(label), g) || strings.Contains(g, strings.ToLower(rowToService[label])) {
			return rowToService[label]
		}
	}
	for k := range s.services {
		if strings.ToLower(k) == g || strings.Contains(looseStr(k), g) || strings.Contains(g, looseStr(k)) {
			return k
		}
	}
	return ""
}

func looseStr(s string) string { return loose.ReplaceAllString(strings.ToLower(s), "") }

func (s *Server) Toggle(key string) string {
	s.mu.Lock()
	row, ok := s.services[key]
	if !ok {
		row = &Row{ID: key, DisplayName: key}
		s.services[key] = row
	}
	next := Active
	if row.StateRaw == 5 {
		next = Idle
	}
	row.StateText = key + " service can be requested"
	if next == Active {
		row.State = "performing"
		row.StateRaw = 5
		row.CanTrigger = false
		row.StateText = key + " service is being performed"
	} else {
		row.State = "available"
		row.StateRaw = 1
		row.CanTrigger = true
	}
	rows := s.rowsLocked()
	s.mu.Unlock()
	s.broadcast(map[string]any{"type": "patch", "path": "/services", "value": rows})
	return next
}

func (s *Server) Progress(key string, pct int) {
	s.mu.Lock()
	row := s.services[key]
	if row == nil {
		s.mu.Unlock()
		return
	}
	row.ProgressTxt = itoa(pct) + "% complete"
	rows := s.rowsLocked()
	s.mu.Unlock()
	s.broadcast(map[string]any{"type": "patch", "path": "/services", "value": rows})
}

func (s *Server) SetRaw(key, stateText string) {
	s.mu.Lock()
	row := s.services[key]
	if row == nil {
		row = &Row{ID: key, DisplayName: key}
		s.services[key] = row
	}
	row.StateText = stateText
	if stateText == Active {
		row.State, row.StateRaw, row.CanTrigger = "performing", 5, false
	} else {
		row.State, row.StateRaw, row.CanTrigger = "available", 1, true
	}
	rows := s.rowsLocked()
	s.mu.Unlock()
	s.broadcast(map[string]any{"type": "patch", "path": "/services", "value": rows})
}

func (s *Server) OpenMenu(open bool) {
	s.mu.Lock()
	s.menuOpen = open
	s.mu.Unlock()
	s.broadcast(map[string]any{"type": "patch", "path": "/menuShown", "value": open})
}

func (s *Server) StateOf(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.services[key]; ok {
		if r.StateRaw == 5 {
			return Active
		}
		return Idle
	}
	return ""
}

func (s *Server) CountOf(verb string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.cmds {
		if c.Verb == verb {
			n++
		}
	}
	return n
}

func (s *Server) Cmds() []Cmd {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Cmd{}, s.cmds...)
}

func (s *Server) rowsLocked() []any {
	out := []any{}
	for _, k := range []string{"Boarding", "Deboarding", "Refueling", "Catering", "GPU", "Jetways", "Chocks", "Pushback"} {
		if r, ok := s.services[k]; ok {
			b, _ := json.Marshal(r)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			out = append(out, m)
		}
	}
	return out
}

func (s *Server) broadcast(m map[string]any) {
	s.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.SetWriteDeadline(time.Now().Add(3 * time.Second))
		_ = c.WriteJSON(m)
	}
}

func asS(v any) string  { s, _ := v.(string); return s }
func asN(v any) float64 { f, _ := v.(float64); return f }

func itoa(i int) string { return strconv.Itoa(i) }

func (s *Server) SetMenu(entries []string) {
	s.mu.Lock()
	s.entries = append([]string{}, entries...)
	s.stateClass = make([]string, len(entries))
	for i := range s.stateClass {
		s.stateClass[i] = "gsxMenuItemEnabled"
	}
	m := map[string]any{"type": "patch", "path": "/menu", "value": map[string]any{
		"title": "Ramp Manager", "entries": append([]string{}, s.entries...),
		"disabled": []bool{}, "stateClass": append([]string{}, s.stateClass...)}}
	s.mu.Unlock()
	s.send(m)
}

func (s *Server) Pick(i int) {
	s.mu.Lock()
	var label string
	if i >= 0 && i < len(s.entries) {
		label = s.entries[i]
	}
	s.cmds = append(s.cmds, Cmd{Verb: "menu.pick", Args: map[string]any{"index": float64(i)}})
	frame := s.pickFrameLocked(i)
	open := true
	if i >= 0 && i < len(s.entries) {
		s.menuOpen = true
	}
	s.mu.Unlock()
	s.send(frame)
	s.send(map[string]any{"type": "patch", "path": "/menuShown", "value": open})
	if key := rowToService[label]; key != "" {
		s.Toggle(key)
	}
}

func (s *Server) pickFrameLocked(i int) map[string]any {
	for len(s.stateClass) < len(s.entries) {
		s.stateClass = append(s.stateClass, "gsxMenuItemEnabled")
	}
	if i >= 0 && i < len(s.stateClass) {
		for j := range s.stateClass {
			s.stateClass[j] = "gsxMenuItemEnabled"
		}
		s.stateClass[i] = "gsxMenuItemPicked"
	}
	return map[string]any{"type": "patch", "path": "/menu", "value": map[string]any{
		"title": "Ramp Manager", "entries": append([]string{}, s.entries...),
		"disabled": []bool{}, "stateClass": append([]string{}, s.stateClass...)}}
}

func (s *Server) SetEntries(entries []string) {
	s.mu.Lock()
	s.entries = append([]string{}, entries...)
	s.stateClass = make([]string, len(entries))
	for i := range s.stateClass {
		s.stateClass[i] = "gsxMenuItemEnabled"
	}
	m := map[string]any{"type": "patch", "path": "/menu", "value": map[string]any{
		"title": "Ramp Manager", "entries": append([]string{}, s.entries...),
		"disabled": []bool{}, "stateClass": append([]string{}, s.stateClass...)}}
	s.mu.Unlock()
	s.send(m)
}

func (s *Server) send(m map[string]any) { s.broadcast(m) }
