package room

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"
)

var stunServers = []string{
	"stun.l.google.com:19302",
	"stun.cloudflare.com:3478",
	"stunserver2.mm-sip.com:3478",
	"stun.voipbuster.com:3478",
}

const stunCookie = 0x2112A442

type stunPending struct {
	ch chan string
}

func (u *udpSock) stunQuery(server string, timeout time.Duration) (string, error) {
	txn := make([]byte, 12)
	if _, err := rand.Read(txn); err != nil {
		return "", err
	}
	pkt := make([]byte, 20+12)
	binary.BigEndian.PutUint16(pkt[0:], 0x0001)
	binary.BigEndian.PutUint16(pkt[2:], 0)
	binary.BigEndian.PutUint32(pkt[4:], stunCookie)
	copy(pkt[8:], txn)
	_ = u.pc.SetWriteDeadline(time.Now().Add(2 * time.Second))
	addr, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return "", err
	}
	if _, err := u.pc.WriteTo(pkt, addr); err != nil {
		return "", err
	}
	key := string(txn)
	u.stunMu.Lock()
	ch := make(chan string, 1)
	u.stunWait[key] = ch
	u.stunMu.Unlock()
	defer func() {
		u.stunMu.Lock()
		delete(u.stunWait, key)
		u.stunMu.Unlock()
	}()
	select {
	case res := <-ch:
		if res == "" {
			return "", errors.New("no mapping")
		}
		return res, nil
	case <-time.After(timeout):
		return "", errors.New("no answer from " + server)
	}
}

func (u *udpSock) stunIngest(b []byte) {
	if len(b) < 20 {
		return
	}
	if binary.BigEndian.Uint16(b[0:]) != 0x0101 {
		return
	}
	if binary.BigEndian.Uint32(b[4:]) != stunCookie {
		return
	}
	txn := string(b[8:20])
	var mapped string
	attrs := b[20:]
	for len(attrs) >= 4 {
		atype := binary.BigEndian.Uint16(attrs[0:])
		alen := int(binary.BigEndian.Uint16(attrs[2:]))
		if 4+alen > len(attrs) {
			break
		}
		v := attrs[4 : 4+alen]
		switch atype {
		case 0x0001:
			if len(v) >= 8 {
				port := int(binary.BigEndian.Uint16(v[2:]))
				ip := net.IP(v[4:8])
				mapped = net.JoinHostPort(ip.String(), strconv.Itoa(port))
			}
		case 0x0020:
			if len(v) >= 8 {
				port := int(binary.BigEndian.Uint16(v[2:])) ^ int(stunCookie>>16)
				x := make([]byte, 4)
				xor4(x, v[4:8], stunCookie)
				mapped = net.JoinHostPort(net.IP(x).String(), strconv.Itoa(port))
			}
		}
		attrs = attrs[4+pad4(alen):]
	}
	u.stunMu.Lock()
	if ch, ok := u.stunWait[txn]; ok {
		select {
		case ch <- mapped:
		default:
		}
		delete(u.stunWait, txn)
	}
	u.stunMu.Unlock()
}

func xor4(out, in []byte, cookie uint32) {
	out[0] = in[0] ^ byte(cookie>>24)
	out[1] = in[1] ^ byte(cookie>>16)
	out[2] = in[2] ^ byte(cookie>>8)
	out[3] = in[3] ^ byte(cookie)
}

func pad4(n int) int {
	if n%4 == 0 {
		return n
	}
	return n + 4 - n%4
}

func (l *Link) Discover() []string {
	if l.udp == nil {
		return nil
	}
	var wg sync.WaitGroup
	out := make([]string, len(stunServers))
	for i, s := range stunServers {
		wg.Add(1)
		go func(i int, s string) {
			defer wg.Done()
			if addr, err := l.udp.stunQuery(s, 2*time.Second); err == nil {
				out[i] = addr
			}
		}(i, s)
	}
	wg.Wait()
	pub := dedupe(nonEmpty(out))
	l.mu.Lock()
	l.public = pub
	l.mu.Unlock()
	l.udp.mu.Lock()
	l.udp.cand = dedupe(append(l.udp.cand, pub...))
	l.udp.mu.Unlock()
	return pub
}

func nonEmpty(in []string) []string {
	out := []string{}
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
