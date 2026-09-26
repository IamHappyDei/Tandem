package room

import (
	"sync"
	"time"
)

type reliable struct {
	mu    sync.Mutex
	items map[string]*pending
	Link  *Link
}

type pending struct {
	msg   *Msg
	peer  *Peer
	tr    *transport
	tries int
	next  time.Time
}

func newReliable(l *Link) *reliable {
	r := &reliable{items: map[string]*pending{}, Link: l}
	go r.loop()
	return r
}

func (r *reliable) send(p *Peer, m *Msg) {
	p.send(m)
	if m.T != "action" || m.ID == "" {
		return
	}
	r.mu.Lock()
	r.items[m.ID] = &pending{msg: m, peer: p, tries: 1, next: time.Now().Add(900 * time.Millisecond)}
	r.mu.Unlock()
}

func (r *reliable) ack(id string) {
	r.mu.Lock()
	delete(r.items, id)
	r.mu.Unlock()
}

func (r *reliable) ackReceived(id string) { r.ack(id) }

func (r *reliable) loop() {
	for range time.Tick(300 * time.Millisecond) {
		now := time.Now()
		r.mu.Lock()
		due := make([]string, 0, len(r.items))
		for id, p := range r.items {
			if p.next.After(now) {
				continue
			}
			if p.tries > 8 {
				delete(r.items, id)
				continue
			}
			due = append(due, id)
		}
		r.mu.Unlock()
		for _, id := range due {
			r.mu.Lock()
			p, ok := r.items[id]
			if ok {
				p.tries++
				p.next = time.Now().Add(900 * time.Millisecond)
			}
			r.mu.Unlock()
			if ok {
				switch {
				case p.peer != nil:
					p.peer.send(p.msg)
				case p.tr != nil:
					_ = p.tr.send(p.msg)
				}
			}
		}
	}
}

func (r *reliable) sendRaw(t *transport, m *Msg) {
	_ = t.send(m)
	if m.ID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.items[m.ID]
	if !ok {
		r.items[m.ID] = &pending{msg: m, tr: t, tries: 1, next: time.Now().Add(900 * time.Millisecond)}
		return
	}
	p.tr = t
}
