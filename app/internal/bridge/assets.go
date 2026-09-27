package bridge

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:package
var pkg embed.FS

const marker = ".tandem-bridge"

var installedNote = "tandem put this folder here"

func (s *Server) InstallInto(community string) (string, error) {
	dest := filepath.Join(community, "tandem-bridge")
	if err := os.MkdirAll(filepath.Join(dest, marker), 0o755); err != nil {
		return "", err
	}
	err := fs.WalkDir(pkg, "package", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := pkg.ReadFile(p)
		if err != nil {
			return err
		}
		to := filepath.Join(dest, filepath.FromSlash(p[len("package/"):]))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o644)
	})
	if err != nil {
		return "", err
	}
	note := filepath.Join(dest, marker, "installed")
	if err := os.WriteFile(note, []byte(installedNote), 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

func InstalledIn(community string) bool {
	_, err := os.Stat(filepath.Join(community, "tandem-bridge", marker, "installed"))
	return err == nil
}

func UninstallFrom(community string) error {
	if !InstalledIn(community) {
		return nil
	}
	return os.RemoveAll(filepath.Join(community, "tandem-bridge"))
}
