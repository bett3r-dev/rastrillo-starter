# The root gate (docs/decisions/0002-monorepo-layout.md). One rastrillo
# app needs one module root, so every app keeps its own make ci, run
# from its own directory, and this file only loops over them, gates the
# shared modules (libs/, tools/) and runs gitleaks once, here: an app's
# own gitleaks target scans nothing below the repository root.
#
#   make ci            every app's gate, libs and tools, gitleaks
#   make ci-app APP=x  one app's gate (same as make -C apps/x ci)
#   make new-app NAME=x  scaffold apps/x with this repository's conventions

# The same named Go as every app's Makefile; toolchain-check holds
# them equal. Raise all of them together with each Go security release.
export GOTOOLCHAIN = go1.26.6

APPS := $(patsubst apps/%/,%,$(sort $(wildcard apps/*/)))
SHARED := libs tools

STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.7.0
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.8.0
GITLEAKS := github.com/zricethezav/gitleaks/v8@v8.30.1

ifneq ($(filter /%,$(firstword $(GOCACHE))),)
export STATICCHECK_CACHE ?= $(GOCACHE)/staticcheck
endif

.NOTPARALLEL:
.PHONY: ci apps shared toolchain-check gitleaks ci-app new-app ledger-install ledger-push

ci: toolchain-check shared apps gitleaks

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
