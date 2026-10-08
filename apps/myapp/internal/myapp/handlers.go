package myapp

import (
	"log/slog"
	"net/http"

	"amadan.net/rastrillo/rastrillo/db"
)

// app holds what every handler needs. When the app grows accounts,
// this is where the sessions handle lands too.
type app struct {
	db     *db.DB
	logger *slog.Logger
}

func (a *app) index(w http.ResponseWriter, r *http.Request) {
	render(w, "index", nil)
}
