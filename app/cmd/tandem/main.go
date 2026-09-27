package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tandem/internal/aircraft"
	"tandem/internal/bridge"
	"tandem/internal/conf"
	"tandem/internal/discover"
	"tandem/internal/fake"
	"tandem/internal/logx"
	"tandem/internal/relay"
	"tandem/internal/tray"
	"tandem/internal/ui"
)

func main() {
	mode := "app"
	args := os.Args[1:]
	if len(args) > 0 && !isFlag(args[0]) {
		mode = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet(mode, flag.ExitOnError)
	port := fs.Int("port", 0, "listen port (relay / fake couatl)")
	uiPort := fs.Int("ui", 0, "dashboard port (default 8795)")
	gsxURL := fs.String("gsx", "", "Couatl address (default ws://127.0.0.1:8744)")
	noGUI := fs.Bool("nogui", false, "no tray icon, no window")
	browser := fs.Bool("browser", false, "open the dashboard in your default browser instead of an app window")
	console := fs.Bool("console", false, "keep the console window visible (log stays in tandem.log either way)")
	listen := fs.Int("listen", 0, "TCP port to host the room on")
	udp := fs.Int("udp", 0, "UDP port to punch from")
	name := fs.String("name", "", "what this cockpit is called (default: the PC name)")
	bind := fs.String("bind", "", "address to listen on (discover / relay)")
	once := fs.Bool("once", false, "print status and exit (headless use)")
	get := fs.Bool("get", false, "with update: download the installer and run it")
	_ = fs.Parse(args)

	switch mode {
	case "relay":
		runRelay(*port, *bind)
	case "fake":
		runFake(*port)
	case "status":
		runStatus()
	case "stop":
		runStop()
	case "discover":
		runDiscover(*port, *bind)
	case "window":
		runWindow(fs.Args())
	case "update":
		runUpdate(*get)
	case "sim-install":
		cmdSimInstall(fs.Args())
	case "sim-remove":
		cmdSimRemove(fs.Args())
	case "version":
		fmt.Println(ui.Version)
	default:
		runApp(*uiPort, *gsxURL, !*noGUI, *once, !*browser, *console, *listen, *udp, *name)
	}
}

func isFlag(s string) bool { return len(s) > 0 && s[0] == '-' }

func runApp(uiPort int, gsxURL string, gui, once, win, showConsole bool, listen, udp int, name string) {

	if name != "" || listen > 0 || udp > 0 {
		n := name
		if n == "" {
			n = conf.Hostname()
		}
		conf.Path = filepath.Join(conf.Dir(), "config-"+n+itoa(listen)+".json")
	}
	cfg := conf.Load()
	if uiPort > 0 {
		cfg.UI.Port = uiPort
	}
	if gsxURL != "" {
		cfg.GSX.URL = gsxURL
	}
	if name != "" {
		cfg.Net.Name = name
	}
	if listen > 0 {
		cfg.Net.Port = listen
	}
	if udp > 0 {
		cfg.Net.UDPPort = udp
	}
	logDir := conf.Dir()
	f := openLog(logDir)
	log := logx.New(cfg.Log.Level)
	if f != nil {
		log.SetFile(f)
	}
	log.Info("tandem %s starting (config in %s)", ui.Version, conf.ConfigPath())

	app := ui.New(cfg, log)
	if err := app.Start(); err != nil {
		log.Error("cannot start: %s", err)
		os.Exit(1)
	}
	if err := app.ServeWeb(cfg.UI.Port); err != nil {
		if ui.FocusOther() {
			log.Error("another copy is already running - its window is in front of you")
			os.Exit(0)
		}
		log.Error("dashboard port %d is busy - close the other copy (%s)", cfg.UI.Port, err)
		os.Exit(1)
	}
	log.Info("dashboard on %s", app.URL())
	writeLock(os.Getpid(), cfg.UI.Port)

	if gui && !showConsole {
		if f := openLogFile(); f != nil {
			log.SetFile(f)
		}
		ui.HideConsole()
	}

	win0 := ui.NewWindow(app.URL())
	app.OnQuit = win0.Close
	show := func() {
		if err := win0.Show(); err != nil {
			log.Error("window: %s - opening in your browser instead", err)
			ui.OpenBrowser(app.URL())
		}
	}
	if gui {
		tray.Start(tray.Options{
			OnShow:    show,
			OnHide:    win0.Hide,
			OnBrowser: func() { ui.OpenBrowser(app.URL()) },
			OnQuit:    func() { win0.Close(); app.Stop(); os.Exit(0) },
		})
		if cfg.UI.AutoOpen && !once {
			go func() {
				time.Sleep(350 * time.Millisecond)
				if win {
					show()
				} else {
					ui.OpenBrowser(app.URL())
				}
			}()
		}
	}
	if once {
		st, _ := json.MarshalIndent(app.StatusSnapshot(), "", "  ")
		fmt.Println(string(st))
		app.Stop()
		return
	}
	select {}
}

func runDiscover(port int, bind string) {
	log := logx.New("info")
	p := port
	if p == 0 {
		p = 8788
	}
	if bind == "" {
		bind = "0.0.0.0"
	}
	s := discover.New(log.Child("[pnp]"))
	if err := s.Listen(bind, p); err != nil {
		log.Error("%s", err)
		os.Exit(1)
	}
	go func() {
		for range time.Tick(60 * time.Second) {
			log.Info("online now: %d codes", s.Online())
		}
	}()
	select {}
}

func runRelay(port int, bind string) {
	log := logx.New("info")
	p := port
	if p == 0 {
		p = 8790
	}
	s := relay.New(p, "0.0.0.0", log.Child("[relay]"))
	log.Info("relay listening on 0.0.0.0:%d - cockpits join ws://<this box>:%d and pick a room code", p, p)
	if err := s.Listen(); err != nil {
		log.Error("%s", err)
		os.Exit(1)
	}
}

func runFake(port int) {
	log := logx.New("info")
	p := port
	if p == 0 {
		p = 8744
	}
	s := fake.New(p, false)
	if err := s.Start(); err != nil {
		log.Error("%s", err)
		os.Exit(1)
	}
	log.Info("fake Couatl on ws://127.0.0.1:%d - toggle with: tandem fake-toggle? (use the tests)", p)
	select {}
}

func runStop() {
	url := fmt.Sprintf("http://127.0.0.1:%d/api/quit", runningPort())
	res, err := http.Post(url, "application/json", strings.NewReader("{}"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "no running app answered (%v)\n", err)
		os.Exit(1)
	}
	defer res.Body.Close()
	fmt.Println("asked the running tandem to quit:", res.Status)
}

func runStatus() {
	out, err := httpGetJSON(fmt.Sprintf("http://127.0.0.1:%d/api/status", runningPort()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "no running app answered (%v) - start one by double-clicking tandem.exe\n", err)
		os.Exit(1)
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
}

func httpGetJSON(url string) (map[string]any, error) {
	res, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", res.Status, strings.TrimSpace(string(body)))
	}
	var out map[string]any
	return out, json.Unmarshal(body, &out)
}

func openLog(dir string) *os.File {
	p := filepath.Join(dir, "tandem.log")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return f
}

func itoa(i int) string { return strconv.Itoa(i) }

func openLogFile() *os.File {
	f, err := os.OpenFile(filepath.Join(conf.Dir(), "tandem.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return f
}

func runWindow(args []string) {
	what := "state"
	if len(args) > 0 {
		what = args[0]
	}
	switch what {
	case "show":
		if !ui.WindowShow() {
			fmt.Println("no Tandem window to bring back - start tandem.exe")
		}
	case "hide":
		if !ui.WindowHide() {
			fmt.Println("no Tandem window found")
		}
	default:
		fmt.Println("window:", ui.WindowState())
	}
}

type lockFile struct {
	PID  int   `json:"pid"`
	Port int   `json:"port"`
	At   int64 `json:"at"`
}

func lockPath() string { return filepath.Join(conf.Dir(), "running.json") }

func writeLock(pid, port int) {
	b, _ := json.Marshal(lockFile{pid, port, time.Now().Unix()})
	_ = os.MkdirAll(conf.Dir(), 0o755)
	_ = os.WriteFile(lockPath(), b, 0o644)
}

func runningPort() int {
	cfg := conf.Load()
	tries := []int{}
	var l lockFile
	if b, err := os.ReadFile(lockPath()); err == nil && json.Unmarshal(b, &l) == nil && l.Port > 0 {
		tries = append(tries, l.Port)
	}
	tries = append(tries, cfg.UI.Port)
	for i := 1; i <= 6; i++ {
		tries = append(tries, cfg.UI.Port+i)
	}
	for _, p := range tries {
		c := http.Client{Timeout: 350 * time.Millisecond}
		res, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/status", p))
		if err != nil {
			continue
		}
		res.Body.Close()
		return p
	}
	return cfg.UI.Port
}

func runUpdate(get bool) {
	if !get {
		fmt.Println(ui.UpdateLine(ui.CheckRelease()))
		return
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/update/apply", runningPort())
	res, err := http.Post(url, "application/json", strings.NewReader("{}"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "no running app answered (%v) - the update has to come from the copy that is running\n", err)
		os.Exit(1)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if res.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "it did not start: %v\n", out["error"])
		os.Exit(1)
	}
	fmt.Printf("the installer (%v) is downloaded and starting - this copy is closing so the files are free\n", out["installer"])
}

func cmdSimInstall(a []string) {
	dir := communityFrom(a)
	if dir == "" {
		dir = aircraft.Open("").CommunityDir()
	}
	if dir == "" {
		fmt.Fprintln(os.Stderr, "no community folder found - name the folder as an argument")
		os.Exit(2)
	}
	b := bridge.New(0, logx.New("warn"))
	dest, err := b.InstallInto(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not be put there:", err)
		os.Exit(1)
	}
	fmt.Println("the bridge is in", dest)
	fmt.Println("restart the sim if it was already open")
}

func cmdSimRemove(a []string) {
	dir := communityFrom(a)
	if dir == "" {
		dir = aircraft.Open("").CommunityDir()
	}
	if err := bridge.UninstallFrom(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("it is gone from", dir)
}

func communityFrom(a []string) string {
	for _, x := range a {
		if strings.HasPrefix(x, "--community=") {
			x = strings.TrimPrefix(x, "--community=")
		} else if strings.HasPrefix(x, "-") {
			continue
		}
		fi, err := os.Stat(x)
		if err != nil || !fi.IsDir() {
			fmt.Fprintln(os.Stderr, "that folder is not there:", x)
			os.Exit(2)
		}
		return x
	}
	return ""
}
