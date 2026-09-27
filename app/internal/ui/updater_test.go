package ui

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tandem/internal/logx"
)

type stub struct {
	t      *testing.T
	rel    release
	body   []byte
	asset  asset
	chop   int
	server *httptest.Server
}

func newStub(t *testing.T, rel release, body []byte, assetName string) *stub {
	s := &stub{t: t, body: body, asset: asset{Name: assetName}}
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if rel.TagName == "" {
			w.WriteHeader(404)
			io.WriteString(w, `{"message":"Not Found"}`)
			return
		}
		rel.Assets = []asset{s.asset}
		for i := range rel.Assets {
			rel.Assets[i].BrowserDownloadURL = s.server.URL + "/dl"
		}
		json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/dl", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		if s.chop > 0 {
			w.Header().Set("Content-Length", fmt.Sprint(len(s.body)))
			s.chop--
			w.Write(s.body[:len(s.body)/2])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			panic("stop")
		}
		w.Write(s.body)
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		w.Write(s.body)
	})
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

func (s *stub) client() *http.Client {
	base := s.server.URL
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Host, ":80") || strings.Contains(r.URL.Host, "127.0.0.1") {
				return http.DefaultTransport.RoundTrip(r)
			}
			req, _ := http.NewRequest(r.Method, base+"/releases/latest", r.Body)
			req.Header = r.Header
			return http.DefaultTransport.RoundTrip(req)
		}),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func zipWith(t *testing.T, name string, body []byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("Tandem-" + Version + "/" + name)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(body)
	zw.Close()
	return buf.Bytes()
}

func TestPickInstallerPrefersTheSingleFile(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
		fail bool
	}{
		{name: "named exactly", in: []string{"tandem-setup.exe"}, want: "tandem-setup.exe"},
		{name: "versioned name", in: []string{"Tandem-1.0.0-setup.exe"}, want: "Tandem-1.0.0-setup.exe"},
		{name: "wrapped", in: []string{"Tandem-1.0.0.zip"}, want: "Tandem-1.0.0.zip"},
		{name: "sources only", in: []string{"source.zip", "Source code (zip)", "Tandem-source.zip", "notes.txt"}, fail: true},
		{name: "a bundle", in: []string{"Source code (zip)", "Tandem-1.0.0.zip"}, want: "Tandem-1.0.0.zip"},
		{name: "both", in: []string{"Tandem-1.0.0.zip", "tandem-setup.exe"}, want: "tandem-setup.exe"},
	}
	for _, c := range cases {
		var rel release
		for _, n := range c.in {
			rel.Assets = append(rel.Assets, asset{Name: n})
		}
		got, err := pickInstaller(rel)
		if c.fail {
			if err == nil {
				t.Errorf("%s: took %q as an installer", c.name, got.Name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got.Name != c.want {
			t.Errorf("%s: got %s want %s", c.name, got.Name, c.want)
		}
	}
}

func TestDownloadChecksTheLength(t *testing.T) {
	body := bytes.Repeat([]byte("go"), 40000)
	s := newStub(t, release{TagName: "v9.9.9"}, body, "tandem-setup.exe")
	dir := t.TempDir()

	got, err := downloadTo(s.client(), s.server.URL+"/dl", dir, "tandem-setup.exe", 4, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(got)
	if st.Size() != int64(len(body)) {
		t.Fatalf("wrote %d bytes, sent %d", st.Size(), len(body))
	}

	s.chop = 1
	var notes []string
	if _, err := downloadTo(s.client(), s.server.URL+"/dl", dir, "short.exe", 4, func(x string) { notes = append(notes, x) }); err == nil {
		t.Fatal("a download that stopped half way was accepted as complete")
	}
	if _, err := os.Stat(filepath.Join(dir, "short.exe")); !os.IsNotExist(err) {
		t.Error("the half-written file was left behind")
	}

	if _, err := downloadTo(s.client(), s.server.URL+"/nothing", dir, "x.exe", 4, func(string) {}); err == nil {
		t.Error("a 404 was accepted")
	}
}

func TestDigestGate(t *testing.T) {
	body := []byte("the installer bytes")
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])
	dir := t.TempDir()
	p := filepath.Join(dir, "tandem-setup.exe")
	if err := os.WriteFile(p, body, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkDigest(p, want); err != nil {
		t.Errorf("a matching checksum was rejected: %v", err)
	}
	if err := checkDigest(p, strings.Repeat("ab", 32)); err == nil {
		t.Error("a wrong checksum passed - the gate does nothing")
	}
	if err := checkDigest(p, ""); err != nil {
		t.Errorf("no checksum should not fail here: %v", err)
	}
	if got := wantDigest(asset{Digest: "SHA256:" + want}); got != want {
		t.Errorf("digest prefix: %s", got)
	}
}

func TestExtractSetupFromAZip(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "bundle.zip")
	body := []byte("installer")
	if err := os.WriteFile(zipPath, zipWith(t, "tandem-setup.exe", body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := extractSetup(zipPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "tandem-setup.exe" {
		t.Errorf("unpacked %s", got)
	}
	b, _ := os.ReadFile(got)
	if string(b) != "installer" {
		t.Errorf("contents came out wrong: %q", b)
	}

	other := filepath.Join(dir, "nope.zip")
	os.WriteFile(other, zipWith(t, "tandem.exe", body), 0o644)
	if _, err := extractSetup(other, dir); err == nil {
		t.Error("an archive without an installer in it was accepted")
	}
}

func TestApplyUpdateEndToEnd(t *testing.T) {
	body := bytes.Repeat([]byte("MZ"), 3000)
	sum := sha256.Sum256(body)
	dir := t.TempDir()

	s := newStub(t, release{TagName: "v9.9.9"}, body, "tandem-setup.exe")
	s.asset.Digest = "sha256:" + hex.EncodeToString(sum[:])

	old := updateDirName
	updateDirName = func() string { return filepath.Join(dir, "upd") }
	defer func() { updateDirName = old }()

	var started []string
	oldSpawn := spawn
	spawn = func(p string, args ...string) error { started = append(started, p); return nil }
	defer func() { spawn = oldSpawn }()

	a := &App{log: quietLog(), upClient: s.client()}
	got, err := a.applyUpdate()
	if err != nil {
		t.Fatalf("applyUpdate: %v", err)
	}
	if len(started) != 1 || !strings.HasSuffix(started[0], "tandem-setup.exe") {
		t.Fatalf("it ran %+v, not the installer", started)
	}
	if got != started[0] {
		t.Errorf("returned %q but started %q", got, started[0])
	}
	b, err := os.ReadFile(got)
	if err != nil || !bytes.Equal(b, body) {
		t.Error("the installer that was run is not the bytes that were published")
	}

	started = started[:0]
	if _, err := a.applyUpdate(); err != nil {
		t.Errorf("asking a second time failed: %v", err)
	}
	if len(started) != 1 {
		t.Errorf("a second press started %d installers, want 1", len(started))
	}

	s.asset.Digest = "sha256:" + strings.Repeat("00", 32)
	if _, err := a.applyUpdate(); err == nil || !strings.Contains(err.Error(), "not what was published") {
		t.Errorf("a file with the wrong checksum was allowed through: %v", err)
	}
}

func TestApplyUpdateRefusesWhatIsNotNewer(t *testing.T) {
	s := newStub(t, release{TagName: "v" + Version}, []byte("x"), "tandem-setup.exe")
	a := &App{log: quietLog(), upClient: s.client()}
	if _, err := a.applyUpdate(); err == nil || !strings.Contains(err.Error(), "not newer") {
		t.Errorf("it accepted the version it already has: %v", err)
	}

	none := newStub(t, release{}, nil, "")
	a2 := &App{log: quietLog(), upClient: none.client()}
	if _, err := a2.applyUpdate(); err == nil {
		t.Error("it claimed success with no release published")
	}
}

func TestReleaseWithoutInstaller(t *testing.T) {
	s := newStub(t, release{TagName: "v9.9.9"}, []byte("x"), "Source code (zip)")
	a := &App{log: quietLog(), upClient: s.client()}
	if _, err := a.applyUpdate(); err == nil {
		t.Fatal("a zip with no installer inside was started anyway")
	}
}

func quietLog() *logx.Log {
	l := logx.New("debug")
	l.SetQuiet(true)
	return l
}

func TestDownloadGetsFiveMinutesNotTwentySeconds(t *testing.T) {
	body := bytes.Repeat([]byte("setup"), 40000)
	s := newStub(t, release{TagName: "v9.9.9"}, body, "tandem-setup.exe")
	fast := &http.Client{Timeout: 10 * time.Millisecond}

	if _, err := downloadTo(fast, s.server.URL+"/slow", t.TempDir(), "tandem-setup.exe", 4, func(string) {}); err != nil {
		t.Fatalf("a lookup-sized client aborted the installer download: %v", err)
	}
	if got := bodyClient(nil).Timeout; got < 5*time.Minute {
		t.Errorf("a download client waits %s, want at least five minutes", got)
	}
	if got := bodyClient(&http.Client{Timeout: time.Second}).Timeout; got != 5*time.Minute {
		t.Errorf("a short client was left at %s", got)
	}
	long := &http.Client{Timeout: 2 * time.Hour}
	if bodyClient(long) != long {
		t.Errorf("a client that already allows a long body was shortened")
	}
}
