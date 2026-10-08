module myapp

go 1.25.0

require (
	amadan.net/rastrillo/rastrillo v0.28.0
	github.com/chromedp/chromedp v0.14.2
	github.com/go-chi/chi/v5 v5.3.2
)

require (
	amadan.net/rastrillo/rastrillo/money v0.1.0 // indirect
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/chromedp/cdproto v0.0.0-20250724212937-08a3db8b4327 // indirect
	github.com/chromedp/sysutil v1.1.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-json-experiment/json v0.0.0-20250725192818-e39067aee2d2 // indirect
	github.com/gobwas/httphead v0.1.0 // indirect
	github.com/gobwas/pool v0.2.1 // indirect
	github.com/gobwas/ws v1.4.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	gorm.io/gorm v1.31.2 // indirect
	gorm.io/plugin/dbresolver v1.6.2 // indirect
	modernc.org/libc v1.74.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.55.0 // indirect
)

// The CLI this app's own gate runs: make migration-check, and
// rastrillo generate once the app declares manifest resources. It is
// declared as a tool so that go mod tidy records the CLI's build
// dependencies in go.sum alongside the app's own. Nothing an app
// imports reaches, say, the TOML parser behind manifest, so without
// this line tidy prunes it and the first make ci on a clean scaffold
// dies with "missing go.sum entry for github.com/BurntSushi/toml" —
// a dependency that is real at go run time and invisible at tidy
// time. Deleting this line breaks the gate, not the app.
tool amadan.net/rastrillo/rastrillo/cmd/rastrillo
