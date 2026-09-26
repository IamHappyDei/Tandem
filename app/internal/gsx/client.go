package gsx

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"tandem/internal/logx"
	"tandem/internal/wire"
)

var (
	errClosed  = errors.New("link closed")
	errNoLink  = errors.New("GSX link is down")
	errTimeout = errors.New("no verdict from GSX in time")
)

const resultWindow = 5 * time.Second

var shapeKeys = []string{"service", "name", "id", "command"}

type Frame struct {
	Type    string
	Path    string
	Value   json.RawMessage
	Ok      *bool
	Error   any
	Topic   string
	GsxRun  *bool
	AuthReq *bool
	Caps    []string
	Rest    map[string]any
}

type Stats struct {
	Messages     int `json:"messages"`
	Patches      int `json:"patches"`
	Snapshots    int `json:"snapshots"`
	Commands     int `json:"commands"`
	CommandFails int `json:"commandFails"`
	Serialized   int `json:"serialized"`
}

type Client struct {
	URL      string
	Channels []string

	log      *logx.Log
	debounce time.Duration

	cmdMu    sync.Mutex
	awaiting atomic.Bool
	result   chan *Frame
	inflight sync.Mutex

	mu         sync.Mutex
	ws         *wsConn
	state      map[string]any
	connected  bool
	simReady   bool
	gsxRunning bool
	primed     bool
	before     map[string]any
	flushAt    *time.Timer
	shape      map[string]string
	stats      Stats
	reconnect  bool
	closed     bool
	onChange   func(wire.Change)
	onLink     func(bool)
}

func New(url string, channels []string, log *logx.Log, debounceMS int) *Client {
	if len(channels) == 0 {
		channels = []string{"state", "services", "menu", "prompts", "billing"}
	}
	return &Client{
		URL: url, Channels: channels, log: log, reconnect: true,
		debounce: time.Duration(debounceMS) * time.Millisecond,
		state:    map[string]any{},
		shape:    map[string]string{},
		result:   make(chan *Frame, 1),
	}
}

func (c *Client) OnChange(f func(wire.Change)) { c.onChange = f }
func (c *Client) OnLink(f func(bool))          { c.onLink = f }
func (c *Client) SetReconnect(v bool)          { c.mu.Lock(); c.reconnect = v; c.mu.Unlock() }

func (c *Client) Start() { go c.dial() }

func (c *Client) Stop() {
	c.mu.Lock()
	c.closed = true
	w := c.ws
	c.ws = nil
	c.mu.Unlock()
	if w != nil {
		w.Close()
	}
}

func (c *Client) State() map[string]any { c.mu.Lock(); defer c.mu.Unlock(); return c.state }
func (c *Client) Connected() bool       { c.mu.Lock(); defer c.mu.Unlock(); return c.connected }
func (c *Client) SimReady() bool        { c.mu.Lock(); defer c.mu.Unlock(); return c.simReady && c.gsxRunning }
func (c *Client) Stats() Stats          { c.mu.Lock(); defer c.mu.Unlock(); return c.stats }

func (c *Client) Shapes() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]string{}
	for k, v := range c.shape {
		out[k] = v
	}
	return out
}

func (c *Client) TopKeys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return sortedKeys(c.state)
}

func (c *Client) ServicesRaw() any { c.mu.Lock(); defer c.mu.Unlock(); return c.state["services"] }
func (c *Client) MenuRaw() any     { c.mu.Lock(); defer c.mu.Unlock(); return c.state["menu"] }
func (c *Client) MenuShown() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return asBool(c.state["menuShown"])
}

func (c *Client) dial() {
	d := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	ws, _, err := d.Dial(c.URL, nil)
	if err != nil {
		c.mu.Lock()
		again := c.reconnect && !c.closed
		c.mu.Unlock()
		c.log.Warn("GSX not reachable at %s (%s)", c.URL, firstLine(err))
		if again {
			time.AfterFunc(time.Second, c.dial)
		}
		return
	}
	conn := newWSConn(ws, c.log)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		conn.Close()
		return
	}
	c.ws = conn
	c.connected = true
	c.mu.Unlock()
	c.log.Info("GSX link up %s", c.URL)
	if c.onLink != nil {
		c.onLink(true)
	}
	if err := conn.Send(map[string]any{"type": "subscribe", "channels": c.Channels}); err != nil {
		c.log.Warn("subscribe send failed: %s", err)
	}
	conn.Run(c.handle, func(err error) { c.down(firstLine(err)) })
}

func (c *Client) down(why string) {
	c.mu.Lock()
	c.connected = false
	c.primed = false
	c.before = nil
	if c.flushAt != nil {
		c.flushAt.Stop()
		c.flushAt = nil
	}
	again := c.reconnect && !c.closed
	c.mu.Unlock()
	c.log.Warn("GSX link down (%s)", why)
	if c.onLink != nil {
		c.onLink(false)
	}
	if again {
		time.AfterFunc(time.Second, c.dial)
	}
}

func (c *Client) handle(f *Frame) {
	c.mu.Lock()
	c.stats.Messages++
	c.mu.Unlock()
	switch f.Type {
	case "hello":
		c.mu.Lock()
		if f.GsxRun != nil {
			c.gsxRunning = *f.GsxRun
		}
		c.mu.Unlock()
		if f.AuthReq != nil && *f.AuthReq {
			c.log.Warn("GSX wants auth on the Remote control server - nothing can be read until that is turned off")
		}
		if len(f.Caps) > 0 {
			c.log.Debug("GSX capabilities: %s", strings.Join(f.Caps, ", "))
		}
	case "snapshot":
		c.mu.Lock()
		c.state = f.Rest
		c.primed = true
		c.simReady = true
		c.stats.Snapshots++

		c.before = nil
		if c.flushAt != nil {
			c.flushAt.Stop()
			c.flushAt = nil
		}
		c.mu.Unlock()
		c.log.Debug("GSX snapshot mirrored (%d top-level keys)", len(f.Rest))
	case "patch":
		var v any
		if len(f.Value) > 0 {
			if err := json.Unmarshal(f.Value, &v); err != nil {
				return
			}
		}
		c.mark()
		c.mu.Lock()
		applyPatch(c.state, f.Path, v)
		c.stats.Patches++
		c.mu.Unlock()
	case "result":
		if c.awaiting.CompareAndSwap(true, false) {
			select {
			case c.result <- f:
			default:
			}
		}
	case "event":
		if f.Topic == "engine" && f.GsxRun != nil {
			c.mu.Lock()
			c.gsxRunning = *f.GsxRun
			c.mu.Unlock()
		}
	}
}

func (c *Client) mark() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.primed {
		return
	}
	if c.before == nil {
		c.before = c.projectLocked()
	}
	if c.flushAt != nil {
		c.flushAt.Stop()
	}
	c.flushAt = time.AfterFunc(c.debounce, c.flush)
}

func (c *Client) flush() {
	c.mu.Lock()
	before := c.before
	c.before = nil
	c.flushAt = nil
	after := c.projectLocked()
	cb := c.onChange
	c.mu.Unlock()
	if before == nil {
		return
	}
	if !changed(before, after) {
		return
	}
	if cb != nil {
		cb(wire.Change{Before: before, After: after})
	}
}

func (c *Client) projectLocked() map[string]any {
	return map[string]any{
		"services":  deepClone(c.state["services"]),
		"menu":      deepClone(c.state["menu"]),
		"menuShown": c.state["menuShown"],
		"message":   deepClone(c.state["message"]),
		"top":       strings.Join(sortedKeys(c.state), ","),
	}
}

func changed(a, b map[string]any) bool {
	if hashJSON(a["services"]) != hashJSON(b["services"]) {
		return true
	}
	if cheapHash(a["menu"]) != cheapHash(b["menu"]) {
		return true
	}
	if asBool(a["menuShown"]) != asBool(b["menuShown"]) {
		return true
	}
	if hashJSON(a["message"]) != hashJSON(b["message"]) {
		return true
	}
	return asString(a["top"]) != asString(b["top"])
}

func cheapHash(v any) string {
	m, _ := v.(map[string]any)
	if m == nil {
		return "nil"
	}
	return hashJSON(map[string]any{"t": m["title"], "e": m["entries"], "s": m["stateClass"]})
}

func (c *Client) Command(verb string, args map[string]any) (*Frame, error) {
	c.cmdMu.Lock()
	defer c.cmdMu.Unlock()
	c.mu.Lock()
	w := c.ws
	c.stats.Commands++
	if c.awaiting.CompareAndSwap(false, true) {

	} else {
		c.stats.Serialized++
		c.awaiting.Store(true)
	}
	c.mu.Unlock()
	if w == nil {
		c.awaiting.Store(false)
		return nil, errNoLink
	}
	select {
	case <-c.result:
	default:
	}
	if err := w.Send(map[string]any{"type": "command", "verb": verb, "args": args}); err != nil {
		c.awaiting.Store(false)
		return nil, err
	}
	select {
	case f := <-c.result:
		if f != nil && f.Ok != nil && !*f.Ok {
			c.mu.Lock()
			c.stats.CommandFails++
			c.mu.Unlock()
			return f, errors.New(errorText(f.Error))
		}
		return f, nil
	case <-time.After(resultWindow):
		c.awaiting.Store(false)
		return nil, errTimeout
	}
}

func (c *Client) TriggerService(name string) (bool, []string) {
	const key = "service.trigger"
	c.mu.Lock()
	known := c.shape[key]
	c.mu.Unlock()
	order := append([]string{}, shapeKeys...)
	if known != "" {
		order = append([]string{known}, without(order, known)...)
	}
	tried := []string{}
	for _, arg := range order {
		_, err := c.Command("service.trigger", map[string]any{arg: name})
		if err == nil {
			c.mu.Lock()
			c.shape[key] = arg
			c.mu.Unlock()
			return true, tried
		}
		tried = append(tried, arg+" ("+firstLine(err)+")")
	}
	return false, tried
}

func without(list []string, skip string) []string {
	out := []string{}
	for _, s := range list {
		if s != skip {
			out = append(out, s)
		}
	}
	return out
}

func (c *Client) PickMenu(index int) error {
	_, err := c.Command("menu.pick", map[string]any{"index": index})
	return err
}

func applyPatch(state map[string]any, path string, value any) {
	segs := []string{}
	for _, s := range strings.Split(strings.Trim(path, "/"), "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	if len(segs) == 0 {
		return
	}
	node := any(state)
	for i := 0; i < len(segs)-1; i++ {
		node = descend(node, segs[i])
		if node == nil {
			return
		}
	}
	last := segs[len(segs)-1]
	switch n := node.(type) {
	case map[string]any:
		if value == nil {
			delete(n, last)
		} else {
			n[last] = value
		}
	case []any:
		if i := atoi(last); i >= 0 && i < len(n) {
			n[i] = value
		}
	}
}

func descend(node any, key string) any {
	switch n := node.(type) {
	case map[string]any:
		v, ok := n[key]
		if !ok {
			v = map[string]any{}
			n[key] = v
		}
		return v
	case []any:
		i := atoi(key)
		if i < 0 || i >= len(n) {
			return nil
		}
		return n[i]
	}
	return nil
}

func deepClone(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

func asBool(v any) bool { b, _ := v.(bool); return b }

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func hashJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "null"
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])[:16]
}
func asString(v any) string     { s, _ := v.(string); return s }
func num(v any) (float64, bool) { f, ok := v.(float64); return f, ok }

func atoi(s string) int {
	n := 0
	if s == "" {
		return -1
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func firstLine(err error) string {
	if err == nil {
		return "no error"
	}
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}

func (c *Client) SetURL(u string) {
	c.mu.Lock()
	changed := u != c.URL
	c.URL = u
	c.mu.Unlock()
	if changed {
		c.log.Info("GSX address is now %s", u)
		c.dial()
	}
}
