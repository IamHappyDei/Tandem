package ui

import (
	"net/http"

	"tandem/internal/pick"
)

func (a *App) hBrowse(w http.ResponseWriter, r *http.Request) (any, error) {
	where, err := pick.Folder("")
	if err != nil {
		return nil, err
	}
	a.log.Info("you picked %s", where)
	return map[string]any{"path": where}, nil
}
