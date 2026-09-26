package discover

import (
	"encoding/json"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tandem/internal/logx"
)

const (
	proto      = "gsxd1"
	peerIDLen  = 8
	tableTTL   = 5 * time.Minute
	cleanupSec = 60
)

type Packet struct {
	P []string
}

func (p Packet) S() string { return strings.Join(p.P, "|") }

func Parse(b []byte) (Packet, bool) {
	fields := strings.Split(strings.TrimSpace(string(b)), "|")
	if len(fields) < 2 || fields[0] != proto {
		return Packet{}, false
	}
	return Packet{fields}, true
}

type entry struct {
	ext, lan string
	seen     time.Time
}

type Server struct {
	mu     sync.Mutex
	tbl    map[string]*entry
	log    *logx.Log
	pcp    *net.UDPConn
	addr   string
	Stats  Stats
	closed chan struct{}
}

type Stats struct {
	Reg     int            `json:"reg"`
	Calls   int            `json:"calls"`
	Intro   int            `json:"intros"`
	Rejects int            `json:"rejects"`
	Online  int            `json:"online"`
	Tbl     map[string]any `json:"table"`
}

func New(log *logx.Log) *Server {
	return &Server{tbl: map[string]*entry{}, log: log, closed: make(chan struct{})}
}

func (s *Server) Listen(bind string, port int) error {
	udpAddr, err := net.ResolveUDPAddr("udp", bind+":"+strconv.Itoa(port))
	if err != nil {
		return err
	}
	pc, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}
	s.pcp = pc
	s.addr = pc.LocalAddr().String()
	s.log.Info("rendezvous on udp://%s - give your co-pilot your 8-character code", s.addr)
	go s.readLoop()
	go s.sweep()
	return nil
}

func (s *Server) Stop() {
	close(s.closed)
	if s.pcp != nil {
		_ = s.pcp.Close()
	}
}

func (s *Server) readLoop() {
	buf := make([]byte, 2048)
	for {
		n, from, err := s.pcp.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-s.closed:
				return
			default:
				time.Sleep(100 * time.Millisecond)
				continue
			}
		}
		p, ok := Parse(buf[:n])
		if !ok {
			continue
		}
		switch p.P[1] {
		case "REG":
			s.handleReg(p, from)
		case "CALL":
			s.handleCall(p, from)
		case "PING":
			s.send(from, Packet{[]string{proto, "PONG", p.P[2]}})
		}
	}
}

func (s *Server) handleReg(p Packet, from *net.UDPAddr) {
	if len(p.P) < 3 || !validID(p.P[2]) {
		s.reject(from, "BAD_ID")
		return
	}
	id := p.P[2]
	lan := ""
	if len(p.P) > 3 {
		lan = p.P[3]
	}
	ext := from.String()
	s.mu.Lock()
	e, known := s.tbl[id]
	if e == nil {
		e = &entry{}
		s.tbl[id] = e
	}
	e.ext, e.lan, e.seen = ext, lan, time.Now()
	s.Stats.Reg++
	s.Stats.Online = len(s.tbl)
	s.mu.Unlock()
	if !known {
		s.log.Info("code %s registered at %s (%d online)", id, ext, s.Online())
	}
	s.send(from, Packet{[]string{proto, "REGOK", id, ext, lan}})
}

func (s *Server) handleCall(p Packet, from *net.UDPAddr) {
	if len(p.P) < 4 || !validID(p.P[2]) || !validID(p.P[3]) {
		s.reject(from, "BAD_ID")
		return
	}
	self, target := p.P[2], p.P[3]
	s.mu.Lock()
	s.Stats.Calls++
	me, okMe := s.tbl[self]
	peer, okPeer := s.tbl[target]
	if okMe && me != nil {

		me.ext, me.seen = from.String(), time.Now()
	}
	switch {
	case !okMe || me == nil:
		s.mu.Unlock()
		s.reject(from, "NOT_FOUND")
		s.log.Warn("call from unknown code %s", self)
		return
	case self == target:
		s.mu.Unlock()
		s.reject(from, "SELF")
		return
	case !okPeer || peer == nil:
		s.mu.Unlock()
		s.reject(from, "NOT_FOUND")
		s.log.Info("%s called %s - that code has not registered (is their app open?)", self, target)
		return
	default:

		selfExt, selfLan := me.ext, me.lan
		peerExt, peerLan := peer.ext, peer.lan
		s.Stats.Intro++
		s.mu.Unlock()
		s.log.Info("introducing %s (%s) <-> %s (%s)", self, selfExt, target, peerExt)
		if a, err := net.ResolveUDPAddr("udp", peerExt); err == nil {
			s.send(a, Packet{[]string{proto, "INTRO", target, self, selfExt, selfLan, self}})
		}
		s.send(from, Packet{[]string{proto, "INTRO", self, target, peerExt, peerLan, target}})
	}
}

func (s *Server) send(to *net.UDPAddr, p Packet) {
	_, _ = s.pcp.WriteToUDP([]byte(p.S()), to)
}

func (s *Server) reject(to *net.UDPAddr, code string) {
	s.mu.Lock()
	s.Stats.Rejects++
	s.mu.Unlock()
	s.send(to, Packet{[]string{proto, "ERR", code}})
}

func (s *Server) Online() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tbl)
}

func (s *Server) sweep() {
	t := time.NewTicker(cleanupSec * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.closed:
			return
		case now := <-t.C:
			s.mu.Lock()
			for id, e := range s.tbl {
				if now.Sub(e.seen) > tableTTL {
					delete(s.tbl, id)
					s.log.Debug("code %s expired", id)
				}
			}
			s.mu.Unlock()
		}
	}
}

func (s *Server) Snapshot() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.Stats
	out.Online = len(s.tbl)
	out.Tbl = map[string]any{}
	for id, e := range s.tbl {
		out.Tbl[id] = map[string]any{"ext": e.ext, "lan": e.lan, "age": int(time.Since(e.seen).Seconds())}
	}
	return out
}

func validID(s string) bool {
	if len(s) != peerIDLen {
		return false
	}
	for _, c := range []byte(s) {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

type Client struct {
	Addr    *net.UDPAddr
	pc      *net.UDPConn
	ID      string
	Log     *logx.Log
	OnIntro func(peerID, ext, lan string)
	mu      sync.Mutex
	closed  chan struct{}
}

func NewClient(server, id string, log *logx.Log) (*Client, error) {
	u, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return nil, err
	}
	pc, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	c := &Client{Addr: u, pc: pc, ID: id, Log: log, closed: make(chan struct{})}
	go c.read()
	return c, nil
}

func (c *Client) read() {
	buf := make([]byte, 2048)
	for {
		n, _, err := c.pc.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-c.closed:
				return
			default:
				continue
			}
		}
		p, ok := Parse(buf[:n])
		if !ok {
			continue
		}
		switch p.P[1] {
		case "REGOK":
			if len(p.P) > 3 {
				c.Log.Debug("rendezvous sees us as %s", p.P[3])
			}
		case "INTRO":

			if len(p.P) > 5 && p.P[2] == c.ID && c.OnIntro != nil {
				c.OnIntro(p.P[3], p.P[4], p.P[5])
			}
		case "ERR":
			code := ""
			if len(p.P) > 2 {
				code = p.P[2]
			}
			c.Log.Warn("rendezvous refused us: %s", code)
		}
	}
}

func (c *Client) Announce(lan []string) {
	stop := make(chan struct{})
	_ = stop
	t := time.NewTicker(20 * time.Second)
	first := make(chan struct{}, 1)
	first <- struct{}{}
	go func() {
		for {
			select {
			case <-c.closed:
				t.Stop()
				return
			case <-t.C:
				c.write(Packet{[]string{proto, "REG", c.ID, strings.Join(lan, ",")}})
			}
		}
	}()
	c.write(Packet{[]string{proto, "REG", c.ID, strings.Join(lan, ",")}})
}

func (c *Client) Call(target string) {
	c.write(Packet{[]string{proto, "CALL", c.ID, strings.ToUpper(target)}})
}

func (c *Client) write(p Packet) {

	_ = c.pc.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.pc.WriteTo([]byte(p.S()), c.Addr); err != nil {
		c.Log.Warn("rendezvous write failed: %s", err)
	}
}

func (c *Client) Close() {
	close(c.closed)
	_ = c.pc.Close()
}

var _ = json.Marshal
var _ = sort.Strings
