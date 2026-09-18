# syntax=docker/dockerfile:1

# Every stage is pinned by digest. A tag like :1.27-alpine is mutable - the
# upstream maintainers can repoint it at any time, and `docker build` would
# pick that up with no diff anywhere in this repository. The digest makes the
# image content immutable; the tag stays in front of it so a human reading
# the file can still tell what it is. .github/dependabot.yml proposes the
# next digest as a reviewable pull request.
FROM golang:1.27-alpine@sha256:cf6fca6641884b8433441b2b0652976f975e1d0fdd26d177eaaf8596087f3125 AS build
WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
# internal/store embeds the migration SQL, so the build stage needs it too.
COPY sql ./sql

# CGO disabled: the two Go binaries link no C, so they are fully static. A
# single build stage produces both because they share every dependency. The
# image still carries a libc, but only for the pinned DuckDB CLI below.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/lolstats-ingest ./cmd/lolstats-ingest \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/lolstats-aggregate ./cmd/lolstats-aggregate

# The aggregation step is the one part of the pipeline that needs a SQL engine,
# and there is no static DuckDB CLI to link against: both official Linux CLI
# builds for v1.4.5 are dynamically linked ELF binaries whose DT_NEEDED list is
# libstdc++.so.6, libgcc_s.so.1, libm.so.6, libc.so.6, libdl.so.2 and
# libpthread.so.0 (verified with readelf on the release artifacts).
#
# Version policy: v1.4.5 is an LTS release and is deliberately not `latest`,
# because ADR-002 forbids re-validating the analytics engine inside a build.
# Bumping this ARG is an explicit, reviewable act, and
# the download is verified against a per-architecture sha256 of the release
# zip, so a repointed or tampered release asset fails the build instead of
# shipping. See docs/aggregation.md and docs/decisions/ADR-007.
FROM debian:12-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171 AS duckdb

# TARGETARCH is a predefined *global* build argument, and a stage only sees one
# if it declares it. Without this line the download step dies at
# `/bin/sh: TARGETARCH: parameter not set` under `set -u` - the shell's own
# error, one stage before anything to do with DuckDB. Declaring it with no
# default is correct: a default would silently build the wrong architecture's
# CLI whenever BuildKit did not supply one.
ARG TARGETARCH

ARG DUCKDB_VERSION=1.4.5
# sha256 of duckdb_cli-linux-amd64.zip and duckdb_cli-linux-arm64.zip for that
# release, taken from https://github.com/duckdb/duckdb/releases.
ARG DUCKDB_SHA256_AMD64=ff4ef9ec59fe3e1a1f3dd1004c6218d1fd59c0533c185c968c4403fd0240d02b
ARG DUCKDB_SHA256_ARM64=c6d1c19631bb4d7a2a5dcf30586d888e167ce6fb22396060110c7a32e2bfc298

# debian:12-slim, not alpine, because this stage has to *run* the CLI to assert
# the version, and the CLI is a glibc binary: on musl it fails with
# `/opt/duckdb/duckdb: not found`, which is the loader missing rather than the
# file. Debian 12 is also the exact library generation of the runtime base.
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl unzip; \
    rm -rf /var/lib/apt/lists/*; \
    case "${TARGETARCH}" in \
      amd64) want="${DUCKDB_SHA256_AMD64}" ;; \
      arm64) want="${DUCKDB_SHA256_ARM64}" ;; \
      *) echo "no pinned DuckDB CLI for TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /duckdb.zip \
      "https://github.com/duckdb/duckdb/releases/download/v${DUCKDB_VERSION}/duckdb_cli-linux-${TARGETARCH}.zip"; \
    echo "${want}  /duckdb.zip" | sha256sum -c -; \
    mkdir -p /opt/duckdb; \
    unzip -q /duckdb.zip -d /opt/duckdb; \
    /opt/duckdb/duckdb --version; \
    /opt/duckdb/duckdb --version | grep -qF "v${DUCKDB_VERSION}"

# Runtime. The base is distroless `cc` rather than `static` because the pinned
# CLI needs glibc, libstdc++ and libgcc_s, and `static` has no libc at all.
# `cc` is still distroless: no shell, no package manager, and a nonroot user.
# docs/decisions/ADR-007 records this as an amendment to the image contract in
# docs/contracts.md section 5.
FROM gcr.io/distroless/cc-debian12:nonroot@sha256:9dac0a79194e45a7da0158a9c6da57b217585af0786db3845d1f0ec1a0dd182f

# One image, one entrypoint. Deployments and CronJobs select the binary they
# need with `command`, so a subcommand change never needs a new image.
COPY --from=build /out/lolstats-ingest /lolstats-ingest
COPY --from=build /out/lolstats-aggregate /lolstats-aggregate
COPY --from=duckdb /opt/duckdb/duckdb /usr/local/bin/duckdb

# The aggregate build resolves the engine version and refuses to publish from a
# binary other than the one pinned here unless --duckdb-allow-mismatch is given,
# so the CLI in the image is the CLI that produced the artifacts.
ENV LOLSTATS_DUCKDB_BIN=/usr/local/bin/duckdb

# CI supplies these. The defaults exist so that a local `docker build` without
# build args still produces an image whose labels are honest rather than
# claiming a version it does not have. The label set and the tag scheme are
# frozen in docs/contracts.md section 5.
ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=1970-01-01T00:00:00Z

# The revision has to reach the process, not just the image metadata. The
# aggregate binary records the revision it was built from in the manifest and the
# build_runs row, and it reads it from GIT_SHA - the same build arg CI already
# passes for the label. Without this line every published number carries
# "unknown" behind it, which is the one value the field is not allowed to
# silently degrade to when a real commit is available.
ENV GIT_SHA=${REVISION}

LABEL org.opencontainers.image.title="league-of-legends stats pipeline" \
      org.opencontainers.image.description="Riot API ingestion and DuckDB aggregation for a self-hosted League of Legends statistics site" \
      org.opencontainers.image.source="https://github.com/Erik-Schuetze/league-of-legends" \
      org.opencontainers.image.url="https://github.com/Erik-Schuetze/league-of-legends" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.created="${CREATED}" \
      org.opencontainers.image.base.name="gcr.io/distroless/cc-debian12:nonroot"

# Nothing in the image is writable state: raw archive, aggregate output and
# the site volume are all mounted. Running as nonroot means a compromised
# process cannot write to its own image layer either.
USER nonroot:nonroot
ENTRYPOINT ["/lolstats-ingest"]
CMD ["worker"]
