# deploy

The Kustomize manifests for the lolstats service, and the only copy of them.

This application repository is the single source of truth for what runs. The
GitOps repository, `homecluster`, holds exactly one object that mentions this
service - the ArgoCD `Application` at `argocd-apps/lolstats.yaml` - and no
duplicated copy of anything below. A resource declared in both places has two
owners and a race between them, which is why the only service manifests that
live in `homecluster` are the ones for services that existed before this
repository did.

## Layout

```
.gitignore                covers the real secrets and local edits; nothing wider
base/                     what the service is
  namespace.yaml          namespace, restricted Pod Security profile
  config.yaml             non-secret configuration, shared by every workload
  secret.example.yaml     the shape of the two Secrets; the values never ship
  storage/pvc.yaml        the RWX data volume
  postgres/               independent Postgres: StatefulSet, Service, PVC
  ingest/                 the long-running worker
  jobs/                   the scheduled jobs, and the PreSync migration hook
  web/                    Caddy config, Deployment, Service
  network/                default-deny and the exceptions to it
overlays/homelab/         what is different about this cluster
  kustomization.yaml      storage classes, node exclusion, image tags
```

`overlays/homelab` is the path ArgoCD points at. `base` is never applied on its
own, and an overlay is never a place for a second copy of a base resource.

## Applying

Nothing here is applied by hand. The sequence that brings it up for the first
time is:

1. Create the secrets (below). They are not kustomize resources, so nothing else
   will do this.
2. Sync the ArgoCD root application once, so it picks up the new `Application`
   file: `argocd app sync argocd-apps`. The root app has no automated sync
   policy, which means a new file in `argocd-apps/` is inert until a human
   syncs it.
3. Wait. The `lolstats` application then syncs itself, and keeps itself synced
   with prune and selfHeal.

The public entry point is `https://lol.erik-schuetze.dev`, served by the shared
Caddy in namespace `web` (see `homecluster/web/caddy/configmap.yaml`), which
reverse-proxies to `lolstats-web.lolstats.svc.cluster.local:80`. The Caddyfile in
`base/web/caddyfile.yaml` is a different Caddy: the one in this namespace that
serves the rendered files with caching. The edge Caddy terminates TLS; this one
does not.

### Secrets

| Secret | Keys | Required |
| --- | --- | --- |
| `lolstats-postgres` | `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, `DSN` | yes - Postgres does not initialise without the first three, and every Go workload that touches the database reads `DSN` |
| `lolstats-riot` | `RIOT_API_KEY` | no - referenced with `optional: true` |

Start from the template, then apply the copy by hand:

```
cp deploy/base/secret.example.yaml deploy/base/secret.yaml
$EDITOR deploy/base/secret.yaml
kubectl apply -f deploy/base/secret.yaml
```

`deploy/.gitignore` covers `deploy/**/secret.yaml`, so the copy that holds real
values cannot be committed. The example stays tracked.

Secrets are deliberately not part of the kustomize tree: an automated sync with
`prune` and `selfHeal` would otherwise be entitled to overwrite or delete
whatever an operator created, and anything in the kustomize tree is in git. They
are also not listed in any `kustomization.yaml`, which is why a missing Secret
file never breaks `kubectl kustomize` or an ArgoCD sync.

What "no Riot key" actually means, because the answer is not uniform:

- **The stack comes up without it.** Postgres, `lolstats-web`, `site-build`,
  `lolstats-aggregate`, `maintain` and `static-sync` never read the Riot key.
- **The Riot consumers do not.** `lolstats-ingest worker`, `discover-seeds` and
  the `backfill` re-fetch path call `require(cfg.Riot, ...)` and exit non-zero
  with `RIOT_API_KEY is required` when it is unset. The key is an optional env
  var so that this is a clear start-up error in the log rather than a pod stuck
  in `CreateContainerConfigError`, but the consequence is real:
  `kubectl -n lolstats get pods` shows `lolstats-ingest` in `CrashLoopBackOff`
  and `discover-seeds` failing nightly until the Secret exists. Nothing else
  depends on either of them, and installing the Secret - with no manifest change
  and no restart of anything - is the fix.
- **And the public site serves a labelled preview, not real statistics.** With
  no key the archive stays empty, so `site-build` renders the checked-in demo
  fixtures and every page carries the preview banner; no crawled data is
  published. That is what
  `docs/decisions/ADR-010-public-preview-posture.md` and plan risk R2 require
  while a production key application is pending, and it is one key in
  `base/config.yaml`: `LOLSTATS_AGG_FIXTURES: "only"`. When the key is approved
  and the archive has produced a published snapshot, change that value to
  `"off"` - "render `LOLSTATS_AGG_ROOT` and never substitute fixtures" - and the
  next `site-build` serves real aggregate data. While it stays `"only"`, a real
  snapshot on the volume would be ignored by the build.
- The `backfill` job is suspended and stays that way; it is a manual tool, so a
  missing key only matters on the day someone runs it.
- `static-sync` is suspended too, for a different reason: the subcommand exists
  and works (it mirrors public Data Dragon and was measured to exit 0 with 5
  documents archived), but while the build runs with `LOLSTATS_AGG_FIXTURES=only`
  it renders the committed fixtures and ignores the volume, so the static tree
  this job writes would not be rendered. Remove the single `suspend: true` line
  from `base/jobs/static-sync.yaml` when the build moves to real ingestion
  (`AGG_FIXTURES=off`); nothing else changes.

If the requirement is that `lolstats-ingest` itself be green with no key, that is
a change in `cmd/lolstats-ingest` (an idle mode that serves metrics), not a
change here.

## Migrations

The schema comes from `sql/migrations/`, is applied by
`lolstats-ingest migrate up`, and is applied **from this directory** by
`base/jobs/migrate.yaml` - an ArgoCD `PreSync` hook. Nothing else creates a
table: there is no `schema.sql` to load by hand and no init script on the
Postgres pod, so a cluster whose `lolstats` database has no `schema_migrations`
table has simply never run the hook.

### Why a PreSync hook

Two properties are wanted at once, and only this shape gives both.

**Ordering.** A PreSync hook runs to completion before ArgoCD applies anything
else in this directory, so no workload can start against a schema that is behind
the binary. The alternative - an initContainer on the workloads that touch
Postgres - orders startup just as well, but it has to be replicated into the
ingest Deployment and all seven job templates, and it makes a Postgres that is
briefly unreachable into a crash-loop of every workload rather than one failed
hook that says what went wrong.

**Not being a Job.** `batch/v1` Jobs are effectively immutable: once the
controller has run one, an edit to its pod template is rejected by the API
server. A plain Job in this tree would leave ArgoCD reporting a permanent diff
the first time the migration command, image or resources changed, and - the
worse half - an *unchanged* Job does not run again on a later sync. The hook
carries `argocd.argoproj.io/hook-delete-policy: BeforeHookCreation`, so ArgoCD
deletes the previous hook Job before creating the new one. A changed spec
therefore applies cleanly and an unchanged one still re-runs. It is `PreSync`
with `BeforeHookCreation` and deliberately *not* `HookSucceeded`, because a Job
that is deleted on success is gone before the operator can read its logs, and it
would still be the immutable object ArgoCD tries to re-create on the next sync.

**Re-running every sync is safe and cheap.** `internal/store/migrate.go` takes a
session advisory lock (`pg_advisory_lock`, key `0x10646c6f6c73` - `lols`) for
the whole run, so the hook cannot interleave with itself, with another hook, or
with the imperative `make migrate` below. It keeps a `schema_migrations` ledger
of `(version, name, sha256 checksum, applied_at)`, it re-verifies each applied
checksum before running anything and refuses to proceed on a mismatch, and it
commits each migration body atomically with its ledger row. A run with nothing
new to do is one round trip and logs `migrations up: schema is already current`.
It is forward-only: there is no `migrate down`, so rolling back means writing a
new migration.

### The proof

Run in the `lolstats` namespace on 2026-09-17, starting from a database with
zero rows in `pg_tables`:

```
$ kubectl -n lolstats exec deploy/lolstats-ingest -- /lolstats-ingest migrate up
{"level":"INFO","msg":"migrations up: schema updated","versions":[1,2,3]}

$ kubectl -n lolstats exec lolstats-postgres-0 -- psql -U lolstats -d lolstats -tAc \
    "select tablename from pg_tables where schemaname='public' order by 1"
build_runs crawl_frontier crawl_seeds fetch_queue matches schema_migrations source_toggles

$ kubectl -n lolstats exec deploy/lolstats-ingest -- /lolstats-ingest migrate up
{"level":"INFO","msg":"migrations up: schema is already current"}
$ # same seven tables, ledger still (1,'init'),(2,'ingest'),(3,'revival_budget')
```

The same thing through the hook, which is what a sync does:

```
$ kubectl kustomize deploy/overlays/homelab | \
    awk 'BEGIN{RS="\n---\n"} /name: lolstats-migrate/' | kubectl apply -f -
job.batch/lolstats-migrate created
$ kubectl -n lolstats wait --for=condition=Complete job/lolstats-migrate --timeout=300s
job.batch/lolstats-migrate condition met
$ kubectl -n lolstats logs job/lolstats-migrate
{"level":"INFO","msg":"migrations up: schema is already current"}
```

### Running it now, without a sync

`make migrate` applies the hook Job on its own, for the case where the schema
has to move *now* rather than at the next sync. The hook is an ordinary Job
object when it is applied by hand, it is idempotent, and applying it twice is a
no-op - `ttlSecondsAfterFinished: 86400` removes it a day later. Because
ArgoCD skips resources it has classified as hooks when it is not running that
phase, a hand-applied hook is not tracked by the Application either; it will not
be pruned and it must be deleted if it is no longer wanted.

### Adding a migration

Put the `.sql` file in `sql/migrations/`, where the naming (
`NNNN_name.up.sql`) and the checksum ledger are enforced by
`internal/store/migrate.go`, and note the trigger problem: the `.sql` files are
embedded in the *image*, not copied from this repository by ArgoCD, so adding
one produces no diff in this Application's rendered manifests and therefore no
sync. The hook re-runs on any sync the change does produce, and the thing that
produces one today is the `latest` re-pull; once the images are pinned by digest
(see Open TODOs) it is the digest bump that ships the new binary.

## Data

One RWX volume, `lolstats-data` on the `nfs-client` StorageClass, mounted at
`/var/lib/lolstats` by every workload:

- `raw/` - the immutable archive. This is the primary data: nothing else in the
  system can reproduce it.
- `agg/` - the published aggregates, published by renaming a directory into
  place, so a reader never sees a half-written tree.
- `site/` - the rendered HTML that `lolstats-web` serves.

Postgres keeps its own `longhorn` PVC instead: it wants replicated local NVMe,
not a network filesystem (see `homecluster/docs/architecture.md` section 3).

Two things to know about the data volume before you touch it:

- `nfs-client` has `reclaimPolicy: Delete` and `archiveOnDelete: "false"`, so
  deleting the PVC destroys the archive on Atlas. Both PVCs carry
  `argocd.argoproj.io/sync-options: Delete=false` so that a stray sync cannot do
  it by accident.
- The volume root has to be writable by uid 65532 (Go workloads) and 1000 (site
  and web). The nfs-subdir provisioner creates the export directory as root, so
  on a fresh volume check this before trusting a green sync:
  `kubectl -n lolstats exec deploy/lolstats-web -- ls -ldn /var/lib/lolstats`.
  If it is not writable, the fix is on Atlas (`chown`/`chmod` the directory under
  `/nas-main/k3s-volumes`, or set the StorageClass's `uid`/`gid` parameters) -
  a root init container is not an option here, because the namespace enforces the
  restricted Pod Security profile.

## Observability

Prometheus in namespace `monitoring` scrapes `lolstats-ingest` at `/metrics` on
:9090 through `monitoring/servicemonitors/lolstats-servicemonitor.yaml`, which
lives in `homecluster` rather than here because the scrape configuration is a
property of the monitoring stack, not of this service.

That ServiceMonitor has to be in the `monitoring` namespace, with a
`namespaceSelector` naming `lolstats`: this Prometheus instance is configured
with an empty `serviceMonitorSelector` but no namespace selector, so it only ever
sees ServiceMonitors in its own namespace. A copy placed in the `lolstats`
namespace would apply cleanly and be scraped by nothing.

The metrics are the ones registered in `internal/obs`: Riot request counts,
latencies and retries, queue claims, persisted matches, raw bytes written,
frontier size, pipeline staleness, and build duration, cell and failure counts.

## Checking a change

```
kubectl kustomize deploy/overlays/homelab
```

CI builds the three images and runs the Go and Astro checks, including the
DuckDB-dependent build tests (the `verify` job installs the pinned DuckDB client
and runs `make test-build`, which fails on a skip), but it does not render these
manifests, so this command - plus a read of the rendered output - is the check
that matters before a change to this directory is pushed.

## Open TODOs

- **Images are pinned by tag only.** Every image reference is `:latest` until the
  first tags exist. When they do, replace each one with `tag@sha256:...` (plan
  section 12, R13) at the reference in `base/` and in `overlays/homelab`, which
  is where the tag lives. A tag can be re-pushed; a digest cannot.
- **`static-sync` is implemented and deliberately not armed.** Plan section 5.2
  lists the job, and `lolstats-ingest` carries the `static-sync` subcommand: it
  mirrors the public Data Dragon CDN and was measured to exit 0 with 5 documents
  archived, needing no Riot key. It keeps its schedule - the schedule itself is
  not the thing left to do later - and it is **suspended**, because with
  `LOLSTATS_AGG_FIXTURES=only` the site build renders the committed fixtures, so
  the static tree this job writes is not rendered; arming it would only spend a
  nightly run on data nothing reads. Delete the `suspend: true` line in
  `base/jobs/static-sync.yaml` when the build moves to real ingestion.
- **`site-build` writes into the volume layout above.** It renders into a staging
  directory and renames it onto `/var/lib/lolstats/site`. If the frontend's build
  output directory changes, this job changes with it.

<!-- Everything from here to the end of the file was added by the operations
     workstream (backups, alerts, runbooks). It changes nothing above it. -->

## Backups

Two CronJobs, both in `base/jobs/`, both scheduled in `Europe/Berlin` with
`concurrencyPolicy: Forbid`. Both mount the `lolstats-data` ReadWriteMany volume,
whose whole point is that an artifact written by a job on one node is readable
from every other node - so a backup is not trapped on the node that produced it.
Both run under the namespace's **restricted** pod security profile: `runAsNonRoot`
with uid/gid/fsGroup `65532`, `seccompProfile: RuntimeDefault`,
`allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true` and
`capabilities: { drop: ["ALL"] }`, with a `tmp` emptyDir for scratch.

| job | schedule | mechanism | output |
| --- | --- | --- | --- |
| `backup-postgres` | `30 4 * * *` | `pg_dump --format=custom` from the pinned `postgres:16.10` image | `backups/postgres/<stamp>.dump` |
| `backup-archive` | `0 5 * * *` | `restic backup` from the official `restic/restic` image | a snapshot in `RESTIC_REPOSITORY` |

The ordering matters: the dump runs first because it is the smaller, faster job
and the aggregate build (01:00) has long finished by then, while the archive
snapshot runs after it so that the two never contend for the same NFS export.

### `backup-postgres`

- **Plain `pg_dump` and `psql`, no Prisma.** The dump has to be restorable by
  something other than this repository's own tooling, and `-Fc` is what makes a
  *selective* restore possible - one table, or the schema without the rows.
- **Timestamped**: `lolstats-<YYYYmmddTHHMMSSZ>.dump`, UTC, so two runs in one
  day cannot collide and the ordering is visible in any shell.
- **Never a half file.** The dump is written to `<name>.dump.partial` and renamed
  on success, so a `*.dump` in the listing is always complete. Every step runs
  under `set -eu` with `umask 077`, so a failure is an exit code and not a
  truncated file that looks like a backup.
- **A dump is not assumed to be good.** Before the rename, the job asserts that
  `pg_restore --list` reports at least ten table-of-contents entries. The classic
  silent failure is a dump that exits 0 and contains nothing.
- **A manifest per dump**: per-table row counts, an md5 over the schema, the TOC
  entry count and the byte size, written next to the dump. It is what makes "is
  this backup sane" answerable without a restore, and it is what
  `scripts/backup-verify.sh` compares.
- **Role state too**: `pg_dumpall --globals-only` into `globals.sql`, so a
  rebuild has something to recreate the `lolstats` role from. If only this step
  fails, the job says so loudly and continues - the data dump is the artifact
  that matters.
- **`LATEST`** names the newest complete set, so a runbook never has to guess.

### `backup-archive`

- **restic, not tar.** Deduplicated, encrypted, and verifiable: `restic check
  --read-data-subset` proves the repository can actually be read, which a
  tarball cannot.
- The path it snapshots is `LOLSTATS_RAW_ROOT` from `lolstats-config`, the same
  variable the worker writes and the aggregate build reads, so the snapshot
  cannot drift from the thing being backed up.
- `--tag raw-archive --host lolstats-archive`, `--one-file-system`, and excludes
  for `*.tmp`, `*.partial` and caches - the worker stages and renames, so those
  extensions never belong to a finished file.
- **Exit code 3 (some files unreadable) is reported as its own outcome**, not as
  success and not as a hard failure, and a post-backup assertion then checks that
  a snapshot for this host actually exists. A run that exits 3 having created
  nothing is a failed backup, and this job is written so that it cannot look like
  anything else.
- **Sundays add `forget --prune` and `check --read-data-subset=2%`.** The daily
  run only adds; the weekly run is the one that takes space back and proves the
  repository is readable end to end.
- **`RESTIC_PASSWORD` comes from Secret `lolstats-restic` key `RESTIC_PASSWORD`,
  referenced `optional: true`** - the same pattern as the Riot key in
  `ingest/deployment.yaml`. A cluster without it still brings up every other
  workload in the namespace; this job alone fails, loudly, printing the exact
  `kubectl create secret` command that fixes it. The Secret is not in this
  repository, and **the Kubernetes Secret must not be the only copy**: a lost
  password makes every existing snapshot unreadable, forever, by design.

### Retention

Both policies live in `base/backup/config.yaml` (`lolstats-backup-config`):

| artifact | daily | weekly | monthly | window covered |
| --- | --- | --- | --- | --- |
| Postgres dumps | 7 | 4 (Sundays) | - | ~31 days |
| restic snapshots | 7 | 4 | 6 | ~6 months |

Seven dailies is a week of point-in-time recovery, which is longer than any
mistake in this stack takes to notice. Four weeklies fit on the 50 Gi volume
without competing with the archive and reach back past a full crawl cycle. Six
monthly snapshots reach back past a League patch or two, which is the timescale
on which "the numbers used to be different" becomes a question. These are
deliberate numbers, not defaults; they change by editing that ConfigMap.

The prune is by filename for the dumps (the timestamp is in the name, so the
ordering is unambiguous without reading any file) and by `restic forget` for the
snapshots.

### What is NOT covered: there is no off-site copy

`RESTIC_REPOSITORY` defaults to `/var/lib/lolstats/backups/restic` - **a path on
the same ReadWriteMany export that already holds the raw archive.** That protects
against a bad crawl, an accidental `rm`, and a corrupted file. It does not
protect against the loss of the export, the host of it, or the site. Plan risk
**R6 is accepted and unmet**, and no off-site media is known to exist. The restic
job prints a `NOTE:` saying exactly this on every run, so the position is visible
in the logs and not only here.

Swapping it is a configuration change plus one more: `RESTIC_REPOSITORY` in
`base/backup/config.yaml` (plus the backend's credentials as extra keys in
`lolstats-restic`), *and* an egress rule, because this namespace runs under
`network/default-deny.yaml` and can reach nothing but DNS, Riot over 443 and its
own ports. A remote repository hangs until it times out without that rule.
`docs/runbooks/restore-raw.md` ends with the six-step procedure, including that
rule, for when media appears.

### Running and restoring

```
# Run either job now - the CronJob template, including the security context and
# the node affinity that keeps it off vega, is inherited. ttlSecondsAfterFinished
# applies, so the Job disappears a day later; it is not tracked by ArgoCD.
kubectl -n lolstats create job backup-postgres-now --from=cronjob/backup-postgres
kubectl -n lolstats logs -f job/backup-postgres-now

# Look at the tree. lolstats-web is the only workload with a shell that mounts
# the data volume; the Go images are distroless.
kubectl -n lolstats exec deploy/lolstats-web -- sh -c 'ls -l /var/lib/lolstats/backups/postgres | tail -5'
kubectl -n lolstats exec deploy/lolstats-web -- cat /var/lib/lolstats/backups/postgres/LATEST
```

Runbooks: `docs/runbooks/restore-postgres.md`, `docs/runbooks/restore-raw.md`,
`docs/runbooks/rebuild-aggregates.md`, `docs/runbooks/site-integrity.md` (the
served-response integrity check and the cache-handler defect it exists for),
`docs/runbooks/key-rotation.md`,
`docs/runbooks/enable-alert-delivery.md` (the opt-in Alertmanager procedure), plus
the pre-existing `ingest-down.md`.

The restore **drill** is `scripts/backup-verify.sh`, which is the evidence behind
the Postgres half of the launch gate "Postgres and the raw archive have both been
restored from backup in a test". It dumps a real database, restores into a fresh
throwaway one, compares table count, column definitions, index definitions and
every table's row count, and then proves the comparison can fail by deleting one
row and asserting a mismatch. `make backup-verify` runs it against a throwaway
Postgres in Docker; `make backup-verify-cluster` runs it against the cluster's
Postgres using a database named `lolstats_verify_<epoch>` that it drops
afterwards.

The raw-archive half of the same gate is `scripts/archive-verify.sh`, run by
`make archive-verify`. It builds a synthetic raw archive in the layout
`internal/raw` documents, initialises a restic repository with the image
`deploy/base/jobs/backup-archive.yaml` pins, backs up with that job's own flags,
proves a wrong password cannot open the repository, runs the job's Sunday branch
(`forget --prune` plus `check --read-data-subset=2%`) and then `check
--read-data`, restores into an empty directory and compares archive and restore
by sha256 manifest. It then proves the comparison can fail - a changed byte, an
extra file, a missing file - before asserting the archive and the restore match.
Like `backup-verify` it needs no cluster and no credentials, and everything it
writes stays in `.agent-artifacts/archive-verify/`.

Neither drill is the whole gate on its own, and neither is a substitute for the
runbooks: the drills prove the *format* survives a round trip, while restoring
what is actually on the cluster's data volume is a human run of
`docs/runbooks/restore-postgres.md` and `docs/runbooks/restore-raw.md`.

## Alerts

The rules are in `homecluster`, not here: `monitoring/prometheus/lolstats-rules.yaml`,
a `PrometheusRule` named `lolstats-prometheus-rule`. Rule objects are a property
of the monitoring stack, exactly like the ServiceMonitor.

**The namespace is load-bearing.** It must be `monitoring`. The Prometheus CR
(`monitoring/prometheus/prometheus.yaml`, `prometheus-persistant`) sets an empty
`serviceMonitorSelector` and neither `ruleSelector` nor `ruleNamespaceSelector`;
prometheus-operator's default for an unset `ruleNamespaceSelector` is the
Prometheus object's own namespace. A rule placed in `lolstats` would apply
cleanly, look correct in `kubectl get prometheusrules -A`, and be loaded by
nothing.

Twelve rules in four groups:

| group | alert | what it fires on | severity | for |
| --- | --- | --- | --- | --- |
| `lolstats-crawl` | `LolstatsCrawlStale` | `lolstats_pipeline_staleness_seconds{stage="crawl"} > 1h` | warning | 30m |
| | `LolstatsCrawlStalled` | the same, `> 6h` | critical | 30m |
| | `LolstatsFrontierNotDraining` | frontier non-empty and `increase(lolstats_matches_persisted_total[1h]) == 0` | critical | 30m |
| | `LolstatsIngestMetricsAbsent` | the ingest Deployment wants replicas and its metrics are not there | critical | 10m |
| `lolstats-build` | `LolstatsBuildNotScheduled` | `kube_cronjob_status_last_schedule_time` **or** `kube_cronjob_next_schedule_time` more than 26h in the past | critical | 30m |
| | `LolstatsBuildJobFailed` | `kube_job_status_failed > 0` for an `lolstats-aggregate-*` job | critical | 5m |
| | `LolstatsBuildStuck` | `kube_job_status_active > 0` for more than 5h | warning | 5h |
| | `LolstatsBuildFailures` | `increase(lolstats_build_failures_total[6h]) > 0` - **inert, see below** | critical | 10m |
| `lolstats-riot-api` | `LolstatsRiotRateLimited` | `429` above 5% of requests over 15m | warning | 15m |
| | `LolstatsRiotAuthFailures` | `403` above 1% of requests over 15m | warning | 15m |
| | `LolstatsRiotKeyRevoked` | `403` above 0.01/s **and** `2xx` at zero | critical | 10m |
| `lolstats-riot-key` | `LolstatsRiotKeyOld` | `lolstats_riot_key_age_seconds > 12h` - **inert, see below** | warning | 30m |

Every rule has `for:`, a `severity`, a `summary`, a `description` naming the next
command or runbook, and two extra annotations - `empty_means` (what an empty
result means for that expression) and `runbook_url`. An empty result means "not
firing" and that is precisely how a rule can be silently useless, so the
expressions are written to be diagnosable at the Prometheus UI and
`LolstatsIngestMetricsAbsent` exists as the backstop for the case where the
*cause* of the emptiness is that the metrics are not being produced.

Thresholds come from the configured cadence, not from taste:
`DefaultPollInterval = 15s` and `DefaultReportInterval = 60s` in
`internal/crawl`, the aggregate CronJob at 01:00 with a 26-hour budget for "it
did not even start", and its `activeDeadlineSeconds: 7200` plus generous margin
for "it started and is not finishing".

Two honest caveats, both recorded in the rule file itself:

- **`LolstatsBuildFailures` cannot fire.** `lolstats_build_failures_total` is
  registered by the aggregate binary, which runs as a Job with no Service and is
  never scraped - only the long-running `lolstats-ingest` Service has a
  ServiceMonitor. The rule is kept because it will start working the day the
  aggregate metrics have somewhere to come from, and the three kube-state-metrics
  rules above cover the same question in the meantime. Making it scrapable is a
  deployment change, not a code change: either put a Service in front of the
  Job's metrics port (`LOLSTATS_METRICS_ADDR`, `:9090`, alive only while a build
  runs) with a ServiceMonitor that selects it, or have the aggregate push to a
  gateway that is scraped instead. Both need a manifest that does not exist yet;
  this Prometheus is configured only by ServiceMonitors (`serviceMonitorSelector:
  {}`, no extra scrape configuration), so neither option is config-free.
- **`LolstatsRiotKeyOld` cannot fire either, and the cause is a code defect
  rather than a deployment gap.** `lolstats_riot_key_age_seconds` is registered
  by the ingest worker - which *is* scraped - but nothing ever writes it:
  `internal/crawl/worker.go` refreshes it from inside an assertion that the
  fetcher implements `Age() (time.Duration, bool)`, while the value the ingest
  binary passes there is a `*riot.Client`, whose only age surface is
  `KeyProvider.Age() time.Duration` - one result, not the two the assertion asks
  for - so the assertion never succeeds and the gauge is scraped as a constant
  `0`. The unit test that covers this path passes only because its fake fetcher
  implements the two-value form (`internal/crawl/fakes_test.go`), which is why
  CI does not catch it. Fixing that assertion belongs to the ingestion
  workstream, and would still not turn this into an expiry clock:
  `lolstats_riot_key_age_seconds` counts from when *this process* first saw the
  key currently configured, so it resets to `0` on every restart and is `0` when
  no key is configured - a key that was already 20 hours old at pod start never
  trips it. A real expiry alert needs a metric derived from the key's own
  `expires_at`; `LOLSTATS_RIOT_API_KEY_EXPIRES_AT` is parsed by `internal/config`
  and consumed by nothing, so that is a code change owned by the ingestion
  workstream, not a rule. Until then, the observable symptom of an expiry is a
  sustained 403, which is what `LolstatsRiotKeyRevoked` watches, and the
  practical control is a calendar alarm (`docs/runbooks/key-rotation.md`).

### What is NOT covered: nothing is delivered

**There is no Alertmanager in this cluster.** The `alertmanager` CRD is installed
by prometheus-operator, but no Alertmanager custom resource exists
(`kubectl get alertmanager -A` is empty), no pod is running anywhere, and
Prometheus reports `activeAlertmanagers: []`.

So these rules are loaded and evaluated, and they are delivered **nowhere**. The
only place to see them is the Prometheus UI - `/alerts` and `/api/v1/alerts` on
`prometheus-persistant`, reachable with
`kubectl -n monitoring port-forward svc/prometheus 9090:9090`. "The operator is
told when something breaks" is therefore an **unmet gate** until an Alertmanager
exists with a receiver, and it is recorded as unmet rather than papered over with
rules that fire into the void. The rules were still written, and their syntax
checked with `promtool check rules` run against the `spec.groups` block extracted
from the manifest (`promtool` cannot read a `PrometheusRule` directly); there is
no committed `promtool test rules` case for them and no CI job, so a later edit is
re-checked by reading. That way, the day an Alertmanager exists, the alerting is
already in place - with the two rules above that cannot fire recorded as such
rather than counted as coverage.

The Alertmanager and the receiver for it are an **opt-in** bundle in
`homecluster/monitoring/alertmanager/`, deliberately not applied by ArgoCD because
activating it also edits the shared `Prometheus` CR and rolls the StatefulSet that
serves every workload's metrics. `docs/runbooks/enable-alert-delivery.md` is the
three-step procedure, with its rollback.

### Dashboards

None were added, deliberately. Grafana in this cluster is an ArgoCD application
pointing at the repo-root `grafana/` directory of `homecluster`, and that
directory holds no dashboards as files - so a dashboard JSON added here would be
the first file in a directory nothing reads. Recording that is more useful than
shipping a file that appears nowhere.

<!-- end additions: operations workstream -->
