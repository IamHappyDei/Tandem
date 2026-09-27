package aircraft

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var simLine = regexp.MustCompile(`(?im)^\s*(sim|aircraft)\s*=\s*([^\r\n;]{3,120})`)

type flt struct {
	path string
	rank int
}

func FlightFiles() []string {
	var out []flt
	roots := []string{}
	if ld := os.Getenv("LOCALAPPDATA"); ld != "" {
		roots = append(roots,
			filepath.Join(ld, `Packages`),
			filepath.Join(ld, `Microsoft\MSFS2024`),
			filepath.Join(ld, `Microsoft\Flight Simulator 2024`),
		)
	}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			base := strings.ToLower(e.Name())
			switch {
			case strings.HasPrefix(base, "microsoft.limitless"),
				strings.HasPrefix(base, "microsoft.flightsimulator"):
			default:
				continue
			}
			rank := 0
			if strings.Contains(base, "limitless") {
				rank = 1
			}
			for _, f := range gather(filepath.Join(root, e.Name())) {
				out = append(out, flt{f, rank})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].rank != out[j].rank {
			return out[i].rank > out[j].rank
		}
		ai, aerr := os.Stat(out[i].path)
		bi, berr := os.Stat(out[j].path)
		if aerr != nil || berr != nil {
			return false
		}
		return ai.ModTime().After(bi.ModTime())
	})
	names := make([]string, 0, len(out))
	for _, f := range out {
		names = append(names, f.path)
	}
	return names
}

func gather(root string) []string {
	var found []string
	seen := 0
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			if fi != nil && fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if fi.IsDir() {
			if depth(p, root) > 6 || seen > 4000 {
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if !strings.EqualFold(filepath.Ext(fi.Name()), ".flt") {
			return nil
		}
		if strings.Contains(strings.ToLower(p), `\missions\asobo\`) {
			return nil
		}
		found = append(found, p)
		return nil
	})
	return found
}

func FromFlightFiles(pkgs []Package) (Package, string, bool) {
	pkgs = Aircraft(pkgs)
	files := FlightFiles()
	for _, f := range files[:minInt(len(files), 6)] {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, m := range simLine.FindAllSubmatch(b[:minInt(len(b), 1<<16)], -1) {
			said := strings.TrimSpace(string(m[2]))
			if said == "" || strings.EqualFold(said, "generic") {
				continue
			}
			if p, ok := Guess(pkgs, said); ok {
				return p, "from the last flight file", true
			}
			return Package{}, "the sim says " + said + " and nothing in the folder answers to it", false
		}
	}
	return Package{}, "", false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
