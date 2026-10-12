APP := myapp

# The gate and the release run on one named Go, not whichever release this
# machine has. govulncheck judges the standard library of the Go doing the
# scan, and make release builds the binary you ship with it, so a patch
# release behind is both a red gate and an unpatched binary. go fetches
# this release through the module proxy on first use. It is the Go
# rastrillo's own gate runs; raise it with each Go security release.
#
# A plain =, not ?=: Go's container images set GOTOOLCHAIN=local, and ?=
# would quietly keep whatever Go they ship. To override for one run:
#   make ci GOTOOLCHAIN=local
export GOTOOLCHAIN = go1.26.9

# What `carlos ship -target` defaults to, and therefore what a release
# must be built for. Building for your own machine and shipping that is a
# silent architecture mismatch: the binary uploads fine and fails to exec
# on the instance. Override for a one-off:
#   make release RELEASE_GOARCH=amd64
RELEASE_GOOS   ?= linux
RELEASE_GOARCH ?= arm64
RELEASE_BIN := releases/$(APP)-$(RELEASE_GOOS)-$(RELEASE_GOARCH)

# What the running binary reports from GET /api/version. The platform's
# deploy verification polls that endpoint on every instance socket and
# compares what it finds against the release it believes it adopted; an
# unstamped binary answers "dev" from every build you have ever made, so
# those two facts can never disagree and a process that was never
# recycled onto the new release verifies green. Stamping this is what
# gives that check the ability to fail.
#
# git describe, because the tag is what you ship against. Outside a
# repository, or before the first commit, name it yourself:
#   make release VERSION=v0.1.0
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null)

# One target at a time, even under make -j: the perf lane measures time,
# and a suite running beside it would be measured too.
.NOTPARALLEL:

.PHONY: build release version-check test vet fmt-check staticcheck govulncheck gitleaks budget perf migration-check contexts escapes flow flow-check oracle browser ci dev observe observe-run observe-down

RASTRILLO := go run amadan.net/rastrillo/rastrillo/cmd/rastrillo

# build is the compile check: with more than one package matched, go
# build discards the output, so this catches a broken package without
# producing an artifact.
build:
	CGO_ENABLED=0 go build ./...

# dev is the edit loop: rastrillo dev rebuilds and restarts the server on
# every save under internal/, cmd/ and the rest of its watch list. The
# templates and static/ are go:embed'ed, so a rebuild is the only way a
# change reaches the page. MYAPP_DEV_RELOAD makes every page reload
# itself once the new build is serving (devreload.go). It serves the
# real myapp.db: point DEV_DB at a copy to experiment. Override per run:
#   make dev DEV_ADDR=:8091 DEV_DB=/tmp/scratch.db
#
# rastrillo dev also runs rastrillo generate on every rebuild, which
# writes gen/ even with no manifests; that empty router fails go vet, so
# the trap removes it when the loop stops (Ctrl-C included). The day a
# manifest lands, gen/ is source: drop the trap and the .gitignore line.
DEV_ADDR ?= :8080
DEV_DB ?= myapp.db
DEV_ORIGIN ?= http://localhost$(DEV_ADDR)
dev:
	trap 'rm -rf gen' EXIT INT TERM; \
	CGO_ENABLED=0 MYAPP_DEV_RELOAD=1 MYAPP_ORIGIN=$(DEV_ORIGIN) $(RASTRILLO) dev -- -addr $(DEV_ADDR) -db $(DEV_DB)

# observe is the Grafana stack for local work (ops/observability at the
# repository root): an OpenTelemetry Collector in front of Tempo,
# Prometheus and Loki, and Grafana on http://localhost:3000 with the
# myapp dashboard provisioned. observe-run is make dev exporting to it:
# traces, metrics and logs are pushed every 5s, and flushed when
# rastrillo dev restarts the server on a save (the same SIGTERM a
# hibernating instance gets). Name the tenant a run reports as with
# OBSERVE_TENANT; run two on different DEV_ADDRs and DEV_DBs to see the
# dashboard split by tenant.
OBSERVE_COMPOSE := docker compose -f ../../ops/observability/compose.yaml
OBSERVE_TENANT ?= local
observe:
	$(OBSERVE_COMPOSE) up -d --wait
	@echo "Grafana: http://localhost:3000/d/myapp-overview/myapp"

observe-run:
	OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 OTEL_METRIC_EXPORT_INTERVAL=5000 \
	OTEL_BSP_SCHEDULE_DELAY=1000 OTEL_BLRP_SCHEDULE_DELAY=1000 MYAPP_TENANT=$(OBSERVE_TENANT) \
	$(MAKE) dev

# The stack's data survives this in a named volume; add -v to drop it.
observe-down:
	$(OBSERVE_COMPOSE) down

# release is what ships. -s -w drops the symbol table and DWARF, which on
# this family's apps is 23-31% off the binary and 45-53% off the
# compressed artifact — and compressed size is what actually gets
# transferred. carlos is built the same way (see platform's Makefile).
#
# What survives: panic traces keep their function names AND line numbers
# (Go's pclntab is untouched by these flags), debug.ReadBuildInfo works,
# and go version -m still reports module metadata. What you lose is
# source-level debugging with delve or gdb — build without the flags for
# that. rastrillo dev never strips, for the same reason.
release: version-check
	@mkdir -p releases
	CGO_ENABLED=0 GOOS=$(RELEASE_GOOS) GOARCH=$(RELEASE_GOARCH) \
		go build -ldflags="-s -w -X 'amadan.net/rastrillo/rastrillo.BuildVersion=$(VERSION)'" \
		-o $(RELEASE_BIN) ./cmd/$(APP)
	@echo
	@echo "built $(RELEASE_BIN) reporting version $(VERSION)"
	@echo "ship it with:"
	@echo "  carlos ship -app $(APP) -target $(RELEASE_GOOS)-$(RELEASE_GOARCH) $(RELEASE_BIN)"

# version-check refuses rather than letting release ship a binary whose
# version is untrue. Both refusals are about the same thing: /api/version
# is only evidence if the answer identifies one artifact.
#
# An empty stamp is worse than the "dev" default, because "dev" is
# saying something true about itself and "" looks like a value that
# arrived from somewhere.
#
# A dirty tree is refused rather than suffixed. A -dirty stamp names a
# commit plus changes nobody can name, so two different binaries can
# carry the identical string — which does not fix the check, it makes
# it look reliable while it is not, and that is the more expensive
# failure. Refusing costs one `git stash`; a wrong stamp ships. If you
# mean to ship uncommitted work, say so out loud:
#   make release VERSION=v0.1.0-wip
version-check:
	@if [ -z "$(VERSION)" ]; then \
		echo "make release: no version to stamp."; \
		echo "  git describe found nothing here: no repository, or no commit yet."; \
		echo "  Commit, or name it:  make release VERSION=v0.1.0"; \
		exit 1; \
	fi
	@case "$(VERSION)" in *-dirty) \
		echo "make release: the tree is dirty, so $(VERSION) does not name what you are about to ship."; \
		echo "  Commit or stash first, or name it:  make release VERSION=v0.1.0-wip"; \
		exit 1;; \
	esac

# budget size holds each directory to 5,000 source and 8,000 test lines.
# Past that, split by feature (docs/site/testing.md); a record in
# .rastrillo/budgets.txt with a reason is the exception, not the fix.
budget:
	$(RASTRILLO) budget size

# budget test runs go test itself, so its exit status survives, and holds
# each package to 10s using the time budget.Main prints in the package's
# TestMain, cached runs included. Timing fails only on CI (AMADAN_CI or
# CI set); here it reports.
test:
	$(RASTRILLO) budget test ./...

# The perf lane: every GET screen at 150ms to first byte, cold boot at
# 500ms. Serial (-p 1 -parallel 1) and uncached (-count=1), because a
# measurement taken beside other work, or replayed from a cache, is not
# one. -require fails the lane if the perf tests did not both run.
perf:
	$(RASTRILLO) budget test -no-time -require TestPerfScreens,TestPerfBoot -tags perf -count=1 -p 1 -parallel 1 -run '^TestPerf' ./...

# -tags oracle so the black-box scenarios (oracle/) are vetted too.
vet:
	go vet -tags oracle ./...

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

# staticcheck catches what vet does not: deprecated APIs, values that are
# never read, a &*x that looks like a copy and is not.
#
# go run with a version, not a tool directive in go.mod. A tool's
# requirements join the app's own module graph and can move the versions
# of modules the app shares with it (golang.org/x/tools, which sqlc also
# needs); go run resolves the tool in a graph of its own. The version is
# the one rastrillo's own gate runs. Bump it when you raise the go
# directive: a staticcheck older than your Go does not know that
# release's deprecations, and says nothing.
#
# -tags browser,perf,oracle so the browser drive, the perf tests and
# the black-box scenarios are read too; no file is left out by them,
# so it covers everything the plain build does.
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.7.0
# staticcheck keeps its cache in the user cache directory, ~/.cache,
# which a sandboxed CI runner mounts read-only: "failed to initialize
# build cache ... read-only file system". Where the runner hands the job
# an absolute GOCACHE, staticcheck's goes inside it: writable wherever
# Go's is, and evicted with it. Anywhere else it stays where staticcheck
# puts it. Not beside it: a writable GOCACHE says nothing about its
# parent.
ifneq ($(filter /%,$(firstword $(GOCACHE))),)
export STATICCHECK_CACHE ?= $(GOCACHE)/staticcheck
endif

staticcheck:
	go run $(STATICCHECK) -tags browser,perf,oracle ./...

# govulncheck reports only the vulnerabilities your code can reach: a call
# path, not a module that merely sits in go.sum. It reads the live
# vulnerability database, so it can go red with no change here, which is
# the point of running it on every push. A module finding is fixed by
# raising that requirement in go.mod; a standard-library one by raising
# GOTOOLCHAIN above.
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.8.0
govulncheck:
	go run $(GOVULNCHECK) ./...

# gitleaks scans every commit, not just the files you have now: a key that
# was committed and then deleted is still in every clone. It only scans
# when this directory is the root of its own repository. Before git init
# there is nothing committed, and an app scaffolded inside somebody else's
# checkout must not scan theirs. --redact keeps a finding out of CI logs.
GITLEAKS := github.com/zricethezav/gitleaks/v8@v8.30.1
gitleaks:
	@if [ "$$(git rev-parse --is-inside-work-tree 2>/dev/null)" = true ] && [ -z "$$(git rev-parse --show-prefix)" ]; then \
		go run $(GITLEAKS) git --no-banner --redact .; \
	else \
		echo "SKIP gitleaks reason=this directory is not the root of a git repository, so there is nothing committed to scan here"; \
	fi

# A step of its own, not folded into another target's recipe: ci.d/
# steps each exec one make target, so migration-check needs its own
# name to get its own reported step on a runner with step support.
#
# go run, not a bare "rastrillo": go.mod already requires rastrillo, so
# this needs nothing on PATH but the Go toolchain, and works offline
# after go mod download. A bare "rastrillo migration check" failed a
# fresh clone's very first CI run with "make: rastrillo: Command not
# found", and nothing in the scaffold said what to install.
#
# What makes the go run resolve is the tool directive at the foot of
# go.mod — it is what keeps the CLI's own dependencies in go.sum. See
# the comment there before removing it.
migration-check:
	go run amadan.net/rastrillo/rastrillo/cmd/rastrillo migration check

# contexts reads the app's source files, which Go's test cache cannot
# see as inputs, so it runs uncached on its own.
contexts:
	CGO_ENABLED=0 go test -count=1 -run 'TestNoJoinsAcrossContexts|TestNoFeatureImportsAnother' ./internal/myapptest/

# escapes is the escape-hatch census (escapes_test.go): every SkipHooks,
# raw SQL on a tracked table, sql.Open outside cmd/ and exempt column
# in the app and libs/ named with a reason. It parses source Go cannot
# see as test inputs, hence -count=1.
escapes:
	CGO_ENABLED=0 go test -count=1 -run 'TestEveryEscapeHatchIsNamed|TestTheEscapeCensusSeesPlantedHatches' ./internal/myapptest/

# The flow chain (Command -> [context] Event ==> [context] Event), read
# off the change records every scenario lands (ADR 0003). It needs all
# of them, so it runs only when asked, after the whole package: flow
# writes docs/flow.md and docs/flow.json, flow-check fails only on a
# committed copy the scenarios no longer produce (empty or unnamed
# entry points, mixed-context requests and implicit crossings are
# reported there, never failed). It writes and reads files Go cannot
# see, hence -count=1.
flow:
	CGO_ENABLED=0 FLOW_UPDATE=1 go test -count=1 ./internal/myapptest/

flow-check:
	CGO_ENABLED=0 FLOW_CHECK=1 go test -count=1 ./internal/myapptest/

# The oracle: the scenarios as black-box HTTP tests (oracle/), one
# fresh myapp process per scenario on a free port, its records read
# through /changes.json, so the same suite can judge another binary
# (MYAPP_ORACLE_BIN) or a running instance (MYAPP_ORACLE_URL);
# oracle/oracle_test.go says how. It execs go build, which Go's test
# cache cannot see, hence -count=1 and the tag that keeps it out of
# make test. budget test holds it to the same 10s.
# An app without an oracle/ (it is optional) says so, as a SKIP.
oracle:
	@if [ ! -d oracle ]; then echo "SKIP oracle reason=this app has no oracle/"; exit 0; fi; \
	echo 'CGO_ENABLED=0 $(RASTRILLO) budget test -tags oracle -count=1 ./oracle/'; \
	CGO_ENABLED=0 $(RASTRILLO) budget test -tags oracle -count=1 ./oracle/

# The browser drive (browser_test.go): the app in a real Chromium.
# With no Chromium on this machine the harness skips it (the
# RASTRILLO_BROWSER_OPTIONAL it is run with here), and this target
# says so as a SKIP, with the harness's own words: the drive did not
# run, so the gate did not pass it. Install Chromium, or point
# RASTRILLO_CHROME at one, to make it run.
browser:
	@out=$$(CGO_ENABLED=0 RASTRILLO_BROWSER_OPTIONAL=1 go test -tags browser -count=1 -v -run '^TestBrowser' ./internal/myapptest/ 2>&1); st=$$?; \
	echo "$$out" | grep -E '^(--- |ok|FAIL|PASS)' ; \
	if [ $$st -ne 0 ]; then echo "$$out"; exit $$st; fi; \
	if echo "$$out" | grep -q -- '--- SKIP'; then \
		why=$$(echo "$$out" | grep -B1 -- '--- SKIP' | sed -n 's/^ *[a-z_]*\.go:[0-9]*: //p' | head -1); \
		echo "SKIP browser reason=$${why:-the browser drive skipped}"; \
	fi

# ci is the one gate, run by scripts/ci-gate one target at a time: each
# target runs, or prints "SKIP <target> reason=<why>" and is listed as
# skipped in the summary the gate ends with; a target that runs nothing
# and says no SKIP fails it.
#
# ci is the one gate: what a runner executes and what you run before
# pushing are the same definition. amadan's runner falls back to this
# target when .amadan/ci is absent; ci.d/ carries the same targets,
# reported one by one, for a runner with step support. migration-check
# fails the build the moment models.go and migrations/ disagree,
# instead of at boot on whatever machine notices next. If the app
# declares manifest resources, also add: rastrillo generate --check
CI := vet fmt-check staticcheck govulncheck gitleaks budget test perf migration-check contexts escapes flow-check oracle browser
ci:
	@../../scripts/ci-gate $(CI)
