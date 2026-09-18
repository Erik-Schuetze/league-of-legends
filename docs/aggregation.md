# Aggregation: the DuckDB build step behind `agg/v1`

This is the design and operations note for `cmd/lolstats-aggregate`, the only
producer of the `agg/v1` artifacts the site serves. It covers the engine pin,
the extraction rules, the cell policy, the gates that make it fail closed, the
publish protocol, the audit row, and the deterministic demo mode. The rest of
the contract lives in `docs/contracts.md` (frozen), the nightly operational
steps in `docs/runbooks/rebuild-aggregates.md`, and the decisions with their
alternatives in `docs/decisions/` (index in `docs/decisions/README.md`).

| Question | Answer |
| --- | --- |
| Input | the immutable raw archive, `raw/riot/match-v5/dt=<date>/part-*.parquet[.zst]` |
| Engine | DuckDB **1.4.5 LTS**, run as a subprocess, pinned by release, version asserted at startup |
| Never read | timelines. A tier list is computed from match summaries alone |
| Output | `agg/v1/manifest.json` plus one directory per `p/<patch>/<region>/<queue>/<bracket>` |
| Publish | staged directory, then `rename(2)`; `manifest.json` swaps last |
| Failure | the build fails, rolls itself back, and the previous artifacts stay live |
| Audit | one `build_runs` row per run, through the frozen `contract.Store` interface |

## 1. Why the engine is a subprocess, not a linked library

The build needs a SQL engine with parquet and JSON support, and it has to fit an
aggregator that is compiled with `CGO_ENABLED=0`. Two Go bindings were tried and
both failed `CGO_ENABLED=0`:

| Attempt | Result |
| --- | --- |
| `github.com/marcboeker/go-duckdb` v1.8.5 | compiles only with cgo; without it the package is empty and the build fails on `undefined: Conn` |
| `github.com/duckdb/duckdb-go/v2` v2.10505.0 | `build constraints exclude all Go files` without cgo |

The plan's stop condition for this is explicit, so the build shells out to the
official DuckDB CLI instead. `docs/decisions/ADR-007-pinned-duckdb-cli-engine.md`
records the tradeoffs. Two consequences are worth stating here:

- The engine is a **runtime dependency of the aggregator**, not of the ingest
  binary. The image ships it at `/usr/local/bin/duckdb` and the base is
  distroless `cc` rather than `static` because the CLI is a glibc binary. A
  `demo` run needs no engine at all.
- The version is asserted, not assumed. `build` reads `duckdb --version` and
  refuses to publish unless it reports the pinned release;
  `--duckdb-allow-mismatch` is the explicit, logged override for a developer
  machine. An artifact produced by an unknown engine is not reproducible, and
  reproducibility is the reason the raw archive is the only input.

Version pin: **v1.4.5**, an LTS release, deliberately not `latest` - see plan
section 4-D2 and ADR-002. The image takes it from the pinned release archive and
verifies a per-architecture sha256 of the zip before unpacking, so a repointed
or tampered asset fails the build instead of shipping.

| Artifact | sha256 |
| --- | --- |
| `duckdb_cli-linux-amd64.zip` (v1.4.5) | `ff4ef9ec59fe3e1a1f3dd1004c6218d1fd59c0533c185c968c4403fd0240d02b` |
| `duckdb_cli-linux-arm64.zip` (v1.4.5) | `c6d1c19631bb4d7a2a5dcf30586d888e167ce6fb22396060110c7a32e2bfc298` |
| unpacked `linux-amd64/duckdb` | `2dcae9f283d3d9609fb1ddcaceb943fdec2f452754ba6e95dc6a696874b5fa5a` |
| unpacked `linux-arm64/duckdb` | `6c4f25b6efc6290f46e70a4191fe772ecbbf929e59a2be082590ac6a1c7e4fca` |
| macOS arm64 (developer machines) | `duckdb_cli-osx-arm64.zip`, sha256 `756ed85623b18aafd1971f90188fc56bd6ed3d75aea2cd8078c52228f8fbffa7` |

`duckdb --version` for the pinned release prints `v1.4.5 (Andium) f31be57c18`.

### Getting the engine locally

Preferred: let the Makefile fetch the pin into `bin/` (gitignored), verified
against the same per-architecture digests the image uses.

```
make duckdb       # Linux x86_64/aarch64 and macOS arm64; other platforms fail loudly
```

Idempotent, so it is cheap to put in front of a test target. By hand:

```
curl -fsSL -o duckdb.zip \
  https://github.com/duckdb/duckdb/releases/download/v1.4.5/duckdb_cli-linux-amd64.zip
shasum -a 256 duckdb.zip   # must match the table above
unzip duckdb.zip -d ~/.local/duckdb
export LOLSTATS_DUCKDB_BIN="$HOME/.local/duckdb/duckdb"
```

`make duckdb` refuses to guess: a platform with no sha256 in the Makefile gets a
message and a non-zero exit rather than unverified bytes, and the installed
binary is asked for `--version` before it is accepted, so a repointed asset
cannot be adopted silently.

Resolution order for the client is: `--duckdb-bin`, then `LOLSTATS_DUCKDB_BIN`,
then the repository-local download in `.agent-artifacts/duckdb/duckdb`, then
`bin/duckdb` (where `make duckdb` puts it), then `PATH`. Nothing downloads an
engine at runtime; a build without one fails immediately with the path it looked
for.

### Why the runtime image cannot be static or musl

The DuckDB CLI is published as a **glibc** binary and there is no musl build to
fall back on, so the aggregator's runtime base has to carry glibc: the image uses
distroless `cc-debian12`, not `static` and not Alpine. This is not a preference -
it was learned the hard way. The first image built the CLI stage on Alpine and
downloaded the release binary into it; the *build* succeeded and the *runtime*
failed with

```
/opt/duckdb/duckdb: not found
```

which is what a musl loader says about a glibc ELF, and reads like a missing file
rather than a libc mismatch. The fix, now in the `Dockerfile`, is a glibc base
(Debian slim) in the CLI stage plus an assertion that the downloaded binary runs
(`duckdb --version`) *inside the image that will ship it* - a download-and-copy
stage can otherwise only prove it fetched bytes. See
`docs/decisions/ADR-007-pinned-duckdb-cli-engine.md`.

### When the version assertion fires

`build`, `verify` and `manifest` all open the engine through `OpenCLIEngine`,
which runs `duckdb --version` and compares the first field against the pin
(`internal/aggregate.DuckDBVersion`, currently `1.4.5`). A mismatch stops the
build before a single row is read:

```
duckdb version mismatch: found 1.5.0, pinned 1.4.5 (set --duckdb-allow-mismatch to override)
```

The pin matters because the engine *is* the arithmetic: a different release can
change a cast, a division, a `NULL` ordering or a parquet reader default, and the
build would then publish different `win_rate`, `tier` and `n` values for the same
immutable archive. Reproducibility is the whole reason the raw archive is the
only input, so an artifact produced by an unknown engine is not comparable with
the ones already live.

What to do, in order:

1. **In a deployment** - do not override. The image ships the pinned client at
   `/usr/local/bin/duckdb`; if the assertion fires there, the image is wrong, not
   the engine. Pull the published tag, or rebuild from the pinned `Dockerfile`,
   and let the nightly job retry. The previous artifacts stay live.
2. **On a developer machine** - install the pin (`make duckdb`, then
   `export LOLSTATS_DUCKDB_BIN="$PWD/bin/duckdb"`). Prefer this: it is the only
   path that makes a local number comparable with a published one.
3. **Only to investigate** - `--duckdb-allow-mismatch` runs anyway, logs a warn
   line naming both versions, and is never a way to fix a red deploy. Treat any
   tree it produced as unverified until the pin has rebuilt it.

### Resource bounds: the ceiling DuckDB cannot see for itself

DuckDB sizes its buffer manager from the **host's** RAM, not from the cgroup the
process runs in. On a 12.7 GiB node it reports
`SELECT current_setting('memory_limit')` as `12.7 GiB` even inside a pod limited
to 3 GiB, so a build that grows past the pod limit is killed by the kernel rather
than by the engine. That is what the nightly aggregate Job did: 20 000 synthetic
matches peaked at 3.59 GiB RSS (already over the 3 Gi limit) and 100 KiB
payloads over a four-day window hit
`Out of Memory Error: failed to allocate data of size 2.0 MiB (12.7 GiB/12.7 GiB used)`.

Every `Exec` therefore prepends a preamble that pins four settings, because each
statement is its own short-lived `duckdb` client and a setting that is not
repeated is not in force:

| Setting | Default | Why it is bounded |
| --- | --- | --- |
| `memory_limit` | `1GiB` | A third of the Job's 3 Gi limit. The Go runtime (`GOMEMLIMIT=2GiB`), the page cache and DuckDB's own non-buffer allocations share the same cgroup, so a limit equal to the pod limit is still an OOM kill. |
| `threads` | `2` | Matches the Job's 2-CPU limit. DuckDB's default comes from the host's core count (10 on the measured node), and each thread carries its own operator state. |
| `temp_directory` | `$TMPDIR/duckdb-spill` | The spill target. Root filesystems here are read-only, so the Job mounts an `emptyDir` at `/tmp`; the engine creates the directory and proves it writable at open, and fails loudly if it cannot. |
| `max_temp_directory_size` | `10GiB` | DuckDB's own default is "90% of available disk space", i.e. the whole node. Bounding the spill keeps the fix for one outage from causing another. |

The limit is **hard**, but it is not a promise that the build succeeds, and it is
not a promise that RSS stays under it. Measured on the demo-scale input
(20 000 fixture-shaped matches) with the 1 GiB default in force, the build
completes at 2.2 GiB RSS - roughly twice what DuckDB accounts for, because the
buffer manager is not the whole process. The next size up does not spill; it
stops:
`Out of Memory Error: failed to allocate data of size 16.0 MiB (1009.8 MiB/1.0 GiB used)`
at 50 000 matches, and
`Out of Memory Error: failed to allocate data of size 16.0 MiB (1009.3 MiB/1.0 GiB used)`
at 100 000, with the spill directory staying empty each time. Both outcomes keep
the pod alive and publish nothing half-written, which is what the limit is for;
only the first produces artifacts. A build that stops this way is the signal
that the window or the payloads have outgrown the limit, and
`LOLSTATS_AGG_DUCKDB_MEMORY_LIMIT` is the knob to turn *after* checking the pod
can afford it: at 1500MiB the 100 000-match build does finish, at 4.0 GiB RSS,
i.e. past the Job's 3 Gi limit. Raising the limit therefore needs a pod with the
memory to match, and a window whose payloads fit that pod at all is a question
about how the extract is batched, not about this knob.

Configuration is `LOLSTATS_AGG_DUCKDB_MEMORY_LIMIT`, `..._THREADS`,
`..._TEMP_DIR` and `..._MAX_TEMP_SIZE`, surfaced as `--duckdb-memory-limit`,
`--duckdb-threads`, `--duckdb-temp-dir` and `--duckdb-max-temp-size`. An empty
value (or `0` threads) means the default above; a value that is not a size
DuckDB accepts is refused at open rather than passed through, because the batch
client prints `Parser Error: Memory limit must have a number` for a rejected
`SET` and then runs the rest of the script at the host-sized default (the
`-bail` flag turns that into a non-zero exit as a second line of defence).
`TestEngineBoundsAreInForce` reads the four values back out of DuckDB with
`current_setting(...)`, and `TestDuckDBDefaultsFitInsideThePodLimit` reads the
pod limit out of `deploy/base/jobs/aggregate.yaml`, so lowering the pod limit
without lowering the default fails the suite.

## 2. Extraction: what is read, and from where

The archive layout is frozen in `docs/contracts.md` section 4:

```
<raw root>/riot/match-v5/dt=<YYYY-MM-DD>/part-00001.parquet.zst
```

`internal/aggregate/rawarchive.go` walks it, sorts the parts, sniffs the magic
bytes of every part (a zstd *frame* around parquet and a parquet file with the
zstd *codec* are both accepted) and hands DuckDB a readable path. Partitions
strictly older than the window start are pruned by directory name, which is
exact because a match is never crawled before it was played.

One pass over the staged parts produces five things, in this order:

1. `matches` - the `(match_id, payload)` envelope filtered to the region,
   platform, queue and window;
2. `archive_stats` - the row count of the whole archive and the number of rows
   whose payload is not a match envelope;
3. `features` - one row per participant: `match_id`, `champion_id`, `role`,
   `win`, `item0..item6`, `perks.styles`, `summoner1_id`, `summoner2_id`,
   `opponent_id` (the lane opponent, derived inside the SQL);
4. `bans` - one row per banned champion per match;
5. `patches` - the distinct patches in the window, from `gameVersion`.

The role rule is `COALESCE(NULLIF(teamPosition, ''), individualPosition)`, then
normalised to the five canonical roles (`MIDDLE` -> `MID`, `UTILITY` ->
`SUPPORT`, `ADC` -> `BOTTOM`). A participant whose position normalises to none
of the five, or whose champion id is 0 or missing, is counted in `rejected_rows`
rather than silently attributed to a role.

The rule is deliberately literal, and the live archive contains rows it cannot
classify: Riot reports one participant per remake with `teamPosition: ""` and
`individualPosition: "Invalid"` (its own sentinel, not a spelling this code
invented). Measured over the 2026-09-04..2026-09-17 EUW/420 window, that is 30
of 141 150 participant rows (0.021%); earlier sightings of the same window
ranged from 20 of 105 980 (0.018%) to 23 of 124 530 (0.011%), which is the
archive growing under the crawl rather than the extraction degrading. Such a
row is dropped from every cell - it is never guessed into a role - and the
build continues only up to the allowance
`max(LOLSTATS_AGG_MAX_REJECTED_ROWS, ceil(LOLSTATS_AGG_MAX_REJECTED_RATE *
participant_rows))` (`GateConfig.AllowedRejectedRows`): it is a ceiling on rows
Riot itself leaves position-less, not a licence to publish a build whose
extraction stopped classifying. Both defaults are 0, and `deploy/base/config.yaml`
deploys a floor of 25 and a rate of 0.0008 - 3.8x the worst rate measured and
under a tenth of a percent of any window. The rate is what keeps the floor from
going stale: participant rows per window grew from 105 980 to 141 150 in two
weeks, and an absolute 25 quarantined build run 17 at 30 rejected rows, while
the floor alone still covers a quiet window. A regression, which rejects a large
fraction rather than a handful, trips either input and still stops the build.
Every run logs `rejected_rows`, `participant_rows` and the allowance in force,
so a tolerated rejection is never silent.

Every JSON extraction is wrapped in `CASE WHEN json_valid(payload) ... END`.
Without that guard one unparseable payload aborts the whole DuckDB statement
with an opaque parser error, which would report a corrupt archive as a crash
instead of as `malformed_rows`, and the operator would be looking at the wrong
file. A payload that *does* parse but carries no `info` block is counted as
malformed too: a readable row that is not a match is the corruption a gate has
to catch, because nothing downstream will ever complain about it.

The window is inclusive: `window_end` is the last day, and the start is
`window_end - (window_days - 1)`. `window_end` defaults to the newest partition
date in the archive, never to the wall clock, so an offline rebuild of a
historical window is byte-identical to the one that ran that night.

Both ends are canonical `YYYY-MM-DD` and the window runs forwards. The first
property is enforced by `parseDate`, which rejects anything whose formatted form
differs from its input - so `2026-02-31`, which `time.Parse` would normalise, and
an unpadded `2026-9-01` never reach a SQL literal or a published envelope. The
second is enforced by `verify`, which reports an artifact whose `source_window`
ends before it starts. The writer cannot produce one, because `windowForEnd`
derives the start from a day count; an inverted window only ever appears in a
tree damaged on disk, which is precisely what the verifier is for.

## 3. Cells, rates, and the tier ladder

A **cell** is one `(champion, role)` pair for one patch window. Its sample size
`n` is mandatory: there is no shape in `aggmodel` that carries a rate without
the number of games behind it, and a tally with `n <= 0` is a hard error rather
than a skipped row.

| Field | Definition |
| --- | --- |
| `n` | participant rows for the champion in the role inside the window |
| `wins` | how many of those rows won |
| `win_rate` | `wins / n`, rounded to four decimals |
| `pick_rate` | `n / (2 * matches)`: the share of all team slots in the window filled by the champion |
| `ban_rate` | matches the champion was banned in `/ matches`, counted once per match |
| `tier` | `tierForRate(baseline, wins/n)` (below) |
| `ci95_half_width` | `0.98 / sqrt(n)`, rounded to four decimals |

`baseline` is the window's overall win rate, `sum(n) / total` over every
champion and role - every match has one winner, so it is 0.5 up to the rounding
of the participant count, and it is computed rather than assumed.

A published rate is in `[0, 1]` with **both** ends reachable. `win_rate = 0` is
real data, not a defect: a champion that lost every classified game of a window
has a zero win rate, and `verify` accepts it. Anything stricter than `wins / n`
is a rule the writer cannot satisfy, and a verifier that rejects a correct tree
is worse than no verifier at all - which is exactly why
`TestVerifyAcceptsTheFixtureBuild` runs the verifier over what the writer
produced before it checks that each rule rejects a damaged copy.

Rounding happens at cell construction, not at render time, so a rerun over the
same archive produces byte-identical artifacts.

### Tier scoring

A cell is graded on `(win_rate - baseline) * 100` percentage points, using the
**unrounded** rate, against a fixed ladder:

| Tier | Lower bound (percentage points over baseline) |
| --- | --- |
| `S+` | `>= +2.0` |
| `S` | `>= +1.2` |
| `A` | `>= +0.6` |
| `B` | `>= -0.6` |
| `C` | `>= -1.5` |
| `D` | anything below `-1.5` |

The function is total: the last band is unbounded below, so every cell gets a
grade. Grading against the window baseline rather than a fixed 50 percent is
what makes the ladder say something - a fixed target would put half the
champions "above average" through no merit of their own, and a patch where top
lane is weak would be invisible. The bands are wide relative to the confidence
half width at the default `min_cell_n` of 100 (0.98/sqrt(100) = 0.098, i.e. 9.8
percentage points at the worst-case rate), so a tier is a statement about a
champion's position in a patch window and **not** a significance claim about
its win rate. `ci95_half_width` travels with every cell so a reader can see how
much of a 0.6 point step is inside the noise.

## 4. Suppression

`min_cell_n` (default 100, `--min-cell-n`) is the confidence floor.

- A tier-list cell with `n < min_cell_n` is **suppressed**: it is counted in
  `suppressed_cells` on the envelope, the partition and the audit row, and it is
  never written to an artifact.
- A matchup pair with `n < min_cell_n` is dropped from the matrix. The
  champions still appear on the matrix axes, because a heatmap whose order
  changed between builds would be unreadable.
- A champion whose every cell was suppressed still gets a
  `champions/<id>.json`, with an empty `roles` list. A page that 404s because a
  champion was merely rare is a worse answer than a page that says the sample
  is too thin.
- Build rows (items, runes, spells) are **not** filtered by `min_cell_n` in v1.
  They are rankings of observed item sets rather than cells, and each row
  carries its own `n` next to its `win_rate`, so nothing is published without a
  sample size attached. See the backlog in section 11.

The suppression boundary is exact and tested at both sides: `n == min_cell_n`
publishes, `n == min_cell_n - 1` suppresses.

## 5. The published paths

Everything under `agg/v1/` is written by this build; nothing else writes there.
The paths are frozen in `docs/contracts.md` section 4.

| Path | Content |
| --- | --- |
| `manifest.json` | `{schema, source, generated_at, latest, partitions[]}`; `partitions` lists every published partition with its window, `min_cell_n`, suppressed count, published count, build run id and git sha, plus the champion ids to prerender |
| `p/<patch>/<region>/<queue>/<bracket>/tierlist.json` | the graded cells for the partition |
| `p/.../champions/<champion_id>.json` | one champion, with one entry per role it played |
| `p/.../matchups/<role>.json` | one matrix per role, five files, empty matrices included |

`bracket` is `all` in v1: nothing in the ingest can attribute a rank to a
player, so a bracket facet would be a lie dressed as a filter. The path element
exists so that adding a real bracket later is a change of content rather than a
change of layout. `queue` is the queue id (`420` for ranked solo).

`manifest.json` is the index, and it is written last. A reader that finds a
partition in the manifest can rely on the partition directory being complete.

## 6. Gates: fail closed

A build either publishes a complete, reconciled artifact set or changes
nothing. The gates run in this order, and each one has a sentinel error so the
audit row and the log line name the cause.

| Gate | Fails when | Sentinel |
| --- | --- | --- |
| archive readable | the archive holds no match envelope at all | `ErrArchiveEmpty` |
| archive parseable | a row is not a match envelope: unparseable JSON, or a document with no `info` block | `ErrMalformedArchive` |
| window non-empty | the window selected no match, or no row carries a patch | `ErrEmptyWindow` |
| patch well formed | a `gameVersion` in the window is not `major.minor` | `ErrMalformedArchive` |
| input sufficient | `matches_used == 0` or `classified_rows == 0` | `ErrEmptyWindow` |
| rejection rate | `rejected_rows` exceeds `GateConfig.AllowedRejectedRows(participant_rows)` | `ErrRejectedRows` |
| reconciliation | `abs(sum(cell n) - classified_rows) > GateConfig.ReconcileTolerance` | `ErrReconciliation` |
| something to say | `cells_published == 0` | `ErrNoPublishedCells` |
| confidence majority | `cells_published / cells_total < GateConfig.MinConfidentShare` | `ErrSuppressionMajority` |

Two of these deserve their reasoning spelled out.

**The rejection rate** is the one gate with an allowance rather than a fixed
threshold, and it is `AllowedRejectedRows(participant_rows) =
max(GateConfig.MaxRejectedRows, ceil(GateConfig.MaxRejectedRate *
participant_rows))`. The two inputs are `LOLSTATS_AGG_MAX_REJECTED_ROWS`
(`--max-rejected-rows`, default 0) and `LOLSTATS_AGG_MAX_REJECTED_RATE`
(`--max-rejected-rate`, default 0, deployed 0.0008), and either alone leaves the
gate exactly as fail-closed as it was. It exists because a handful of the rows
Riot reports as position-less are in the archive as shipped (section 2), and a
gate that refuses those refuses to publish at all; the allowance is set to a
measured multiple of them, not to a number large enough to hide a defect. The
floor answers "how many position-less rows does a normal quiet window carry",
which does not change; the rate answers "how many does this window carry", which
tracks the archive as the crawl grows. A rejected row still never reaches a
cell, and it is excluded from `classified_rows` (on both sides of the
reconciliation check), so tolerating it cannot flatter a rate.

**Reconciliation** is the check that the cells add up to the rows they came
from. `sum(n)` over the published cells plus the suppressed cells must equal the
number of participant rows the extraction classified, within
`GateConfig.ReconcileTolerance` (default 0). It is the only check that would
catch an extraction bug that
silently dropped rows without dropping a whole column, and it is why the
extraction reports both a row count and a classified count.

**The confidence majority** is what turns "some cells are thin" into "this
build has little to say". If most cells fall below the floor, the window is too
small, or the extraction is mis-attributing roles, and the honest move is to
leave the previous artifacts live. The threshold is
`GateConfig.MinConfidentShare`, and it is calibrated rather than assumed,
because a real window always carries a long tail of one-off champion/role pairs
(a champion played once, off-role, in two weeks) and the share of cells that
clears `min_cell_n` therefore measures how deeply the patch the window ends on
has been crawled, not how healthy the pipeline is. Measured on the live EUW/420
archive at `min_cell_n=100`: 19.3% of cells at 34,780 classified rows
(2026-09-04..2026-09-17), 12.1% at 21,620 (the same window cut to its last four
days, when the crawler was already running at its current depth) and 2.1% at
8,770 (patch 16.17 pinned, which the crawler barely covered). Meeting the 0.5
default would need a floor near `min_cell_n=15`, and a cell of 15 games has a
±25 point 95% interval, so the threshold moves instead of the floor:
`GateConfig.MinConfidentShare` is `LOLSTATS_AGG_MIN_CONFIDENT_SHARE`, surfaced
as `--min-confident-share`, with 0.5 as the default and the deployed value
calibrated to the archive (see `deploy/base/config.yaml`, which carries the
measurement and the instruction to raise it as the crawl deepens). Nothing else
relaxes: every published cell still holds at least `min_cell_n` games, the
suppressed cells are counted in the manifest, and a window materially thinner
than the last one still stops the build - the failure names both the share of
cells and the share of classified rows the surviving cells hold, so an operator
sees immediately whether the artifact that was not replaced would have been
more representative than the one that would. `GateConfig` is a library-level
struct - `build` fills it from `DefaultGateConfig` plus the operator's values -
so the gates stay directly testable without a command line in the way.

Every failure path also rolls the staging directory back (section 7) and closes
the audit row with `failed` (or `quarantined` for a data problem), carrying the
sentinel text in `err`.

## 7. Publishing: stage, then rename

```
agg/v1/                            the live tree, served to clients
agg/.staging-<pid>-<nanos>/        the whole new partition set is built here
agg/.trash-<pid>-<nanos>/          displaced directories, deleted on success
```

Both transient directories sit beside `v1`, not inside it, so a crash cannot
leave a partly built or displaced directory under the served path. Publishing is
a directory rename, and `rename(2)` does not cross filesystems, so staging has
to be inside the aggregate root rather than somewhere like `/tmp`.

1. The build renders the complete document set into the staging directory.
   Nothing under the live tree is opened for writing.
2. Each partition directory is moved into place with `rename(2)` on the same
   filesystem. A rename over an existing directory is not atomic, so the
   existing directory is first renamed into the trash and then removed; the
   window between the two is microseconds and the manifest still points at the
   old content because the manifest swaps last.
3. `manifest.json` is written to a temporary file and renamed over the old one -
   one `rename(2)`, so a reader sees either the old manifest or the new one.
4. On any failure the trash is restored and the staging directory is removed.

Consequence: a crashed or failed build cannot leave a half-published
partition. The fail-closed test asserts exactly this - it corrupts the input,
runs the build, and compares the pre-existing artifact directory byte for byte.

`build` refuses to run at all if the aggregate root already looks like demo
output (`refuseDemoTree`): a simulated set that a real build silently overwrote
would be indistinguishable from real data afterwards.

### Permissions

| Path | Directory | File |
| --- | --- | --- |
| the served tree (`agg/v1/**`) | `0o755` | `0o644` |
| staging, trash, raw scratch, `build-runs/` | `0o750` | `0o640` |

The modes are named in `internal/aggregate/perms.go`, which carries the full
argument; the short version is that the writer and the reader are different
uids with different groups. The aggregate job runs as `65532:65532` with
`fsGroup: 65532` (`deploy/base/jobs/aggregate.yaml`), and the serving tier reads
the same tree as the image's distroless nonroot uid, which is the same `65532`
(`deploy/base/web/go-deployment.yaml`). Two other workloads used to read it - the
`site-build` job (`1000:1000`) and the inner Caddy serving `/var/lib/lolstats/agg`
with `file_server` - and both were deleted with the static tier.
The volume is the `nfs-client` StorageClass, and the kubelet cannot chown an NFS
export, so `fsGroup` is not honoured there: the group on disk stays whatever the
writer left and two workloads do not share one. That is why the pre-existing
persistent volumes on that provisioner are mode `0777`, and it is why a
group-only mode (`0o750`/`0o640`) published artifacts that uid 1000 could see the
name of but not open, while the aggregate build itself passed - a failure that
looked like a bug in the site build. The tree is public web content with no
secret in it, so the modes that always work are the ones with the other bits set.
Only the owner can write; nobody else can.

The **root needs the served mode as well as the directories under it**, and it
is the easy one to get wrong: `os.MkdirAll` gives every directory it creates the
mode of the call, so creating a private staging directory inside a root that
does not exist yet creates the root private as a side effect. In deployment the
root is `/var/lib/lolstats/agg` (`deploy/base/config.yaml`), a subdirectory of
the shared volume rather than a mount point, so it really is created by
whichever job touches the volume first. `build`, `demo` and `Publish` therefore
create the root explicitly with the served mode before they create anything
private inside it. `TestDemoPublishesATraversableRoot`,
`TestBuildPublishesATraversableRoot` and `TestPublishCreatesATraversableRoot`
run into a root that does not exist yet and assert that a reader which is not
the owner can traverse and read every entry, and
`TestPublishedModesAreTheDocumentedOnes` pins the modes themselves.

Everything private to the build gets the tight mode instead, because no other
uid has any business there: the staging tree (a half-built partition must not be
readable at a guessable path), the trash directory (a displaced artifact is
still on disk for the moment the rename takes, and a reader should reach the
live tree or nothing, never the previous copy under a temporary name), the
decompressed raw-archive scratch (which lives at `<staging>/.scratch` and is
removed with the staging tree), and the on-disk `build-runs/` breadcrumbs, which
live beside the aggregate root and are not under any path the serving tier
reads.

## 8. The audit row

Every run that gets far enough to know its partition opens one `build_runs` row
through the frozen `contract.Store` interface and closes it exactly once, with
`defer`-style discipline so a panic or a gate failure still closes it.

| Column | Value |
| --- | --- |
| `patch`, `region`, `queue`, `bracket` | the partition the run targeted |
| `started_at`, `finished_at` | wall clock, from the injected `Now` |
| `git_sha` | `GIT_SHA` or `LOLSTATS_GIT_SHA`, else `unknown` - never a fabricated revision |
| `cells_total`, `cells_published`, `cells_suppressed` | the counters the gates used |
| `artifact_uri` | `s3://lolstats-artifacts/agg/v1/p/<patch>/<region>/<queue>/<bracket>` style locator from `aggmodel.URLPrefix` |
| `status` | `ok`, `failed` or `quarantined`; `err` carries the sentinel text |

The contract's vocabulary is the *build's* verdict; `internal/store` maps it onto
the column's `running | succeeded | failed`, so `quarantined` lands as `failed`
with the reason preserved - a quarantined build is never recorded as success.

Without a `DSN` the build is running outside the stack (a developer machine, an
offline verification) and `FileAuditor` writes `build-run-<nanos>.json` into the
aggregate root instead. That is the honest substitute for a table that is not
there, and it keeps the whole build runnable with no Postgres.

## 9. Demo mode

```
lolstats-aggregate demo --out ./out
```

`demo` writes a complete, deterministic, clearly labelled simulated artifact set
for every frozen path, using a fixed seed (`DemoSeed = 20260917`) and a
generated population of 2000 matches on a fixed patch and window. Two runs in
the same binary produce byte-identical files.

It exists for exactly one reason: no Riot API key is available in the
development and CI environments, and the frontend needs a shape-correct artifact
set to render against. It is made impossible to mistake for real data:

- `manifest.json` and every envelope carry `"source": "demo"`; a real build
  always writes `"source": "riot-match-v5"`;
- the root of the demo tree gets `README-DEMO.txt` with a plain-language
  warning;
- `demo` refuses to write into a directory that holds a real artifact set, and
  `build` refuses to publish over a demo tree.

`source` is an additive field - see
`docs/decisions/ADR-005-demo-data-provenance.md`. No real statistics are ever
fabricated: everything `demo` writes is labelled simulated **inside the
artifact**, not only in its filename.

## 10. Fixtures and tests

There is no real Riot data in this environment, so the build is verified against
a hand-authored archive in `fixtures/agg/` plus hand-computed expectations - see
`fixtures/agg/README.md` for what each file is. The test suite covers:

| Test | What it pins |
| --- | --- |
| `TestBuildAgainstHandComputedFixture` | every number in the fixture: 14 archive rows, 9 matches in the window, 90 participant rows, 13 cells, 11 published, 2 suppressed, `sum(n) = 90` |
| `TestFixtureSuppressionBoundary` | `n = min_cell_n` publishes, `n = min_cell_n - 1` suppresses |
| role fallback | `teamPosition` empty falls back to `individualPosition` |
| patch boundary | the window includes the first and last day and excludes the days outside it |
| fail closed | an archive holding a readable row that is not a match envelope fails with `ErrMalformedArchive` and leaves the previous artifact directory byte-identical |
| reconciliation tolerance | one row of drift fails at tolerance 0 and passes at tolerance 1 |
| demo determinism | two `demo` runs are byte-identical, and every artifact says `source = demo` |
| `TestVerifyAcceptsTheFixtureBuild` | `verify` accepts the tree `build` produced - the writer and the verifier agree on what correct means - and still rejects a copy of it damaged one field at a time (a cell below the floor, a wrong half width, a rate that is not `wins / n`, a missing cell, an unpublished grade, a missing champion document, a manifest that claims the wrong provenance) |
| incremental publish | a second partition is added without disturbing the first |
| `TestManifestReindexRebuildsFromTheTree` | a re-index with the zero partition indexes exactly the partitions the tree holds, never an entry with an empty patch, and carries `latest` to a real one |
| `TestManifestReindexKeepsLiveEntries` | a re-index preserves `build_run_id` and `git_sha`, which only the manifest holds |
| `TestWriteManifestSwapsAtomically` | the manifest swap leaves no temporary name behind and keeps the file readable at the published mode |
| `TestManifestSubcommandWritesTheManifest` | the `manifest` subcommand actually writes `v1/manifest.json` and reports the newest partition |
| `TestManifestSubcommandRefusesAnUnpublishedPartition` | `--patch` naming a partition the tree does not hold fails and leaves the live manifest byte-identical |
| `TestManifestReindexPreservesWhatItDoesNotKnow` | a re-index with no `--generated-at` keeps the generation time the live manifest carries rather than stamping the zero time, and neither invocation path rewrites `min_cell_n`, `cells_published`, `champions` or `matchup_roles` of the entry it is re-indexing |
| `TestParseDateIsCanonical` | every date in a window or a partition prefix is `YYYY-MM-DD` and round-trips unchanged, including the days `time.Parse` would silently normalise |
| `TestWindowForEndIsInclusiveAndBounded` | `window_end - (window_days - 1)`, so the window spans exactly the days asked for across month and year boundaries, and refuses a zero, negative or implausibly long span |
| `TestVerifyRejectsAnInvertedWindow` | a tree whose `source_window` ends before it starts fails, with the manifest damaged alongside the envelope so the ordering rule is the only thing left to catch it |
| `TestDemoPublishesATraversableRoot`, `TestBuildPublishesATraversableRoot`, `TestPublishCreatesATraversableRoot` | a build into a root that does not exist yet leaves every directory traversable and every file readable by a uid that is not the owner - the property the `nfs-client` volume with no honoured `fsGroup` depends on |
| `TestPublishedModesAreTheDocumentedOnes` | every directory in a published tree is the served directory mode and every file the served file mode |
| `TestFileAuditorKeepsItsParentServed` | the breadcrumb directory is private while the directory holding it - which also holds the served tree - stays traversable |

All of them are table-driven where a table is natural, and the suite is run with
`-race`. The window helpers used to carry two predicates no caller used - one
that validated a window and one that asked whether it contained a date. Both were
deleted rather than silenced: the containment question moved into the SQL the
engine runs, and the validation is a property of construction, not something a
caller asserts afterwards. The guarantees they were reaching for are pinned by
`TestParseDateIsCanonical` and `TestWindowForEndIsInclusiveAndBounded`, and the
one that was genuinely missing - a window that ends before it starts - is now
enforced by the verifier and pinned by `TestVerifyRejectsAnInvertedWindow`.

Nine of the tests in the package - `TestBuildAgainstHandComputedFixture`,
`TestFixtureSuppressionBoundary`, `TestFixturePatchAndWindowBoundaries`,
`TestBuildFailsClosedOnBadInput`, `TestBuildIsIncrementalAndKeepsOlderPartitions`,
`TestBuildReconciliationGateIsExactAndBounded`, `TestVerifyAcceptsTheFixtureBuild`,
`TestBuildPublishesATraversableRoot` and `TestFixtureRoleFallbackOnTheDetectedField`
- drive the real DuckDB CLI over the fixture archive, so they resolve the engine
first and call `t.Skipf` when they cannot. That is deliberate: a contributor without the client should not see
spurious failures. It also means the package reports `ok` while having proved
nothing about the aggregation path, which is why CI runs `make test-build` rather
than `make test` (section 12).

`cmd/lolstats-aggregate` is tested too, and those tests need neither the engine
nor a database: they drive whole invocations through `runEnv` with an injected
environment. That is where the flag layer is pinned - that `manifest` writes what
it reports, that it refuses a partition the tree does not hold, that a re-index
does not blank the entry it is re-indexing, and that `usage` documents every
subcommand.

## 11. Running it

```
# the engine first: see section 1
export LOLSTATS_DUCKDB_BIN="$HOME/.local/duckdb/duckdb"

# build one partition from a raw archive
lolstats-aggregate build \
  --raw ./raw --agg ./agg \
  --region EUW --queue 420 --bracket all \
  --window-end 2026-09-14 --window-days 14 --min-cell-n 100

# check what is live without rebuilding anything
lolstats-aggregate verify --agg ./agg --strict --max-age 48h

# check a tree, including a demo tree, against the schema gen-types emits
go run ./cmd/gen-types -out ./gen
lolstats-aggregate verify --agg ./out --schema ./gen/agg.schema.json --source demo

# a deterministic simulated set for frontend work
lolstats-aggregate demo --out ./out

# re-index a tree whose manifest was lost, without rebuilding anything
lolstats-aggregate manifest --agg ./agg

# re-index and say which partition of the newest patch is `latest`
lolstats-aggregate manifest --agg ./agg \
  --patch 16.18 --region EUW --queue 420 --bracket all
```

`build` also serves Prometheus metrics on `--metrics-addr` (disabled when
empty), including the published and suppressed cell counts of the last run.

### Re-indexing a tree

`manifest` exists because the manifest is derived data: it can always be
recomputed from the artifacts, and the recovery path for a manifest lost to a
crash should not be a full rebuild. It reads the tree, merges what the manifest
on disk already knew, and swaps the result into `v1/manifest.json` the same way
a build does - one `rename(2)`, never a write in place.

Four rules keep the re-index from publishing something that is not true:

- **The tree decides which partitions exist.** Every partition directory under
  `v1/p/` becomes an entry, and an entry nothing backs is dropped rather than
  carried. A re-index with no `--patch` therefore merges no partition of its own
  - the zero partition would otherwise appear as a ghost entry with an empty
  patch and take `latest` with it, sending every consumer to a patch that does
  not exist.
- **`--patch` must name a partition that is published.** A manifest entry
  promises the directory behind it is complete, so naming a partition the tree
  does not hold fails before the swap and leaves the live manifest untouched.
- **A re-index never rewrites a fact it did not measure.** It publishes no
  partition, so it hands `UpdateManifest` the zero partition and the entry
  already on disk - or, failing that, the one derived from `tierlist.json` -
  stays the authority for `generated_at`, `min_cell_n`, `cells_published`,
  `suppressed_cells`, `champions` and `matchup_roles`. Passing the `--patch`
  segment flags through as a partition instead would let four path elements
  outrank a measured entry and blank the rest of it, which is a manifest that
  under-reports a partition the site then cannot render. `--patch` therefore
  only selects which partition wins `latest`.
- **`generated_at` is preserved, not restamped.** With no `--generated-at`, a
  re-index keeps the timestamp the live manifest carries and stamps "now" only
  when there was no manifest to read. A re-index changes no number, so claiming
  a new generation time would be a false provenance record - and the zero time,
  which is what an unset flag used to produce, is worse still: it reads as a
  build from year 1.

What a re-index cannot recover is build bookkeeping: `build_run_id` and
`git_sha` live only in the manifest, because no artifact carries them and
inventing them from a file's contents would put a wrong row id in the site's
metadata. They come back as `0` and an empty string for a re-derived entry, and
an entry that was already in the manifest keeps the values it had.

### Backlog

- **Grade on a shrunk estimate.** `ci95_half_width` is published but the tier
  is not derived from it. Grading `(win_rate - baseline)` after shrinking
  towards the baseline by the half width would stop a 30-game wonder from
  outranking a 30,000-game staple. That is a scoring change, so it belongs in a
  patch where the frontend can be diffed against the old ladder.
- **Suppress build rows below `min_cell_n`** the way matchup pairs already are,
  if review shows rare item sets with a loud win rate are being read as advice.
- **A real rank bracket.** The path element is in place; attributing a rank to
  a player needs the ranked-flex or league-expansion source, which is a plan
  section 8 question rather than an aggregation one.

## 12. Verifying a change to this build

```
make vet
make build
make lint
make test
make test-race
make test-build   # the strict one: installs the engine and refuses a skip
go test ./cmd/lolstats-aggregate/... -race -count=1
```

`make test` and `make test-race` are not proof that this build works. The
end-to-end tests in section 10 need the engine, and they *skip* rather than fail
when they cannot resolve it, so an ordinary suite run is green on a machine with
no DuckDB at all - the run reports `ok` without having executed a single build
against the fixture archive. `make test-build` is the target that cannot pass
that way: it depends on `duckdb`, points `LOLSTATS_DUCKDB_BIN` at the absolute
path of the binary it just installed, runs `./internal/aggregate/...` under
`-race`, and then asserts on the captured output that
`TestBuildAgainstHandComputedFixture` reported `PASS` and that nothing in the
package reported `SKIP`. Either failure exits non-zero and prints the offending
lines; the log is kept at `bin/test-build.log`. CI runs it in the `verify` job
and `build-and-push` needs `verify`, so a run in which these tests did not
execute cannot publish an image.

Against an engine you already have, the same thing by hand - read the output for
absent `--- SKIP:` lines rather than trusting the exit status:

```
LOLSTATS_DUCKDB_BIN=/path/to/duckdb go test ./internal/aggregate/... -race -count=1 -v
```

`internal/aggregate` never needs a database or the network: the engine is
injected as an interface, `Now` is injected, and the audit sink falls back to a
file. That is what makes the whole build verifiable offline against
`fixtures/agg/`, and it is why the DuckDB client is the only external thing a
test has to find.
