# manifest/ — the declarative path

Manifest-driven and code-driven are equal, optional paths: declare the
resources that are pure CRUD, hand-write the ones that aren't, per
resource, in one app. One TOML file here declares a whole screen set —
List → Show → Edit/New plus the confirm-page delete flow — generated
as real committed code under gen/ by `rastrillo generate`:

    name  = "ticket_types"
    route = "/admin/ticket_types"

    [list]
    columns = [{ field = "Name", kind = "text" }, { field = "Price", kind = "money" }]
    search  = true

    [form]
    basics = [{ name = "Name", required = true }, { name = "Price", kind = "money" }]

The vocabulary covers one flat table per resource, three field kinds,
no relations. Add `scope = "user"` and every generated query is
owner-filtered by the session subject — someone else's row answers
404 — for resources a user owns; mount those routes behind
`sessions.Require`. Add `store = "mergeable"` and the resource lives as
event-log streams (tombstone deletes, derive-on-read) instead of a
sqlc table — it needs no sqlc tool. Relations or a custom flow:
hand-write it.

Adding the first manifest to this app:

1. `go get -tool github.com/sqlc-dev/sqlc/cmd/sqlc` (once — the
   generated store is sqlc input).
2. `rastrillo generate` writes gen/ — committed, never hand-edited.
3. Embed the generated templates. This file goes at the MODULE ROOT,
   not beside the hand templates: a go:embed pattern cannot contain
   "..", and gen/templates is not under internal/myapp. Call it
   genassets.go —

       package myappassets

       import "embed"

       //go:embed gen/templates
       var GenTemplatesFS embed.FS

   `rastrillo generate` only ever clears and rewrites gen/actions/,
   never a file sitting beside gen/, so this survives regeneration.

4. Write the Ctx.Render seam. A generated action cannot call an
   app-private helper, so it calls Ctx.Render with a page name that is
   always "<resource>/list", "<resource>/show" or "<resource>/form".
   This is the whole adapter — internal/myapp/genrender.go:

       package myapp

       import (
           "bytes"
           "fmt"
           "html/template"
           "io/fs"
           "net/http"
           "path"
           "strings"

           myappassets "myapp"
           "myapp/gen/locales"

           "amadan.net/rastrillo/rastrillo"
           "amadan.net/rastrillo/rastrillo/ui"
       )

       // One template per generated screen, keyed "<resource>/<page>".
       var genPages = buildGenPages()

       func buildGenPages() map[string]*template.Template {
           sub, err := fs.Sub(myappassets.GenTemplatesFS, "gen/templates")
           if err != nil {
               panic(err) // embedded at compile time
           }
           dirs, err := fs.ReadDir(sub, ".")
           if err != nil {
               panic(err)
           }
           out := map[string]*template.Template{}
           for _, d := range dirs {
               if !d.IsDir() {
                   continue
               }
               files, err := fs.Glob(sub, d.Name()+"/*.html")
               if err != nil {
                   panic(err)
               }
               for _, f := range files {
                   base := template.Must(template.New("layout").
                       Funcs(ui.Funcs(ui.WithT(genT))).
                       Funcs(template.FuncMap{"asset": assets.Path}).
                       ParseFS(ui.Templates(), "*.html"))
                   base = template.Must(base.ParseFS(appFS, "templates/layout.html"))
                   key := d.Name() + "/" + strings.TrimSuffix(path.Base(f), ".html")
                   out[key] = template.Must(base.ParseFS(sub, f))
               }
           }
           return out
       }

       // The generated templates call (T "resource.<name>.…"); those keys
       // live in gen/locales, not in ui's default catalog. A missing key
       // renders as itself, so a typo shows on the page instead of
       // blanking a sentence.
       func genT(key string, args ...any) string {
           if v, ok := locales.BaseCatalog[key]; ok {
               return v
           }
           return key
       }

       func GenRender(ctx *rastrillo.Ctx, w http.ResponseWriter, name string, status int, data any) {
           t, ok := genPages[name]
           if !ok {
               genFail(ctx, w, "rendering "+name, fmt.Errorf("no such page template"))
               return
           }
           var buf bytes.Buffer
           if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
               genFail(ctx, w, "rendering "+name, err)
               return
           }
           w.Header().Set("Content-Type", "text/html; charset=utf-8")
           w.WriteHeader(status)
           buf.WriteTo(w)
       }

       func genFail(ctx *rastrillo.Ctx, w http.ResponseWriter, what string, err error) {
           if ctx != nil && ctx.Logger != nil {
               ctx.Logger.Error("myapp: "+what, "err", err)
           }
           http.Error(w, "something went wrong", http.StatusInternalServerError)
       }

   assets and appFS are render.go's, already in this package. The
   generated screens go through the same layout.html the hand pages
   use, because a generated content template defines "content" exactly
   as a hand one does. When a generated screen needs this request's
   flash or signed-in state, make GenRender a func(r) returning a
   rastrillo.RenderFunc closed over r and call it from the ctx factory
   — RenderFunc carries no *http.Request of its own.

5. Apply the generated store's migrations at boot, or the table the
   screens read does not exist. In internal/myapp/migrations.go:

       import (
           "fmt"

           bookmarksstore "myapp/gen/store/bookmarks"
       )

       func genSchemaSet() *migrate.Set {
           s := new(migrate.Set)
           for i, stmt := range bookmarksstore.Migrations {
               s.Add(migrate.Migration{ID: fmt.Sprintf("bookmarks_gen/%04d_init", i+1), SQL: stmt})
           }
           return s
       }

       var genSchema = genSchemaSet()

       var BootSchema = migrate.Merge(genSchema, Schema)

   genSchema goes in BootSchema and never in Schema: Schema is what
   `rastrillo migration check` diffs against Models, and a generated
   table in it reads as one Models forgot.

   The ledger trap, stated because it bites later rather than now: the
   generated Migrations are rewritten verbatim from the TOML on every
   `rastrillo generate`, and migrate.Apply treats applied SQL as
   immutable. Reshape a manifest after the app has booted once and the
   next boot refuses rather than silently reapplying it —
   `rastrillo migration baseline` is the recovery.

6. Mount the generated router beside the chi router in
   internal/myapp/app.go, and wire its Ctx:

       gmux := gen.Router(func(*http.Request) *rastrillo.Ctx {
           writer, _ := d.G.DB()
           return &rastrillo.Ctx{DB: writer, Logger: logger,
               Actor: rastrillo.Actor{Human: true}, Render: GenRender}
       })
       mux.Handle("/admin/", gmux)

7. Add `rastrillo generate --check` to the Makefile's ci target.

Drop to a .go manifest in this directory (package manifest, a
`var X = rastrillo.Resource{...}`) the moment you need a function
value — a custom Column.Render, say. To take any single generated
screen over by hand, write your own action file at the same computed
path under actions/ — the generator skips that one from then on.
