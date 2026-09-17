# Runbook: restore the raw archive (restic)

Covers the raw archive on the `lolstats-data` volume - `LOLSTATS_RAW_ROOT`,
`/var/lib/lolstats/raw`, the match payloads and timelines the aggregate build
reads. It is the only tree in this project that cannot be regenerated: Riot keeps
match payloads for two years and timelines for one, and once they are gone the
aggregate that came from them cannot be rebuilt. The control plane has its own
runbook (`restore-postgres.md`) and the derived trees have theirs
(`rebuild-aggregates.md`).

The snapshot is taken by the `backup-archive` CronJob at 05:00 Europe/Berlin with
the official `restic/restic` image, pinned by digest:

```
restic backup --host lolstats-archive --tag raw-archive --one-file-system \
  --exclude '*.tmp' --exclude '*.partial' --exclude-caches /var/lib/lolstats/raw
```

Retention is applied on Sundays only (`restic forget --keep-daily 7
--keep-weekly 4 --keep-monthly 6 --prune`), and the Sunday run also reads a 2%
sample of the pack files back (`restic check --read-data-subset=2%`). Weekend
runs are the expensive ones on purpose: the night a backup exists to shorten is
not the night to rewrite a repository.

## READ THIS FIRST: risk R6 is accepted and unmet

`RESTIC_REPOSITORY` defaults to `/var/lib/lolstats/backups/restic` - **a
directory on the same ReadWriteMany NFS export that holds
`/var/lib/lolstats/raw`**. There is no off-site medium and no second machine
(plan open question 3, still unanswered).

What that copy does protect against: an accidental `rm`, a bad crawl that
overwrites good parts, a corrupted file, a mistake in an aggregate or site build
(which never touch `raw/` anyway). What it does **not** protect against: the
`lolstats-data` volume being lost, the NFS export (Tier 2 storage on
`bee01`-`bee03`) being lost, or a node or the cluster dying in a way that takes
the export with it. In those cases the archive and its only snapshot go together.

R6 is a launch gate in plan section 14. Until media exists it is
**accepted and unmet**, and the gate does not pass. The job says so in its own
log every night:

```
backup-archive: NOTE: RESTIC_REPOSITORY=/var/lib/lolstats/backups/restic is a path on the shared volume.
backup-archive: NOTE: that is a NON-off-site copy - the same NFS export as the archive.
backup-archive: NOTE: plan risk R6 stays accepted and unmet while this is true.
```

## The committed drill, and what it does and does not prove

`make archive-verify` (`scripts/archive-verify.sh`) proves the restic half of the
launch gate mechanically, on any machine with Docker, with no cluster access and
no pre-existing repository: it builds a synthetic raw archive in the layout
`internal/raw` documents, initialises a repository with the digest this runbook
and the CronJob pin, backs up with the job's exact flags, proves a wrong password
cannot open the repository, runs the job's Sunday branch (`forget --prune`,
`check --read-data-subset=2%`) and then a full `check --read-data`, restores into
an empty directory and compares the archive and the restore by sha256 manifest.
It then proves that comparison can fail - a changed byte in a restored file, a
file the snapshot never held, and a recorded file missing from the restore - and
finally runs the nightly backup a second time over a grown archive, because the
incremental snapshot is the one that has a parent to fall back on.

What it proves: the repository format survives a round trip through the tool that
will perform the real restore, that the excludes keep half-written parts and
cache directories out of the snapshot, that retention does not break the
repository, and that the comparison used to assert all of that is capable of
failing. What it does not prove: that the archive on the `lolstats-data` volume
restores. The corpus is synthetic, so it also measures nothing about the archive's
size or growth. That part of the gate is the rest of this runbook, run by a human
against the live repository.

## When to use it

- Files under `/var/lib/lolstats/raw` were deleted or truncated.
- The archive is intact but you need one match payload back.
- You are proving the repository can be restored. This is a launch gate, and it
  is the one action that converts "we have a snapshot" into "we have a backup".
  The reproducible half of it is `make archive-verify`, which needs no cluster;
  this runbook is the half that has to touch the live repository, because only a
  restore from *this* repository proves *this* repository restores back to the
  archive it came from.

## The repository shell

No long-running pod carries a `restic` binary: the Go workloads run a distroless
image and the web tier runs Caddy. Every restic command below therefore happens
in a throwaway pod built from the same image the CronJob uses. Write this out and
apply it; `kubectl create job --from=cronjob/backup-archive` cannot help here
because it can only run the CronJob's own command line:

```yaml
# restic-shell.pod.yaml
apiVersion: v1
kind: Pod
metadata:
  name: restic-shell
  namespace: lolstats
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
    - name: restic
      image: restic/restic:0.19.1@sha256:136600b6ff6843d61d355f7f71f460a166429f35de6fd11b568fece3c9a4d510
      command: ["/bin/sh", "-c", "sleep 86400"]
      envFrom:
        - configMapRef:
            name: lolstats-backup-config
      env:
        - name: RESTIC_PASSWORD
          valueFrom:
            secretKeyRef:
              name: lolstats-restic
              key: RESTIC_PASSWORD
        - name: HOME
          value: /tmp
      securityContext:
        allowPrivilegeEscalation: false
        capabilities:
          drop: ["ALL"]
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

```
kubectl -n lolstats apply -f restic-shell.pod.yaml
kubectl -n lolstats exec -it restic-shell -- /bin/sh
# ...and when you are finished, this is not optional:
kubectl -n lolstats delete pod restic-shell
```

The pod is not tracked by ArgoCD, so `prune: true` will not clean it up. It runs
as uid 65532 like everything else here, because the namespace enforces the
restricted Pod Security profile, and it runs as 65532 rather than root so it can
read a repository whose files the CronJob wrote with `umask 077`.

## Prove the snapshots are readable and complete

Inside the shell pod:

```
restic snapshots --host lolstats-archive --tag raw-archive --last
restic stats latest --host lolstats-archive
restic check --read-data-subset=5%
```

`restic check` without `--read-data` verifies the repository structure only -
that is what the weekly run's 2% sample improves on, and a 5% sample here is the
same idea with more coverage. Neither proves the *contents* are a usable tree;
only a restore does.

## Restore

Restore into a staging directory inside the same volume and compare there. Never
restore over `/var/lib/lolstats/raw` in place: restic writes paths as it goes, so
a restore that fails halfway leaves a tree that is neither the old one nor the new
one, and the old one is the only other copy.

Inside the shell pod:

```
# 1. whole tree. restic recreates the absolute paths it saved, so the tree lands
#    at /var/lib/lolstats/restore-check/var/lib/lolstats/raw.
restic restore latest --host lolstats-archive --target /var/lib/lolstats/restore-check

# 2. or one file, which is the cheap and common case
restic restore latest --host lolstats-archive \
  --include /var/lib/lolstats/raw/matches/EUW/2026/09/17 \
  --target /var/lib/lolstats/restore-check

# 3. look before you leap
du -sh /var/lib/lolstats/restore-check/var/lib/lolstats/raw
find /var/lib/lolstats/restore-check/var/lib/lolstats/raw -type f | wc -l
```

Then, only once the staged tree looks right, swap it into place. `mv` inside one
filesystem is a rename, and `/var/lib/lolstats` is a single mount:

```
mv /var/lib/lolstats/raw /var/lib/lolstats/raw.lost-$(date -u +%Y%m%dT%H%M%SZ)
mv /var/lib/lolstats/restore-check/var/lib/lolstats/raw /var/lib/lolstats/raw
rm -rf /var/lib/lolstats/restore-check
```

Keep the `.lost-` directory until the aggregate tree has been rebuilt from the
restored archive and the site looks right, then delete it - it is the only copy
of the difference, and the volume is 50 Gi shared with everything else, so it
cannot be kept forever.

## How to tell it worked

- `restic snapshots` lists at least one snapshot for host `lolstats-archive`.
- The staged tree has roughly the same file count and size as the live one did.
  For the exact version of this check, `make archive-verify` compares the archive
  and the restore by sha256 manifest, both directions, and proves it can fail.
- `lolstats-aggregate verify` passes against `agg/` after a rebuild (see
  `rebuild-aggregates.md`), and a page that reads a match shows real numbers.
- The next `backup-archive` run reports a new snapshot and does not print the
  `no snapshot exists` fatal.
- `kubectl -n lolstats logs job/backup-archive-<stamp>` shows no `FATAL` line.

## Roll back

`mv` the `.lost-` directory back over `raw/`. If there is no `.lost-` directory,
you have not yet overwritten anything: the staged restore is inert and can simply
be deleted. The snapshot itself is never modified by a restore - `restic restore`
only reads, and nothing in this runbook prunes.

## What is destructive here

- `mv /var/lib/lolstats/raw /var/lib/lolstats/raw.lost-...` is destructive only
  in the sense that the live path changes; the bytes survive under the new name.
- `rm -rf /var/lib/lolstats/restore-check` deletes the staged copy.
- `restic forget --prune` deletes snapshots. It runs from the CronJob on Sundays
  and is deliberate; do not run it by hand while investigating.
- Restoring a *partial* tree (`--include`) over an incomplete live tree leaves
  holes: restic restores what you asked for and nothing else, so a whole-tree
  case needs a whole-tree restore.
- `LOLSTATS_RAW_ROOT` is what both the job and the binaries read. If a restore is
  staged somewhere else, nothing will find it until the tree is back at that path.

## When off-site media appears

This is the whole of the work, in order. Steps 1-4 are configuration; step 5 is
the part that makes it a backup.

1. **Pick the medium.** A second machine over SFTP
   (`RESTIC_REPOSITORY=sftp:user@host:/srv/restic/lolstats`) is the smallest step
   from here and needs no object-store account; an S3-compatible endpoint
   (`s3:https://...`) works too and costs whatever the endpoint costs, which on a
   0 EUR/month budget is a decision for the owner. A USB disk exported over SFTP
   by another machine in the homelab counts: it is off the NFS export, which is
   the property that matters.
2. **Give the job its credentials.** Add the keys to the `lolstats-restic`
   Secret (`kubectl -n lolstats patch secret` or recreate it - keep the existing
   `RESTIC_PASSWORD`, and remember a lost password makes every existing snapshot
   unreadable), and reference the extra keys as environment variables on the
   `backup-archive` CronJob's container in `deploy/base/jobs/backup-archive.yaml`:
   `RESTIC_PASSWORD_FILE`/an SSH key path for sftp, `AWS_ACCESS_KEY_ID` and
   `AWS_SECRET_ACCESS_KEY` for the s3 backend. The file already carries the
   `RESTIC_PASSWORD` reference; the new ones go beside it.
3. **Point the repository at the medium.** Change `RESTIC_REPOSITORY` in
   `deploy/base/backup/config.yaml` to the remote URL. That one line is the whole
   of the switch; nothing else in the manifest tree describes the destination.
4. **Open the egress.** `deploy/base/network/default-deny.yaml` denies all
   egress in this namespace, and the only outside flows allowed are DNS, Riot
   over 443 for the four ingest components, and in-namespace 5432/9090/8080.
   `backup-archive` is not in that list, so an off-site repository will hang and
   then time out rather than fail with a useful message. Add an egress rule in
   `deploy/base/network/allow.yaml` selecting
   `app.kubernetes.io/component: backup-archive` and allowing TCP 22 (sftp) or
   443 (s3) - and only the port the medium actually uses.
5. **Run it, then restore from it.** Run the job by hand
   (`kubectl -n lolstats create job backup-archive-first
   --from=cronjob/backup-archive`), read the log, confirm with `restic snapshots`
   from the shell pod that a snapshot exists *at the new repository*, and then run
   the restore above against it. `make archive-verify` cannot stand in for this:
   it drives a repository of its own creation, so it says nothing about whether
   the new medium holds a readable snapshot. A repository that has never been
   restored from is not yet a backup, and until this step succeeds R6 stays unmet
   even though the bytes are finally somewhere else.
6. **Record it.** Update `deploy/README.md` (section Backups) and this runbook to
   say R6 is met, with the date and the medium. Optionally keep the local path as
   a second copy with `restic copy` (it needs both passwords, via
   `RESTIC_FROM_PASSWORD`/`RESTIC_TO_PASSWORD`), which costs only the chunks the
   two repositories do not already share.

Nothing in step 1-6 requires a schema change, a new image, or a change to any
manifest other than `deploy/base/backup/config.yaml`, the CronJob's `env:` list
and one NetworkPolicy. The two edits outside this workstream's ownership are the
NetworkPolicy and, if the medium is a new machine, that machine's own setup.

## Addendum, 2026-09-17 (operations lane): what is true now

Read this with the two sections above, not instead of them. Three things in them
have been overtaken, and one of them was wrong.

**State of the repository, observed, not expected:**

```sh
sh scripts/backup-status.sh          # the whole picture, exit code 0 only if all of it is fresh
kubectl -n lolstats get cronjob backup-postgres backup-archive
kubectl -n lolstats get secret lolstats-restic     # exists now: 17 Sep
```

- The `lolstats-restic` Secret exists (created out of band; its value is stored
  off-cluster, because a lost password makes every snapshot unreadable).
- Snapshots exist: `63eaf09e` (2026-09-17T18:36Z), `7220621a` (18:42Z), `ccafc2f5`
  (18:44Z) - 802 files, 1.329 GiB in the tree, ~190 MiB stored. This runbook now
  has something to restore.
- The repository is still `/var/lib/lolstats/backups/restic`, a path on the same
  NFS export as the archive. **R6 stays accepted and unmet.** There is still no
  destination that leaves the premises; `docs/runbooks/offsite-options.md` is the
  options paper, and the gate is blocked on an owner decision, not on engineering.

**Steps 2 and 3 are now easier than written, and step 5 was never wrong.**

- Step 2: extra keys are no longer referenced one by one on the CronJob. The
  container takes **every** key of the Secret with `envFrom`, so adding
  `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` (S3, R2, B2) to the Secret is the
  whole of the credential work. An `sftp` host needs no SSH key at all if it has a
  password; if it needs one, the Secret already mounts at `/etc/restic` (0440) and
  `RESTIC_EXTRA` carries the `-o sftp.command=...`.
- Step 3: `RESTIC_REPOSITORY` in `deploy/base/backup/config.yaml` no longer has to
  change. Set `RESTIC_REPOSITORY_OVERRIDE` in the Secret instead; it wins by name,
  so no manifest edit and no dependency on `envFrom` ordering. The `allow.yaml`
  egress rule from step 4 is **still required** and still belongs to another lane.
- Step 5's `kubectl create job --from=cronjob/backup-archive` is correct and is what
  this lane used to take the snapshots above; it inherits the security context, the
  env, the volume and the node affinity, so it is the CronJob's own command rather
  than an approximation of it. It *writes* a snapshot into the live repository -
  that is the point of it - which is why `docs/runbooks/restore-drill.md`'s table
  forbids it inside a drill whose invariant is "nothing was written". The two rules
  do not contradict each other once the different invariants are named.

**Proving the off-site mechanism works before a destination exists:** run
`sh scripts/offsite-verify.sh --docker`. It stands up a real S3-compatible
endpoint, moves a tree there with credentials supplied only as environment
variables, restores it and compares every file by sha256. It proves the transport,
and it prints that it does **not** prove the off-site property.

## State observed on the cluster, 2026-09-18 00:15 CEST

Measured with `sh scripts/backup-status.sh --all-snapshots`, not read off the
manifest - the difference matters, because until 2026-09-17 the honest answer to
"does a repository exist?" was *no*, and the YAML said otherwise:

| what | observed |
| --- | --- |
| repository | `/var/lib/lolstats/backups/restic` - **exists and reads** (228.3 MB on the volume) |
| snapshots | **5** for host `lolstats-archive`: `63eaf09e` 18:36:40Z 1.296 GiB, `7220621a` 18:42:57Z 1.321 GiB, `ccafc2f5` 18:44:49Z 1.329 GiB, `5e3c8217` 18:45:40Z 1.331 GiB, `aee7bcb0` 22:10:00Z 2.090 GiB |
| source tree | `/var/lib/lolstats/raw`, 803 files unmodified between two runs, 455 new files in the 22:10 run |
| newest | 2026-09-17 22:10:00Z, tag `raw-archive`, host `lolstats-archive` |

Three things about that table are easy to get wrong:

- **Who created the repository:** the job itself. `backup-archive` runs
  `restic cat config` and, if that fails, prints the failure and runs
  `restic init`. So the first successful run of the CronJob *is* how the
  repository comes into being; there is no separate bootstrap step, and a job
  that has never run has a repository that has never existed. That is exactly
  what the drill lane saw before this run, and it is why "no repository" and
  "job never fired" are one finding rather than two.
- **`1 snapshots` in the default output is a number `--latest 1` printed, not
  the number the repository holds.** Use `--all-snapshots` when the question is
  retention ("is this accumulating one snapshot per night?") rather than
  freshness.
- **Where the snapshots land is not off-site, and it is not a second failure
  domain either.** The repository is a directory on the `lolstats-data` PVC:
  `ReadWriteMany`, PV `pvc-67c695c9-68af-4100-a4a6-606b07d9bb33`, backed by
  `192.168.10.100:/nas-main/k3s-volumes/lolstats-lolstats-data-pvc-...` with
  `reclaim=Delete`. Two consequences worth stating plainly:

  1. it *does* leave the node, so losing `bee01`/`bee02`/`bee03` is survivable;
  2. it does **not** leave the machine holding the live archive, and it is the
     same host (`atlas`, 192.168.10.100) that serves Longhorn's
     `nfs://atlas.hive:/longhorn-backups`. Losing that export loses the live
     archive, the Postgres dumps and every snapshot at once. That is risk R6,
     and it is why the snapshot being recent is not the same claim as the
     backup being safe.
