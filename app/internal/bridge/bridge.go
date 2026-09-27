package bridge

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"tandem/internal/aircraft"
	"tandem/internal/logx"
)

type Server struct {
	log  *logx.Log
	port int

	mu        sync.Mutex
	conns     map[*websocket.Conn]bool
	vars      map[string]any
	title     string
	tail      string
	watched   map[string]bool
	connected bool
	seen      time.Time

	onChange func(map[string]any)
	changed  chan map[string]any
	srv      *http.Server
}

func New(port int, log *logx.Log) *Server {
	return &Server{log: log, port: port, conns: map[*websocket.Conn]bool{},
		vars: map[string]any{}, watched: map[string]bool{}}
}

func (s *Server) Start(onChange func(map[string]any)) error {
	s.onChange = onChange
	if s.changed == nil {
		s.changed = make(chan map[string]any, 16)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)
	s.srv = &http.Server{Addr: "127.0.0.1:" + strconv.Itoa(s.port)}
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return err
	}
	s.port = ln.Addr().(*net.TCPAddr).Port
	s.srv.Handler = mux
	go s.srv.Serve(ln)
	return nil
}

func (s *Server) Stop() {
	if s.srv != nil {
		_ = s.srv.Close()
	}
}

func (s *Server) Port() int { return s.port }
func (s *Server) URL() string {
	return "ws://127.0.0.1:" + strconv.Itoa(s.port) + "/"
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.conns[c] = true
	s.connected = true
	s.seen = time.Now()
	s.mu.Unlock()
	s.log.Info("the sim is talking to Tandem directly, from %s", r.RemoteAddr)
	s.hello(c)
	go s.read(c)
}

func (s *Server) hello(c *websocket.Conn) {
	s.mu.Lock()
	want := map[string]bool{}
	for k := range s.watched {
		want[k] = true
	}
	snap := map[string]any{"type": "snapshot", "aircraft": s.aircraftLocked(), "vars": clone(s.vars),
		"wanted": want}
	s.mu.Unlock()
	write(c, snap)
}

func (s *Server) read(c *websocket.Conn) {
	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			s.mu.Lock()
			delete(s.conns, c)
			s.connected = len(s.conns) > 0
			s.mu.Unlock()
			s.log.Info("the sim let go of the bridge")
			return
		}
		var f struct {
			Type  string          `json:"type"`
			Path  string          `json:"path"`
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		switch f.Type {
		case "subscribe":
			s.hello(c)
		case "patch":
			var v any
			if len(f.Value) > 0 {
				_ = json.Unmarshal(f.Value, &v)
			}
			s.receive(f.Path, v)
		}
	}
}

func (s *Server) receive(path string, v any) {
	switch {
	case path == "aircraft.title":
		s.setTitle(asStr(v))
	case path == "aircraft.tail":
		s.mu.Lock()
		s.tail = asStr(v)
		s.mu.Unlock()
	case len(path) > 5 && path[:5] == "vars.":
		name := path[5:]
		s.mu.Lock()
		s.vars[name] = v
		s.mu.Unlock()
		out := map[string]any{name: v}
		if s.onChange != nil {
			s.onChange(out)
		}
		select {
		case s.changed <- out:
		default:
		}
	}
}

func (s *Server) setTitle(t string) {
	s.mu.Lock()
	s.title = t
	s.mu.Unlock()
}

func (s *Server) Watch(names []string) {
	s.mu.Lock()
	s.watched = map[string]bool{}
	for _, n := range names {
		s.watched[n] = true
	}
	s.mu.Unlock()
}

func (s *Server) Write(name string, v any) {
	s.mu.Lock()
	s.vars[name] = v
	s.mu.Unlock()
	s.fan(map[string]any{"type": "patch", "path": "vars." + name, "value": v, "write": true})
}

func (s *Server) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

func (s *Server) Vars() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.vars)
}

func (s *Server) aircraftLocked() map[string]any {
	m := map[string]any{}
	if s.title != "" {
		m["title"] = s.title
	}
	if s.tail != "" {
		m["tail"] = s.tail
	}
	return m
}

func (s *Server) Aircraft() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.title, s.tail
}

func (s *Server) Detect(pkgs []aircraft.Package) (aircraft.Package, string, bool) {
	title, _ := s.Aircraft()
	if title == "" {
		return aircraft.Package{}, "", false
	}
	if p, ok := aircraft.Guess(pkgs, title); ok {
		return p, "the sim said so", true
	}
	return aircraft.Package{}, "", false
}

func (s *Server) fan(m map[string]any) {
	s.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		write(c, m)
	}
}

func write(c *websocket.Conn, m map[string]any) {
	b, _ := json.Marshal(m)
	_ = c.WriteMessage(websocket.TextMessage, b)
}

func clone(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func asStr(v any) string { s, _ := v.(string); return s }
