package myapp

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"

	"amadan.net/rastrillo/rastrillo"
	"amadan.net/rastrillo/rastrillo/ui"

	"myapp/internal/myapp/icons"
)

//go:embed templates static
var appFS embed.FS

// assets fingerprints static/: {{asset "static/tokens.css"}} in a
// template returns a content-hashed URL, served immutable-cacheable by
// the handler App mounts.
var assets = rastrillo.NewAssets(appFS)

// pages is one *template.Template per page, each combining layout.html
// with that page's own file — kept separate so every page can define a
// template named "content" without colliding with the others.
var pages = map[string]*template.Template{}

func init() {
	for _, name := range []string{"index", "errors"} {
		pages[name] = template.Must(
			// ui's partials and their helpers first, so every page can
			// call one without registering anything. WithIcons points
			// ui's two icon seams at the app's own set
			// (internal/myapp/icons, scaffolded by rastrillo new) —
			// both of them, always: passing only icon would leave
			// iconAssets on the framework default and silently break
			// cdn and js delivery. iconAssets is whatever <head> markup
			// the chosen delivery needs, empty for the vendored-inline
			// default, so the layout calls it unconditionally and
			// switching delivery later needs no template edit.
			template.Must(template.New("layout").
				Funcs(ui.Funcs(ui.WithIcons(icons.Icon, icons.Assets))).
				Funcs(template.FuncMap{"asset": assets.Path}).
				ParseFS(ui.Templates(), "*.html")).
				// Then the app's own: the shell and this page's body.
				ParseFS(appFS, "templates/layout.html", "templates/"+name+".html"))
	}
}

// render executes name's layout into a buffer before anything touches
// the wire: a template error becomes a clean 500 instead of garbage
// appended to a half-written page, and headers (flash cookies, say)
// stay settable until the status line is written.
func render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := pages[name].ExecuteTemplate(&buf, "layout", data); err != nil {
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		return
	}
	buf.WriteTo(w)
}

// ErrorPage renders the "errors" page — templates/errors.html, which
// wraps ui's error-page partial in the layout — for status inside ref
// (rastrillo.NewRef, or empty when there is none to show). It is the
// rastrillo.ErrorPageFunc cmd/myapp/main.go points Options.ErrorPage at,
// so a panic gets the same page a handled failure would; wire it to
// Ctx.ErrorPage too as the app grows handlers of its own.
func ErrorPage(w http.ResponseWriter, r *http.Request, status int, ref string) {
	w.WriteHeader(status)
	render(w, "errors", map[string]any{"Status": status, "Ref": ref})
}
