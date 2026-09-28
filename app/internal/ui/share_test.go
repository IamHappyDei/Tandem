package ui

import (
	"strings"
	"testing"

	"tandem/internal/conf"
)

func TestShareRoundTrip(t *testing.T) {
	s := &share{Public: "203.0.113.9:8790", Addr: "203.0.113.9", TCP: 8790, UDPPort: 8791, Room: "KQ7TB", Pass: "hunter2"}
	got := s.short()
	want := "tdm1/203.0.113.9:8790+8791/KQ7TB#hunter2"
	if got != want {
		t.Fatalf("short form changed:\n got %s\nwant %s", got, want)
	}
	back, err := parseShare(got)
	if err != nil {
		t.Fatalf("our own string does not parse: %v", err)
	}
	if back.TCP != 8790 || back.UDPPort != 8791 {
		t.Errorf("ports swapped on decode: tcp=%d udp=%d", back.TCP, back.UDPPort)
	}
	if back.Room != "KQ7TB" || back.Pass != "hunter2" {
		t.Errorf("room/pass lost: %+v", back)
	}
}

func TestLegacyPrefixStillJoins(t *testing.T) {
	for _, in := range []string{
		"gsx1/203.0.113.9:8790/KQ7TB",
		"tdm1/203.0.113.9:8790/KQ7TB",
		"  gsx1/203.0.113.9:8790+8791/KQ7TB  ",
	} {
		sh, err := parseShare(in)
		if err != nil {
			t.Fatalf("%q rejected: %v", in, err)
		}
		if sh.Room != "KQ7TB" || sh.TCP != 8790 {
			t.Errorf("%q decoded wrong: %+v", in, sh)
		}
	}
}

func TestLocalAddressIsAcceptedButFlagged(t *testing.T) {
	sh, err := parseShare("tdm1/192.168.1.20:8790/AAAAA")
	if err != nil {
		t.Fatalf("a LAN string must work on the LAN, it only warns: %v", err)
	}
	if !sh.Local {
		t.Error("LAN string not flagged - the UI cannot explain a silent stall")
	}
	if sh, err := parseShare("tdm1/8.8.8.8:8790/AAAAA"); err != nil || sh.Local {
		t.Errorf("public string misread: %+v %v", sh, err)
	}
}

func TestOpaqueRoundTrip(t *testing.T) {
	for _, pass := range []string{"", "hunter2"} {
		s := &share{Public: "203.0.113.9:8790", TCP: 8790, UDPPort: 8791, Addr: "203.0.113.9",
			Room: "WN5TR", Pass: pass, Name: "Cockpit A", By: "abc123", Cands: []string{"203.0.113.9:8791"}}
		code, err := s.opaque()
		if err != nil {
			t.Fatal(err)
		}
		for _, leak := range []string{"203.0.113.9", "WN5TR", "8790"} {
			if strings.Contains(code, leak) {
				t.Errorf("scrambled string still contains %q: %s", leak, code)
			}
		}
		if !strings.HasPrefix(code, OpaquePrefix) {
			t.Errorf("prefix: %s", code)
		}
		back, err := parseShare(code)
		if err != nil {
			t.Fatalf("open %q: %v", code, err)
		}
		if back.Room != "WN5TR" || back.Public != s.Public || back.UDPPort != 8791 {
			t.Errorf("round trip lost something: %+v", back)
		}
		if pass != "" && back.Pass != pass {
			t.Errorf("passphrase %q did not survive", back.Pass)
		}
		if len(code) < 60 {
			t.Errorf("scrambled form should be visibly longer than a plain one: %q", code)
		}
	}
}

func TestOpaqueNeedsThePassphrase(t *testing.T) {
	s := &share{Public: "203.0.113.9:8790", TCP: 8790, UDPPort: 8790, Room: "WN5TR", Pass: "secret"}
	code, err := s.opaque()
	if err != nil {
		t.Fatal(err)
	}
	naked := strings.TrimSuffix(code, "#secret")
	if _, err := parseShare(naked); err == nil {
		t.Error("a scrambled link opened without its passphrase - the key is not doing anything")
	}
}

func TestOpaqueStaysOpaque(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		s := &share{Public: "203.0.113.9:8790", TCP: 8790, Room: "WN5TR", Cands: []string{"203.0.113.9:8790"}}
		code, err := s.opaque()
		if err != nil {
			t.Fatal(err)
		}
		if seen[code] {
			t.Fatal("two invites came out identical: the nonce is not random, so one leak leaks all")
		}
		seen[code] = true
	}
}

// The relay-join path treats a typed room code as a bare code, and room codes come
// from conf.RoomCode(). If those ever drift apart in length, typing a code stops working.
func TestRoomCodeIsTypedAsBareCode(t *testing.T) {
	for i := 0; i < 20; i++ {
		if c := conf.RoomCode(); !isBareCode(c) {
			t.Fatalf("room code %q would not be recognised when typed", c)
		}
	}
}
