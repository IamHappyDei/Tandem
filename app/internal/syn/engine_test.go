package syn_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"tandem/internal/fake"
	"tandem/internal/gsx"
	"tandem/internal/logx"
	"tandem/internal/relay"
	"tandem/internal/room"
	"tandem/internal/syn"
)

type rig struct {
	name   string
	fake   *fake.Server
	gsx    *gsx.Client
	link   *room.Link
	engine *syn.Engine
}

func newRig(t *testing.T, name string, couatlPort, linkPort, udpPort int, cfg syn.EngineConfig) *rig {
	t.Helper()
	log := logx.New(level())
	log.SetPrefix(name + " ")
	f := fake.New(couatlPort, false)
	if err := f.Start(); err != nil {
		t.Fatalf("%s fake couatl: %v", name, err)
	}
	l := room.NewLink(room.Config{Name: name, Bind: "127.0.0.1", Port: linkPort, UDPPort: udpPort, Log: log.Child("[net]")})
	if err := l.Start(); err != nil {
		t.Fatalf("%s link: %v", name, err)
	}
	g := gsx.New(fmt.Sprintf("ws://127.0.0.1:%d", couatlPort), nil, log.Child("[gsx]"), cfg.DebounceMs)
	cfg.Name = name
	if cfg.ReconcileSeconds == 0 {
		cfg.ReconcileSeconds = 1
	}
	if cfg.ConfirmDigests == 0 {
		cfg.ConfirmDigests = 1
	}
	if cfg.EchoMs == 0 {
		cfg.EchoMs = 1500
	}
	if cfg.DebounceMs == 0 {
		cfg.DebounceMs = 20
	}
	if cfg.GraceSeconds == 0 {
		cfg.GraceSeconds = 0
	}
	if cfg.MaxReplays == 0 {
		cfg.MaxReplays = 3
	}
	e := syn.NewEngine(cfg, g, l, log.Child("[syn]"))
	r := &rig{name: name, fake: f, gsx: g, link: l, engine: e}
	e.Start()
	g.Start()
	return r
}

func level() string {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("GSXTEST_LOG"))); v != "" {
		return v
	}
	return "warn"
}

func freePort(t *testing.T) int {
	t.Helper()

	s := fake.New(0, false)
	_ = s
	return 0
}

func (r *rig) stop(t *testing.T) {
	t.Helper()
	r.engine.Stop()
	r.gsx.Stop()
	r.link.Stop()
	r.fake.Stop()
}

func (r *rig) toggle(svc string) { r.fake.Toggle(svc) }
func (r *rig) state(svc string) string {
	return r.fake.StateOf(svc)
}
func (r *rig) cmds(verb string) int { return r.fake.CountOf(verb) }

func wait(t *testing.T, d time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", msg)
}

func TestMirrorAndEcho(t *testing.T) {
	p1, p2 := 18701, 18702
	l1, l2 := 18801, 18802
	u1, u2 := 18901, 18902
	a := newRig(t, "here", p1, l1, u1, syn.EngineConfig{Role: "symmetric", AutoReconcile: true})
	defer a.stop(t)
	b := newRig(t, "there", p2, l2, u2, syn.EngineConfig{Role: "symmetric", AutoReconcile: true})
	defer b.stop(t)

	if _, err := b.link.Dial(fmt.Sprintf("ws://127.0.0.1:%d", l1)); err != nil {
		t.Fatalf("dial: %v", err)
	}
	wait(t, 5*time.Second, "both see one peer", func() bool {
		return len(a.link.Peers()) == 1 && len(b.link.Peers()) == 1
	})
	wait(t, 5*time.Second, "GSX connected on both", func() bool {
		return a.gsx.Connected() && b.gsx.Connected()
	})
	wait(t, 5*time.Second, "digests flowing", func() bool { return len(a.engine.PeersDigests()) >= 1 })

	a.toggle("Boarding")
	wait(t, 4*time.Second, "the other cockpit ran the same service", func() bool {
		return b.state("Boarding") == fake.Active
	})
	time.Sleep(700 * time.Millisecond)
	if n := a.cmds("service.trigger"); n != 0 {
		t.Errorf("echo bounced: here received %d service.trigger command(s) of its own action", n)
	}
	if n := b.cmds("service.trigger"); n != 1 {
		t.Errorf("there ran service.trigger %d time(s), want exactly 1", n)
	}
	if st := a.engine.Status().Sync.Counters; st.DroppedDupe < 0 {
		t.Errorf("counters broken: %+v", st)
	}
}

func TestChurnIsNotAnAction(t *testing.T) {
	p1, p2 := 18711, 18712
	l1, l2 := 18811, 18812
	u1, u2 := 18911, 18912
	a := newRig(t, "here", p1, l1, u1, syn.EngineConfig{Role: "symmetric", AutoReconcile: true})
	defer a.stop(t)
	b := newRig(t, "there", p2, l2, u2, syn.EngineConfig{Role: "symmetric", AutoReconcile: true})
	defer b.stop(t)
	if _, err := b.link.Dial(fmt.Sprintf("ws://127.0.0.1:%d", l1)); err != nil {
		t.Fatal(err)
	}
	wait(t, 5*time.Second, "peers", func() bool { return len(a.link.Peers()) == 1 && len(b.link.Peers()) == 1 })
	wait(t, 5*time.Second, "connected", func() bool { return a.gsx.Connected() && b.gsx.Connected() })

	a.toggle("Refueling")
	wait(t, 4*time.Second, "first mirror", func() bool { return b.state("Refueling") == fake.Active })
	before := b.cmds("service.trigger")
	for i := 0; i < 8; i++ {
		a.fake.Progress("Refueling", 10+i*10)
		b.fake.Progress("Refueling", 10+i*10)
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(600 * time.Millisecond)
	if got := b.cmds("service.trigger"); got != before {
		t.Errorf("progress churn caused %d replay(s) on the peer, want 0", got-before)
	}
}

func TestMenuByLabel(t *testing.T) {
	p1, p2 := 18721, 18722
	l1, l2 := 18821, 18822
	u1, u2 := 18921, 18922
	a := newRig(t, "here", p1, l1, u1, syn.EngineConfig{Role: "symmetric", MirrorMenuPicks: true, AutoReconcile: false})
	defer a.stop(t)
	b := newRig(t, "there", p2, l2, u2, syn.EngineConfig{Role: "symmetric", MirrorMenuPicks: true, AutoReconcile: false})
	defer b.stop(t)
	if _, err := b.link.Dial(fmt.Sprintf("ws://127.0.0.1:%d", l1)); err != nil {
		t.Fatal(err)
	}
	wait(t, 5*time.Second, "peers", func() bool { return len(a.link.Peers()) == 1 })
	wait(t, 5*time.Second, "connected", func() bool { return a.gsx.Connected() && b.gsx.Connected() })

	a.fake.SetMenu([]string{"Operate GPU", "Boarding", "Ramp Notes", "Fuelup"})
	b.fake.SetMenu([]string{"Ramp Notes", "Fuelup", "Boarding", "Operate GPU"})
	a.fake.OpenMenu(true)
	b.fake.OpenMenu(false)
	wait(t, 3*time.Second, "menu visible", func() bool { return len(a.engine.MenuLabels()) > 0 })

	a.fake.Pick(2)
	wait(t, 4*time.Second, "peer picked its own index for the same words", func() bool {
		return b.cmds("menu.pick") > 0
	})
	for _, c := range b.fake.Cmds() {
		if c.Verb == "menu.pick" {
			if idx := int(c.Args["index"].(float64)); idx == 2 {
				t.Errorf("peer replayed OUR index 2 instead of finding its own: %+v", c.Args)
			}
		}
	}

}

func TestDriftReconcile(t *testing.T) {
	p1, p2 := 18731, 18732
	l1, l2 := 18831, 18832
	u1, u2 := 18931, 18932
	a := newRig(t, "here", p1, l1, u1, syn.EngineConfig{Role: "symmetric", AutoReconcile: true, ConfirmDigests: 1, GraceSeconds: 0})
	defer a.stop(t)
	b := newRig(t, "there", p2, l2, u2, syn.EngineConfig{Role: "symmetric", AutoReconcile: true, ConfirmDigests: 1, GraceSeconds: 0})
	defer b.stop(t)
	if _, err := b.link.Dial(fmt.Sprintf("ws://127.0.0.1:%d", l1)); err != nil {
		t.Fatal(err)
	}
	wait(t, 5*time.Second, "peers", func() bool { return len(a.link.Peers()) == 1 })
	wait(t, 5*time.Second, "connected", func() bool { return a.gsx.Connected() && b.gsx.Connected() })
	time.Sleep(800 * time.Millisecond)

	b.fake.Toggle("Catering")
	wait(t, 4*time.Second, "catering mirrored here", func() bool { return a.state("Catering") == fake.Active })
	a.fake.SetRaw("Catering", fake.Idle)
	wait(t, 6*time.Second, "reconcile pulls us up to the peer", func() bool {
		return a.state("Catering") == fake.Active
	})
	if n := a.cmds("service.trigger"); n == 0 {
		t.Error("reconcile fixed the state without issuing a command?")
	}
}

func TestSilenceIsSilence(t *testing.T) {
	p1, p2 := 18741, 18742
	l1, l2 := 18841, 18842
	u1, u2 := 18941, 18942
	a := newRig(t, "here", p1, l1, u1, syn.EngineConfig{Role: "symmetric", AutoReconcile: true})
	defer a.stop(t)
	b := newRig(t, "there", p2, l2, u2, syn.EngineConfig{Role: "symmetric", AutoReconcile: true})
	defer b.stop(t)
	if _, err := b.link.Dial(fmt.Sprintf("ws://127.0.0.1:%d", l1)); err != nil {
		t.Fatal(err)
	}
	wait(t, 5*time.Second, "peers", func() bool { return len(a.link.Peers()) == 1 })
	wait(t, 5*time.Second, "connected", func() bool { return a.gsx.Connected() && b.gsx.Connected() })
	before := a.cmds("service.trigger") + b.cmds("service.trigger")
	time.Sleep(3 * time.Second)
	if got := a.cmds("service.trigger") + b.cmds("service.trigger"); got != before {
		t.Errorf("idle room issued %d command(s), want 0", got-before)
	}
}

func TestRelayExactlyOnce(t *testing.T) {
	relayPort := 18850
	log := logx.New(level())
	r := relay.New(relayPort, "127.0.0.1", log.Child("[relay]"))
	go r.Listen()
	defer r.Stop()
	time.Sleep(300 * time.Millisecond)

	mk := func(name string, cp, lp, up int) *rig {
		cfg := syn.EngineConfig{Role: "symmetric", AutoReconcile: false}
		rg := newRig(t, name, cp, lp, up, cfg)
		rg.link.SetRoom("TEST1", "")
		rg.link.Relay("ws://127.0.0.1:"+itoa(relayPort), "TEST1", "")
		return rg
	}
	x := mk("x", 18751, 18851, 18951)
	defer x.stop(t)
	y := mk("y", 18752, 18852, 18952)
	defer y.stop(t)
	z := mk("z", 18753, 18853, 18953)
	defer z.stop(t)

	wait(t, 8*time.Second, "three cockpits in one relayed room (relay rooms: "+fmt.Sprint(r.Rooms())+")", func() bool {
		return len(x.link.Peers()) == 2 && len(y.link.Peers()) == 2 && len(z.link.Peers()) == 2
	})
	x.fake.Toggle("GPU")
	wait(t, 5*time.Second, "both others applied the GPU action", func() bool {
		return y.state("GPU") == fake.Active && z.state("GPU") == fake.Active
	})
	time.Sleep(600 * time.Millisecond)
	if y.cmds("service.trigger") != 1 || z.cmds("service.trigger") != 1 {
		t.Errorf("relay fan-out applied %d and %d times, want 1 each", y.cmds("service.trigger"), z.cmds("service.trigger"))
	}
}

func itoa(i int) string { return fmt.Sprint(i) }

func TestMain(m *testing.M) {

	_ = filepath.Join
	m.Run()
}

var _ = http.StatusOK
var _ = websocket.DefaultDialer
var _ = sync.Mutex{}
