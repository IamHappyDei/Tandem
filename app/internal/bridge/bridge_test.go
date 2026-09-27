package bridge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"tandem/internal/aircraft"
	"tandem/internal/logx"
)

func start(t *testing.T) (*Server, func(map[string]any)) {
	t.Helper()
	seen := make(chan map[string]any, 8)
	s := New(0, logx.New("warn"))
	s.changed = seen
	if err := s.Start(func(v map[string]any) { seen <- v }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	return s, func(map[string]any) {}
}

func dial(t *testing.T, s *Server) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(s.URL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func readFrame(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := c.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSnapshotOnConnect(t *testing.T) {
	s, _ := start(t)
	c := dial(t, s)
	m := readFrame(t, c)
	if m["type"] != "snapshot" {
		t.Fatalf("first frame is %v, want snapshot", m["type"])
	}
}

func TestInboundVarReachesWatchers(t *testing.T) {
	s, _ := start(t)
	c := dial(t, s)
	_ = readFrame(t, c)
	_ = c.WriteMessage(websocket.TextMessage,
		[]byte(`{"type":"patch","path":"vars.KAP700_STANDBY_POWER","value":2}`))
	ok := false
	for i := 0; i < 20 && !ok; i++ {
		select {
		case v := <-s.changed:
			if got, _ := v["KAP700_STANDBY_POWER"].(float64); got == 2 {
				ok = true
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !ok {
		t.Fatal("the var never reached the app")
	}
	if v := s.Vars(); v["KAP700_STANDBY_POWER"] == nil {
		t.Fatal("the server did not keep it")
	}
}

func TestIdentityFromTheSim(t *testing.T) {
	s, _ := start(t)
	c := dial(t, s)
	_ = readFrame(t, c)
	_ = c.WriteMessage(websocket.TextMessage,
		[]byte(`{"type":"patch","path":"aircraft.title","value":"Fenix A321"}`))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if title, _ := s.Aircraft(); strings.HasPrefix(title, "Fenix") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	pkgs := []aircraft.Package{{Title: "Fenix Airbus A321"}}
	p, why, ok := s.Detect(pkgs)
	if !ok || p.Title != "Fenix Airbus A321" || why != "the sim said so" {
		t.Fatalf("detect: title=%v why=%q ok=%v", p.Title, why, ok)
	}
}

func TestWriteGoesOutAsAPatch(t *testing.T) {
	s, _ := start(t)
	c := dial(t, s)
	_ = readFrame(t, c)
	s.Write("BLEED_AIR", true)
	m := readFrame(t, c)
	if m["type"] != "patch" || m["path"] != "vars.BLEED_AIR" || m["write"] != true {
		t.Fatalf("outbound frame: %v", m)
	}
}
