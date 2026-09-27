package ui

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const updateFolder = "tandem-update"

const downloadFor = 5 * time.Minute

type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
}

type release struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Assets  []asset `json:"assets"`
}

func (r release) version() string { return strings.TrimPrefix(r.TagName, "v") }

func latestRelease(client *http.Client) (release, error) {
	var rel release
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequest("GET", strings.Replace(tagsAPI, "/tags", "/releases/latest", 1), nil)
	if err != nil {
		return rel, err
	}
	req.Header.Set("User-Agent", "tandem/"+Version)
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := client.Do(req)
	if err != nil {
		return rel, fmt.Errorf("github did not answer")
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return rel, fmt.Errorf("no release has been published for %s yet", repo)
	}
	if res.StatusCode != 200 {
		return rel, fmt.Errorf("github said %s", strconv.Itoa(res.StatusCode))
	}
	if err := json.NewDecoder(res.Body).Decode(&rel); err != nil {
		return rel, fmt.Errorf("the release description did not parse")
	}
	return rel, nil
}

func pickInstaller(rel release) (asset, error) {
	var exact, named asset
	for _, a := range rel.Assets {
		n := strings.ToLower(a.Name)
		switch {
		case n == "tandem-setup.exe":
			exact = a
		case strings.HasSuffix(n, ".exe") && strings.Contains(n, "setup"):
			named = a
		}
	}
	if exact.Name != "" {
		return exact, nil
	}
	if named.Name != "" {
		return named, nil
	}

	for _, a := range rel.Assets {
		n := strings.ToLower(a.Name)
		if strings.HasSuffix(n, ".zip") && strings.Contains(n, "tandem") && !strings.Contains(n, "source") {
			return a, nil
		}
	}
	return asset{}, fmt.Errorf("that release carries no installer of its own - attach tandem-setup.exe to it")
}
func downloadTo(client *http.Client, url, dir, name string, every int, note func(string)) (string, error) {
	client = bodyClient(client)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "tandem/"+Version)
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("the download did not start")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("the download was refused with http %s", strconv.Itoa(res.StatusCode))
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	buf := make([]byte, 256*1024)
	var done, next int64
	total := res.ContentLength
	if total > 0 {
		next = total / int64(every)
	}
	for {
		n, err := res.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				_ = os.Remove(path)
				return "", werr
			}
			done += int64(n)
			if total > 0 && done >= next && next > 0 {
				note(fmt.Sprintf("%s of %s", mb(done), mb(total)))
				for next <= done {
					next += total / int64(every)
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			f.Close()
			_ = os.Remove(path)
			return "", fmt.Errorf("the download stopped after %s: %v", mb(done), err)
		}
	}
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(path)
		return "", cerr
	}
	if total > 0 && done != total {
		_ = os.Remove(path)
		return "", fmt.Errorf("it ended after %s of %s", mb(done), mb(total))
	}
	return path, nil
}

func mb(n int64) string {
	if n >= 1<<20 {
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
	}
	return strconv.FormatInt(n, 10) + " KB"
}

func sha256Of(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func wantDigest(a asset) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(a.Digest)), "sha256:")
}

func checkDigest(path, want string) error {
	if want == "" {
		return nil
	}
	got, err := sha256Of(path)
	if err != nil {
		return err
	}
	if len(want) < 12 || len(got) < 12 {
		return fmt.Errorf("the published checksum is not a sha256")
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("what arrived is not what was published - sha256 %s, expected %s", got[:12], want[:12])
	}
	return nil
}

func extractSetup(src, dir string) (string, error) {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return "", fmt.Errorf("that archive did not open: %v", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if f.FileInfo().IsDir() || !strings.EqualFold(base, "tandem-setup.exe") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		dst := filepath.Join(dir, base)
		wf, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			rc.Close()
			return "", err
		}
		_, cerr := io.Copy(wf, rc)
		rc.Close()
		if zerr := wf.Close(); cerr == nil {
			cerr = zerr
		}
		if cerr != nil {
			_ = os.Remove(dst)
			return "", cerr
		}
		return dst, nil
	}
	return "", fmt.Errorf("there is no tandem-setup.exe inside that archive")
}

func bodyClient(c *http.Client) *http.Client {
	if c == nil {
		return &http.Client{Timeout: downloadFor}
	}
	if c.Timeout > 0 && c.Timeout < downloadFor {
		dup := *c
		dup.Timeout = downloadFor
		return &dup
	}
	return c
}

var updateDirName = func() string { return filepath.Join(os.TempDir(), updateFolder) }

func (a *App) applyUpdate() (string, error) {
	rel, err := latestRelease(a.upClient)
	if err != nil {
		return "", err
	}
	if !less(parseVer(Version), parseVer(rel.version())) {
		return "", fmt.Errorf("%s is not newer than the %s you are running", rel.version(), Version)
	}
	pick, err := pickInstaller(rel)
	if err != nil {
		return "", err
	}
	a.log.Info("update: fetching %s from the %s release", pick.Name, rel.TagName)
	raw, err := downloadTo(a.upClient, pick.BrowserDownloadURL, updateDirName(), pick.Name, 8, func(s string) {
		a.log.Debug("update: %s", s)
	})
	if err != nil {
		return "", err
	}
	if want := wantDigest(pick); want != "" {
		if err := checkDigest(raw, want); err != nil {
			_ = os.Remove(raw)
			return "", err
		}
		a.log.Info("update: checksum matches the one github published with it")
	} else {
		a.log.Warn("update: that release has no checksum attached, so the file is taken on trust")
	}
	setup, err := installerFrom(raw, updateDirName())
	if err != nil {
		return "", err
	}
	if !hasSetupName(setup) {
		return "", fmt.Errorf("what came down (%s) is not an installer", filepath.Base(setup))
	}
	a.log.Info("update: starting %s - this copy is closing so the files are free", filepath.Base(setup))
	if a.OnQuit != nil {
		go func() {
			time.Sleep(600 * time.Millisecond)
			if err := spawn(setup); err != nil {
				a.log.Warn("update: could not start it: %v", err)
			}
			a.log.Info("update: finish the installer's window, then start Tandem again")
			os.Exit(0)
		}()
	} else {
		if err := spawn(setup); err != nil {
			return "", err
		}
	}
	return setup, nil
}

func installerFrom(raw, dir string) (string, error) {
	if !strings.HasSuffix(strings.ToLower(raw), ".zip") {
		return raw, nil
	}
	setup, err := extractSetup(raw, dir)
	if err != nil {
		return "", err
	}
	if err := os.Remove(raw); err != nil && !os.IsNotExist(err) {
		_ = err
	}
	return setup, nil
}

func hasSetupName(p string) bool {
	return strings.Contains(strings.ToLower(filepath.Base(p)), "setup") && strings.HasSuffix(strings.ToLower(p), ".exe")
}

var spawn = startInstaller

func startInstaller(path string, args ...string) error {
	cmd := exec.Command(path, args...)
	cmd.Dir = filepath.Dir(path)
	return cmd.Start()
}
