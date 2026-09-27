package main

import (
	"os/exec"
	"strings"
	"testing"

	"tandem/internal/clip"
)

func TestClipboardRoundTrip(t *testing.T) {
	want := `D:\MSFS\Community`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", "Set-Clipboard -Value '"+want+"'")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skip("no clipboard here:", string(out))
	}
	got, err := clip.Text()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) != want {
		t.Fatalf("clipboard said %q, wanted %q", got, want)
	}
}
