package room

import (
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	udpMagic0 = 'G'
	udpMagic1 = '1'
	udpHead   = 5
	udpChunk  = 1100
)

type udpPath struct {
	addr   *net.UDPAddr
	origin string
	lastRX int64
	tr     *transport
	parts  map[int]*reassembled
}

type reassembled struct {
	total int
	got   map[int]bool
	buf   [][]byte
	at    int64
}

type udpSock struct {
	l        *Link
	pc       *net.UDPConn
	mu       sync.Mutex
	paths    map[string]*udpPath
	undo     chan struct{}
	cand     []string
	stunMu   sync.Mutex
	stunWait map[string]chan string
}

func (l *Link) startUDP() error {
	addr := net.JoinHostPort(bindHost(l.cfg.Bind), itoa(l.cfg.UDPPort))
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	u := &udpSock{l: l, pc: pc.(*net.UDPConn), paths: map[string]*udpPath{}, undo: make(chan struct{}), stunWait: map[string]chan string{}}
	l.udp = u
	l.log.Debug("udp on %s", addr)
	go u.readLoop()
	return nil
}

func (l *Link) stopUDP() {
	if l.udp != nil {
		close(l.udp.undo)
		l.udp.pc.Close()
	}
}

func bindHost(b string) string {
	if b == "" || b == "0.0.0.0" || b == "*" {
		return "0.0.0.0"
	}
	return b
}

func (u *udpSock) readLoop() {
	buf := make([]byte, 64*1024)
	for {
		_ = u.pc.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, from, err := u.pc.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				select {
				case <-u.undo:
					return
				default:
					continue
				}
			}
			return
		}
		u.incoming(from, buf[:n])
	}
}

func (u *udpSock) incoming(from net.Addr, b []byte) {
	if len(b) < 4 {
		return
	}
	if len(b) > 6 && string(b[:6]) == "gsxd1|" {
		u.l.handleRendezvous(from, string(b))
		return
	}
	if b[0] == 0x01 && b[1] == 0x01 {
		u.stunIngest(b)
		return
	}
	if b[0] != udpMagic0 || b[1] != udpMagic1 || len(b) < udpHead {
		return
	}
	total := int(b[3])
	idx := int(b[4])
	if total == 0 || idx >= total || len(b) < udpHead+6 {
		return
	}
	key := from.String()
	path := u.pathFor(key, from.(*net.UDPAddr))
	if path == nil {
		return
	}
	var m Msg
	if total == 1 {
		if err := json.Unmarshal(b[udpHead:], &m); err != nil {
			return
		}
	} else {
		full := path.join(idx, total, b[udpHead:])
		if full == nil {
			return
		}
		if err := json.Unmarshal(full, &m); err != nil {
			return
		}
	}
	u.mu.Lock()
	path.lastRX = time.Now().UnixMilli()
	tr := path.tr
	u.mu.Unlock()
	if tr == nil {
		tr = u.l.addTransport("udp", key, func(mm *Msg) error { return u.sendTo(path, mm) }, func() {}, false)
		path.tr = tr
	}
	u.l.handleFrame(tr, &m)
}

func (u *udpSock) pathFor(key string, addr *net.UDPAddr) *udpPath {
	u.mu.Lock()
	defer u.mu.Unlock()
	if p, ok := u.paths[key]; ok {
		return p
	}
	if len(u.paths) > 64 {
		return nil
	}
	p := &udpPath{addr: addr, lastRX: time.Now().UnixMilli(), parts: map[int]*reassembled{}}
	u.paths[key] = p
	return p
}

func (p *udpPath) join(idx, total int, b []byte) []byte {
	r, ok := p.parts[total]
	if !ok {
		r = &reassembled{total: total, got: map[int]bool{}, buf: make([][]byte, total), at: time.Now().UnixMilli()}
		p.parts = map[int]*reassembled{total: r}
	}
	if r.got[idx] {
		return nil
	}
	r.got[idx] = true
	r.buf[idx] = append([]byte(nil), b...)
	if len(r.got) < r.total {
		return nil
	}
	var out []byte
	for _, x := range r.buf {
		out = append(out, x...)
	}
	delete(p.parts, r.total)
	return out
}

func (u *udpSock) sendTo(p *udpPath, m *Msg) error {
	if p == nil || p.addr == nil {
		return errNoPath
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	for i, chunk := range chunkJSON(b, udpChunk) {
		head := []byte{udpMagic0, udpMagic1, 0, byte(len(chunk)), byte(i)}
		dgram := append(head, chunk...)
		_ = u.pc.SetWriteDeadline(time.Now().Add(3 * time.Second))
		if _, err := u.pc.WriteTo(dgram, p.addr); err != nil {
			return err
		}
	}
	return nil
}

func (l *Link) SendCandidate(addr, msgType string) {
	if l.udp == nil {
		return
	}
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return
	}
	m := l.envelope(&Msg{T: msgType, Name: l.cfg.Name, Room: l.cfg.Room})
	_ = l.udp.sendTo(l.udp.pathFor(ua.String(), ua), m)
}

func chunkJSON(b []byte, size int) [][]byte {
	if len(b) <= size {
		return [][]byte{b}
	}
	out := [][]byte{}
	for i := 0; i < len(b); i += size {
		end := i + size
		if end > len(b) {
			end = len(b)
		}
		out = append(out, b[i:end])
	}
	return out
}

func (l *Link) punchBeacon() {
	if l.udp == nil {
		return
	}
	l.udp.mu.Lock()
	paths := make([]*udpPath, 0, len(l.udp.paths))
	for _, p := range l.udp.paths {
		paths = append(paths, p)
	}
	l.udp.mu.Unlock()
	for _, p := range paths {
		_ = l.udp.sendTo(p, l.envelope(&Msg{T: "probe"}))
	}
	l.udp.mu.Lock()
	cand := append([]string{}, l.udp.cand...)
	l.udp.mu.Unlock()
	for _, c := range cand {
		l.SendCandidate(c, "probe")
	}
}

func (l *Link) Punch(addrs []string) {
	if l.udp == nil {
		return
	}
	l.udp.mu.Lock()
	l.udp.cand = append([]string{}, addrs...)
	l.udp.mu.Unlock()
	go func() {

		for i := 0; i < 12; i++ {
			for _, a := range addrs {
				l.SendCandidate(a, "hello")
			}
			time.Sleep(400 * time.Millisecond)
		}
	}()
}

func (l *Link) Candidates() []string {
	out := []string{}
	l.mu.Lock()
	pub := append([]string{}, l.public...)
	cfgPort := itoa(l.cfg.Port)
	l.mu.Unlock()

	for _, c := range pub {
		out = append(out, c)
		if host, _, err := net.SplitHostPort(c); err == nil {
			out = append(out, "ws://"+net.JoinHostPort(host, cfgPort))
		}
	}
	if l.udp != nil {
		l.udp.mu.Lock()
		for k, p := range l.udp.paths {
			if p.origin == "" || p.origin == l.origin {
				out = append(out, k)
			}
		}
		l.udp.mu.Unlock()
	}
	for _, ip := range localIPv4() {
		out = append(out, net.JoinHostPort(ip, itoa(l.cfg.UDPPort)))
	}
	out = append(out, l.tcpAddr()...)
	return dedupe(out)
}

func (l *Link) tcpAddr() []string {
	out := []string{}
	for _, ip := range localIPv4() {
		out = append(out, "ws://"+net.JoinHostPort(ip, itoa(l.cfg.Port)))
	}
	return out
}

func localIPv4() []string {
	out := []string{}
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifa := range ifs {
		if ifa.Flags&net.FlagUp == 0 || ifa.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifa.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if v4 := ip.To4(); v4 != nil && !v4.IsLoopback() {
				out = append(out, v4.String())
			}
		}
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func (u *udpSock) sendText(to *net.UDPAddr, text string) error {
	_ = u.pc.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_, err := u.pc.WriteToUDP([]byte(text), to)
	return err
}

func privateIPv4() []string {
	out := []string{}
	for _, ip := range localIPv4() {
		if strings.HasPrefix(ip, "192.168.") || strings.HasPrefix(ip, "10.") {
			out = append(out, ip)
		}
		if strings.HasPrefix(ip, "172.") {
			if second := atoiPart(ip); second >= 16 && second <= 31 {
				out = append(out, ip)
			}
		}
	}
	return out
}

func atoiPart(ip string) int {
	parts := strings.Split(ip, ".")
	if len(parts) < 2 {
		return -1
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return -1
	}
	return n
}

var errNoPath = errors.New("no path")

func (u *udpSock) sendOne(addr *net.UDPAddr, m *Msg) error {
	if addr == nil {
		return errNoPath
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	for i, chunk := range chunkJSON(b, udpChunk) {
		head := []byte{udpMagic0, udpMagic1, 0, byte(len(chunk)), byte(i)}
		_ = u.pc.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if _, err := u.pc.WriteTo(chunkDatagram(head, chunk), addr); err != nil {
			return err
		}
	}
	return nil
}

func chunkDatagram(head, chunk []byte) []byte { return append(append([]byte{}, head...), chunk...) }
