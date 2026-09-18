# Runbook: rebuild the aggregates

Covers the two derived trees on the `lolstats-data` volume: `LOLSTATS_AGG_ROOT`
(`/var/lib/lolstats/agg`), the nightly tier list, and `LOLSTATS_AGG_DATASET_ROOT`
(`/var/lib/lolstats/datasets`), the timeline feature dataset. Since the web tier
was retired on 2026-09-18 (`docs/decisions/ADR-011-retire-the-web-tier.md`)
nothing in this repository reads either one - the published tree is the end of
the pipeline. The control plane has its own runbook (`restore-postgres.md`) and
the archive has its own (`restore-raw.md`).

The chain has no workflow engine in it - every step is a command, on a schedule
or by hand:

| when | job | reads | writes |
| --- | --- | --- | --- |
| 01:00 | `lolstats-aggregate` (`lolstats-aggregate build`) | `raw/` | `agg/v1/**`, `agg/v1/manifest.json` |
| by hand | `lolstats-aggregate features` | `raw/riot/match-v5/`, `raw/riot/match-v5-timeline/` | `datasets/timeline-v1/**` |

Both rows publish transactionally and both leave the previous tree live when they
fail, but they are otherwise independent on purpose: the first publishes a frozen
reader contract, the second an exploratory dataset with no such promise, and a
failed run of either leaves the other alone. The second tree is the one
`docs/decisions/ADR-012-ingest-match-timelines.md` decided; it has its own
section at the end of this file, and everything before that section is about
`agg/v1` alone and is unchanged by the dataset's existence.

Nothing renders ahead of anything, because nothing renders. A reader of the tree
resolves the manifest and the partition it names at the moment it reads, so the
number it sees is the number the last successful build published, and a build that
fails to publish changes nothing.

## When to use it

- `LolstatsBuildJobFailed` or `LolstatsBuildNotScheduled` fired.
- A consumer is showing yesterday's numbers, or a patch that exists in the
  archive has no artifacts.
- You restored the raw archive (`restore-raw.md`). Nothing derived from it is
  valid any more.
- You changed a build input (`LOLSTATS_AGG_PATCH`, `LOLSTATS_AGG_QUEUE_ID`,
  `LOLSTATS_AGG_BRACKET`, the source window or the minimum cell size).
- A new patch appeared and you want it published without waiting for 01:00.

## What a build does

`lolstats-aggregate build` is a single DuckDB pass over the raw archive, followed
by several independent assembles: one partition per
`v1/p/<patch>/<region>/<queue>/<bracket>`, plus the manifest that lists them.
Everything comes from configuration, not from flags in the manifest - `build` is
called with no arguments at all, and the patch (`LOLSTATS_AGG_PATCH`, empty means
"the newest patch in the archive"), region, queue (default `420`), bracket
(default `all`), window (default 14 days) and `min-cell-n` (default 100) come from
`lolstats-config`. Cells with fewer than `min-cell-n` observations are suppressed
and counted, never published.

Publishing is transactional, and this is the property that makes a re-run the
only repair tool you need:

- the build writes into `agg/.staging-<pid>-<nanos>/`, which is removed on every
  exit path, success included;
- a partition is swapped in by `rename(2)` from a directory that is already
  complete, and the live directory it displaces is held in `.trash-<pid>-<nanos>`
  until the whole publish succeeds;
- `manifest.json` is swapped in **last**, so no reader is ever pointed at a
  partition whose files are not in place yet;
- any error after the first swap restores everything it displaced before
  returning. A failed build leaves the tree exactly as it was.

There is therefore no rollback for a build: a build that failed has already been
rolled back, and a build that succeeded and published wrong numbers is fixed by
re-running it with better inputs.

### Repointing `latest` at the previous patch

There is one revert that is not a re-run: `lolstats-aggregate manifest --agg
/var/lib/lolstats/agg --source riot-match-v5 --patch <old-patch>` re-derives the
manifest from the tree and repoints `latest` at the patch you name, rewriting no
partition. A reader that follows the manifest resolves that patch's own bytes
again, and the patch you repointed away from stays on disk and stays addressable
at `/patch/<it>/...` for inspection.

What it cannot do is *remove* a partition. The manifest is a union of the disk
manifest, a scan of the tree and the current build, so an entry that is already
listed survives every re-index; a stale partition can only be repointed away from
or overwritten in place. And a partition that is deleted while the manifest still
advertises it makes a **fail-closed** reader mandatory rather than optional: a
reader must present an error, never the previous patch's numbers under the new
one's label. The first is a property of `internal/aggregate`'s index. The second
was asserted by `TestMissingArtifactIs503WithAPage` in `internal/webtier/server_test.go`
until that package was deleted on 2026-09-18, so it is now a requirement written
in `docs/contracts.md` section 4.4 with no test behind it - `docs/compliance.md`
records the gap.

## Re-run the build

Check first: `concurrencyPolicy: Forbid` stops the CronJob overlapping *itself*,
but a Job created by hand runs alongside a nightly one, and two builds publishing
into the same aggregate root interleave their swaps.

```
kubectl -n lolstats get jobs -l app.kubernetes.io/component=aggregate
kubectl -n lolstats create job aggregate-manual --from=cronjob/lolstats-aggregate
kubectl -n lolstats logs -f job/aggregate-manual
```

The manual Job inherits the template it was copied from: same image, same
`args: ["build"]`, same `activeDeadlineSeconds: 7200`, same resource limits, same
`ttlSecondsAfterFinished: 86400`, and the same node affinity that keeps it off
`vega`. It is **not** tracked by ArgoCD - `prune: true` will not remove it - so
delete it when you are done, or let the TTL do it.

Its log is the build's own output. The numbers it reports are also written to the
`build_runs` table, which is what the alerts and the verification below read.

## Then check the tree

There is nothing to rebuild after the aggregate job and no renderer to warm: the
tree the job published is the deliverable. Check the artifact rather than a page.

```
kubectl -n lolstats exec statefulset/lolstats-postgres -- ls -l /var/lib/lolstats/agg/v1
```

Read the path the manifest names, and confirm the partition directory holds the
cells and the labelling the build reported. Two things used to be checked here and
cannot be any more, both because the tooling was deleted on 2026-09-18 with the
web tier:

- **that the tree is being served.** A serving script used to fetch each route
  over HTTPS and fail on a `200` that hid a body not ending in `</html>` or a page
  missing the labelling its own data state declared. Nothing of this project
  answers a request now, so there is no page to fetch and no script to run.
- **that a publish is visible.** A refresh was bounded by the reader's own cache
  (`max-age=60` with an `ETag` revalidation), so a publish showed up within one
  `max-age` window. That is now a requirement on a future reader rather than a
  property of anything running: `docs/contracts.md` section 4.4.

What is left is the tree's own self-check, in "How to tell it worked" below.

## How to tell it worked

```
# 1. the build ledger. One row per run, newest last; `status` is
#    running|succeeded|failed and `error` is only set on failure.
kubectl -n lolstats exec statefulset/lolstats-postgres -- psql -U lolstats -d lolstats -c "
  select id, status, patch, region, queue, bracket, cells_total, cells_published,
         cells_suppressed, git_sha, started_at, finished_at, error
    from build_runs order by id desc limit 5"

# 2. the tree itself, against its own schema and manifest
kubectl -n lolstats apply -f aggregate-verify.job.yaml   # template below
kubectl -n lolstats logs job/aggregate-verify

# 3. the tree on disk (see "Then check the tree" above)
kubectl -n lolstats exec statefulset/lolstats-postgres -- ls -l /var/lib/lolstats/agg/v1
```

`cells_suppressed` is normal and not a fault: it is the count of cells too thin to
publish. `cells_published + cells_suppressed <= cells_total` is enforced by a
check constraint on the table, so a row that violates it cannot exist.

`lolstats-aggregate verify` is not something any CronJob runs, so it needs a Job
written out and applied, exactly like `migrate up` in `restore-postgres.md`:

```yaml
# aggregate-verify.job.yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: aggregate-verify
  namespace: lolstats
  labels:
    app.kubernetes.io/name: lolstats
spec:
  backoffLimit: 0
  ttlSecondsAfterFinished: 86400
  template:
    metadata:
      labels:
        app.kubernetes.io/name: lolstats
    spec:
      restartPolicy: Never
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: verify
          image: ghcr.io/erik-schuetze/league-of-legends:latest
          command: ["/lolstats-aggregate"]
          # No --region: verification covers every region in the tree by default.
          args: ["verify", "--agg", "/var/lib/lolstats/agg"]
          envFrom:
            - configMapRef:
                name: lolstats-config
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
            readOnlyRootFilesystem: true
          volumeMounts:
            - name: data
              mountPath: /var/lib/lolstats
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: lolstats-data
        - name: tmp
          emptyDir: {}
```

`verify` fails on a partition that does not match the schema, on a document that
does not validate, and on a manifest that disagrees with the tree. Adding
`--strict --max-age 48h` asks a different question - "is the tree fresh" - and is
*expected* to fail while the thing you are repairing is a stale tree. Use plain
`verify` to answer "is what is there correct".

## Roll back

- **Build that failed.** Nothing to do: it rolled itself back. Confirm with
  `kubectl -n lolstats logs job/<name>` that the last line is a publish failure
  and not a partial swap.
- **Build that succeeded with wrong inputs.** Re-run it with the inputs fixed -
  `selfHeal` reverts an out-of-band `kubectl patch configmap lolstats-config`
  within minutes, so a changed `LOLSTATS_AGG_PATCH` belongs in
  `deploy/base/config.yaml` (owned by the deployment workstream) or the run will
  silently use the old value. A build is deterministic given its inputs and the
  archive, so the second run produces the tree the first one should have.
- **A tree that was published and then damaged.** Nothing to roll back to: a
  reader gets whatever is on disk, so a partition deleted or truncated under `v1/`
  is a fault - the requirement is an error rather than the previous patch's
  numbers under the new one's label (`docs/contracts.md` section 4.4). Re-run the
  build.

## Debris

A build that was killed between its two renames can leave `.staging-<pid>-<nanos>`
or `.trash-<pid>-<nanos>` under `/var/lib/lolstats/agg`. Both sit **beside** `v1/`,
never under it, so nothing resolves a published path into them and they cannot
break a reader. They
are safe to delete once `kubectl -n lolstats get jobs -l
app.kubernetes.io/component=aggregate` shows nothing running - and a leftover
trash directory is the fingerprint of a build that died mid-publish, which is
worth a line in the incident notes.

## What is destructive here

- `rm -rf /var/lib/lolstats/agg/v1/...` deletes published artifacts. A conforming
  reader answers an error rather than a `404` and never the previous patch's
  numbers; the raw archive is untouched, so it is recoverable by re-running, at
  the cost of a full pass.
- Deleting a running Job (`kubectl -n lolstats delete job aggregate-manual`)
  SIGKILLs the build mid-pass. Survivable thanks to the staging/trash discipline,
  but it wastes the work and leaves debris.
- This runbook never touches the raw archive or the control plane: a rebuild that
  is failing because of missing inputs is an `ingest-down.md` or
  `restore-postgres.md` problem, and re-running the build harder will not fix it.

## The second tree: the timeline feature dataset

Everything above this heading is about `agg/v1`. This section is about the tree
`docs/decisions/ADR-012-ingest-match-timelines.md` added beside it: match
timelines are archived as a second raw payload (`raw/riot/match-v5-timeline/`,
next to the summaries in `raw/riot/match-v5/`) and a separate Parquet dataset is
derived from both. It follows the same shape as the sections above - when to use
it, what a build does, how to re-run it, what to check, how to tell it worked,
how to roll it back, what debris it leaves, what is destructive about it - and it
shares nothing else: it has no `build_runs` ledger, no `verify` counterpart and no
manifest repoint.

### When to use it

- The timeline archive has grown. `backfill-timelines` enqueues a bounded sample
  weekly and the worker fetches it afterwards, so the dataset is rebuilt when
  there is new material rather than on a timer.
- A run failed on one of the gates in "What a build does" below, and the question
  is whether the archive or the build is at fault.
- You changed a build input: the dataset root (`LOLSTATS_AGG_DATASET_ROOT`), the
  duration floor (`LOLSTATS_AGG_FEATURE_MIN_DURATION_S`, 600 by default) or the
  DuckDB bounds in `deploy/base/config.yaml`.
- You restored the raw archive (`restore-raw.md`). Nothing derived from a restored
  archive is valid any more, and the dataset is derived from both archives.
- You want the exclusion ledger re-derived. Riot defects change what the archive
  holds - aborted games, games whose frames are missing under ten minutes - and
  `match_index.exclusion_reason` is where each of them is recorded.

**Nothing schedules this build, and that is the decision, not an omission.** The
nightly job publishes `agg/v1` alone, so a bad timeline extract cannot fail the
tier list; the dataset is built when its inputs have moved and read by hand. See
ADR-012 for the tradeoff.

### What a build does

`lolstats-aggregate features` is a single DuckDB pass over **both** raw archives,
joined on match id. It publishes six tables - each a directory of Parquet parts,
not one file - plus the three documents that describe them, under
`<LOLSTATS_AGG_DATASET_ROOT>/timeline-v1` (by default
`/var/lib/lolstats/datasets/timeline-v1`):

| table | one row per | what it carries |
| --- | --- | --- |
| `match_index` | match | the scope columns and the `exclusion_reason` ledger |
| `participant_minutes` | participant-minute | the per-minute series |
| `events` | event | the payload's events, as read |
| `lane_matchups` | matchup | the two lane opponents, ten rows per five-a-side game |
| `participant_early` | participant | the early-game summary |
| `match_objectives` | objective | one row per objective take |

Publishing is the same discipline as the nightly build - staging directory, then
`rename(2)`, then the documents, then `manifest.json` last - so the same property
follows: a failed run leaves the previous dataset live, and a first-ever run that
fails leaves nothing live at all rather than a partial tree.

**The manifest is a build receipt, not a reader contract**, and it says so in its
own `notice` field. It records what one run read, excluded and wrote - including
the row count it published per table - and promises nothing about its keys to a
future reader. `schema.json` describes the columns, `README.md` carries the
caveats and the query recipes, and the version lives in the directory name
(`timeline-v1`) precisely so that a reshape publishes `timeline-v2` beside it
rather than reinterpreting this one. Do not resolve this tree the way "Repointing
`latest`" above resolves `agg/v1`'s manifest.

Every gate fails closed, and a failure publishes nothing:

- **`ErrNoTimelineArchive`** - the timeline archive holds no payload at all, so
  the backfill has not run yet. It is deliberately distinct from
  `ErrArchiveEmpty`, the *summary* archive being empty: one means the crawler has
  not run, the other that this lane has not been filled in, and the dataset is
  allowed to fail on the second where the tier list must not fail on the first.
- **`ErrFeatureOrphans`** - a timeline whose match has no summary row. A hard
  failure rather than a skip: both archives are written by the same crawler and
  the same queue, so a disagreement about a match means one of them is wrong, and
  dropping it quietly would shrink the dataset by an unknown amount.
- **`ErrFeatureNoEligibleMatches`** - every match in scope was excluded. An empty
  dataset is not published over a good one.
- **`ErrFeatureReconciliation`** - `lane_matchups` is not ten rows per match for a
  five-a-side game. This is the mis-join gate, and a mis-join would publish lane
  differences against the wrong opponent.
- **`ErrFeatureSchemaDrift`** - a built table's columns are not the ones
  `schema.json` describes. The schema is generated from the same specification the
  build is, so drift means the two were edited apart.

### The numbers this build is sized on are estimates

Two figures were adopted as estimates and must not be quoted later as
measurements:

- **the per-timeline payload size** - about 1.1 MB, against about 100 KB for a
  summary, and both are third-party figures rather than Riot documentation. No
  measured per-timeline figure exists yet; `docs/data-sources.md` says the same
  and is where one is recorded once there is one.
- **the extraction batch size** - `featureBatchParts`, four payload-carrying parts
  per statement (`internal/aggregate/features_sql.go`). The constant was
  re-measured for this payload class rather than inherited from the nightly
  extraction, but what it was re-measured against is the estimate above, so it is
  an estimate's estimate until a real batch confirms it.

Measure both on a real bounded run, and take the sample size from a dry run of the
backfill rather than guessing: `lolstats-ingest backfill-timelines -dry-run` prints
the selection without enqueuing, so the size of the sample a measurement will be
taken over is knowable before the rate-limit budget is spent.

```
# Payload bytes and part count. The archive is on the shared claim, and no
# long-lived pod here has a shell in it, so this is a throwaway busybox pod that
# mounts the claim read-only - the recipe deploy/README.md documents. Swap the
# `sh -c` body for whichever of these lines you want:
#
#   kubectl -n lolstats run pvc-measure --rm -it --restart=Never --image=busybox \
#     --overrides='{"spec":{"containers":[{"name":"pvc-measure","image":"busybox","command":["sh","-c","<body>"],"volumeMounts":[{"name":"d","mountPath":"/d","readOnly":true}]}],"volumes":[{"name":"d","persistentVolumeClaim":{"claimName":"lolstats-data"}}]}}'
#
#   # total size in KiB - busybox du has no byte flag - and compressed, because
#   # parts are part-NNNNN.parquet.zst, so this is a floor on what the engine
#   # reads rather than the figure itself
#   du -sk /d/raw/riot/match-v5-timeline
#   # parts, and the four that one batch reads (`featureBatchParts`)
#   find /d/raw/riot/match-v5-timeline -name 'part-*.parquet.zst' | wc -l
#   find /d/raw/riot/match-v5-timeline -name 'part-*.parquet.zst' | sort | head -4 | xargs ls -l

# The payload count that total is divided by is not an estimate - the crawl
# closes a fetch_queue row only after the archive flush, so a `done` row is one
# payload on disk (`internal/crawl/worker.go`) - and it comes from the control
# plane, where psql does exist.
kubectl -n lolstats exec statefulset/lolstats-postgres -- psql -U lolstats -d lolstats -c "
  select kind, status, count(*) from fetch_queue group by 1, 2 order by 1, 2"
```

A measured figure belongs in `docs/data-sources.md`, not here.

### Re-run the build

The verb is not something any CronJob runs, so re-running it in the cluster means
creating a one-off Job from a template that does run an aggregate pass - but the
`lolstats-aggregate` CronJob in `deploy/base/jobs/aggregate.yaml` carries
`args: ["build"]`, and unlike the manual `backfill` instantiation above the
default here is the wrong subcommand rather than an incomplete argument list, so
the args have to be replaced. The `--dry-run=client -o json | python3 | kubectl
create -f -` form documented in `deploy/base/jobs/backfill.yaml` is the idiom:

```
# Check nothing else is publishing this tree first: `concurrencyPolicy: Forbid`
# stops the CronJob overlapping itself, not a hand-created Job overlapping it,
# and two runs exchanging renames under one dataset root interleave their swaps.
kubectl -n lolstats get jobs

kubectl -n lolstats create job features-manual --from=cronjob/lolstats-aggregate \
  --dry-run=client -o json \
| python3 -c 'import json,sys; j=json.load(sys.stdin); j["spec"]["template"]["spec"]["containers"][0]["args"] = ["features"]; json.dump(j,sys.stdout)' \
| kubectl create -f -
kubectl -n lolstats logs -f job/features-manual
```

The manual Job inherits the aggregate template's image, its
`activeDeadlineSeconds: 7200`, its resource limits and the data mount that
carries `/var/lib/lolstats`. It is not tracked by ArgoCD (`prune: true` will not
remove it), so delete it when you are done or let the TTL do it. Note that its
sizing is the nightly build's: five of the six tables are unnested out of payloads
roughly ten times the summaries', and the engine's own memory limit
(`LOLSTATS_AGG_DUCKDB_MEMORY_LIMIT`, `-duckdb-memory-limit`) is what it will
complain about first if the template is too thin. A kill by that limit says so in
the log; raise the flag, keep the Job's own memory limit above it, and re-run.

Over the archive on a workstation, with the pinned engine (`make duckdb`, which
explains in the Makefile why the client is pinned to a version the build will
accept), the same pass is:

```
LOLSTATS_RAW_ROOT=./raw LOLSTATS_AGG_DATASET_ROOT=./data/datasets \
  LOLSTATS_DUCKDB_BIN="$PWD/bin/duckdb" make run-features
```

`-raw` and `-dataset` override those two roots, and `-min-duration` the duration
floor, which is what the sample was chosen by - a dataset built with a different
floor than the backfill used holds matches the crawl did not select.
`-duckdb-allow-mismatch` is the deliberately-awkward escape hatch that exists for
the nightly build's equivalence check and is not needed here.

### Then check the tree

```
kubectl -n lolstats run pvc-ls --rm -it --restart=Never --image=busybox \
  --overrides='{"spec":{"containers":[{"name":"pvc-ls","image":"busybox","command":["ls","-l","/d/datasets/timeline-v1"],"volumeMounts":[{"name":"d","mountPath":"/d","readOnly":true}]}],"volumes":[{"name":"d","persistentVolumeClaim":{"claimName":"lolstats-data"}}]}}'
```

That is the throwaway pod from the estimates section above with `ls` for the
command, for the reason given there: the postgres pod is the control plane and
does not mount this tree. The same tree is readable on the NFS host under
`/nas-main/k3s-volumes`.

The tree is six directories and three documents, and nothing else: a run that
failed mid-publish cannot leave a partial tree here without also leaving its
staging directory in "Debris" below. Read `manifest.json` for the receipt,
`schema.json` for the columns of a table you are about to query, and `README.md`
for what the build is and is not.

### How to tell it worked

```
# 1. the run's own status line - `features: ok dataset=... matches=...
#    with_timeline=... eligible=... excluded=... replaced=...`. A failure prints
#    `features: failed ...` and publishes nothing.
kubectl -n lolstats logs job/features-manual

# 2. the receipt, per table: the row count each table was published with, and the
#    `notice` that says what the document is and is not. Throwaway pod again,
#    with `cat` for the command.
kubectl -n lolstats run pvc-cat --rm -i --restart=Never --image=busybox \
  --overrides='{"spec":{"containers":[{"name":"pvc-cat","image":"busybox","command":["cat","/d/datasets/timeline-v1/manifest.json"],"volumeMounts":[{"name":"d","mountPath":"/d","readOnly":true}]}],"volumes":[{"name":"d","persistentVolumeClaim":{"claimName":"lolstats-data"}}]}}'
```

`match_index` is where a data scientist checks which matches were excluded and
why, and the ledger is a query rather than a document, so it needs the engine:
everything outside the postgres control plane is distroless, and the image's
pinned client is at `/usr/local/bin/duckdb` (`LOLSTATS_DUCKDB_BIN` in the
Dockerfile). The same template as above, with the CLI in place of the aggregate
binary:

```
kubectl -n lolstats create job features-ledger --from=cronjob/lolstats-aggregate \
  --dry-run=client -o json \
| python3 -c 'import json,sys; j=json.load(sys.stdin); c=j["spec"]["template"]["spec"]["containers"][0]; c["command"]=["/usr/local/bin/duckdb"]; c["args"]=["-c","select exclusion_reason, count(*) as matches from read_parquet(\"/var/lib/lolstats/datasets/timeline-v1/match_index/*.parquet\") group by 1 order by 2 desc"]; json.dump(j,sys.stdout)' \
| kubectl create -f -
kubectl -n lolstats logs job/features-ledger
```

The reasons it lists are `no_timeline` (the backfill has not reached this match),
`aborted` (Riot's own flag for a remade or aborted game), `frame_interval_zero`
(the payload's frames carry no usable interval), `too_short` (under the duration
floor), `frames_null` (the timeline is present but empty) and `ok`, the only one
that makes a match eligible. `timeline_eligible` is exactly
`exclusion_reason = 'ok'`, and every other table in the dataset is a subset of
those matches. A large `no_timeline` count is mostly a coverage statement rather
than a fault - it is the backfill still filling in - but not entirely: the
archive holds summaries for two years and timelines for one, so a match older than
that window is permanently unfetchable rather than merely unfetched, and the
dataset's own README says so. `-include-short` is the flag that lets the
`too_short` bucket in if a question needs it.

Finally, check the receipt against the tree: `tables[].rows` in `manifest.json`
is what each table was published with, so a count taken from the Parquet parts
that disagrees with it means the tree on disk is not the tree the receipt
describes.

### Roll back

- **A run that failed.** Nothing to do. Publishing is stage-then-rename with the
  manifest written last, so the failure left the previous dataset live, and the
  only action is to fix the input and re-run.
- **A run that succeeded with the wrong inputs.** Re-run with the inputs fixed.
  The build is deterministic given its inputs and the archive, so the second run
  produces the tree the first one should have.
- **A dataset that was published and then damaged.** Re-run it; there is no older
  copy to fall back to. Unlike `agg/v1`'s manifest, which can be repointed, the
  receipt describes the one tree that is on disk and nothing backs this tree up on
  purpose: the raw archive is the only data here that cannot be regenerated
  (`jobs/backup-archive.yaml`), and this dataset is a pass over it.
- **A dataset built from a restored archive.** Rebuild it. The receipt names the
  source roots it read (`sources`), the part counts (`raw_parts`,
  `timeline_parts`) and when the run happened (`generated_at`), so the fix is a
  run over the restored archive.

### Debris

A run killed between its renames can leave `.staging-<pid>-<nanos>` or
`.trash-<pid>-<nanos>` under `/var/lib/lolstats/datasets`, beside `timeline-v1/`.
Nothing resolves a published path into them, so they cannot make a reader see a
half-written table, and they are safe to delete once the run that made them is
gone: `kubectl -n lolstats get jobs` is the whole check, because only a
hand-created Job ever runs this build and a completed one shows as `Complete`
rather than `Running`. A leftover trash directory is the fingerprint of a run that
died mid-publish.

### What is destructive here

- `rm -rf /var/lib/lolstats/datasets/timeline-v1/...` deletes published tables.
  Recoverable by re-running, at the cost of engine time and no Riot budget: the
  payloads it reads are already in the archive.
- Deleting a running Job SIGKILLs the pass mid-extraction. Survivable thanks to
  the staging/trash discipline, but it wastes the work and leaves debris.
- Pointing `LOLSTATS_AGG_DATASET_ROOT` (or `-dataset`) at the aggregate root, at
  the raw root, or anywhere inside either one publishes `timeline-v1/` into a tree
  with a different contract and a different retention decision. They are separate
  roots on purpose: see the comment on `LOLSTATS_AGG_DATASET_ROOT` in
  `deploy/base/config.yaml`.
- The build writes only under the dataset root. It opens both raw archives
  read-only and never writes to the control plane, so it cannot destroy an input
  or the tier list.
