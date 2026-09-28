package relay

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"tandem/internal/conf"
	"tandem/internal/logx"
	"tandem/internal/room"
)

// hello dials the relay and introduces itself as name from origin, in room.
func hello(t *testing.T, url, name, origin, roomName string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := c.WriteJSON(room.Msg{T: "hello", Name: name, Origin: origin, Room: roomName}); err != nil {
		t.Fatalf("hello: %v", err)
	}
	var welcome room.Msg
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := c.ReadJSON(&welcome); err != nil || welcome.T != "welcome" {
		t.Fatalf("%s got no welcome (%v, t=%q)", name, err, welcome.T)
	}
	_ = c.SetReadDeadline(time.Time{})
	return c
}

func TestRoomCodesAreBareCodeLength(t *testing.T) {
	// The join box treats an input as a bare code only at exactly 8 alphanumerics
	// (isBareCode). A room code that is not 8 chars can never be typed in as a code.
	for i := 0; i < 50; i++ {
		c := conf.RoomCode()
		if len(c) != 8 {
			t.Fatalf("RoomCode returned %q (%d chars) - the paste box will not accept it as a bare code", c, len(c))
		}
		if strings.ToLower(c) != strings.ToLower(strings.TrimSpace(c)) {
			t.Fatalf("RoomCode returned non-alphanumeric: %q", c)
		}
	}
}

func TestRelayRoutesBetweenTwoOriginsAndSwallowsEcho(t *testing.T) {
	s := New(18931, "127.0.0.1", logx.New("error"))
	go func() { _ = s.Listen() }()
	defer s.Stop()
	time.Sleep(150 * time.Millisecond)

	url := "ws://127.0.0.1:18931/ws"

	a := hello(t, url, "Here", "AAA", "TESTRM")
	defer a.Close()
	b := hello(t, url, "There", "BBB", "TESTRM")
	defer b.Close()

	// A's broadcast must reach B, tagged as coming via the relay from A.
	if err := a.WriteJSON(room.Msg{T: "state", Origin: "AAA"}); err != nil {
		t.Fatal(err)
	}
	_ = b.SetReadDeadline(time.Now().Add(3 * time.Second))
	var got room.Msg
	if err := b.ReadJSON(&got); err != nil {
		t.Fatalf("B heard nothing: %v", err)
	}
	if got.From == nil || got.From.Origin != "AAA" {
		t.Fatalf("B got a message not attributed to A: %+v", got.From)
	}

	// The relay must not echo A's own message back to A (same origin filtered).
	_ = a.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
	if err := a.ReadJSON(&got); err == nil && got.T != "pong" {
		t.Fatalf("A heard an echo of its own broadcast: %+v", got)
	}
	_ = a.SetReadDeadline(time.Time{})
}

func TestRelayHealthEndpointAnswers(t *testing.T) {
	s := New(18932, "127.0.0.1", logx.New("error"))
	go func() { _ = s.Listen() }()
	defer s.Stop()
	time.Sleep(150 * time.Millisecond)
	for _, path := range []string{"/", "/stats"} {
		resp, err := http.Get("http://127.0.0.1:18932" + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(b), "tandem relay up") && !strings.Contains(string(b), "connected") {
			t.Fatalf("%s answered %d %q", path, resp.StatusCode, string(b))
		}
	}
}
