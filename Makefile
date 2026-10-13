# The root gate (docs/decisions/0002-monorepo-layout.md). One rastrillo
# app needs one module root, so every app keeps its own make ci, run
# from its own directory, and this file only loops over them, gates the
# shared modules (libs/, tools/) and runs gitleaks once, here: an app's
# own gitleaks target scans nothing below the repository root.
#
#   make ci            every app's gate, libs and tools, blueprint-check, gitleaks
#   make blueprint     the Blueprint graph, .blueprint/graph.json and diagnostics.json
#   make ci-app APP=x  one app's gate (same as make -C apps/x ci)
#   make new-app NAME=x  scaffold apps/x with this repository's conventions

# The same named Go as every app's Makefile; toolchain-check holds
# them equal. Raise all of them together with each Go security release.
export GOTOOLCHAIN = go1.26.9

APPS := $(patsubst apps/%/,%,$(sort $(wildcard apps/*/)))
SHARED := libs tools

STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.7.0
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.8.0
GITLEAKS := github.com/zricethezav/gitleaks/v8@v8.30.1

ifneq ($(filter /%,$(firstword $(GOCACHE))),)
export STATICCHECK_CACHE ?= $(GOCACHE)/staticcheck
endif

.NOTPARALLEL:
.PHONY: ci apps shared toolchain-check blueprint blueprint-check gitleaks ci-app new-app ledger-install ledger-push

# scripts/ci-gate runs each target of CI and certifies only what ran:
# a target that cannot run prints "SKIP <target> reason=<why>", the
# gate ends with every SKIP it saw (each app's included), and a target
# that runs nothing without a SKIP line fails it.
CI := toolchain-check shared apps blueprint-check gitleaks
ci:
	@./scripts/ci-gate $(CI)

apps:
	@set -e; for a in $(APPS); do echo "== apps/$$a"; $(MAKE) -C apps/$$a ci; done

ci-app:
	$(MAKE) -C apps/$(APP) ci

# The shared modules are libraries, not rastrillo apps: no perf lane, no
# migration check. Their tests run uncached (-count=1) because tools'
# ledger tests exec git, whose state Go's test cache cannot see.
shared:
	@set -e; for m in $(SHARED); do \
		echo "== $$m"; \
		(cd $$m && CGO_ENABLED=0 go vet ./...); \
		out=$$(cd $$m && gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed in $$m:"; echo "$$out"; exit 1; fi; \
		(cd $$m && go run $(STATICCHECK) ./...); \
		(cd $$m && go run $(GOVULNCHECK) ./...); \
		(cd $$m && CGO_ENABLED=0 go run amadan.net/rastrillo/rastrillo/cmd/rastrillo budget test -count=1 ./...); \
	done

# Every app pins the same Go as this file.
toolchain-check:
	@set -e; for a in $(APPS); do \
		got=$$(sed -n 's/^export GOTOOLCHAIN = //p' apps/$$a/Makefile); \
		if [ "$$got" != "$(GOTOOLCHAIN)" ]; then echo "apps/$$a pins GOTOOLCHAIN $$got, the root pins $(GOTOOLCHAIN): raise them together"; exit 1; fi; \
	done

gitleaks:
	go run $(GITLEAKS) git --no-banner --redact .

# The Blueprint graph (tools/cmd/blueprint, docs/prs/RS-20/design.md):
# drawn from .blueprint.config.json and every committed
# apps/*/docs/flow.json alone, so it runs no scenario; make flow in an
# app is what refreshes its map. blueprint writes .blueprint/ (ignored),
# where Steward reads it: .blueprint.config.json names this target as
# designTooling.extract. blueprint-check runs the same program into a
# temporary directory and fails when it exits 1 (a refused input, an
# edge the non-ddd grammar refuses, a name collision).
blueprint:
	CGO_ENABLED=0 go -C tools run ./cmd/blueprint -root $(CURDIR)

blueprint-check:
	@set -e; out=$$(mktemp -d); trap 'rm -rf "$$out"' EXIT; \
		CGO_ENABLED=0 go -C tools run ./cmd/blueprint -root $(CURDIR) -out "$$out"; \
		echo "blueprint: graph drawn, no violation or collision"

# new-app scaffolds apps/$(NAME) with rastrillo new and lays this
# repository's conventions over it: see scripts/new-app.
new-app:
	@test -n "$(NAME)" || { echo "usage: make new-app NAME=<app>"; exit 2; }
	./scripts/new-app $(NAME)

# The prompt ledger (tools/cmd/ledger): amadan's format, kept in this
# repository's own refs (refs/amadan/ledger/<branch>). ledger-install
# points git at .githooks (the post-commit marker) and makes every
# remote fetch the ledgers. ledger-push sends them, fast-forward only on
# purpose: a ledger is append-only, so a rejected push means another
# machine wrote records this one lacks. Fetch, read them, never force.
LEDGER_REMOTE ?= origin

ledger-install:
	go -C tools run ./cmd/ledger install

ledger-push:
	git push $(LEDGER_REMOTE) 'refs/amadan/ledger/*:refs/amadan/ledger/*'
