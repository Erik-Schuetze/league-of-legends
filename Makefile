MODULE  := github.com/Erik-Schuetze/league-of-legends
# lolstats-web is the server-rendered web tier: it renders the published
# aggregates with Go templates instead of a Node build step, so it is a third
# binary in the same image (see Dockerfile) and is built by the same target.
BINARIES := lolstats-ingest lolstats-aggregate lolstats-web
IMAGE   := lolstats:latest
ENV     ?= dev

# Tool versions are pinned here rather than in each workflow so that CI and a
# local `make lint` / `make vuln` are guaranteed to run the same thing. Both
# are invoked through `go run` of a pinned module version, so a contributor
# needs only a Go toolchain - not a globally installed linter that may be a
# different version from CI's.
GOLANGCI_LINT_VERSION := v2.13.2
GOVULNCHECK_VERSION   := v1.8.0
GOLANGCI_LINT         := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOVULNCHECK           := go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

GOFLAGS  := -trimpath
LDFLAGS  := -s -w

.PHONY: all build vet test test-race fmt lint vuln types run run-aggregate docker-build clean

all: fmt vet lint test build

# CGO_ENABLED=0 keeps the Go binaries static so they carry no libc dependency
# of their own. The runtime image is nonetheless libc-bearing - see ADR-007 -
# because the aggregate binary shells out to the DuckDB CLI, and every released
# DuckDB CLI is a glibc binary that cannot execute on a musl or static image.
build:
	@mkdir -p bin
	@for b in $(BINARIES); do \
		echo "building $$b"; \
		CGO_ENABLED=0 go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o bin/$$b ./cmd/$$b || exit 1; \
	done

vet:
	go vet ./...

test:
	go test ./...

# The crawler and the aggregator are both concurrent, so the race detector is
# worth the slower run rather than being an occasional extra.
test-race:
	go test ./... -race

# Reports files that need formatting; does not rewrite them, so it is safe to
# run in CI. `make fix` is deliberately not defined - formatting is cheap to
# do by hand and a silent rewrite hides what changed.
fmt:
	@unformatted=$$(gofmt -l -s .); \
	if [ -n "$$unformatted" ]; then \
		echo "not gofmt'd:"; echo "$$unformatted"; exit 1; \
	fi

lint:
	$(GOLANGCI_LINT) run ./...
	$(GOLANGCI_LINT) fmt --diff

vuln:
	$(GOVULNCHECK) ./...

# Regenerates the JSON Schema and TypeScript declaration the frontend consumes.
# The generated output is checked in, so a schema change that was not
# regenerated is caught by `git diff --exit-code` in CI.
types:
	go run ./cmd/gen-types -out schema

# The ingest binary is one binary with subcommands; `run` starts the worker
# because that is the long-running mode, and the cron subcommands are run
# explicitly. TLS/DSN values come from the environment, never from a file in
# the repository.
run: build
	./bin/lolstats-ingest worker

run-aggregate: build
	./bin/lolstats-aggregate build

# Local convenience only. CI pins the builder and runtime stages by digest in
# the Dockerfile and runs this same target.
docker-build:
	docker build -t $(IMAGE) .

clean:
	rm -rf bin/ dist/

# ---- additions: operations workstream (backups) ----
# Declared on separate .PHONY lines rather than by editing the one above, so
# that this addition stays append-only. make accepts any number of them.

.PHONY: backup-verify backup-verify-cluster render-overlay

# On its own line for the same append-only reason.
.PHONY: archive-verify

# The restore drill for Postgres - one half of the launch gate "Postgres and the
# raw archive have both been restored from backup in a test" (plan section 14).
# The raw-archive half is `make archive-verify` below; this target says nothing
# about it. It is what CI should run: a throwaway Postgres in Docker, the
# repository's own migrations, a real pg_dump, a restore into a fresh database,
# a four-part comparison, and a negative control that proves the comparison can
# fail. No cluster, no credentials, and nothing written outside
# .agent-artifacts/.
backup-verify:
	sh scripts/backup-verify.sh --docker

# The same drill against the live Postgres, by exec'ing into the postgres pod.
# It creates and drops a throwaway database named lolstats_verify_<epoch> and
# touches no workload and no Kubernetes object, but it is still a write against
# production, so the script refuses without --yes.
backup-verify-cluster:
	sh scripts/backup-verify.sh --cluster --yes

# The raw archive's half of the same launch gate: the restic repository that
# deploy/base/jobs/backup-archive.yaml writes, backed up, pruned, checked with a
# full --read-data, restored into an empty directory and compared with the
# archive byte for byte. A synthetic corpus stands in for Riot's data, which is
# what fixtures/README.md requires, so the drill needs no cluster, no Riot key
# and no pre-existing repository - and it means the script proves the round trip
# works rather than that one particular archive happened to restore.
archive-verify:
	sh scripts/archive-verify.sh

# Renders the overlay ArgoCD will apply and counts what came out. CI does not do
# this, and it is the only check that catches a manifest added to base/ but not
# to a kustomization - it would be rendered by nothing and missed by everything
# else.
render-overlay:
	@rendered=$$(kubectl kustomize deploy/overlays/homelab) || exit 1; \
	printf '%s\n' "$$rendered" | grep -c '^---' | xargs -I{} echo "{} documents"; \
	printf '%s\n' "$$rendered" | grep -c 'kind: CronJob' | xargs -I{} echo "{} CronJobs"; \
	printf '%s\n' "$$rendered" | grep -c 'kind: Job' | xargs -I{} echo "{} Jobs"

# ---- end additions: operations workstream (backups) ----

# ---- additions: DuckDB-engine CI gate ----
# The analytics tests in internal/aggregate are the only end-to-end proof that
# the build SQL produces the numbers a human computed by hand. They need the
# pinned DuckDB CLI and they *skip* rather than fail when it is absent, which is
# right for a developer without it and wrong for CI: a runner that never
# installed the client still reports a green suite and has never executed the
# aggregation path. So this block fetches the pin (`make duckdb`) and adds a
# target that treats a skip as a failure (`make test-build`). CI runs the latter.
# Declared on their own .PHONY line, like the block above, so this addition is
# append-only.
.PHONY: duckdb test-build

DUCKDB_VERSION := 1.4.5

# sha256 of the DuckDB v1.4.5 release zips, one per platform `make duckdb` can
# install. The Linux digests are the same constants the Dockerfile's `duckdb`
# stage verifies the copy it ships against, and the darwin/arm64 digest was
# taken the same way from the official GitHub release asset, with `shasum -a
# 256` - which reproduces both of the Dockerfile's digests exactly, so the two
# files cannot drift apart in method. A platform with no constant here is not a
# platform the target will download for: it fails instead. See docs/contracts.md
# section 5 and docs/decisions/ADR-007, which pins the client and names the
# version constant this target has to agree with.
DUCKDB_SHA256_LINUX_AMD64   := ff4ef9ec59fe3e1a1f3dd1004c6218d1fd59c0533c185c968c4403fd0240d02b
DUCKDB_SHA256_LINUX_ARM64   := c6d1c19631bb4d7a2a5dcf30586d888e167ce6fb22396060110c7a32e2bfc298
DUCKDB_SHA256_DARWIN_ARM64  := 756ed85623b18aafd1971f90188fc56bd6ed3d75aea2cd8078c52228f8fbffa7

# Absolute, because test-build hands this to a subprocess as
# LOLSTATS_DUCKDB_BIN and each test binary may run from any directory.
DUCKDB_BIN     := $(CURDIR)/bin/duckdb
DUCKDB_BASEURL := https://github.com/duckdb/duckdb/releases/download/v$(DUCKDB_VERSION)

# bin/ is gitignored, so the client never lands in a commit and a fresh clone
# always re-downloads it. Idempotent: a second run that already has the pinned
# version installed does nothing.
duckdb:
	@if [ -f "$(DUCKDB_BIN)" ] && "$(DUCKDB_BIN)" --version 2>/dev/null | grep -qF "v$(DUCKDB_VERSION)"; then \
		echo "duckdb v$(DUCKDB_VERSION) already installed at $(DUCKDB_BIN)"; \
		exit 0; \
	fi; \
	os=$$(uname -s); arch=$$(uname -m); \
	case "$$os/$$arch" in \
		Linux/x86_64|Linux/amd64) slug=linux; goarch=amd64; want="$(DUCKDB_SHA256_LINUX_AMD64)" ;; \
		Linux/aarch64|Linux/arm64) slug=linux; goarch=arm64; want="$(DUCKDB_SHA256_LINUX_ARM64)" ;; \
		Darwin/arm64|Darwin/aarch64) slug=osx; goarch=arm64; want="$(DUCKDB_SHA256_DARWIN_ARM64)" ;; \
		*) \
			echo "no pinned DuckDB v$(DUCKDB_VERSION) release for $$os/$$arch" >&2; \
			echo "this platform has no sha256 constant in the Makefile, and the target will not download bytes it cannot verify" >&2; \
			echo "supported: Linux/x86_64, Linux/aarch64, Darwin/arm64" >&2; \
			echo "set LOLSTATS_DUCKDB_BIN to a DuckDB v$(DUCKDB_VERSION) binary instead" >&2; \
			exit 1 ;; \
	esac; \
	for tool in curl; do \
		command -v "$$tool" >/dev/null 2>&1 || { echo "$$tool is required to install DuckDB" >&2; exit 1; }; \
	done; \
	if ! command -v unzip >/dev/null 2>&1 && ! command -v python3 >/dev/null 2>&1; then \
		echo "neither unzip nor python3 is available to unpack the release zip" >&2; \
		exit 1; \
	fi; \
	url="$(DUCKDB_BASEURL)/duckdb_cli-$$slug-$$goarch.zip"; \
	tmp="$(CURDIR)/bin/.duckdb-download.$$$$"; \
	mkdir -p "$$tmp"; \
	trap 'rm -rf "'"$$tmp"'"' EXIT INT TERM; \
	echo "downloading $$url"; \
	curl -fsSL -o "$$tmp/duckdb.zip" "$$url" || { echo "download failed: $$url" >&2; exit 1; }; \
	if command -v sha256sum >/dev/null 2>&1; then \
		got=$$(sha256sum "$$tmp/duckdb.zip" | awk '{print $$1}'); \
	else \
		got=$$(shasum -a 256 "$$tmp/duckdb.zip" | awk '{print $$1}'); \
	fi; \
	if [ "$$got" != "$$want" ]; then \
		echo "sha256 mismatch for $$url" >&2; \
		echo "  expected $$want" >&2; \
		echo "  got      $$got" >&2; \
		exit 1; \
	fi; \
	echo "sha256 ok: $$got"; \
	if command -v unzip >/dev/null 2>&1; then \
		unzip -q -o "$$tmp/duckdb.zip" -d "$$tmp"; \
	else \
		python3 -c 'import sys, zipfile; zipfile.ZipFile(sys.argv[1]).extractall(sys.argv[2])' "$$tmp/duckdb.zip" "$$tmp"; \
	fi; \
	chmod 0755 "$$tmp/duckdb"; \
	if ! "$$tmp/duckdb" --version | grep -qF "v$(DUCKDB_VERSION)"; then \
		echo "$$url does not report v$(DUCKDB_VERSION): $$("$$tmp/duckdb" --version)" >&2; \
		exit 1; \
	fi; \
	mv "$$tmp/duckdb" "$(DUCKDB_BIN)"; \
	if [ ! -f "$(DUCKDB_BIN)" ] || [ ! -x "$(DUCKDB_BIN)" ]; then \
		echo "no executable file at $(DUCKDB_BIN) after install; refusing to report success" >&2; \
		exit 1; \
	fi; \
	echo "installed $$("$(DUCKDB_BIN)" --version)"

# The strict gate. `go test` alone cannot express "this test must have run": the
# fixture tests skip themselves when the client is missing, so the exit status
# is 0 either way. This target installs the client, points the tests at it, and
# then asserts on the captured output that the hand-computed fixture build
# actually reported PASS and that nothing in the package reported SKIP.
#
# The test's exit status is carried out of the pipeline in a file rather than
# read from it: in a POSIX shell a pipeline's status is the last command's, so
# `go test ... | tee log` would report tee's success and hide a failure. tee
# still streams the run, and the log is kept under bin/ for a CI artifact.
test-build: duckdb
	@log="$(CURDIR)/bin/test-build.log"; statusfile="$$log.status"; \
	{ LOLSTATS_DUCKDB_BIN="$(DUCKDB_BIN)" go test -race -count=1 -v ./internal/aggregate/... 2>&1; echo $$? > "$$statusfile"; } | tee "$$log"; \
	status=$$(cat "$$statusfile"); rm -f "$$statusfile"; \
	if [ "$$status" -ne 0 ]; then \
		echo "FAIL: go test exited $$status; full output in $$log" >&2; \
		exit 1; \
	fi; \
	if ! grep -qE '^--- PASS: TestBuildAgainstHandComputedFixture( |$$)' "$$log"; then \
		echo "FAIL: TestBuildAgainstHandComputedFixture did not report PASS." >&2; \
		echo "      The DuckDB-backed fixture build did not run, so this gate proves nothing." >&2; \
		exit 1; \
	fi; \
	if grep -qE '(^|[[:space:]])--- SKIP:' "$$log"; then \
		echo "FAIL: a test in ./internal/aggregate/... skipped; this gate does not accept a skip:" >&2; \
		grep -E '(^|[[:space:]])--- SKIP:' "$$log" >&2; \
		exit 1; \
	fi; \
	echo "ok: TestBuildAgainstHandComputedFixture PASSed and nothing in ./internal/aggregate/... skipped"

# ---- end additions: DuckDB-engine CI gate ----

# ---- additions: compliance workstream (launch-blocking gate) ----
# Declared on its own .PHONY line so this addition stays append-only.

.PHONY: compliance

# The launch-blocking compliance gate (docs/compliance.md). It reads the source
# tree, the shared wording in internal/webtier, and the corpus of pages a running
# tier served - so `served-pages` captures that corpus first: several checks are
# about what the deployment actually serves rather than about what the source
# intends, and four of them read the deployment's own output. It needs no network
# and no package manager, and it fails when a scan reads so little that its result
# would be meaningless - a check that passes because it scanned nothing is worse
# than no check at all.
compliance: served-pages
	sh scripts/compliance-check.sh

# Kept as an alias, because it used to be a second corpus: the target scanned the
# pages a running tier served while `compliance` scanned web/dist. The Astro tree
# was deleted on 2026-09-18 and there is only one corpus left, so this runs the
# same gate over the same pages rather than being dropped from the CI job list.
.PHONY: compliance-served
compliance-served: compliance

# ---- end additions: compliance workstream ----

# ---- additions: deploy-time migration (the PreSync hook) ----
# Declared on its own .PHONY line so this addition stays append-only, like the
# blocks above it.

.PHONY: migrate

# Applies the schema to the live cluster *now*, for the case where the operator
# does not want to wait for the next ArgoCD sync. It is the imperative twin of
# the `PreSync` hook in deploy/base/jobs/migrate.yaml, and it is the answer to
# "the database is empty and the worker is logging `relation \"fetch_queue\" does
# not exist`" - see deploy/README.md, section Migrations.
#
# It applies the file from base/ rather than the rendered overlay on purpose: the
# only thing the overlay adds to that Job is the node affinity that keeps it off
# vega, and a one-shot migration is happy on any node. Applying the single file
# needs `-n lolstats` explicitly, because the namespace comes from the base
# kustomization, which this path does not go through.
#
# The delete is not belt-and-braces, it is the whole reason the target is three
# lines: a `batch/v1` Job's pod template is immutable, so a plain re-apply of a
# changed spec is rejected by the API server. This is the same reason the hook
# carries `hook-delete-policy: BeforeHookCreation`; doing it by hand just makes
# it explicit. `--ignore-not-found` keeps the first run quiet.
#
# Safe to run twice and safe to run alongside a sync: `migrate up` takes an
# advisory lock, keeps a checksummed ledger and re-applies nothing, so a second
# run against a current schema exits 0 having logged "schema is already current".
migrate:
	kubectl -n lolstats delete job lolstats-migrate --ignore-not-found
	kubectl apply -n lolstats -f deploy/base/jobs/migrate.yaml
	kubectl -n lolstats wait --for=condition=Complete job/lolstats-migrate --timeout=300s

# ---- end additions: deploy-time migration ----

# ---- additions: gates lane (serving and compliance gates) ----
# Appended at the end, and declared on its own .PHONY line, so this addition
# stays append-only like the blocks above it.
.PHONY: served-pages verify-serving verify-serving-local compliance-negative-control compliance-gnu capture-served-pages

# Captures what a running tier serves into bin/served-pages, by starting the tier
# on loopback over the checked-in fixture artifact tree (no cluster, no PVC, no
# network) and reading its own /sitemap.xml. That capture is the corpus the
# launch-blocking gate scans, and the reason it replaced the retired Astro tree
# in web/dist: it is the deployment's own output rather than a pre-rendered
# stand-in, it carries the no-JS filter bar that tree left to a client island,
# and it is what the amended checks 3 and 4 were written for. The tier is an
# ephemeral process on 127.0.0.1 and nothing here touches the cluster.
served-pages: build
	@port=$${LOLSTATS_SERVED_PORT:-18097}; pid=""; \
	cleanup() { [ -n "$$pid" ] && kill "$$pid" 2>/dev/null; }; \
	trap cleanup EXIT INT TERM; \
	( export LOLSTATS_AGG_FIXTURES=only; \
	  export LOLSTATS_WEB_ADDR="127.0.0.1:$$port"; \
	  [ -n "$$LOLSTATS_SITE_URL" ] && export LOLSTATS_SITE_URL; \
	  exec ./bin/lolstats-web ) >bin/served-pages.log 2>&1 & \
	pid=$$!; \
	i=0; \
	while [ $$i -lt 40 ]; do \
		if curl -fsS "http://127.0.0.1:$$port/healthz" >/dev/null 2>&1; then break; fi; \
		kill -0 "$$pid" 2>/dev/null || break; \
		i=$$((i+1)); sleep 0.5; \
	done; \
	if ! curl -fsS "http://127.0.0.1:$$port/healthz" >/dev/null 2>&1; then \
		echo "FAIL: bin/lolstats-web did not answer /healthz on 127.0.0.1:$$port; see bin/served-pages.log" >&2; \
		exit 1; \
	fi; \
	LOLSTATS_SERVE_URL="http://127.0.0.1:$$port" \
		LOLSTATS_SERVED_DIST="$(CURDIR)/bin/served-pages" \
		sh scripts/capture-served-pages.sh || exit 1; \
	echo "ok: the pages the tier serves are captured in bin/served-pages ($$(find bin/served-pages -name '*.html' | wc -l | tr -d ' ') page(s))"

# The serving-contract gate for the deployed tier: /healthz, /metrics, the HTML
# cache/ETag/304 policy, the Data Dragon static policy and the 503 + visible
# error page for a missing agg/v1. It needs a reachable tier, so it is what CI
# runs against a locally started `bin/lolstats-web` (verify-serving-local) and
# what an operator runs against the cluster through
#   kubectl -n lolstats port-forward svc/lolstats-go-web 18099:80
verify-serving:
	@LOLSTATS_SERVE_URL="$${LOLSTATS_SERVE_URL:-http://127.0.0.1:18099}" \
		sh scripts/verify-serving.sh

# Starts the tier on a loopback port with the checked-in fixture artifact tree
# (no cluster, no PVC, no network) and runs the same gate against it, including
# the 503 path with LOLSTATS_EXPECT_NO_AGG=1 - the tier is started a second time
# with a deliberately corrupt aggregate root, because a corrupt artifact must
# produce a visible error page rather than a truncated 200. Nothing here touches
# the cluster: it is an ephemeral process on 127.0.0.1.
verify-serving-local: build
	@port=$${LOLSTATS_LOCAL_PORT:-18098}; pid=""; \
	cleanup() { [ -n "$$pid" ] && kill "$$pid" 2>/dev/null; }; \
	trap cleanup EXIT INT TERM; \
	start() { \
		( export LOLSTATS_AGG_FIXTURES="$$1"; \
		  [ -n "$$2" ] && export LOLSTATS_AGG_ROOT="$$2"; \
		  export LOLSTATS_WEB_ADDR="127.0.0.1:$$port"; \
		  [ -n "$$LOLSTATS_SITE_URL" ] && export LOLSTATS_SITE_URL; \
		  exec ./bin/lolstats-web ) >bin/verify-serving-local.log 2>&1 & \
		pid=$$!; \
		i=0; \
		while [ $$i -lt 40 ]; do \
			if curl -fsS "http://127.0.0.1:$$port/healthz" >/dev/null 2>&1; then return 0; fi; \
			kill -0 "$$pid" 2>/dev/null || break; \
			i=$$((i+1)); sleep 0.5; \
		done; \
		echo "FAIL: bin/lolstats-web did not answer /healthz within 20s; see bin/verify-serving-local.log" >&2; \
		return 1; \
	}; \
	corrupt="$(CURDIR)/bin/verify-serving-corrupt-agg"; \
	rm -rf "$$corrupt"; mkdir -p "$$corrupt/v1"; \
	printf '{"schema": 1, "source": "broken-fixture"' > "$$corrupt/v1/manifest.json"; \
	echo "== tier over the checked-in fixture artifact tree =="; \
	start only "" || exit 1; \
	LOLSTATS_SERVE_URL="http://127.0.0.1:$$port" sh scripts/verify-serving.sh || exit 1; \
	kill "$$pid" 2>/dev/null; wait "$$pid" 2>/dev/null; pid=""; \
	echo "== tier over a corrupt artifact root (must answer 503, never a truncated 200) =="; \
	start off "$$corrupt" || exit 1; \
	LOLSTATS_SERVE_URL="http://127.0.0.1:$$port" LOLSTATS_EXPECT_NO_AGG=1 LOLSTATS_AGG_ROOT="$$corrupt" sh scripts/verify-serving.sh || exit 1; \
	kill "$$pid" 2>/dev/null; wait "$$pid" 2>/dev/null; pid=""; \
	echo "ok: the serving contract holds over the fixtures, and a missing agg/v1 is a visible 503"

# The negative control for the amended compliance gate (scripts/compliance-check.sh,
# amendment of 2026-09-17, docs/compliance.md). Checks 3 and 4 were failing a
# legitimate server-rendered page - a page may load no <script> at all, and a
# no-JS sort/filter/pagination form is a <form> - so their rules were replaced by
# the invariant they were standing in for: no third-party script that phones
# home, and every form submits through an on-origin GET that the server can
# answer. This target is what keeps that amendment honest: it plants one
# violation at a time into a scratch copy of the served capture and fails unless
# the gate rejects each of them, with a page stripped of every <script> passing.
compliance-negative-control: served-pages
	sh scripts/compliance-negative-control.sh

# Capture the HTML a running tier serves into bin/served-pages. Point it at the
# cluster through the same port-forward the serving contract uses:
#   kubectl -n lolstats port-forward svc/lolstats-go-web 18099:80 &
#   make capture-served-pages
capture-served-pages:
	@LOLSTATS_SERVE_URL="$${LOLSTATS_SERVE_URL:-http://127.0.0.1:18099}" \
		sh scripts/capture-served-pages.sh

# The compliance gate under GNU userland, which is what the CI runner has and
# what this machine is not. The gate's scans hand a NUL-delimited list of paths
# to grep, and an empty list is answered differently by the two implementations:
# GNU xargs still runs the command when the list is empty, and grep then reads its
# own standard input, so two phantom "(standard input)" pages were reported as
# missing their banner and CI went red on a tree that passes here. That is a
# class of defect a green local run cannot show, so this target re-runs the same
# script in debian:12-slim with a non-empty stdin. It is the local half of the
# portability control; the half that runs everywhere, including CI, is check 12
# inside the gate, which asserts the empty-list behaviour directly.
# Silent skip with a reason when there is no usable container runtime, because
# this is a verification aid and not a launch gate.
compliance-gnu: served-pages
	@if ! command -v docker >/dev/null 2>&1; then \
		echo "skipped: docker is not installed, so the gate ran only under $(uname -s) grep"; \
	elif ! docker info >/dev/null 2>&1; then \
		echo "skipped: the container runtime is not answering, so the gate ran only under $(uname -s) grep"; \
	else \
		echo "== the compliance gate under GNU userland (debian:12-slim) =="; \
		echo "     over the served corpus at bin/served-pages: $$(find bin/served-pages -name '*.html' | wc -l | tr -d ' ') page(s)"; \
		cat scripts/compliance-check.sh | docker run --rm -i --user "$$(id -u):$$(id -g)" \
			-v "$(CURDIR):/w" -w /w debian:12-slim \
			sh -c 'grep --version | head -1; sh /w/scripts/compliance-check.sh' || exit 1; \
	fi

# ---- end additions: gates lane ----
