package myapp

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"strings"

	"amadan.net/rastrillo/rastrillo"
	"amadan.net/rastrillo/rastrillo/flash"
	"amadan.net/rastrillo/rastrillo/ui"

	"myapp/internal/myapp/icons"
)

//go:embed templates static
var appFS embed.FS

// assets fingerprints static/: {{asset "static/tokens.css"}} in a
// template returns a content-hashed URL, served immutable-cacheable.
var assets = rastrillo.NewAssets(appFS)

// pageNames is every page template: layout.html plus templates/<name>.html.
var pageNames = []string{"errors", "signin", "signup", "notes", "note_new", "note_correct", "changes"}

// pages is one *template.Template per page, each combining layout.html
// with that page's own file, so every page can define "content".
var pages = map[string]*template.Template{}

func init() {
	for _, name := range pageNames {
		pages[name] = template.Must(
			template.Must(template.New("layout").
				Funcs(ui.Funcs(ui.WithIcons(icons.Icon, icons.Assets))).
				Funcs(template.FuncMap{
					"asset":   assets.Path,
					"tone":    flashTone,
					"here":    here,
					"devBoot": devBootID,
				}).
				ParseFS(ui.Templates(), "*.html")).
				ParseFS(appFS, "templates/layout.html", "templates/"+name+".html"))
	}
}

// Page is what every template receives.
type Page struct {
	Title  string
	Path   string       // the request's path, for the nav's aria-current
	Member *Member      // signed-in member, nil on the public pages
	Flash  *flash.Flash // one notice, taken once per page
	Data   any
}

// render executes name's layout into a buffer before anything touches
// the wire: a template error becomes a clean 500 instead of garbage
// appended to a half-written page.
func (a *app) render(w http.ResponseWriter, r *http.Request, name, title string, data any) {
	pg := Page{Title: title, Path: r.URL.Path, Data: data}
	if m, ok := a.member(r); ok {
		pg.Member = &m
	}
	if f, ok := flash.Take(w, r); ok {
		pg.Flash = &f
	}
	renderPage(w, name, pg)
}

// renderFragment executes one block of name's page, without the
// layout: what a [data-live] element re-fetches (Rastrillo-Fragment: 1).
func (a *app) renderFragment(w http.ResponseWriter, r *http.Request, name, block string, data any) {
	pg := Page{Path: r.URL.Path, Data: data}
	if m, ok := a.member(r); ok {
		pg.Member = &m
	}
	renderBlock(w, name, block, pg)
}

func renderPage(w http.ResponseWriter, name string, pg Page) {
	renderBlock(w, name, "layout", pg)
}

func renderBlock(w http.ResponseWriter, name, block string, pg Page) {
	var buf bytes.Buffer
	if err := pages[name].ExecuteTemplate(&buf, block, pg); err != nil {
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

// ErrorPage renders templates/errors.html for status, with ref the
// handle the log line carries. It is the rastrillo.ErrorPageFunc
// main.go points Options.ErrorPage at, so a panic gets the same page a
// handled failure does.
func ErrorPage(w http.ResponseWriter, r *http.Request, status int, ref string) {
	w.WriteHeader(status)
	renderPage(w, "errors", Page{Title: "Something went wrong", Path: r.URL.Path, Data: map[string]any{"Status": status, "Ref": ref}})
}

// flashTone maps a flash kind to a callout tone.
func flashTone(kind string) string {
	switch kind {
	case "warning":
		return "warning"
	case "error":
		return "negative"
	default:
		return "positive"
	}
}

// here reports whether path is inside the nav section rooted at
// section: "/" is only itself, any other section owns its subpaths,
// so the nav marks the section a form page belongs to as well as its
// list.
func here(path, section string) bool {
	if section == "/" {
		return path == "/"
	}
	return path == section || strings.HasPrefix(path, section+"/")
}
