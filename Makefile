MODULE  := github.com/Erik-Schuetze/league-of-legends
BINARIES := lolstats-ingest lolstats-aggregate
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

.PHONY: all build vet test test-race fmt lint vuln types run run-aggregate docker-build web-install web-build clean

all: fmt vet lint test build

# CGO_ENABLED=0 keeps the binary fully static, which is what lets the runtime
# stage be distroless/static with no libc at all.
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
	go run ./cmd/gen-types -out web/src/types

# The ingest binary is one binary with subcommands; `run` starts the worker
# because that is the long-running mode, and the cron subcommands are run
# explicitly. TLS/DSN values come from the environment, never from a file in
# the repository.
run: build
	./bin/lolstats-ingest worker

run-aggregate: build
	./bin/lolstats-aggregate build

web-install:
	cd web && npm ci

web-build: web-install
	cd web && npm run build

# Local convenience only. CI pins the builder and runtime stages by digest in
# the Dockerfile and runs this same target.
docker-build:
	docker build -t $(IMAGE) .

clean:
	rm -rf bin/ dist/ web/dist/ web/node_modules/

# ---- additions: operations workstream (backups) ----
# Declared on separate .PHONY lines rather than by editing the one above, so
# that this addition stays append-only. make accepts any number of them.

.PHONY: backup-verify backup-verify-cluster render-overlay

# The restore drill. This is the evidence behind the launch gate "Postgres and
# the raw archive have both been restored from backup in a test" (plan section
# 14), and it is what CI should run: a throwaway Postgres in Docker, the
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

# Renders the overlay ArgoCD will apply and counts what came out. CI does not do
# this, and it is the only check that catches a manifest added to base/ but not
# to a kustomization - it would be rendered by nothing and missed by everything
# else.
render-overlay:
	@rendered=$$(kubectl kustomize deploy/overlays/homelab) || exit 1; \
	printf '%s\n' "$$rendered" | grep -c '^---' | xargs -I{} echo "{} documents"; \
	printf '%s\n' "$$rendered" | grep -c 'kind: CronJob' | xargs -I{} echo "{} CronJobs"

# ---- end additions: operations workstream (backups) ----
