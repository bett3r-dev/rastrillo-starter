# myproject

CARLOS/rastrillo apps in one repository: `apps/<app>` is one app each,
`libs/` is the code they share (the change record, OpenTelemetry,
socket activation, the SSE doorbell), and
`tools/` holds the prompt ledger. `AGENTS.md` is how to work here;
`docs/decisions/` is why it is shaped this way.

```sh
make ci                          # the whole gate: every app, libs, tools, gitleaks
make -C apps/myapp dev           # edit loop on :8080, pages reload on rebuild
make -C apps/myapp observe       # local Grafana (Tempo, Prometheus, Loki) on :3000
make -C apps/myapp observe-run   # make dev, exporting to it
make -C apps/myapp flow          # regenerate apps/myapp/docs/flow.md and flow.json from the scenarios
make new-app NAME=admin          # another rastrillo app under apps/
make ledger-install              # once per clone: the prompt ledger's post-commit hook
```

`go -C apps/myapp run ./cmd/myapp verify -db <copy of a database>` folds the change
record onto the live rows and names any row it cannot explain.
