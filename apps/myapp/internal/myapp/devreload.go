package myapp

import (
	"net/http"
	"os"

	"amadan.net/rastrillo/rastrillo"
)

// DevReloadEnv turns on the browser refresh for make dev. rastrillo dev
// restarts the process on every save; with this set, each page loads
// static/devreload.js, which polls /dev/boot and reloads the page when
// the id there stops matching the one the page was rendered with — that
// is, when a new build is serving. Unset (production, tests), neither
// the route nor the script exists.
const DevReloadEnv = "MYAPP_DEV_RELOAD"

var devReload = os.Getenv(DevReloadEnv) != ""

// bootID names this process. A restart is a new id.
var bootID = rastrillo.NewRef()

// devBoot answers the script's poll with this process's id.
func devBoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(bootID))
}

// devBootID is the layout's {{devBoot}}: the id the page was rendered
// under, or empty when the refresh is off (the layout then emits no
// script).
func devBootID() string {
	if !devReload {
		return ""
	}
	return bootID
}
