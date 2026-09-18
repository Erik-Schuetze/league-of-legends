# Runbook: rebuild the aggregates

Covers the derived tree on the `lolstats-data` volume: `LOLSTATS_AGG_ROOT`
(`/var/lib/lolstats/agg`). There is no second tree - the tier renders pages from
this one at request time, so a build that publishes is a build that is served.
The control plane has its own runbook (`restore-postgres.md`) and the archive has
its own (`restore-raw.md`).

The chain is one step and no workflow engine:

| when | job | reads | writes |
| --- | --- | --- | --- |
| 01:00 | `lolstats-aggregate` (`lolstats-aggregate build`) | `raw/` | `agg/v1/**`, `agg/v1/manifest.json` |
| always | `lolstats-web` | `agg/v1` | pages, rendered per request |

Nothing renders ahead of the request. The tier reads the manifest and the
partition it names on each request, so the number a reader sees is the number the
last successful build published, and a build that fails to publish changes
nothing.

## When to use it

- `LolstatsBuildJobFailed` or `LolstatsBuildNotScheduled` fired.
- The site is serving yesterday's numbers, or a patch that exists in the archive
  has no pages.
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
- `manifest.json` is swapped in **last**, so the landing page never advertises a
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

# 3. the site (see "Then check that it is being served" above)
sh scripts/verify-serving.sh https://lol.erik-schuetze.dev
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
- **A tree that was published and then damaged.** Nothing to roll back to: the
  tier renders whatever is on disk, so a partition deleted or truncated under
  `v1/` is served as a fault - a page route and `/readyz` answer `503` with
  `data-fault="artifact"` rather than the previous patch's numbers under the new
  one's label. Re-run the build.

## Debris

A build that was killed between its two renames can leave `.staging-<pid>-<nanos>`
or `.trash-<pid>-<nanos>` under `/var/lib/lolstats/agg`. Both sit **beside** `v1/`,
never under it, so the tier never resolves a path into them and they cannot break
the site. They
are safe to delete once `kubectl -n lolstats get jobs -l
app.kubernetes.io/component=aggregate` shows nothing running - and a leftover
trash directory is the fingerprint of a build that died mid-publish, which is
worth a line in the incident notes.

## What is destructive here

- `rm -rf /var/lib/lolstats/agg/v1/...` deletes published pages. The tier answers
  `503` with a visible error page (and `data-fault="artifact"`), not a `404` and
  not the previous patch's numbers; the raw archive is untouched, so it is
  recoverable by re-running, at the cost of a full pass.
- Deleting a running Job (`kubectl -n lolstats delete job aggregate-manual`)
  SIGKILLs the build mid-pass. Survivable thanks to the staging/trash discipline,
  but it wastes the work and leaves debris.
- This runbook never touches the raw archive or the control plane: a rebuild that
  is failing because of missing inputs is an `ingest-down.md` or
  `restore-postgres.md` problem, and re-running the build harder will not fix it.
