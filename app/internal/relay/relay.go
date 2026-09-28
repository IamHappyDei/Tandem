package relay

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"tandem/internal/logx"
	"tandem/internal/room"
)

type member struct {
	conn    *websocket.Conn
	origin  string
	name    string
	room    string
	joined  int64
	writeMu sync.Mutex
}

func (m *member) send(v any) {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	_ = m.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = m.conn.WriteJSON(v)
}

type Server struct {
	mu       sync.Mutex
	mem      map[*member]bool
	log      *logx.Log
	up       websocket.Upgrader
	srv      *http.Server
	RoomPass map[string]string
	stats    Stats
}

type Stats struct {
	Rx       int `json:"rx"`
	Tx       int `json:"tx"`
	Joins    int `json:"joins"`
	Dropped  int `json:"dropped"`
	Rejected int `json:"rejected"`
}

func New(port int, bind string, log *logx.Log) *Server {
	if bind == "" {
		bind = "0.0.0.0"
	}
	s := &Server{
		mem: map[*member]bool{}, log: log, up: websocket.Upgrader{}, RoomPass: map[string]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		s.mu.Lock()
		n := len(s.mem)
		s.mu.Unlock()
		_, _ = w.Write([]byte("tandem relay up - " + itoa(n) + " cockpit(s) connected\n"))
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		st := s.Stats()
		s.mu.Lock()
		n := len(s.mem)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"connected":` + itoa(n) +
			`,"rx":` + itoa(st.Rx) + `,"tx":` + itoa(st.Tx) +
			`,"joins":` + itoa(st.Joins) + `}`))
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := s.up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		go s.handle(c, r.RemoteAddr)
	})
	s.srv = &http.Server{Addr: bind + ":" + itoa(port), Handler: mux}
	return s
}

func (s *Server) Stats() Stats { s.mu.Lock(); defer s.mu.Unlock(); return s.stats }

func (s *Server) Listen() error {
	if err := s.srv.ListenAndServe(); err != nil && !strings.Contains(err.Error(), "Server closed") {
		return err
	}
	return nil
}

func (s *Server) Stop() { s.srv.Close() }

func (s *Server) handle(c *websocket.Conn, addr string) {

	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	var first room.Msg
	if err := c.ReadJSON(&first); err != nil || first.T != "hello" {
		c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	m := &member{conn: c, origin: first.Origin, name: orDefault(first.Name, "peer"), room: strings.ToUpper(strings.TrimSpace(first.Room)), joined: time.Now().UnixMilli()}
	if m.room == "" {
		m.room = "DEFAULT"
	}
	s.mu.Lock()
	if pw, ok := s.RoomPass[m.room]; ok && pw != "" && first.Pass != pw {
		s.stats.Rejected++
		s.mu.Unlock()
		m.send(map[string]any{"t": "reject", "reason": "bad passphrase for room " + m.room})
		c.Close()
		return
	}
	s.mem[m] = true
	s.stats.Joins++
	peers := s.peerNamesLocked(m.room, m.origin)
	s.mu.Unlock()
	m.send(map[string]any{"t": "welcome", "ver": room.ProtoVersion, "origin": "relay", "name": "relay", "peers": peers})
	s.log.Info("room %s: %s joined (%s), %d in room", m.room, m.name, addr, len(peers)+1)

	defer func() {
		s.mu.Lock()
		delete(s.mem, m)
		s.mu.Unlock()
		s.log.Info("room %s: %s left", m.room, m.name)
		c.Close()
	}()

	for {
		var msg room.Msg
		if err := c.ReadJSON(&msg); err != nil {
			return
		}
		s.mu.Lock()
		s.stats.Rx++
		targets := make([]*member, 0, 4)
		for other := range s.mem {
			if other == m || other.room != m.room {
				continue
			}
			if msg.Origin != "" && msg.Origin == other.origin {
				continue
			}
			targets = append(targets, other)
		}
		s.mu.Unlock()
		if msg.T == "ping" {
			m.send(map[string]any{"t": "pong", "origin": "relay"})
			continue
		}
		msg.From = &room.PeerRef{Name: m.name, Origin: m.origin}
		msg.Via = "relay"
		for _, t := range targets {
			t.send(msg)
			s.mu.Lock()
			s.stats.Tx++
			s.mu.Unlock()
		}
		if len(targets) == 0 {
			s.mu.Lock()
			s.stats.Dropped++
			s.mu.Unlock()
		}
	}
}

func (s *Server) peerNamesLocked(roomName, exclude string) []string {
	out := []string{}
	for m := range s.mem {
		if m.room == roomName && m.origin != exclude {
			out = append(out, m.name)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Server) Rooms() map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string][]string{}
	for m := range s.mem {
		out[m.room] = append(out[m.room], m.name)
	}
	return out
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func itoa(i int) string { return strconv.Itoa(i) }
