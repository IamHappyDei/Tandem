//go:build !windows

package pick

import "errors"

// Folder has no picker off Windows (the app is a Windows product; this only needs
// to compile so the Linux relay binary can be built in a plain Docker image).
func Folder(title string) (string, error) {
	return "", errors.New("folder picker is only available on Windows")
}
