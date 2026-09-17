# ADR-007: The aggregation engine is the pinned DuckDB CLI, run as a subprocess

- Status: accepted
- Date: 2026-09-17
- Decision: D2 (engine for the aggregation build step)

## Context

`cmd/lolstats-aggregate` needs a SQL engine that can read parquet partition files
and JSON payloads with projection, filtering and aggregation, and it needs it in
an image that is built with `CGO_ENABLED=0`: the whole point of the aggregator
is that a rebuild is reproducible from a pinned image digest, and a cgo binary
turns "the same digest" into "the same digest and the same libc and the same
toolchain defaults".

Both reasonable Go bindings were tried against this repository, in two genuinely
different forms, and both failed the static requirement:

| Attempt | Command | Result |
| --- | --- | --- |
| `github.com/marcboeker/go-duckdb` v1.8.5 | `CGO_ENABLED=0 go build ./cmd/lolstats-aggregate` | `undefined: Conn` - every type in the package is inside a cgo-only build constraint, so the package compiles to nothing |
| `github.com/duckdb/duckdb-go/v2` v2.10505.0 | `CGO_ENABLED=0 go build ./cmd/lolstats-aggregate` | `build constraints exclude all Go files` - the binding requires `cgo` and a bundled libduckdb |

A third avenue (vendoring the amalgamation and linking it with `-extldflags
-static`) was rejected without a spike: it makes the Go build depend on a
`cc` that can produce a fully static binary against musl or glibc, which is a
toolchain-pinning problem in its own right, and it would put a several-megabyte
cgo surface in the one binary whose whole value is that it is boring to build.

The plan's stop condition for exactly this situation is to pin the CLI and
document it, so that is what this ADR records.

## Decision

**The aggregation engine is DuckDB v1.4.5 (LTS), invoked as a subprocess,
pinned by release and verified by checksum.**

- Version: **v1.4.5**, the current LTS line, deliberately not `latest` (plan
  section 4-D2: a build step that changes engine version when a release
  happens is not reproducible).
- `duckdb --version` prints `v1.4.5 (Andium) f31be57c18` for this release.
- The image fetches the release archive for `TARGETARCH` and verifies a
  per-architecture sha256 before unpacking:

| Asset | sha256 |
| --- | --- |
| `duckdb_cli-linux-amd64.zip` | `ff4ef9ec59fe3e1a1f3dd1004c6218d1fd59c0533c185c968c4403fd0240d02b` |
| `duckdb_cli-linux-arm64.zip` | `c6d1c19631bb4d7a2a5dcf30586d888e167ce6fb22396060110c7a32e2bfc298` |
| `duckdb_cli-osx-arm64.zip` (developer machines) | `756ed85623b18aafd1971f90188fc56bd6ed3d75aea2cd8078c52228f8fbffa7` |
| unpacked `linux-amd64/duckdb` | `2dcae9f283d3d9609fb1ddcaceb943fdec2f452754ba6e95dc6a696874b5fa5a` |
| unpacked `linux-arm64/duckdb` | `6c4f25b6efc6290f46e70a4191fe772ecbbf929e59a2be082590ac6a1c7e4fca` |

- The binary resolves the client in this order: `--duckdb-bin`,
  `$LOLSTATS_DUCKDB_BIN`, the repository-local download
  (`.agent-artifacts/duckdb/duckdb`, for tests), then `PATH`.
- `build` reads `--version` and **fails** unless it reports the pinned release.
  `--duckdb-allow-mismatch` is the explicit, logged override for a developer
  machine. An artifact whose engine version is unknown is not reproducible, and
  reproducibility is the only reason the raw archive is the sole input.
- The engine is injected as an interface (`aggregate.Engine`) and as a
  `BuildOptions.Engine` field, so every test that is not specifically about the
  CLI runs with no engine present.
- `demo` never needs the engine: it synthesises its population in Go, which is
  what makes the demo runnable in CI with no download.

### Amendment to `docs/contracts.md` section 5

The CLI is a **glibc** binary. Its ELF `DT_NEEDED` entries (read directly from
the pinned `linux-amd64/duckdb`) are:

```
libstdc++.so.6  libgcc_s.so.1  libm.so.6  libc.so.6  libdl.so.2  libpthread.so.0
```

`gcr.io/distroless/static-debian12` contains no libc at all, so it cannot run
the binary. The runtime base therefore becomes
`gcr.io/distroless/cc-debian12:nonroot`, which was checked against every
`DT_NEEDED` entry above by unpacking its layers: it carries
`libstdc++.so.6.0.30`, `libgcc_s.so.1`, `libc.so.6`, `libm.so.6`,
`libdl.so.2`, `libpthread.so.0`, `libnsl.so.1`, `librt.so.1`,
`ld-linux-x86-64.so.2`, `/etc/passwd` with the `nonroot` user, `/tmp`, and
`ca-certificates.crt`.

This deviated from the image contract then frozen in `docs/contracts.md`
section 5 in exactly two ways. The contract was corrected in the same change -
section 5 now lists the three stages and names `cc-debian12:nonroot` as the
runtime base - and this ADR is the record of why:

1. the runtime base moves from the `static` variant to the `cc` variant of the
   same distroless family, keeping `:nonroot` and the same non-root UID 65532;
2. the image carries a new binary at `/usr/local/bin/duckdb` and a new
   environment default, `LOLSTATS_DUCKDB_BIN=/usr/local/bin/duckdb`.

The `base.name` label is updated to name the variant. The image is still
distroless, still non-root, still has no shell, and still serves only static
files plus one CronJob entrypoint - the change is the minimum needed to run the
engine the build step depends on.

## Alternatives considered

- **`marcboeker/go-duckdb`, cgo enabled.** Rejected: the build then needs a C
  toolchain and a matching libduckdb at link *and* run time, which moves the
  reproducibility question from "which release archive" to "which base image,
  which gcc, which libstdc++". The failure it produces here is concrete:
  without cgo the package is empty (`undefined: Conn`).
- **`duckdb/duckdb-go/v2`, cgo enabled.** Rejected for the same reason, and its
  static build path was not even reachable: without cgo it is `build constraints
  exclude all Go files`.
- **Link the DuckDB amalgamation statically with `-extldflags -static`.** Would
  keep the process in one binary and avoid the shell-out, at the cost of a cgo
  build that must find a static libstdc++ and a static glibc or musl. That is a
  toolchain spike of its own, and the plan's stop condition explicitly prefers
  the pinned CLI over spending the budget there.
- **Ship the CLI in the distroless `static` image and call it anyway.** Fails at
  run time with a missing dynamic loader (`not found`, which is the shell
  reporting ENOENT for `/lib64/ld-linux-x86-64.so.2`, not for the binary).
  Attempting this on an Alpine base during the spike produced exactly that
  error.
- **A pure-Go parquet reader (`parquet-go`) plus `encoding/json`.** Superficially
  attractive: no engine at all, and the aggregation is "just" a group-by. It was
  rejected because the JSON payloads are large nested documents that must be
  projected and filtered *inside* the scan, and doing that in Go means
  unmarshalling every match summary into Go structs - which is precisely the
  cost profile the DuckDB choice exists to avoid, and it would move a
  considerable amount of SQL into hand-written Go.
- **`latest` DuckDB.** Rejected per plan section 4-D2.

## Consequences

- The engine is a **runtime dependency of the aggregate CronJob and nothing
  else**. `lolstats-ingest`, `lolstats-api` and the web image are unchanged.
- A `duckdb` subprocess costs a process spawn and a JSON round trip per
  statement group. The build runs five statements in the extraction pass and
  seven in the reduction pass, so this is noise against the scan itself.
- The build depends on the archive layout being exactly what the SQL assumes
  (columns `match_id` and `payload` in the partition files). That assumption is
  already frozen in `docs/contracts.md` section 4, and the extraction fails
  closed with `ErrMalformedArchive` rather than publishing a partial read.
- The version assertion is what keeps this honest. Without it, a developer with
  a newer client on `PATH` would produce artifacts from an engine that the image
  does not ship, and the difference would not be visible in the artifact.
- The image grows by the CLI binary and, more importantly, by libstdc++. That is
  a real cost against the "nothing in the image that is not needed" rule, and it
  is the price of the static Go build.

## Migration path

If a Go binding later builds with `CGO_ENABLED=0`, or a static linking story
becomes boring, the swap is one interface: `aggregate.Engine` is already the
seam, `OpenCLIEngine` is the only implementation that spawns a process, and no
other file in `internal/aggregate` knows how the engine is executed. The
`--duckdb-bin` flag and `LOLSTATS_DUCKDB_BIN` would then be dropped, the `cc`
base could go back to `static`, and the DuckDB version would move from the image
`ARG` to `go.mod`.
