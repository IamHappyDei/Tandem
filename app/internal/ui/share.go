package ui

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	Prefix = "tdm1"
	legacy = "gsx1"
)

const OpaquePrefix = Prefix + "~"

func normalize(in string) string {
	if strings.HasPrefix(in, legacy) {
		return Prefix + in[len(legacy):]
	}
	return in
}

type share struct {
	Public  string
	TCP     int
	UDPPort int
	Addr    string
	Room    string
	Pass    string
	Name    string
	By      string
	Cands   []string
	Local   bool
}

func (s *share) short() string {
	out := Prefix + "/" + s.Public + "/" + s.Room
	if s.UDPPort != 0 && s.UDPPort != s.TCP {
		out = Prefix + "/" + net.JoinHostPort(hostOf(s.Public), strconv.Itoa(s.TCP)) + "+" + strconv.Itoa(s.UDPPort) + "/" + s.Room
	}
	if s.Pass != "" {
		out += "#" + s.Pass
	}
	return out
}

func parseShare(in string) (*share, error) {
	in = normalize(strings.TrimSpace(in))
	switch {
	case in == "":
		return nil, fmt.Errorf("nothing to join - paste the whole thing")
	case strings.HasPrefix(in, Prefix+"/"):
		return parseShort(in)
	case strings.HasPrefix(in, Prefix+":"):
		return parseLong(in)
	case strings.HasPrefix(in, OpaquePrefix):
		return parseOpaque(in)
	case strings.HasPrefix(in, "ws://"), strings.HasPrefix(in, "wss://"):
		return &share{Public: in, Addr: in, Room: ""}, nil
	default:
		if host, port, err := net.SplitHostPort(in); err == nil {
			n, _ := strconv.Atoi(port)
			return &share{Public: in, TCP: n, UDPPort: n, Addr: host}, nil
		}
		return nil, fmt.Errorf("that is neither a room string nor an address")
	}
}

func parseShort(in string) (*share, error) {
	body := strings.TrimPrefix(in, Prefix+"/")
	s := &share{}
	if i := strings.Index(body, "#"); i >= 0 {
		s.Pass = body[i+1:]
		body = body[:i]
	}
	parts := strings.Split(body, "/")
	if len(parts) == 0 {
		return nil, fmt.Errorf("that is not a gsx-sync room string")
	}
	hostPort := parts[0]
	if plus := strings.Index(hostPort, "+"); plus >= 0 {
		s.UDPPort, _ = strconv.Atoi(hostPort[plus+1:])
		hostPort = hostPort[:plus]
	}
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		return nil, fmt.Errorf("the address part must look like 1.2.3.4:8790")
	}
	s.TCP, _ = strconv.Atoi(port)
	if s.UDPPort == 0 {
		s.UDPPort = s.TCP
	}
	s.Public = net.JoinHostPort(host, port)
	s.Addr = host

	s.Cands = []string{s.Public}
	if len(parts) > 1 {
		s.Room = strings.ToUpper(parts[1])
	}
	if s.Room == "" {
		return nil, fmt.Errorf("no room code in that string")
	}

	if ip := net.ParseIP(host); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		s.Local = true
	}
	return s, nil
}

func parseLong(in string) (*share, error) {
	raw := strings.TrimPrefix(in, Prefix+":")
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("that is not a gsx-sync invite (a truncated copy is the usual cause)")
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("that is not a gsx-sync room string")
	}
	s := &share{}
	s.Room = strings.ToUpper(asString(m["room"]))
	s.Pass = asString(m["pass"])
	s.Name = asString(m["name"])
	s.By = asString(m["by"])
	s.TCP = int(asFloat(m["tcp"]))
	s.UDPPort = int(asFloat(m["udp"]))
	if arr, ok := m["cands"].([]any); ok {
		for _, c := range arr {
			s.Cands = append(s.Cands, asString(c))
		}
	}
	for _, c := range s.Cands {
		if strings.HasPrefix(c, "ws://") || strings.HasPrefix(c, "wss://") {
			s.Public = c
			break
		}
	}
	return s, nil
}

func (s *share) dialTargets() (ws []string, udp []string) {
	seen := map[string]bool{}
	add := func(list *[]string, v string) {
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		*list = append(*list, v)
	}
	if s.Public != "" {
		if strings.HasPrefix(s.Public, "ws") {
			add(&ws, s.Public)
		} else {
			add(&ws, "ws://"+s.Public)
		}
	}
	for _, c := range s.Cands {
		if strings.HasPrefix(c, "ws://") || strings.HasPrefix(c, "wss://") {
			add(&ws, c)
		} else {
			add(&udp, c)
		}
	}
	return
}

func (s *share) opaque() (string, error) {
	payload, err := json.Marshal(map[string]any{
		"v": 1, "pub": s.Public, "tcp": s.TCP, "udp": s.UDPPort,
		"room": s.Room, "name": s.Name, "by": s.By, "ts": nowTS(),
	})
	if err != nil {
		return "", err
	}
	nonce := make([]byte, 24)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := xorStream(nonce, s.Pass, payload)
	code := OpaquePrefix + base64.RawURLEncoding.EncodeToString(append(append([]byte{}, nonce...), out...))
	if s.Pass != "" {
		code += "#" + s.Pass
	}
	return code, nil
}

func parseOpaque(in string) (*share, error) {
	body := strings.TrimPrefix(in, OpaquePrefix)
	var pass string
	if i := strings.Index(body, "#"); i >= 0 {
		pass, body = body[i+1:], body[:i]
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(body))
	if err != nil || len(raw) < 25 {
		return nil, fmt.Errorf("that scrambled link is not readable (a truncated copy is the usual cause)")
	}
	nonce, cipher := raw[:24], raw[24:]
	var m map[string]any
	if err := json.Unmarshal(xorStream(nonce, pass, cipher), &m); err != nil {
		return nil, fmt.Errorf("that scrambled link does not open - if a passphrase is set, type it in Settings first")
	}
	s := &share{
		Room:   strings.ToUpper(asString(m["room"])),
		Pass:   pass,
		Name:   asString(m["name"]),
		By:     asString(m["by"]),
		TCP:    int(asFloat(m["tcp"])),
		Public: asString(m["pub"]),
	}
	s.UDPPort = int(asFloat(m["udp"]))
	if s.UDPPort == 0 {
		s.UDPPort = s.TCP
	}
	if s.Public == "" || s.Room == "" {
		return nil, fmt.Errorf("that scrambled link is missing its address or room")
	}
	if arr, ok := m["cands"].([]any); ok {
		for _, c := range arr {
			if v := asString(c); v != "" {
				s.Cands = append(s.Cands, v)
			}
		}
	}
	if len(s.Cands) == 0 {
		s.Cands = []string{s.Public}
	}
	s.Addr = hostOf(s.Public)
	if ip := net.ParseIP(s.Addr); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		s.Local = true
	}
	return s, nil
}

func xorStream(nonce []byte, secret string, b []byte) []byte {
	out := make([]byte, len(b))
	ks := keystream(nonce, secret, len(b))
	for i := range b {
		out[i] = b[i] ^ ks[i]
	}
	return out
}

func keystream(nonce []byte, secret string, n int) []byte {
	out := make([]byte, 0, n+32)
	for ctr := 0; len(out) < n; ctr++ {
		h := sha256.New()
		h.Write(nonce)
		h.Write([]byte("tdm1-opaque-v1"))
		h.Write([]byte(secret))
		h.Write([]byte{byte(ctr >> 24), byte(ctr >> 16), byte(ctr >> 8), byte(ctr)})
		out = h.Sum(out)
	}
	return out[:n]
}

func hostOf(hostPort string) string {
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		return h
	}
	return strings.TrimPrefix(strings.TrimPrefix(hostPort, "ws://"), "wss://")
}

func nowTS() int64 { return time.Now().Unix() }
