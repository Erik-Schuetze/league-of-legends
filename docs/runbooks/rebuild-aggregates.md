# Runbook: rebuild the aggregates (and the site from them)

Covers the derived trees on the `lolstats-data` volume: `LOLSTATS_AGG_ROOT`
(`/var/lib/lolstats/agg`) and, because a rebuild is not finished until the site
serves it, `site/`. The control plane has its own runbook
(`restore-postgres.md`) and the archive has its own (`restore-raw.md`).

The chain is three steps and no workflow engine:

| when | job | reads | writes |
| --- | --- | --- | --- |
| 01:00 | `lolstats-aggregate` (`lolstats-aggregate build`) | `raw/` | `agg/v1/**`, `agg/v1/manifest.json` |
| 03:40 | `site-build` (`npm run build`) | `agg/` | `site/` |
| always | `lolstats-web` (Caddy) | `site/` | - |

The ordering is expressed as ordering in the night, not as a dependency: the
aggregate job is bounded by `activeDeadlineSeconds: 7200`, well below 03:40.

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
partition. The tier serves that patch's own bytes again within one cache key, and
the patch you repointed away from stays on disk and stays addressable at
`/patch/<it>/...` for inspection.

What it cannot do is *remove* a partition. The manifest is a union of the disk
manifest, a scan of the tree and the current build, so an entry that is already
listed survives every re-index; a stale partition can only be repointed away from
or overwritten in place. And a partition that is deleted while the manifest still
advertises it makes the tier **fail closed**: every page route and `/readyz`
answer `503` with `data-fault="artifact"` rather than serving the previous
patch's numbers under the new one's label. Both behaviours are measured - with
the commands, the timings and the served responses - in
`docs/PATCH-ROLLOVER-EVIDENCE.md` (§10, §16.2), and
`scripts/verify-patch-rollover.sh` reproduces the whole transition, including
both, in a namespace of its own.

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

## Then rebuild the site

The aggregate tree is not served; `site/` is. If the aggregate job was the only
thing that ran, the web tier is still serving the previous render.

```
kubectl -n lolstats create job site-build-manual --from=cronjob/site-build
kubectl -n lolstats logs -f job/site-build-manual
```

`site-build` renders into `/site/dist`, copies that to
`/var/lib/lolstats/.site-staging`, moves the current `site/` aside to
`.site-previous`, and only then renames the new tree in. A request can never land
in a half-rendered tree, and a failed build leaves the previous one serving - so
running this twice is harmless and interrupting it is survivable.

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

# 3. the site
curl -sSI https://lol.erik-schuetze.dev/ | head -1
#    ...and then read the pages, not just the status line: the web tier caches
#    its own responses, and a damaged cache entry answers 200 with a short body.
#    See docs/runbooks/site-integrity.md.
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
- **Site rendered from a bad tree.** Re-run `site-build` after the aggregate tree
  is right. There is no previous site to restore: `site-build` deletes
  `.site-previous` at the end of a successful publish.

## Debris

A build that was killed between its two renames can leave `.staging-<pid>-<nanos>`
or `.trash-<pid>-<nanos>` under `/var/lib/lolstats/agg`. Both sit **beside** `v1/`,
never under it, so they are never served by Caddy and cannot break the site. They
are safe to delete once `kubectl -n lolstats get jobs -l
app.kubernetes.io/component=aggregate` shows nothing running - and a leftover
trash directory is the fingerprint of a build that died mid-publish, which is
worth a line in the incident notes.

## What is destructive here

- `rm -rf /var/lib/lolstats/agg/v1/...` deletes published pages. Until the next
  build, that path 404s; the raw archive is untouched, so it is recoverable by
  re-running, at the cost of a full pass.
- `rm -rf /var/lib/lolstats/site` makes the site a 404 - the web tier serves
  whatever is at that path. `site-build` recreates it; nothing else does.
- Deleting a running Job (`kubectl -n lolstats delete job aggregate-manual`)
  SIGKILLs the build mid-pass. Survivable thanks to the staging/trash discipline,
  but it wastes the work and leaves debris.
- This runbook never touches the raw archive or the control plane: a rebuild that
  is failing because of missing inputs is an `ingest-down.md` or
  `restore-postgres.md` problem, and re-running the build harder will not fix it.
