# Runbook: the restore drill

This is the rehearsal that converts "we take backups" into "we have backups". It
restores the two artifacts that matter - the Postgres dump and the raw archive -
into a disposable target of your own and proves the result matches the source. Run
it after any change to a backup job, before a launch gate that depends on backups,
and on a cadence (see *Cadence* at the end).

It is additive to the three runbooks that already exist and does not replace any
of them:

| runbook | what it answers |
| --- | --- |
| `restore-postgres.md` | the database is gone - how do I get it back |
| `restore-raw.md` | the raw archive is gone - how do I get it back |
| `rebuild-aggregates.md` | the derived `agg/` and site trees are gone - how do I rebuild them |
| **this file** | do those three actually work, and how long do they take |

Everything below was executed against the live product on 2026-09-17; the numbers
in *Observed results* are measured, not estimated, and the gaps in *Known gaps*
are what that run found. Nothing here writes to the live product.

## READ THIS FIRST: what must never be touched

The live product is namespace `lolstats`. A drill that clobbers it is not a drill:

| must never be touched | why |
| --- | --- |
| PVC `lolstats-data` (RWX NFS) | holds `/var/lib/lolstats/raw` - the only copy of data that cannot be regenerated - and the `agg/`, `backups/` and site trees |
| PVC `lolstats-postgres-data` (RWO longhorn) | the only copy of the control-plane database |
| StatefulSet `lolstats-postgres` / pod `lolstats-postgres-0` | the live server; a restore *into* it overwrites live rows |
| database `lolstats` in that server | never `pg_restore -d lolstats`, never `DROP`, `TRUNCATE` or `DELETE` |
| the CronJobs `backup-postgres` and `backup-archive` | read them with `kubectl get -o yaml`; do not `create job --from` them to "test" - that writes into the live backup tree |
| namespace `web`, the shared Caddy, any other homecluster app | not this project's |
| `/Users/eschuetze/workspace/homecluster` | this lane never needs to open it |

Two more rules that keep the drill harmless **by construction** rather than by
discipline:

1. **Mount the live volume read-only.** Every drill pod mounts the live NFS export
   as `readOnly: true`. A write is then impossible for the kernel, not just
   unlikely. Verify it in the pod with `grep live-ro /proc/mounts` - you want `ro`.
2. **Restore into a namespace of your own.** The drill target is a namespace you
   create (`lolstats-restore-drill` below), with its own PVCs and its own Postgres.
   Do not reuse `restore-postgres.md`'s "manual" rehearsal as your target: that one
   runs `createdb` inside the **live** server, which puts an extra database in the
   cluster you are trying not to disturb.

Namespace `lolstats` runs `default-deny-all`, with ingress allowed only to selected
component pods, so a pod in a drill namespace cannot reach the live database even if
you ask it to (`psql` gets `Connection refused`, not a password prompt). That is a
property of the product, not of your discipline - and note the flip side in *Known
gaps*: it also means the drill cannot read live row counts from outside `lolstats`.

## Step 0: discover what actually exists, before planning a restore

An unverified backup is not a backup. Take these readings first and write them
down; a drill that starts by assuming a backup exists proves nothing.

```sh
# who is supposed to be taking backups, and have they ever run?
kubectl -n lolstats get cronjob                 # LAST SCHEDULE is the column that matters
kubectl -n lolstats get job --sort-by=.metadata.creationTimestamp | tail

# do the credentials the two jobs need exist?
kubectl -n lolstats get secret                  # need lolstats-postgres AND lolstats-restic

# what does the backup tree hold? (read-only mount; see step 1 for how to get this shell)
ls -la  /live-ro/backups/postgres/daily
ls -la  /live-ro/backups/postgres/weekly
cat     /live-ro/backups/postgres/LATEST
ls -la  /live-ro/backups/restic 2>&1            # "No such file or directory" = no repository at all

# is the newest dump actually a dump? this is the check the job itself runs
NEWEST=$(cat /live-ro/backups/postgres/LATEST)
pg_restore --list "/live-ro/backups/postgres/daily/$NEWEST" | wc -l
sha256sum /live-ro/backups/postgres/daily/"$NEWEST"

# how big is the archive, and is the writer mid-file?
du -sb /live-ro/raw
find /live-ro/raw -name '*.parquet.zst' | wc -l
find /live-ro/raw -name '*.tmp' | wc -l         # >0 is normal; the writer publishes by rename
```

Read the result honestly:

- `LAST SCHEDULE <none>` on `backup-postgres` or `backup-archive` means the schedule
  has **never fired**. A dump that exists beside it came from a hand-run job, and
  hand-run backups stop when somebody stops running them.
- A missing `lolstats-restic` Secret means there is no repository to restore from,
  no matter how good the raw archive looks. The `backup-archive` job fails on
  purpose in that state, and its message says so.
- A dump that does not survive `pg_restore --list` is a file, not a backup.
- `weekly/` empty and one file in `daily/` is a backup set with **one** retention
  point. Restoring it is possible; restoring "last week's" is not.

## Step 1: build the drill target

The target is four objects in a namespace you create: two PVCs, one Secret, one
Postgres Deployment, and one tools pod. Nothing is taken from `lolstats` except a
read-only mount of the volume that has to be read.

Write these out under `files/` somewhere outside the repo (they are drill scaffolding,
not product artifacts) and apply them in order.

```yaml
# ns.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: lolstats-restore-drill
```

```yaml
# pvc.yaml  - the restore target and the scratch a restic repository needs
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: drill-data, namespace: lolstats-restore-drill}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: longhorn
  resources: {requests: {storage: 5Gi}}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: drill-pg, namespace: lolstats-restore-drill}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: longhorn
  resources: {requests: {storage: 5Gi}}
```

```yaml
# drill-pg.yaml  - the disposable server the dump is restored into.
# Password is generated for the drill and thrown away with the namespace.
apiVersion: v1
kind: Secret
metadata: {name: drill-pg, namespace: lolstats-restore-drill}
type: Opaque
stringData:
  POSTGRES_PASSWORD: "<openssl rand -hex 16>"
  POSTGRES_USER: drill
  POSTGRES_DB: drill
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: drill-pg, namespace: lolstats-restore-drill}
spec:
  replicas: 1
  strategy: {type: Recreate}
  selector: {matchLabels: {app: drill-pg}}
  template:
    metadata: {labels: {app: drill-pg}}
    spec:
      containers:
        - name: postgres
          # the same pinned image the live StatefulSet runs, so client and server majors match
          image: postgres:16.10@sha256:21f6013073bc6b92830a2129570e2f5ec42a6c734b5a985a41e83aa58f54c3c1
          envFrom: [{secretRef: {name: drill-pg}}]
          env: [{name: PGDATA, value: /var/lib/postgresql/data/pgdata}]
          ports: [{containerPort: 5432}]
          volumeMounts: [{name: data, mountPath: /var/lib/postgresql/data}]
      volumes:
        - name: data
          persistentVolumeClaim: {claimName: drill-pg}
---
apiVersion: v1
kind: Service
metadata: {name: drill-pg, namespace: lolstats-restore-drill}
spec:
  selector: {app: drill-pg}
  ports: [{port: 5432, targetPort: 5432}]
```

The tools pod is the drill's shell. Three containers because three tools are needed:
`psql`/`pg_restore` (the postgres image), `restic` (the pinned restic image the
archive job uses) and a Python container with `pyarrow`/`zstandard` for the Parquet
half. All three mount the live export **read-only**.

```yaml
# drill-tools.yaml
apiVersion: v1
kind: Pod
metadata: {name: drill-tools, namespace: lolstats-restore-drill, labels: {app: drill-tools}}
spec:
  restartPolicy: Never
  terminationGracePeriodSeconds: 5
  containers:
    - name: tools
      image: postgres:16.10@sha256:21f6013073bc6b92830a2129570e2f5ec42a6c734b5a985a41e83aa58f54c3c1
      command: ["sleep", "infinity"]
      env:
        - {name: PGHOST, value: drill-pg}
        - {name: PGUSER, value: drill}
        - {name: PGPASSWORD, valueFrom: {secretKeyRef: {name: drill-pg, key: POSTGRES_PASSWORD}}}
        - {name: PGDATABASE, value: drill}
        - {name: HOME, value: /tmp}
      volumeMounts:
        - {name: live, mountPath: /live-ro, readOnly: true}
        - {name: scratch, mountPath: /drill}
        - {name: tmp, mountPath: /tmp}
    - name: restic
      image: restic/restic:0.19.1@sha256:136600b6ff6843d61d355f7f71f460a166429f35de6fd11b568fece3c9a4d510
      command: ["sleep", "infinity"]
      env:
        - {name: RESTIC_PASSWORD, value: "drill-only-password-not-a-real-repository"}
        - {name: RESTIC_CACHE_DIR, value: /tmp/restic-cache}
        - {name: HOME, value: /tmp}
      volumeMounts:
        - {name: live, mountPath: /live-ro, readOnly: true}
        - {name: scratch, mountPath: /drill}
        - {name: tmp, mountPath: /tmp}
    - name: py
      image: python:3.12-slim
      command: ["sleep", "infinity"]
      env: [{name: HOME, value: /tmp}]
      volumeMounts:
        - {name: live, mountPath: /live-ro, readOnly: true}
        - {name: scratch, mountPath: /drill}
        - {name: tmp, mountPath: /tmp}
  volumes:
    - name: live
      nfs:
        # the live export, mounted READ-ONLY. Both lines are copied from
        # `kubectl -n lolstats get pv pvc-<id> -o yaml` for lolstats-data.
        server: 192.168.10.100
        path: /nas-main/k3s-volumes/lolstats-lolstats-data-pvc-67c695c9-68af-4100-a4a6-606b07d9bb33
        readOnly: true
    - name: scratch
      persistentVolumeClaim: {claimName: drill-data}
    - name: tmp
      emptyDir: {}
```

`kubectl apply -f ns.yaml -f pvc.yaml -f drill-pg.yaml -f drill-tools.yaml`, then

```sh
kubectl -n lolstats-restore-drill get pods
# the read-only mount is the whole safety argument - confirm it is what you think it is
kubectl -n lolstats-restore-drill exec drill-tools -c tools -- grep live-ro /proc/mounts
# expect: ... /live-ro nfs4 ro,...
```

The Python container needs its two libraries once per drill:

```sh
kubectl -n lolstats-restore-drill exec drill-tools -c py -- pip install --quiet zstandard pyarrow
```

## Step 2: Postgres restore drill

Do it in this order: parse the archive, restore it, compare it against the manifest
that the job wrote *beside* the dump. The manifest is the baseline; live row counts
are not, because live churns every second.

```sh
DUMP=/live-ro/backups/postgres/daily/$(cat /live-ro/backups/postgres/LATEST)

# 1. parse: does the custom-format header and TOC read back?
pg_restore --list "$DUMP" | tail -5
pg_restore --list "$DUMP" | wc -l          # the manifest's toc_entries must agree

# 2. an empty database of your own, and the restore, timed
createdb -h drill-pg -U drill drill_first
t0=$(date +%s%N)
pg_restore -h drill-pg -U drill --dbname=drill_first \
  --no-owner --no-acl --exit-on-error -j 4 "$DUMP"
echo "pg_restore rc=$? elapsed_ms=$(( ($(date +%s%N)-t0)/1000000 ))"

# 3. row counts, using the same query shape the backup job uses
PGT="select string_agg(format('select %L::text as t, count(*) as n from %I.%I', tablename, schemaname, tablename), ' union all ') from pg_tables where schemaname not in ('pg_catalog','information_schema')"
COUNTS="$(psql -h drill-pg -U drill -d drill_first -Atc "$PGT")"
psql -h drill-pg -U drill -d drill_first -AtF' ' -c "$COUNTS" | sort
sed -n '/# rows per table/,$p' "$DUMP.manifest"        # the baseline, to compare against

# 4. the schema fingerprint, three ways: manifest, live, restored
MD5Q="select md5(string_agg(t, chr(10) order by t)) from (select table_name||'.'||column_name||':'||data_type||':'||is_nullable as t from information_schema.columns where table_schema='public') s"
grep ^schema_md5 "$DUMP.manifest"
psql -h drill-pg -U drill -d drill_first -Atc "$MD5Q"

# 5. which schema version was restored
psql -h drill-pg -U drill -d drill_first -c 'table schema_migrations'
```

Pass criteria:

- `pg_restore --list` exits 0. Its output line count is the manifest's `toc_entries`
  (the backup job computes that field as `wc -l` over the whole listing, header
  comment lines included, so it is a few higher than the archive's own
  `TOC Entries:` header field - both describe the same archive).
- every per-table count equals the manifest, with **one documented exception**: a
  queue table can differ by a few rows, because the manifest is counted from the
  live server a fraction of a second *after* the dump was taken, and the crawl is
  writing to `crawl_frontier` continuously. Prove it is churn and not corruption by
  restoring the same dump a second time into a second empty database - the two
  restores must agree with **each other** exactly, and only the manifest may differ:

  ```sh
  createdb -h drill-pg -U drill drill_second
  pg_restore -h drill-pg -U drill --dbname=drill_second --no-owner --no-acl --exit-on-error -j 4 "$DUMP"
  psql -h drill-pg -U drill -d drill_second -AtF' ' -c "$COUNTS" | sort   # identical to drill_first
  ```

- `schema_md5` matches the manifest's. This is the strongest single check: it is an
  md5 over every column name, type and nullability in `public`.
- `schema_migrations` lists the same versions, names and checksums as the live
  database, which is what identifies *which* schema the restore produced.

Live row counts can only be read from inside the namespace (see *Known gaps*):

```sh
kubectl -n lolstats exec lolstats-postgres-0 -- \
  psql --no-psqlrc --no-align -U lolstats -d lolstats -c "$COUNTS"
```

This is a `SELECT` on the live server and nothing else. Do not use it as the
baseline - it is three hours of churn away from the dump by the time you read it.

## Step 3: raw archive restore drill

Three independent proofs. They fail for different reasons, so run all three.

### 3a. The live archive reads back, at the byte level

```sh
# one sha256 manifest of the real parts, taken from the read-only mount
cd /live-ro/raw && find . -type f ! -name '*.tmp' | sort | xargs sha256sum > /drill/evidence/source-sha256.txt
```

Then confirm the parts are *Parquet that the product can read*, not merely present.
`internal/raw` writes Parquet with an internal zstd column codec, so the first four
bytes are `PAR1`; a naive `zstd -d` on one of these files fails with "Unknown frame
descriptor" and that failure means nothing. Parse a sample instead, and check the
`payload_sha256` column against the payload it describes:

```python
# parse_sample.py  (run in the `py` container; needs zstandard + pyarrow)
import glob, hashlib, random, zstandard, pyarrow.parquet as pq
parts = sorted(glob.glob('/live-ro/raw/**/*.parquet.zst', recursive=True))
sample = random.Random(20260917).sample(parts, 24)
for p in sample:
    with open(p, 'rb') as fh:
        assert fh.read(4) == b'PAR1', p
    t = pq.read_table(p)                     # pyarrow reads the column codec itself
    n = 0
    for payload, want in zip(t['payload'].to_pylist(), t['payload_sha256'].to_pylist()):
        got = hashlib.sha256(payload.encode() if isinstance(payload, str) else payload).hexdigest()
        assert got == want, p
        n += 1
    print(f'{p} rows={n} ok')
```

The two negatives matter as much as the positive: a truncated part and a part whose
payload does not hash to `payload_sha256` must both be detected. If they are not, the
check is not a check.

### 3b. A restic round trip of the real archive, into your own repository

The point is to exercise the *job's* path - the same image, the same flags, a real
repository - without needing the live repository, which may not exist yet.

```sh
export RESTIC_REPOSITORY=/drill/restic       # on the drill PVC, never on the live volume
restic init

restic backup \
  --host lolstats-archive --tag raw-archive --one-file-system \
  --exclude '*.tmp' --exclude '*.partial' --exclude-caches \
  /live-ro/raw                               # flags verbatim from the backup-archive CronJob

restic check --read-data                     # reads every pack back; the whole point
rm -rf /drill/restored && mkdir -p /drill/restored && chmod 777 /drill/restored
restic restore latest --target /drill/restored --json | tail -1

# compare, by content
cd /drill/restored/live-ro/raw && find . -type f | sort | xargs sha256sum > /drill/evidence/restored-sha256.txt
# diff the two manifests: every file present in both must have the same hash,
# there must be no file in the source that the restore lacks, and any extra file in
# the restore is one written to the live archive after the snapshot was taken.
```

Files under `*.tmp` must be **absent** from the snapshot: the writer publishes a part
by rename, so a `.tmp` is half a file. `restic ls latest | grep -c 'tmp$'` must be 0.

### 3c. `internal/aggregate` reads the restored archive

The strongest available end-to-end check: run the real binary, from the real image,
over the restored tree, with the same environment the CronJob uses. It must be
run with **no** `LOLSTATS_POSTGRES_DSN`, so it records its build run as a file and
cannot touch any database.

```yaml
# agg-read-job.yaml - a Job, not an exec, because the app image is distroless:
# there is no shell in it to exec into.
# Omit envFrom of lolstats-config (a ConfigMap cannot be referenced across a
# namespace): copy its keys in as literal env instead.
containers:
  - name: aggregate
    image: ghcr.io/erik-schuetze/league-of-legends:<current digest>
    command: ["/lolstats-aggregate"]
    args: ["build", "--raw", "/drill/restored/live-ro/raw", "--agg", "/drill/cmp/restored",
           "--window-days", "1", "--min-cell-n", "5", "--metrics-addr", ""]
    # no LOLSTATS_POSTGRES_DSN anywhere in this pod
```

`--min-cell-n` is set low on purpose: with one day of live data almost every cell
falls under the production floor of 100, and a build that publishes nothing exits
non-zero. The drill is asking "can this binary read this archive and produce a
truthful artifact", so publish the cells and read the status line:

```
build: ok partition=v1/p/16.18/EUW/420/all patch=16.18 cells_total=... cells_published=... matches=...
```

Run the identical job a second time with `--raw /live-ro/raw` and compare the two
trees: the file set and the partition path must match, and the numbers must differ
only by however many matches landed between the snapshot and the live read. A build
that fails, or a tree with a different shape, means the restore is not usable.

## Observed results (2026-09-17 drill, all times UTC)

State of the product when the drill started: namespace `lolstats` 4h32m old; both
CronJobs present and not suspended, both `LAST SCHEDULE <none>`.

| what | measured |
| --- | --- |
| dumps in `daily/` | **1** - `lolstats-20260917T155053Z.dump`, 4,057,341 B, sha256 `5acb0dcd...` |
| dumps in `weekly/` | **0** |
| restic repository | **none** - `/var/lib/lolstats/backups/restic` does not exist, no `lolstats-restic` Secret |
| `pg_restore --list` on the dump | exit 0; the listing is 53 lines = the manifest's `toc_entries: 53`; the archive header itself says `TOC Entries: 42` (the job counts the listing, header comments included) |
| empty Postgres stood up from nothing | **2,512 ms** (Deployment recreated to `pg_isready` true) |
| `pg_restore --exit-on-error -j 4` of the 4.06 MB dump | rc=0, **877 / 1,052 / 1,641 ms** over three runs |
| restore **RTO, database** | **~3.4 s** end to end (stand up + restore), dump read over NFS |
| `schema_md5` manifest / live / restored | `93266586b9c074ce408310e4e47e4f84` **three-way identical** |
| row counts, manifest vs restored | `build_runs 2/2`, `crawl_frontier 59917/59909`, `crawl_seeds 3/3`, `fetch_queue 7376/7376`, `matches 7122/7122`, `schema_migrations 3/3`, `source_toggles 0/0` |
| same dump restored twice into two empty databases | identical counts both times (so the −8 is in the live table, not in the restore) |
| raw archive read through the read-only mount | 1,046,058,161 B; 595 `*.parquet.zst` counted at that moment, 1 in-flight `*.tmp` (the tree grows continuously - the sha256 manifest below counted 576) |
| sha256 of the whole archive (576 files: 557 `match-v5` + 19 `league-v4`) | **10,576 ms** (measured in the drill session; only the resulting manifest was archived, not its timing output) |
| Parquet sample parsed | 24 parts, 455 rows, **455/455 `payload_sha256` verified** |
| `restic backup` of the real archive | 578 files / 967.873 MiB, **4.63 s**, 1,015,100,407 B read, 144,821,934 B stored (≈7:1) |
| `restic check --read-data` | **10/10 packs read, no errors**, ~2 s |
| `restic restore` into an empty target | 586 files / 1,014,888,805 B, **1 s** |
| restored vs source by sha256 | **576/576 identical, 0 differing, 0 missing, 2 extra** (`part-00558`, `part-00559`, written after the manifest) |
| `*.tmp` in the snapshot | **0** - the exclusion works on real data |
| aggregate build over the **live** archive | `build: ok ... cells_total=379 cells_published=202 matches=383` |
| aggregate build over the **restored** archive | `build: ok ... cells_total=378 cells_published=200 matches=371`, 559 parts planned, 27.6 s end to end |
| published artifact trees, live vs restored | the same 180 output files (173 champion JSONs plus the partition and manifest files), values differing only in the rates that move with the 12 matches ingested between snapshot and read |

**RPO - what a restore would actually lose.** The newest restorable database state
is `2026-09-17T15:50:53Z`. At the time of the drill that was 84 minutes old, and
`matches` had grown from 7,122 to 10,840 (+52%) and `crawl_frontier` from 59,917 to
88,121 since. Those are the crawl's own regenerable queue rows, so the honest framing
is: the *durable* loss is small today only because the product is young and the crawl
can re-queue its own work - but the mechanism that keeps that true is the nightly
CronJob, which has never fired, so the real RPO is "however long since the last
hand-run dump, and unbounded the first night nobody runs one".

For the raw archive there is no RPO to state: no snapshot exists, so the loss would
be total and permanent. The archive is the only tree that cannot be regenerated
(Riot expires payloads), which makes this the single largest exposure in the project.

## Known gaps and risks

Written down deliberately, because the drill's value is in what it found.

1. **The raw archive has never been backed up.** No `lolstats-restic` Secret, no
   repository directory, `backup-archive` has never fired. Step 3b proves the
   *mechanism* works on *this* archive; it does not make a snapshot of it exist.
   Until a snapshot is taken, `restore-raw.md`'s procedure has nothing to restore.
2. **`backup-postgres` has never fired either.** The one dump is a hand-run artifact
   (its Job object is already gone from the history). One dump also means the
   retention/prune path has never run against a real tree.
3. **One restoration point, no older one.** `weekly/` is empty, so there is nothing
   to fall back on if the newest dump turns out to be the bad one.
4. **No off-site copy (risk R6, accepted and unmet).** The configured repository
   path is on the same NFS export as the archive, and this drill's repository was on
   a longhorn PVC in the same cluster. Both prove restic round-trips; neither is a
   second failure domain.
5. **`.globals.sql` contains a credential.** It holds `CREATE ROLE lolstats` with a
   SCRAM password hash. Treat the file as a secret; quote only its sha256.
6. **The committed automated drills are synthetic.** `make backup-verify` and
   `make archive-verify` never open the real dump or the real archive, so they cannot
   detect a missing or stale backup. This runbook is the half that reads the real
   artifacts, and it is a human procedure on purpose.
7. **Namespace isolation cuts both ways.** `default-deny-all` is why a drill pod
   cannot reach the live database, and also why there is no in-cluster way to compare
   restored counts to live counts from outside `lolstats`.
8. **A queue table's row count is not a stable check.** `crawl_frontier` was 8 rows
   below the manifest here. The reproducible check is the pair
   (manifest `schema_md5`, a second restore agreeing with the first), not the count.
9. **The drill target is `ReadWriteOnce`.** Two drill pods needing `drill-data` at
   the same time fail with `Multi-Attach error` if they land on different nodes;
   serialize the drill's jobs or pin them with a `nodeSelector`.
10. **The app image is distroless.** No shell to exec into, so the aggregate half of
    the drill is a Job, and a ConfigMap in `lolstats` cannot be referenced from the
    drill namespace - copy its keys in as literal env.

## Cleanup

```sh
kubectl delete namespace lolstats-restore-drill
kubectl get namespace lolstats-restore-drill            # expect NotFound
kubectl -n lolstats-restore-drill get pvc,pods          # expect empty
```

Then prove the live product is untouched. Capture this pair *before* the drill and
again after it, and diff them:

```sh
kubectl -n lolstats get pvc,pods -o wide
kubectl -n lolstats exec lolstats-postgres-0 -- psql -U lolstats -d lolstats -Atc 'select count(*) from matches'
kubectl -n lolstats get secret,deploy,cronjob
```

The PVC list, the PV ids, the StatefulSet, the secrets and the CronJobs must be
byte-identical. The pod list legitimately differs (other work happens in that
namespace); what must still be there is `lolstats-postgres-0`, the web and ingest
pods, and any probe pod another operator owns - do not delete someone else's pod
just because it looks like drill scaffolding.

Leave nothing of yours behind: no namespace, no PVC, no hostPath directory, no
repository. A drill that leaves a copy of the dump on a volume nobody remembers is
a new leak, not a rehearsal.

## Cadence

- After any change to `backup-postgres`, `backup-archive`, their images, their flags
  or their volume.
- Before any launch gate that claims "restored from backup in a test" - that claim
  is only true for the artifact and the date you drilled.
- Quarterly, at minimum, and when the dump or the archive changes shape (schema
  migration, a new region, a new partition layout).
- Immediately after any real restore, because a restore is the only fully honest
  rehearsal there is.

Record the result of each run: date, dump name and sha256, the counts table, the
measured RTO, and anything in *Known gaps* that changed. The gap list shrinking over
time is the point.

## Addendum, 2026-09-17 (operations lane): what changed since this drill

Appended, not rewritten: the drill above is dated and still correct for the state it
observed. Everything below was observed after it, and three of its findings have
moved.

### Why the two CronJobs had never fired: arithmetic, not a defect

Both were created `2026-09-17T12:43:04Z`. `backup-postgres` runs `30 4 * * *` and
`backup-archive` `0 5 * * *`, both `Europe/Berlin` (CEST, UTC+2 until late October),
so their daily fire times are 02:30Z and 03:00Z - **both were already in the past
when the objects were created**. Neither is suspended, `startingDeadlineSeconds` is
600, and Kubernetes does not back-fill a schedule that old, so the controller does
the only correct thing and waits for tomorrow. That the hourly `maintain` CronJob in
this namespace shows a recent `LAST SCHEDULE` proves the scheduler itself is healthy.
Next fire: 2026-09-18 04:30/05:00 CEST. Nothing needed fixing here; the manifests
were never the problem, and `scripts/backup-status.sh` now says so in one line
instead of leaving `LAST SCHEDULE <none>` to be read as a defect.

### Both jobs have now completed, by hand, with visible output

`kubectl -n lolstats create job <name> --from=cronjob/<cronjob>` for each, both
`succeeded=1/failed=0`:

| job | duration | output |
| --- | --- | --- |
| `backup-postgres-prove-1` | ~16 s | `backups/postgres/daily/lolstats-20260917T183639Z.dump`, `done, 7844722 bytes`, sha256 `c12a7e0ef3d787be06d92107cdb3aa286bcffde810d30162e4d0fb102f1d6711`, 53 TOC entries |
| `backup-archive-prove-1` | ~31 s | repository created at `/var/lib/lolstats/backups/restic`, `783 new files, 1.296 GiB`, snapshot `63eaf09e` |

This is the distinction the launch gate turns on: **the manual one-off is proven;
the scheduled fire had not happened when the drill ran and its first opportunity is
2026-09-18 04:30/05:00 CEST.** A YAML review cannot tell those apart, which is why
`scripts/backup-status.sh` exists: it reads the artifacts, the `LATEST` marker, the
dump's manifest (`schema_md5` `93266586b9c074ce408310e4e47e4f84`, plus the row
counts: `matches` 14502, `crawl_frontier` 114712, `fetch_queue` 14853, `build_runs`
10, `crawl_seeds` 3, `schema_migrations` 3, `source_toggles` 0) and the newest
snapshot, and it fails loudly on an empty result.

### Status of the gaps this drill listed

1. **Closed** for the archive: snapshots exist (`63eaf09e`, `7220621a`, `ccafc2f5`,
   802 files / 1.329 GiB). The sentence in the list above is now historical.
2. **Half closed** for Postgres: a real dump exists and the job is proven. Retention
   and prune still have not run against a real tree - that path is Sunday's, and
   `weekly/` stays empty until then.
3. **Unchanged**: one restoration point per artifact, no weekly yet.
4. **Unchanged and still the gate: no off-site copy (R6).** Surveyed with evidence;
   `docs/runbooks/offsite-options.md`. Blocked on the owner naming a destination.
5. **Unchanged**: `.globals.sql` carries a credential hash. Quote only its sha256.
6. **Narrowed**: `scripts/backup-status.sh` now reads the real artifacts and exits
   non-zero on a missing or stale one, and `scripts/offsite-verify.sh` proves a
   remote round-trip. Neither replaces this runbook, and both are still instruments
   rather than a rehearsal.
7. **Unchanged**: the namespace policies are the reason the drill needs its own
   namespace.
8. **Unchanged**, and confirmed: `crawl_frontier` moved under the drill's feet.
9. **Unchanged.**
10. **Unchanged.**

### Alerts: still delivered nowhere

`kubectl get alertmanager -A` returns **No resources found**, and the Prometheus CR
`prometheus-persistant` in namespace `monitoring` has `alerting: {}` - so the nine
rules in `homecluster/monitoring/prometheus/lolstats-rules.yaml` are loaded and
evaluated and their firings go nowhere. An opt-in Alertmanager bundle exists in
`homecluster/monitoring/alertmanager/` and is deliberately not applied by ArgoCD;
`docs/runbooks/enable-alert-delivery.md` is the switch. This is an unmet gate of the
same kind as R6: recorded, not hidden.



