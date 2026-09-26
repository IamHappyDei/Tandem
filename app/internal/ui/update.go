package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	repo        = "IamHappyDei/Tandem"
	tagsAPI     = "https://api.github.com/repos/" + repo + "/tags"
	releasePage = "https://github.com/" + repo + "/releases"
	updateEvery = 6 * time.Hour
)

type assetRef struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest,omitempty"`
	HasURL bool   `json:"hasAsset"`
}

type Update struct {
	Current string    `json:"current"`
	Latest  string    `json:"latest,omitempty"`
	Newer   bool      `json:"newer"`
	URL     string    `json:"url"`
	Checked time.Time `json:"checked"`
	Fail    string    `json:"fail,omitempty"`
	Asset   assetRef  `json:"asset"`
}

var tagRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

func checkUpdate(client *http.Client) Update {
	u := Update{Current: Version, URL: releasePage, Checked: time.Now()}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequest("GET", tagsAPI, nil)
	if err != nil {
		u.Fail = err.Error()
		return u
	}
	req.Header.Set("User-Agent", "tandem/"+Version)
	res, err := client.Do(req)
	if err != nil {
		u.Fail = "no answer from github"
		return u
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		u.Fail = "github said " + strconv.Itoa(res.StatusCode)
		return u
	}
	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(res.Body).Decode(&tags); err != nil {
		u.Fail = "the tag list did not parse"
		return u
	}
	here, best, bestV := parseVer(Version), "", []int(nil)
	for _, t := range tags {
		v := parseVer(t.Name)
		if v == nil {
			continue
		}
		if bestV == nil || less(bestV, v) {
			bestV, best = v, strings.TrimPrefix(t.Name, "v")
		}
	}
	if best == "" {
		u.Fail = "no tagged release yet"
		return u
	}
	u.Latest = best
	u.Newer = bestV != nil && less(here, bestV)
	if u.Newer {
		u.URL = "https://github.com/" + repo + "/releases/tag/v" + best
	}
	return u
}

func parseVer(s string) []int {
	m := tagRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return nil
	}
	out := make([]int, 3)
	for i := 1; i <= 3; i++ {
		out[i-1], _ = strconv.Atoi(m[i])
	}
	return out
}

func less(a, b []int) bool {
	if len(a) != 3 || len(b) != 3 {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func (a *App) updateNow() Update {
	u := checkUpdate(a.upClient)
	a.mu.Lock()
	a.up = u
	a.mu.Unlock()
	if u.Newer {
		a.log.Info("Tandem %s is out, you are on %s - %s", u.Latest, Version, u.URL)
	} else if u.Fail != "" {
		a.log.Debug("update check: %s", u.Fail)
	}
	if u.Newer {
		if rel, err := latestRelease(nil); err == nil && strings.EqualFold(rel.version(), u.Latest) {
			if a2, err := pickInstaller(rel); err == nil {
				u.Asset = assetRef{Name: a2.Name, Size: a2.Size, Digest: wantDigest(a2), HasURL: a2.BrowserDownloadURL != ""}
			}
		}
		if u.Asset.Name == "" {
			u.Fail = "no installer is attached to that release yet"
		}
	}
	return u
}

func (a *App) updateInfo() Update {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.up.URL == "" {
		return Update{Current: Version, URL: releasePage}
	}
	return a.up
}

func (a *App) updateLoop() {
	if a.cfg.UI.NoUpdateCheck {
		return
	}
	for attempt := 0; attempt < 3; attempt++ {
		if a.updateNow().Fail == "" {
			break
		}
		time.Sleep(time.Duration(5*(attempt+1)) * time.Second)
	}
	for {
		time.Sleep(updateEvery)
		a.updateNow()
	}
}

func UpdateLine(u Update) string {
	switch {
	case u.Newer && u.Asset.Name != "":
		return fmt.Sprintf("Tandem %s is available - you have %s. Installer: %s (%s), ready to download",
			u.Latest, u.Current, u.Asset.Name, mb(u.Asset.Size))
	case u.Newer:
		return fmt.Sprintf("Tandem %s is available - you have %s, but no installer is attached to that release yet: %s",
			u.Latest, u.Current, u.URL)
	case u.Latest != "" && less(parseVer(u.Latest), parseVer(Version)):
		return fmt.Sprintf("you are ahead of the newest published release (%s)", u.Latest)
	case u.Latest != "":
		return fmt.Sprintf("%s is the newest release - nothing to do", u.Current)
	case u.Fail != "":
		return "update check: " + plainFail(u.Fail)
	default:
		return "not checked yet"
	}
}

func plainFail(f string) string {
	switch {
	case strings.Contains(f, "tagged release"):
		return "no release has been published yet"
	case strings.Contains(f, "no answer"):
		return "github was not reachable"
	default:
		return f
	}
}

func (a *App) hUpdate(r *http.Request, body map[string]any) (any, error) {
	return a.updateNow(), nil
}

func CheckNow() Update { return checkUpdate(nil) }

func (a *App) hApply(r *http.Request, body map[string]any) (any, error) {
	path, err := a.applyUpdate()
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "installer": filepath.Base(path)}, nil
}
