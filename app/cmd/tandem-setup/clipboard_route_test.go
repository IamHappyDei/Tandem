package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestClipboardRoute(t *testing.T) {
	want := filepath.FromSlash("D:/MSFS/Community")
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("no powershell here:", err)
	}
	ps := "Set-Clipboard -Value ('" + want + "'.Replace('/', [char]92))"
	if out, err := exec.Command("powershell.exe", "-NoProfile", "-Command", ps).CombinedOutput(); err != nil {
		t.Skip("no clipboard here:", string(out))
	}
	srv := httptest.NewServer((func() http.Handler { g := &gui{}; return g.Handler() })())
	defer srv.Close()
	res, err := http.Post(srv.URL+"/api/clipboard", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var d map[string]any
	if json.NewDecoder(res.Body).Decode(&d) != nil {
		t.Fatal("the route did not answer with json")
	}
	got, _ := d["text"].(string)
	if strings.TrimSpace(got) != want {
		t.Fatalf("the page would have pasted %q, wanted %q", got, want)
	}
}
