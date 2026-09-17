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

## When to use it

- Files under `/var/lib/lolstats/raw` were deleted or truncated.
- The archive is intact but you need one match payload back.
- You are proving the repository can be restored. This is a launch gate, and it
  is the one action that converts "we have a snapshot" into "we have a backup".

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
   the restore drill above against it. A repository that has never been restored
   from is not yet a backup, and until this step succeeds R6 stays unmet even
   though the bytes are finally somewhere else.
6. **Record it.** Update `deploy/README.md` (section Backups) and this runbook to
   say R6 is met, with the date and the medium. Optionally keep the local path as
   a second copy with `restic copy` (it needs both passwords, via
   `RESTIC_FROM_PASSWORD`/`RESTIC_TO_PASSWORD`), which costs only the chunks the
   two repositories do not already share.

Nothing in step 1-6 requires a schema change, a new image, or a change to any
manifest other than `deploy/base/backup/config.yaml`, the CronJob's `env:` list
and one NetworkPolicy. The two edits outside this workstream's ownership are the
NetworkPolicy and, if the medium is a new machine, that machine's own setup.
