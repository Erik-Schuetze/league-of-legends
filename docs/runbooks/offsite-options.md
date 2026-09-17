# Off-site backup: the decision that is not mine to make

**Status: BLOCKED on an owner decision.** Plan risk R6 is accepted and unmet. This
page records the survey, the arithmetic, and the exact steps for each option so
that the day a destination is named, pointing the nightly job at it is a
configuration change and not a rewrite.

Written 2026-09-17 by the operations lane, replacing guesswork with evidence.
`docs/runbooks/restore-raw.md` is the runbook; this is the options paper.

## What the plan requires, and what exists

The plan calls the raw-archive copy to external media or a second machine, with a
tested restore, a **launch gate**. The plan's own open question 3 - "does off-site
backup media exist?" - is still unanswered, and the survey below found no
destination that leaves the premises.

What exists today, and is working:

| item | state |
| --- | --- |
| `backup-postgres` CronJob, `30 4 * * *` Europe/Berlin | manifests render, Job proven by hand (`succeeded=1`), first scheduled fire 2026-09-18 04:30 CEST |
| `backup-archive` CronJob, `0 5 * * *` Europe/Berlin | same, with a real snapshot taken by a hand-run Job: `7220621a`, 797 files, 1.321 GiB |
| repository location | `/var/lib/lolstats/backups/restic` - a path on the **same NFS export as the archive**. This is a guard against a bad write, an accidental `rm` and file corruption. It is not a guard against losing the export. |
| repository password | Secret `lolstats-restic` in `lolstats`, created out of band. The only copy of the value is off-cluster (see `docs/runbooks/restore-raw.md`). |
| remote destinations | **none.** |

Every statement above was observed, not inferred: `sh scripts/backup-status.sh`
prints the freshness of both artifacts and names the repository as non-off-site.

## The survey (what is actually reachable)

| candidate | evidence | leaves the premises? | cost |
| --- | --- | --- | --- |
| NAS/dev box `atlas`, `192.168.10.100` | reported to Prometheus as instance `atlas` (`homecluster/monitoring/node-exporter/service-monitor.yaml`); it is also the NFS server of the `lolstats-data` volume, and Longhorn's `BackupTarget/default` is `nfs://atlas.hive:/longhorn-backups`, where a daily RecurringJob already writes `Completed` backups | **no** - same building, and the same machine that serves the live volume | 0 EUR |
| Hetzner Cloud | `HETZNER_CLOUD_API_TOKEN` in `hetzner-ddns` is a DNS-capable Cloud API token: `/v1/servers`, `/v1/volumes`, `/v1/floating_ips` all return zero objects, and `/v1/storage_boxes` is 404 on that API | would, if storage existed - it does not | any Hetzner Storage Box or Object Storage is a **new purchase**, which breaks the 0 EUR/month budget (BX11 1 TB ≈ 3.20 EUR/mo; Object Storage ≈ 6.49 EUR/mo) |
| Cloudflare R2, free tier | 10 GB storage, free egress, S3-compatible | yes | 0 EUR, may require a card on file |
| Backblaze B2, free tier | 10 GB storage, S3-compatible | yes | 0 EUR |
| any S3/MinIO already in the cluster | none: no object-store Service in any namespace | n/a | - |
| a USB disk rotated by hand | nothing to buy if the owner has one | yes, if it leaves the house | 0 EUR recurring; one-off hardware if not owned |

**Sizing, because 10 GB is not infinite.** The archive is 1.321 GiB across 797
files. The first snapshot stored **189.6 MiB** compressed and deduplicated; the
second, taken 6 minutes later, added **3.6 MiB** because 783 of 797 files were
unchanged. A 10 GB free tier therefore holds months of daily snapshots with
headroom - but only while the daily *change* stays small. A backfill that rewrites
the whole tree would add roughly 190 MiB per such day. Watch `restic stats` and the
repository size that `scripts/backup-status.sh` prints.

## What the mechanism already supports (proven, not promised)

`deploy/base/jobs/backup-archive.yaml` takes the destination from the Secret, so
choosing one is a Secret edit and one egress rule:

- `RESTIC_REPOSITORY_OVERRIDE` (in `lolstats-restic`) wins over the ConfigMap
  default **by name**, so it does not depend on `envFrom` ordering.
- every key of the Secret arrives as an environment variable, so `AWS_ACCESS_KEY_ID`
  / `AWS_SECRET_ACCESS_KEY` (S3, R2, B2) need no manifest change at all.
- `RESTIC_EXTRA` is prepended to every restic invocation, which is how an `sftp`
  host passes `-o sftp.command=ssh -i /etc/restic/id_ed25519 ...`. That key
  material, if it is ever needed, mounts at `/etc/restic` (already wired, mode 0440).
- **an egress rule is required.** `deploy/base/network/default-deny.yaml` denies all
  egress in `lolstats` and `allow.yaml` permits only DNS, Riot over 443, and
  in-namespace traffic. Without a rule selecting `app.kubernetes.io/component:
  backup-archive` on the destination's port (443 for S3, 22 for sftp) the job hangs
  and then times out. `allow.yaml` is another lane's file; this lane may not edit it.

Proof that the mechanism works against a remote destination:
`sh scripts/offsite-verify.sh --docker` builds a real S3-compatible endpoint, moves
the tree there with credentials supplied only as environment variables, restores it
into scratch and compares every file by sha256 - **195/195 identical**, and
`restic check --read-data-subset=25%` reports no errors. It proves the transport and
the credential path; it does not prove that a copy off the premises exists, and it
says so in its own output.

## The steps, once a destination is chosen

```sh
# 1. the secret. Nothing in git ever holds a real credential.
kubectl -n lolstats patch secret lolstats-restic --type merge -p '{"stringData":{
  "RESTIC_REPOSITORY_OVERRIDE":"s3:https://<account>.r2.cloudflarestorage.com/lolstats-restic",
  "AWS_ACCESS_KEY_ID":"...","AWS_SECRET_ACCESS_KEY":"..."}}'

# 2. one egress rule in deploy/base/network/allow.yaml for component backup-archive
#    on 443 (s3) or 22 (sftp). Another lane owns that file.

# 3. prove the transport and the restore before trusting it
sh scripts/offsite-verify.sh --docker      # the S3 code path, locally
kubectl -n lolstats create job backup-archive-offsite-1 --from=cronjob/backup-archive
kubectl -n lolstats logs -f job/backup-archive-offsite-1     # expect "repository is remote"

# 4. then the gate itself: a restore from the off-site repository into scratch,
#    compared byte for byte to the live archive. docs/runbooks/restore-raw.md,
#    "When off-site media appears", is the checklist.

# 5. only then is risk R6 met, and the acceptance paragraph in restore-raw.md
#    should be replaced by the observed result and the date.
```

## What the owner has to decide

1. **A destination that leaves the premises.** `atlas` at `192.168.10.100` is 0 EUR
   and is a second physical box with room, but it is in the same building and it
   *serves the live volume* - a fire, a theft or a power event takes both. It is
   better than nothing and worse than the gate. If the owner accepts it, say so
   explicitly and record R6 as *partially* mitigated, not met.
2. **Whether a paid tier is acceptable.** Cloudflare R2 and Backblaze B2 free tiers
   are 0 EUR and genuinely off-site; Hetzner storage costs 3.20-6.49 EUR/month and
   therefore breaks the stated budget. The owner's call, not this lane's.
3. **A credential.** R2 and B2 both need an account and an API token; R2 may want a
   card on file even for the free tier. That is owner information, and it must go
   into the Secret, never into a manifest.

Until one of these is answered, **the off-site restore gate is blocked**, not
pending: nothing this lane can do makes a copy exist.
