//go:build windows

package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tandem/internal/ui"
)

//go:embed web
var webFS embed.FS

type target struct {
	dir     string
	machine bool
}

func (t target) exe() string { return filepath.Join(t.dir, exeName) }

func targets() (all, mine target) {
	return target{filepath.Join(programFiles(), appName), true},
		target{filepath.Join(envOr("LOCALAPPDATA", ""), "Programs", appName), false}
}

type evt struct {
	topic string
	data  any
}

type gui struct {
	mu    sync.Mutex
	subs  map[chan evt]struct{}
	quit  chan struct{}
	port  int
	busy  bool
	last  []evt
	elev  bool
	start time.Time
}

func (g *gui) emit(topic string, data any) {
	g.mu.Lock()
	e := evt{topic, data}
	g.last = append(g.last, e)
	if len(g.last) > 400 {
		g.last = g.last[len(g.last)-400:]
	}
	subs := make([]chan evt, 0, len(g.subs))
	for c := range g.subs {
		subs = append(subs, c)
	}
	g.mu.Unlock()
	for _, c := range subs {
		select {
		case c <- e:
		default:
		}
	}
}

func (g *gui) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/info", g.info)
	mux.HandleFunc("/api/install", g.async(g.doInstall))
	mux.HandleFunc("/api/uninstall", g.async(g.doUninstall))
	mux.HandleFunc("/api/launch", g.post(g.doLaunch))
	mux.HandleFunc("/api/elevate", g.post(g.doElevate))
	mux.HandleFunc("/api/quit", g.post(g.doQuit))
	mux.HandleFunc("/api/events", g.events)
	return mux
}

func (g *gui) run(port int) error {
	g.subs = map[chan evt]struct{}{}
	g.quit = make(chan struct{})
	g.start = time.Now()
	g.elev = elevated()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	g.port = ln.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: g.Handler()}
	go srv.Serve(ln)

	url := fmt.Sprintf("http://127.0.0.1:%d/?t=%d", g.port, time.Now().UnixNano())
	ui.HideConsole()
	w := ui.NewWindowAs(setupTitle, url)
	if err := w.Show(); err != nil {
		fmt.Fprintln(sink, "window:", err, "- opening in the browser instead")
		ui.OpenBrowser(url)
	}
	go g.watchWindow(w)
	<-g.quit
	time.Sleep(150 * time.Millisecond)
	w.Close()
	return nil
}

func (g *gui) info(w http.ResponseWriter, r *http.Request) {
	all, mine := targets()
	where := ""
	switch {
	case hasFile(all.exe()):
		where = all.dir
	case hasFile(mine.exe()):
		where = mine.dir
	}
	self, _ := os.Executable()
	writeJSON(w, map[string]any{
		"version":   ui.Version,
		"all":       all.dir,
		"mine":      mine.dir,
		"installed": where,
		"here":      where != "" && strings.EqualFold(filepath.Dir(self), where),
		"elevated":  g.elev,
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (g *gui) post(h func(map[string]any) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if len(b) > 0 {
			_ = json.Unmarshal(b, &body)
		}
		out, err := h(body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, out)
	}
}

func (g *gui) async(h func(map[string]any, rep)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if len(b) > 0 {
			_ = json.Unmarshal(b, &body)
		}
		g.mu.Lock()
		if g.busy {
			g.mu.Unlock()
			w.WriteHeader(http.StatusConflict)
			writeJSON(w, map[string]any{"error": "the setup is already working"})
			return
		}
		g.busy = true
		g.mu.Unlock()
		go func() {
			h(body, &evtRep{g})
			g.mu.Lock()
			g.busy = false
			g.mu.Unlock()
		}()
		writeJSON(w, map[string]any{"ok": true})
	}
}

type evtRep struct{ g *gui }

func (r *evtRep) step(t string) { r.g.emit("line", map[string]any{"kind": "step", "text": t}) }
func (r *evtRep) ok(t string)   { r.g.emit("line", map[string]any{"kind": "ok", "text": t}) }
func (r *evtRep) bad(t string)  { r.g.emit("line", map[string]any{"kind": "bad", "text": t}) }
func (r *evtRep) run(t string)  { r.g.emit("line", map[string]any{"kind": "run", "text": t}) }
func (r *evtRep) pct(n int)     { r.g.emit("line", map[string]any{"kind": "pct", "text": fmt.Sprint(n)}) }

func (g *gui) doInstall(body map[string]any, r rep) {
	all, mine := targets()
	t := all
	if fmt.Sprint(body["where"]) == "me" {
		t = mine
	}
	if t.machine && !g.elev {
		if relaunch(false) {
			g.emit("done", map[string]any{"ok": true, "what": "elevate"})
			go func() { time.Sleep(400 * time.Millisecond); close(g.quit) }()
			return
		}
		g.emit("done", map[string]any{"ok": false, "what": "install",
			"error": "that folder needs administrator rights, and the request was refused. Choose \"only for me\", or start this file as administrator."})
		return
	}
	err := runInstall(&t, boolOf(body["firewall"]), boolOf(body["autorun"]), r)
	g.emit("done", map[string]any{"ok": err == nil, "what": "install", "error": errText(err), "dir": t.dir})
}

func (g *gui) doUninstall(body map[string]any, r rep) {
	admin, err := runUninstall(boolOf(body["firewall"]), boolOf(body["purge"]), r)
	g.emit("done", map[string]any{"ok": err == nil, "what": "uninstall", "error": errText(err), "needAdmin": admin && !g.elev})
}

func (g *gui) doLaunch(map[string]any) (any, error) {
	t := targetOf()
	if hasFile(t.exe()) {
		_ = exec.Command(t.exe()).Start()
	}
	go func() { time.Sleep(300 * time.Millisecond); close(g.quit) }()
	return map[string]any{"ok": true}, nil
}

func (g *gui) doElevate(body map[string]any) (any, error) {
	what := fmt.Sprint(body["what"])
	if what != "uninstall" && what != "install" {
		return nil, fmt.Errorf("what should the elevated copy do?")
	}
	args := []string{"--uninstall", "--quiet"}
	if what == "install" {
		args = []string{"--quiet"}
		if d := fmt.Sprint(body["dir"]); d != "" && d != "undefined" {
			args = []string{"--quiet", "-dir", d}
		}
	}
	if !relaunchWith(args...) {
		return nil, fmt.Errorf("Windows said no to the elevation request")
	}
	go func() { time.Sleep(500 * time.Millisecond); close(g.quit) }()
	return map[string]any{"ok": true}, nil
}

func (g *gui) doQuit(map[string]any) (any, error) {
	go close(g.quit)
	return map[string]any{"ok": true}, nil
}

func (g *gui) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no streaming", http.StatusInternalServerError)
		return
	}
	ch := make(chan evt, 512)
	g.mu.Lock()
	g.subs[ch] = struct{}{}
	replay := append([]evt{}, g.last...)
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.subs, ch)
		g.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	g.mu.Lock()
	busy := g.busy
	g.mu.Unlock()
	if err := flushEvent(w, fl, evt{"hello", map[string]any{"busy": busy}}); err != nil {
		return
	}
	for _, e := range replay {
		_ = flushEvent(w, fl, e)
	}
	tick := time.NewTicker(12 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			if err := flushEvent(w, fl, e); err != nil {
				return
			}
		case <-tick.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

func flushEvent(w io.Writer, fl http.Flusher, e evt) error {
	b, err := json.Marshal(e.data)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.topic, b); err != nil {
		return err
	}
	fl.Flush()
	return nil
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func boolOf(v any) bool { b, _ := v.(bool); return b }

func runGUI(port int) error {
	g := &gui{}
	return g.run(port)
}

func (g *gui) watchWindow(w *ui.Window) {
	for i := 0; i < 50 && w.Gone(); i++ {
		time.Sleep(200 * time.Millisecond)
	}
	for {
		time.Sleep(600 * time.Millisecond)
		if w.Gone() {
			select {
			case <-g.quit:
			default:
				close(g.quit)
			}
			return
		}
	}
}
