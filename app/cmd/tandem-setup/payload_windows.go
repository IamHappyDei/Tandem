//go:build windows

package main

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:payload
var payloadFS embed.FS

const payloadDir = "payload"

func payloadNames() []string {
	return append(append([]string{}, payload...), setupName)
}

func embedded(name string) (fs.File, int64, error) {
	f, err := payloadFS.Open(payloadDir + "/" + name)
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

func installBytes(name, to string, src io.Reader, size int64) error {
	tmp := to + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, src)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if size > 0 && n != size {
		_ = os.Remove(tmp)
		return fmt.Errorf("%s came out %d bytes, was expected %d", filepath.Base(to), n, size)
	}
	if hasFile(to) {
		if err := os.Remove(to); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	return os.Rename(tmp, to)
}

func writeEmbedded(name, to string) (int64, error) {
	f, size, err := embedded(name)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if err := installBytes(name, to, f, size); err != nil {
		return 0, err
	}
	return size, nil
}

func selfCopyInto(dir string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	in, err := os.Open(self)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	return installBytes(setupName, filepath.Join(dir, setupName), in, st.Size())
}

func hasPayload() bool {
	_, _, err := embedded(exeName)
	return err == nil
}

func payloadState() string {
	if hasPayload() {
		return "the program is carried inside this installer"
	}
	self, _ := os.Executable()
	dir := filepath.Dir(self)
	var found []string
	for _, n := range payload {
		if hasFile(filepath.Join(dir, n)) {
			found = append(found, n)
		}
	}
	if len(found) == 0 {
		return "nothing to install: this copy carries no program and none sits beside it"
	}
	return "installing what sits beside this file: " + strings.Join(found, ", ")
}
