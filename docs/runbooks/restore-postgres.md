# Runbook: restore Postgres

Covers the control-plane database in namespace `lolstats`: the crawl frontier,
the fetch queue, per-match job state, the build manifest and the applied-migration
ledger. It does not cover the raw archive (`restore-raw.md`) or the aggregate and
site trees (`rebuild-aggregates.md`) - those are files on the `lolstats-data`
volume rather than rows, which is the property that lets them be recovered
independently of this database.

The dump is written by the `backup-postgres` CronJob at 04:30 Europe/Berlin into
`/var/lib/lolstats/backups/postgres/` on the `lolstats-data` volume: 7 daily
dumps, plus a Sunday copy in `weekly/`, of which 4 are kept. `LATEST` names the
newest dump. `deploy/README.md`, section Backups, describes what is produced and
what is not covered; this file describes how to use it.

## When to use it

- `lolstats-postgres-0` lost its volume, or the `lolstats-postgres-data` PVC had
  to be recreated.
- The database is logically corrupt: a migration half-applied, a table dropped by
  hand, a `DELETE` that should not have run.
- You are rehearsing. Nothing else in this file is a reason not to rehearse in a
  throwaway database, and the rehearsal is the first thing to do in every case
  below.
- You want a copy of production locally.

## Anatomy of one backup

Everything in `/var/lib/lolstats/backups/postgres/daily/` is one set of three
files and one pointer:

| file | what it is |
| --- | --- |
| `lolstats-<UTC stamp>.dump` | `pg_dump --format=custom --compress=zstd:3`; `pg_restore` can list, restore and selectively restore it |
| `lolstats-<UTC stamp>.dump.manifest` | the row count of every table and an md5 of the schema **as they were when the dump was taken** - this is the baseline a restore has to reproduce |
| `lolstats-<UTC stamp>.dump.globals.sql` | `pg_dumpall --globals-only`: roles. Databases are not in here. If the globals pass failed, the file contains a single comment saying so |
| `../LATEST` | the file name of the newest dump |

A dump is written under a `.partial` name and renamed only after `pg_restore
--list` has read its table of contents back, so a dump that is present is a dump
that parsed. Nothing in the tree is world-readable: the job runs with `umask 077`.

## Reading the backup tree

The Go workloads run a distroless image with no shell, and the Postgres pod does
not mount this volume, so `lolstats-web` is the pod to read it from - its Caddy
container has a shell and mounts `lolstats-data` read-only, so nothing done here
can disturb what it reads:

```
kubectl -n lolstats exec deploy/lolstats-web -- cat /var/lib/lolstats/backups/postgres/LATEST
kubectl -n lolstats exec deploy/lolstats-web -- ls -l /var/lib/lolstats/backups/postgres/daily
kubectl -n lolstats exec deploy/lolstats-web -- cat /var/lib/lolstats/backups/postgres/daily/lolstats-20260917T043012Z.dump.manifest
```

## Rehearse: restore into a throwaway database

Never find out what is in a dump by restoring it over production. There are two
ways to rehearse, and they prove different things:

**The automated one** - `scripts/backup-verify.sh --cluster --yes` takes a fresh
`pg_dump` from the live database, restores it into a database named
`lolstats_verify_<epoch>`, compares table count, a column-definition md5, an
index md5 and every per-table row count, proves the comparison can fail by
deleting a row and expecting a mismatch, and drops the throwaway database. It
answers "does the dump/restore path work here right now", and it is the drill
behind the launch gate. It does not read a backup *file*.

**The manual one** - restores the artifact you are actually about to use. Both
the `pg_dump` client and the server are in the `lolstats-postgres-0` container;
only the dump's bytes have to come from the web pod:

```
# 1. a fresh, empty database to restore into
kubectl -n lolstats exec statefulset/lolstats-postgres -- createdb -U lolstats lolstats_restore_check

# 2. stream the artifact in. pg_restore reads stdin when it is given no file.
kubectl -n lolstats exec deploy/lolstats-web -- cat /var/lib/lolstats/backups/postgres/daily/lolstats-20260917T043012Z.dump \
  | kubectl -n lolstats exec -i statefulset/lolstats-postgres -- \
      pg_restore --no-owner --no-acl --exit-on-error -d lolstats_restore_check -U lolstats

# 3. the counts, in the same shape as the manifest's `# rows per table` section
kubectl -n lolstats exec statefulset/lolstats-postgres -- psql -U lolstats -d lolstats_restore_check -AtF' ' -c "
  select 'matches' as t, count(*) as n from matches
  union all select 'fetch_queue', count(*) from fetch_queue
  union all select 'crawl_frontier', count(*) from crawl_frontier
  union all select 'crawl_seeds', count(*) from crawl_seeds
  union all select 'build_runs', count(*) from build_runs
  union all select 'source_toggles', count(*) from source_toggles
  union all select 'schema_migrations', count(*) from schema_migrations
  order by t"

# 4. tear it down
kubectl -n lolstats exec statefulset/lolstats-postgres -- dropdb -U lolstats lolstats_restore_check
```

Step 3 and the `# rows per table` block of the manifest must agree line for line.
`--exit-on-error` matters: without it a restore stops at the first bad object and
still exits 0. If a table is missing from the list because a migration added it,
the manifest is the authority - it was generated from the same query at dump
time.

A restore does not need `psql` at all, and nothing in this path needs the
Prisma CLI, a Node runtime or the application binary.

## The real restore

Destructive. Read the whole sequence before starting, and prefer a quiet window:
the nightly jobs run 01:00-05:00 Europe/Berlin and the dump is taken at 04:30.

```
# 0. keep the current state, even the broken one. It costs a file and it is the
#    only way back if the dump turns out to be older than the damage.
kubectl -n lolstats exec statefulset/lolstats-postgres -- \
  sh -c 'pg_dump -U lolstats -Fc -f /tmp/prerestore.dump'
kubectl -n lolstats cp lolstats/lolstats-postgres-0:/tmp/prerestore.dump ./prerestore.dump

# 1. stop the only long-running writer. The CronJobs take care of themselves:
#    concurrencyPolicy is Forbid and none of them runs at this hour.
kubectl -n lolstats scale deploy/lolstats-ingest --replicas=0

# 2. replace the database. WITH (FORCE) ends any straggling backend instead of
#    failing on "database is being accessed by other users".
kubectl -n lolstats exec statefulset/lolstats-postgres -- \
  psql -U postgres -d postgres -c 'drop database if exists lolstats with (force)'
kubectl -n lolstats exec statefulset/lolstats-postgres -- createdb -U lolstats -O lolstats lolstats

# 3. restore
kubectl -n lolstats exec deploy/lolstats-web -- cat /var/lib/lolstats/backups/postgres/daily/lolstats-20260917T043012Z.dump \
  | kubectl -n lolstats exec -i statefulset/lolstats-postgres -- \
      pg_restore --no-owner --no-acl --exit-on-error -d lolstats -U lolstats

# 4. if the dump predates the newest migration, bring the schema forward. Idempotent:
#    applied versions are skipped and their checksums are verified.
#    See "Running one of the binaries by hand" below for the job template.
kubectl -n lolstats apply -f migrate-up.job.yaml

# 5. let the crawl resume
kubectl -n lolstats scale deploy/lolstats-ingest --replicas=1
```

Roles (`./prerestore.dump.globals.sql`) are only needed on a cluster that does
not have the `lolstats` role yet - a fresh install. On this cluster the role is
already there, and the dump was taken with `--no-owner`, so a restore does not
depend on it. If the globals file holds the "pg_dumpall failed" comment instead
of SQL, create the role by hand before step 3 (`create role lolstats login
password '<from the lolstats-postgres Secret>' superuser`) and let the nightly
job's next run record whether it worked.

## Running one of the binaries by hand

`kubectl create job --from=cronjob/<name>` covers the CronJobs (`maintain`,
`site-build`, `static-sync`, `discover-seeds`, `lolstats-aggregate`,
`backup-postgres`, `backup-archive`). It does not cover `migrate up` or
`lolstats-aggregate verify`, because no CronJob runs those arguments and a Job's
pod template cannot be patched to change them afterwards. Write the Job out,
apply it, delete it:

```yaml
# migrate-up.job.yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: migrate-up
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
        - name: migrate
          image: ghcr.io/erik-schuetze/league-of-legends:latest
          command: ["/lolstats-ingest"]
          args: ["migrate", "up"]
          envFrom:
            - configMapRef:
                name: lolstats-config
          env:
            - name: LOLSTATS_POSTGRES_DSN
              valueFrom:
                secretKeyRef:
                  name: lolstats-postgres
                  key: DSN
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
            readOnlyRootFilesystem: true
          volumeMounts:
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: tmp
          emptyDir: {}
```

A Job applied this way is not tracked by ArgoCD, so `prune: true` will not clean
it up: delete it when you are done. Its log is the only output, so read it with
`kubectl -n lolstats logs job/migrate-up` before deleting anything.

## How to tell it worked

- The manifest's per-table counts and the restored copy's counts agree.
- `kubectl -n lolstats logs deploy/lolstats-ingest --tail=50` shows the worker
  claiming rows again rather than reporting an empty frontier.
- `lolstats_matches_persisted_total` starts moving again, and
  `lolstats_pipeline_staleness_seconds{stage="crawl"}` stops climbing.
- `lolstats-aggregate verify` passes against the aggregate tree (see
  `rebuild-aggregates.md`) - a healthy control plane with a tree built from a
  lost frontier is still wrong.
- The `LolstatsCrawlStale` alert clears.

## Roll back

The restore *is* the rollback mechanism, so the rollback is another restore:
`./prerestore.dump` from step 0 for the state you replaced, or an older dump if
the newest one turns out to contain the damage you were undoing - which is what
`weekly/` is for. Rows written between the dump and the restore are gone either
way; the jobs that produce them are all idempotent and will re-derive them.

There is no transaction around the sequence above. If step 3 dies halfway, the
database is neither the old one nor the new one: drop it and start again from
step 2.

## What is destructive here

- `drop database ... with (force)` destroys every row of the live database,
  including everything crawled since the dump was taken. The raw archive and the
  aggregate tree are untouched.
- `scale deploy/lolstats-ingest --replicas=0` stops ingestion. It is additive and
  reversible, but while it is at 0 nothing is fetched and no alert will fire
  until the staleness thresholds are crossed.
- `kubectl cp` of `prerestore.dump` writes to the pod's `/tmp` first, which is an
  `emptyDir`: it is lost when the pod restarts, and it is not a backup.
- Restoring a dump taken by an *older* migration set and then not running
  `migrate up` leaves the worker writing rows the schema cannot accept.

## Retention cannot remove the last recoverable artifact, 2026-09-18

The concern is the obvious one: a retention policy that prunes "old" dumps while
`LATEST` still points at one, or a prune that runs before the day's dump is
published, turns a backup set into an empty directory with a stale pointer beside
it. Read against `deploy/base/jobs/backup-postgres.yaml`, the order of operations
is:

1. `pg_dump -Fc` writes to `"$partial"`, a name the prune grep does **not**
   match, then the globals and manifest are written through the same `.partial`
   prefix;
2. `mv "$partial" "$daily/$name"` - the artifact gets its real name only after
   the dump has succeeded;
3. `printf '%s\n' "$name" >"$root/postgres/LATEST"` - the pointer is written
   from the same `$name` that was just published;
4. **then** Sunday's promotion (`cp -p` into `weekly/`) and **then** `prune`.

`prune` sorts with `sort -r`, so a name is ranked by recency and only ranks
greater than the keep count are removed. The file `LATEST` names is always rank
1, so the pointer can never reference a file prune removed. With fewer dumps
than the keep count - which is the situation today - `prune` removes nothing at
all, and a run killed between step 3 and step 4 leaves *more* dumps than the
policy allows and self-corrects on the next run rather than fewer.

Observed on the cluster 2026-09-18 00:15 CEST: `2 daily, 0 weekly dump(s)` -
`weekly/` is empty because promotion happens on Sundays (`date -u +%u` = 7) and
the first dump was taken on a Thursday. `LATEST` holds
`lolstats-20260917T183639Z.dump`, the newest of the two, with sha256
`c12a7e0ef3d787be06d92107cdb3aa286bcffde810d30162e4d0fb102f1d6711` and 53
`pg_restore --list` TOC entries that parse back out of the stored file.

Two things this does **not** cover, so nobody relies on it:

- the `.partial` family is never pruned by design (it is never matched), so a
  series of failed runs grows `daily/` with files nobody will restore. Watch the
  directory size in `backup-status.sh` output rather than expecting prune to
  clean up after a failure;
- `weekly/` receives a `cp -p` of the `.dump` only, so a weekly entry has no
  `.manifest` beside it. The row counts and `schema_md5` for an older dump live
  in the daily copy of that manifest, and the daily copy is what prune removes
  after `BACKUP_KEEP_DAILY` - so the weekly dump is restorable but is not
  self-describing. If you need the manifest of a dump that is only in `weekly/`,
  take it from the restored database rather than from a file.
