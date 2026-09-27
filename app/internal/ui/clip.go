package ui

import (
	"net/http"

	"tandem/internal/clip"
)

func (a *App) hClipboard(w http.ResponseWriter, r *http.Request) (any, error) {
	t, err := clip.Text()
	if err != nil {
		return nil, err
	}
	return map[string]any{"text": t}, nil
}
