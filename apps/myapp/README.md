# myapp

A [rastrillo](https://amadan.net/rastrillo/rastrillo) app. `make ci` is the gate: vet, gofmt, staticcheck, govulncheck, gitleaks, tests and the migration check, one definition for CI and for you. AGENTS.md carries the working conventions.

## Browser drive

`internal/myapptest/browser_test.go` walks the app in a real
Chromium — loud on any console error, failed request, or JS value that
leaked to the screen:

```sh
go test -tags browser ./...   # real browser, loud on any console error
                              # — not part of the plain suite
```

It needs a Chromium: on PATH, via `RASTRILLO_CHROME`, or in a
Playwright cache. **A skip is not a pass:** with no browser it fails,
unless `RASTRILLO_BROWSER_OPTIONAL=1` makes the skip a deliberate,
visible choice. `make ci` runs the plain suite only — whether CI
also runs the browser drive, and on what runner, is this app's call.
