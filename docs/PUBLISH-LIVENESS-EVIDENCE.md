# PUBLISH-LIVENESS-EVIDENCE - publish-by-rename reaches the web tier with no restart

Measured 2026-09-17 17:44-17:50 UTC against the live `lolstats` cluster by a verification agent
that owns no production code. Raw transcripts of all three runs, including the failed one, are
quoted below; the instrument is `scripts/verify-publish-liveness.sh`.

**This document records measurements. It changes no product behaviour.**

## 1. The claim, and where in the code it lives

The Phase 6 exit criterion under test: **a new aggregate snapshot that appears on the volume by
`rename(2)` becomes visible to the Go web tier without restarting, redeploying or rolling
anything.**

The implementation it exercises (all read, none modified):

| Requirement | Where | What it does |
| --- | --- | --- |
| Publish is a rename, manifest last | `internal/aggregate/publish.go` `Publish()` | `mkdir` a `.trash-<pid>-<nano>`; `displace()` the live `v1/p/...` dir into `<trash>/old-N`; `os.Rename` the staged dir into place; rename `v1/manifest.json` last; best-effort rollback; `defer RemoveAll(trash)` |
| The reader notices | `internal/webtier/artifacts.go` (`fileCache`) | cache key is `path\|Size\|ModTime.UnixNano()` from `os.Stat` on `<root>/v1/manifest.json`; whenever the key changes the whole `Site` is re-derived. `rename(2)` always installs a new inode, so a new mtime, so a new key |
| The banner and the sitemap both move | `internal/webtier/artifacts.go:380`, `render.go:267`, `view_feeds.go:44` | `site.latest = &site.manifest.Latest`, so `Site.GeneratedAt()` (the rendered banner) is `latest.generated_at`, while `Site.ManifestGeneratedAt()` (sitemap `lastmod`) is the manifest's own `generated_at`. This test asserts **both** moved |
| No symlink, no `current` pointer | `internal/aggregate/paths.go` | readers follow `manifest.json`; nothing has to be re-pointed |

The `fileCache` comment in `publish.go` states the mtime key exists precisely so that a rollout is
not needed. The measurement below is of that claim, not of a restatement of it.

## 2. What was measured, and what was not

| Item | Value |
| --- | --- |
| Cluster / namespace / tier | `lolstats` / `lolstats-go-web` |
| Serving pods | `lolstats-go-web-86c6b9fb86-dn54j`, `lolstats-go-web-86c6b9fb86-jgh8g` |
| Deployment revision | `deployment.kubernetes.io/revision=8`, `generation=8`, uid `68d4362e-c2e0-4869-9b81-46a1bf553401` |
| Image measured | `ghcr.io/erik-schuetze/league-of-legends@sha256:d4e136ddda522238ddc1976afb713173b4c7a1a576a80f28edf3a0e150555f5f` |
| Volume | `pvc/lolstats-data`, NFS-backed RWX, mounted at `/var/lib/lolstats` in the tier, aggregate root `/var/lib/lolstats/agg` |
| Reached through | `kubectl port-forward svc/lolstats-go-web 18820:80` and, for a second opinion, `port-forward pod/lolstats-go-web-86c6b9fb86-dn54j 18821:80` |
| Not measured | the real `build` subcommand end to end (it needs Postgres and the raw archive). The publisher's *effect* was reproduced by an injector that follows `Publish()`'s step order exactly - see §12.4. Also not measured: sub-second latency, and the static Astro/Caddy tier |

Map from the brief's required order of proofs to what was done:

| Brief step | Where |
| --- | --- |
| 1. Baseline: the pointer's sha256, `data-state`, the rendered marker | §4 |
| 2. Positive control first: prove the instrument can observe a change at all | §5 |
| 3. The real publish by rename, tier untouched | §8 |
| 4. Direct evidence that no restart happened (uid, creationTimestamp, restartCount) | §9 |
| 5. Negative control: no publish, nothing changes | §6 |
| 6. Restore exactly, proven by the baseline sha256, zero residue | §10 |

## 3. Facts that reconnaissance had to establish first

The first three of these would each have silently produced a meaningless PASS, so they are recorded:

1. **The tier selector is not `app=lolstats-go-web`.** `kubectl get pods -l app=lolstats-go-web`
   returns nothing. The tier's selector is
   `app.kubernetes.io/component=web-go,app.kubernetes.io/name=lolstats`. Other lanes' probe pods
   (`coord-503-corrupt`, `coord-503-missing`, `coord-503-real`, `coord-preview-posture`) also carry
   `component=web-go`, so the first version of the script happily sampled a *foreign* pod. The full
   deployment selector is now used for every pod list, which also makes the before/after pod
   comparison in §9 compare the right objects.
2. **The manifest is served under `/agg`, not `/`.** `GET /v1/manifest.json` is `404`;
   `GET /agg/v1/manifest.json` is `200`. The HTML banner alone would have been a weaker instrument.
3. **`LOLSTATS_AGG_FIXTURES` decides what is served at all.** `candidateRoots`
   (`internal/webtier/artifacts.go:145-167`) returns *only* the checked-in fixtures when the value is
   `only` (the configMap's value, and the ADR-010 preview posture), *only* the aggregate root when it
   is `off`, and the primary root when unset-and-configured. Revisions 6-8 of the deployment carried
   an operator override `LOLSTATS_AGG_FIXTURES=off`; revision 8 is the revision this measurement ran
   against. The script's `preflight` now refuses to run unless the served root is the aggregate root
   and the tier reports `data-state=live`, so a fixtures-served tier can never be mistaken for a
   proven hot reload.
4. The writer is not reachable as a subcommand. `lolstats-aggregate` offers
   `build|verify|manifest|demo`. `Publish()` is called only from `build` (needs Postgres) and from
   `demo` (which refuses the live root, by ADR-005's source-consistency checks). §12.4 explains what
   the script does instead and what that costs in fidelity.

## 4. Baseline - the exact bytes that had to come back

Taken before anything was written, and independently re-taken after the tier changed underneath us
(§15).

| Item | Value |
| --- | --- |
| `v1/manifest.json` sha256 (on the PVC **and** as served over HTTP) | `3099458535c8544ffab395fa6a6ee5ca645322e4df62aa2ee8142049237ef1a2` (5119 bytes) |
| sha256 over the whole served tree, paths included | `ad16e15429f02197a560cf05aeb1dcff40c66d2986baf208e9b03dbf8368e450`, 180 files |
| sha256 of the rendered `/` HTML body (logged truncated to 12 hex) | `b184fea65aa6` |
| Served state | `data-state="live"`, `source: riot-match-v5`, `generated_at=2026-09-17T15:48:33.230338338Z`, banner `2026-09-17 15:48 UTC`, 1 partition |
| Partition the manifest points at | `v1/p/16.18/EUW/420/all` |
| Scheduled writers | CronJob `lolstats-aggregate` is `0 1 * * *` (~7 h outside the window) and `maintain` is `20 * * * *` (postgres frontier state only). `preflight` also asserts no aggregate job pod is running |

```
== baseline: identity of the pods that are serving, and the exact live bytes
   | lolstats-go-web-86c6b9fb86-dn54j	49e5c278-3077-47ae-9281-56c24b153f27	2026-09-17T17:16:34Z	0	Running
   | lolstats-go-web-86c6b9fb86-jgh8g	a760c94e-a6f4-4033-88ae-965b629eb459	2026-09-17T17:16:29Z	0	Running
   manifest sha256 = 3099458535c8544ffab395fa6a6ee5ca645322e4df62aa2ee8142049237ef1a2
   tree sha256 over 180 files = ad16e15429f02197a560cf05aeb1dcff40c66d2986baf208e9b03dbf8368e450
baseline before               svc-sha=b184fea65aa6 gen=2026-09-17T15:48:33.230338338Z latest=2026-09-17T15:48:33.230338338Z state=live rendered=2026-09-17 15:48 UTC pvc-sha=3099458535c8
   the tier reports data-state=live, so it is reading a riot-match-v5 aggregate root
```

Under the PVC's `agg/` root the injector snapshots all 180 files (bytes and modes) into
`.baseline-publish-liveness` so that later phases can put them back exactly.

## 5. Positive control - proving the instrument can see a change at all

The control came first because **the instrument is the thing most likely to be broken here.** The
tier caches `os.Stat` of `manifest.json` for its own cache key, and the NFS client caches attributes
for the *whole mount*, so "the page did not change" is equally consistent with "the tier is fine but
my reader is blind". A publish that produced no observable change would have been reported as a
NFS-attribute-cache limitation, not as a hot-reload success, and this section is why that did not
happen.

The injector cloned the live snapshot, rewrote the marker to `2026-09-18T04:10:00Z` in
`tierlist.json` and `manifest.json`, ran the real `lolstats-aggregate manifest` writer over the clone
to regenerate the manifest, and then performed the same sequence `Publish()` performs:
displace the live partition into `.trash-<pid>-<nano>/old-0`, rename the staged partition in, **rename the
manifest last**.

```
== POSITIVE CONTROL: publish it and see whether the tier can notice a change at all
   | displaced v1/p/16.18/EUW/420/all -> .trash-89-1789667078138760224/old-0
   | renamed staged v1/p/16.18/EUW/420/all into place
   | renamed staged v1/manifest.json into place (manifest last)
   | published at 2026-09-17T17:44:38Z: v1/manifest.json sha256=8a12e646ea92dd162f6ff6babcc68060e206119e789d2543553468c17f6c9388 size=5089
control  poll+5s              svc-sha=68ed8ede15fc gen=2026-09-18T04:10:00Z latest=2026-09-18T04:10:00Z state=live rendered=2026-09-18 04:10 UTC pvc-sha=8a12e646ea92
   control PASS: within 5s of the rename the tier reported generated_at=2026-09-18T04:10:00Z and rendered '2026-09-18 04:10 UTC'
```

* **Result: PASS.** The serving tier moved from `2026-09-17 15:48 UTC` to `2026-09-18 04:10 UTC` and
  its served manifest sha256 moved `b184fea65aa6` -> `68ed8ede15fc`.
* The criterion is met on the **first** sample taken after the rename - the poll loop samples before
  it sleeps, and its label `poll+5s` names the sample index, not an elapsed wait. So the honest
  bound is "one sample interval, ≤5 s"; the true latency is lower and this test does not resolve it.
* NFS attribute caching therefore did **not** blind the instrument: both a size change (5119 -> 5089
  bytes) and a new inode/mtime propagated inside the default attribute-cache window.

## 6. Negative control (the no-write window) - separating "the publish did it" from drift

Without this, "it changed" could have been any background process. So before judging the real
publish, the script held the system still and watched: 270 s with no writes by anything the script
controls, sampled continuously (49 samples; each iteration costs 5 s of sleep plus one `kubectl exec`
for the PVC sha256, which is why the last sample reads `+245s`):

```
== waiting 270s with no writes, to see what moves on its own
drift    noswrites+5s         svc-sha=68ed8ede15fc gen=2026-09-18T04:10:00Z latest=2026-09-18T04:10:00Z state=live rendered=2026-09-18 04:10 UTC pvc-sha=8a12e646ea92
   ... (48 more, identical)
drift    noswrites+245s       svc-sha=68ed8ede15fc gen=2026-09-18T04:10:00Z latest=2026-09-18T04:10:00Z state=live rendered=2026-09-18 04:10 UTC pvc-sha=8a12e646ea92
```

Nothing moved: same served HTML sha, same manifest `generated_at`, same `latest.generated_at`, same
`data-state`, same rendered banner, same PVC sha256, for the whole window. The tier does not refresh
on a timer and nothing else was writing, so a change observed later can only have been caused by the
publish.

## 7. Reverse control - restoring the bytes moves the tier back

```
== restoring the original bytes, then watching the tier move back
   | restored 180 file(s) at 2026-09-17T17:49:12Z; manifest sha256=3099458535c8544ffab395fa6a6ee5ca645322e4df62aa2ee8142049237ef1a2
reverse  poll+5s              svc-sha=b184fea65aa6 gen=2026-09-17T15:48:33.230338338Z latest=2026-09-17T15:48:33.230338338Z state=live rendered=2026-09-17 15:48 UTC pvc-sha=3099458535c8
   the tier returned to generated_at=2026-09-17T15:48:33.230338338Z within 5s, rendered '2026-09-17 15:48 UTC'
```

The tier returned to the baseline stamp and the baseline HTML sha on the first sample after the
restore. By this point the reader has been observed moving in both directions twice - forward for the
control and the real publish, back for this restore and for the final one - with the pods untouched
throughout.

## 8. The real publish

Between the control and the real test, the bytes were restored and a **fresh** snapshot re-cloned,
so the real publish is not a repeat of an already-warm cache: `real-a before` shows the tier sitting
on the baseline stamp again, then the rename happens, then the tier is sampled. Nothing else was
written in between.

```
real-a   before               svc-sha=b184fea65aa6 gen=2026-09-17T15:48:33.230338338Z latest=2026-09-17T15:48:33.230338338Z state=live rendered=2026-09-17 15:48 UTC pvc-sha=3099458535c8
   | displaced v1/p/16.18/EUW/420/all -> .trash-437-1789667358322216394/old-0
   | renamed staged v1/p/16.18/EUW/420/all into place
   | renamed staged v1/manifest.json into place (manifest last)
   | published at 2026-09-17T17:49:18Z: v1/manifest.json sha256=8a12e646ea92dd162f6ff6babcc68060e206119e789d2543553468c17f6c9388 size=5089
   published_at=2026-09-17T17:49:18Z (no restart, no rollout, no other write followed)
real-a   poll+5s              svc-sha=68ed8ede15fc gen=2026-09-18T04:10:00Z latest=2026-09-18T04:10:00Z state=live rendered=2026-09-18 04:10 UTC pvc-sha=8a12e646ea92
   the tier served the new snapshot within 5s, with no restart and no rollout
real-a   settled              svc-sha=68ed8ede15fc gen=2026-09-18T04:10:00Z latest=2026-09-18T04:10:00Z state=live rendered=2026-09-18 04:10 UTC pvc-sha=8a12e646ea92
```

* **Result: PASS.** A new snapshot, published only by renaming, was being served by the same pods on
  the first sample after the rename.
* Both stamps the criterion cares about moved: `generated_at` (sitemap `lastmod`) and
  `latest.generated_at` (the visible banner), so the reader re-derived the whole `Site`, not just one
  field.
* The served HTML sha moved `b184fea65aa6` -> `68ed8ede15fc`, matching the control's post-publish
  value, which is the expected result of publishing the same marker twice.

## 9. Proof that no restart happened

The exit criterion is only meaningful if the pods that served the new snapshot are the pods that
were serving before it. Identity was captured with `uid`, `creationTimestamp` and `restartCount` at
baseline and re-read after the last write:

```
== did any pod restart?
   | lolstats-go-web-86c6b9fb86-dn54j	49e5c278-3077-47ae-9281-56c24b153f27	2026-09-17T17:16:34Z	0	Running
   | lolstats-go-web-86c6b9fb86-jgh8g	a760c94e-a6f4-4033-88ae-965b629eb459	2026-09-17T17:16:29Z	0	Running
   IDENTICAL: same pod names, uids, creationTimestamps and restart counts before and after
   deploy/lolstats-go-web 68d4362e-c2e0-4869-9b81-46a1bf553401 generation=8
```

Same two pods, same uids, same creationTimestamps (`17:16:29Z` and `17:16:34Z`, i.e. created ~28
minutes before the control publish and left running until the end), restart count 0 before and after,
and a deployment uid and generation that never moved during the run. Had any of those changed, the
run would have been reported void instead of passing (exit code 5 exists for exactly that).

## 10. Restoration and zero residue

The public site is served from this volume, so the run had to leave no trace.

```
== change restored a second time, so the tree is left as it was found
   | restored 180 file(s) at 2026-09-17T17:49:23Z; manifest sha256=3099458535c8544ffab395fa6a6ee5ca645322e4df62aa2ee8142049237ef1a2
final    restore              svc-sha=b184fea65aa6 gen=2026-09-17T15:48:33.230338338Z latest=2026-09-17T15:48:33.230338338Z state=live rendered=2026-09-17 15:48 UTC pvc-sha=3099458535c8
final    poll+5s              svc-sha=b184fea65aa6 gen=2026-09-17T15:48:33.230338338Z latest=2026-09-17T15:48:33.230338338Z state=live rendered=2026-09-17 15:48 UTC pvc-sha=3099458535c8
   the tier is back on generated_at=2026-09-17T15:48:33.230338338Z, rendered '2026-09-17 15:48 UTC'

== clearing the scratch the run created, then looking for residue and the sha256 that has to come back
   | removed /data/agg/.staging-publish-liveness
   | removed /data/agg/.baseline-publish-liveness
   | residue: none
   | content: every baselined file matches
{"residue": [], "content_mismatches": []}
   manifest sha256 = 3099458535c8544ffab395fa6a6ee5ca645322e4df62aa2ee8142049237ef1a2 (baseline 3099458535c8544ffab395fa6a6ee5ca645322e4df62aa2ee8142049237ef1a2)
   tree sha256 over 180 files = ad16e15429f02197a560cf05aeb1dcff40c66d2986baf208e9b03dbf8368e450 (baseline ad16e15429f02197a560cf05aeb1dcff40c66d2986baf208e9b03dbf8368e450)
   the top of the mount and of the aggregate root list exactly what they listed at the start
```

* Every one of the 180 baselined files is byte-identical, and no file exists that was not there
  before (`content_mismatches: []`).
* No name with the `.staging-publish-liveness`, `.baseline-publish-liveness`, `.trash-*` or
  `.incoming*` prefixes survives anywhere under the aggregate root (`residue: []`). `.trash-*` and
  `.incoming-*` are the shapes the real `Publish()`/`WriteManifest` leave behind, so they count as
  residue even though they were produced by the real writer as part of a deliberate failure-safe
  sequence.
* The top of `/var/lib/lolstats` and the top of `/var/lib/lolstats/agg` list exactly what they listed
  at baseline. This is checked by name, not by count.
* The tier came back to the baseline stamp on the first sample after the restore, which is also the
  final end-to-end check that the reader is still healthy.

The probe pod used to reach the volume was created by the script and deleted by its `EXIT` trap;
`kubectl -n lolstats get pod publish-liveness-probe` is `NotFound` after the run. Nothing else was
created: no deployment, service, secret or configmap was applied, patched or deleted.

## 11. Verdict

```
== VERDICT
   control (can the tier notice a change at all)     : PASS
   control held through a 270s no-write window        : YES
   the tier moved back when the bytes were restored  : YES
   new snapshot served after the rename, no restart  : YES (visible in 5s)
   same pod uid/creationTimestamp/restarts throughout: YES
   PVC restored, sha256 back to the baseline         : YES
   pvc/lolstats-data left byte-identical                     : YES

== exit 0
```

**The Phase 6 exit criterion is met for deployment revision 8 of `lolstats-go-web`:**
`internal/aggregate/publish.go`'s rename-last sequence makes a new `agg/v1` snapshot visible to a
running Go tier with no restart, no rollout and no pod replacement, in ≤ one 5 s sample interval,
with the reader's own cache key (`size`, `mtime`) as the only trigger. The instrument was proven
capable of seeing a change before that verdict was reached, and the volume was left byte-identical.

## 12. What did not work

Reported because the brief asks for it, and because two of these failures initially *looked like the
feature failing*. The full failed transcript is kept as
`.agent-artifacts/publish-liveness/transcript-run1-failed-instrument.txt` (git-ignored scratch, so
the salient lines are reproduced here).

### 12.1 A control that passed was reported as failed, because the expected string was predicted instead of read back

The injector asks the real writer for `--generated-at 2026-09-18T04:10:00.000000000Z`. Go's
`RFC3339Nano` **trims trailing zeros in the fractional second**, so the document contains
`2026-09-18T04:10:00Z`, and the manifest shrinks 5119 -> 5089 bytes. Run 1 compared the tier's stamp
against the *predicted* string, so the positive control polled the full 240 s timeout and was then
reported FAIL even though the tier had been serving the new marker since the **first** sample - the
run-1 transcript shows `control poll+5s ... gen=2026-09-18T04:10:00Z` repeated on every one of its
samples. The real test then failed the same way, and the process exited 6 (§12.2). The script now
reads the expected value back out of the generated plan (`manifest_staged_generated_at`,
`manifest_staged_latest_generated_at`) and never predicts it.

### 12.2 Two more instrument bugs found by the same run

* `MANIFEST_SHA` was reading `cut -f2` of the stamp row - i.e. `latest.generated_at` - while the
  field was named and used as the manifest's own `generated_at`. The "real test" was therefore
  comparing the tier's banner value against the marker. Fixed by splitting the row into
  `MANIFEST_SHA` = `cut -f1` and `MANIFEST_LATEST` = `cut -f2` and asserting **both**.
* `injector scratch_clean` used an underscore; the injector's dispatch table has `scratch-clean`
  (hyphen). The subcommand silently did not run, so an earlier run's scratch directories were still
  present and were reported as residue at the end. Fixed, and a `scratch-clean` now also runs
  *before* the baseline so an aborted earlier run cannot poison the final residue check.

`record()` also carried an unused `local psa`; removed.

Run 1 took ~17 minutes and exited 6. Its substantive output was still usable and was cross-checked
against run 3: the published marker restored the baseline sha256, the tier moved back
(`2026-09-17T15:48:33.230338338Z`), and the pod table was identical - i.e. the failure was the
instrument, not the feature. That is exactly the situation the positive control exists to expose:
had the control been skipped as an unnecessary formality, run 1's FAIL verdicts would have been read
as "publish-by-rename is not visible to the tier", which is the opposite of what was happening.

### 12.3 Bugs found before any write was attempted (read-only dry runs)

* `pod_names` selected on `app.kubernetes.io/component=web-go` alone, which matches **other lanes'
  probe pods** (`coord-503-corrupt`, `coord-503-missing`, `coord-503-real`, `coord-preview-posture`).
  Had this shipped, the before/after "no restart" comparison could have been made against a
  stranger's pod. Fixed by using the deployment's full selector everywhere.
* The HTTP manifest URL was wrong (`/v1/manifest.json` -> 404 instead of `/agg/v1/manifest.json`).
* The probe pod's PVC mount was `readOnly: true`, which would have made every injection fail.
* `cleanup` ran before the residue check in the `EXIT` path, and `create_probe` was not idempotent.

### 12.4 Limits of the measurement itself

* **The publisher is reproduced, not invoked.** No subcommand exposes `Publish()`; `build` needs
  Postgres and `demo` refuses the live root (ADR-005). The injector follows `Publish()`'s exact step
  order - displace into `.trash-<pid>-<nano>/old-0`, rename the partition, rename the manifest **last** - and
  the *manifest generation* really is done by the shipped `lolstats-aggregate manifest` writer. What
  is not covered by this test: the `build` subcommand's own call path, `UpdateManifest`'s merge of an
  existing manifest with a new build, and concurrent reads landing in the window between the
  partition rename and the manifest rename (a reader in that window can see a new partition under an
  old manifest; the window was not probed because the site cannot be driven to read at a chosen
  instant).
* **Latency is bounded, not measured.** Resolution is the 5 s sample interval; the true time to
  visibility is ≤5 s and was not resolved further.
* **Metadata cannot be restored.** File bytes, modes and names are restored exactly; the NFS inode
  numbers, ctimes and directory mtimes of the touched directories are not, because that is not
  expressible through the filesystem API available to a container (§13).
* Only the served `/` page and `/agg/v1/manifest.json` were used as the reader's witness. Every other
  route re-derives from the same cached `Site`, which is why the two stamps were asserted instead of
  crawling the site.

## 13. Side effects this run had on the live system

Disclosed in full, because the volume backs the public site:

* The aggregate root's **directory mtimes** were rewritten by the restore step, and
  `v1/manifest.json`'s inode was replaced roughly six times (three publishes plus three restores).
  File bytes, sizes, modes and names are byte-identical (proven by the tree sha256 above);
  inode numbers and ctimes are not restorable from inside a container and were not.
* The probe pod wrote a 24 MB statically-linked `lolstats-aggregate` binary to `/tmp` **inside its
  own container**, which was created for the test and deleted at the end.
* The two port-forwards and the periodic polling added a small, bounded request load to the tier for
  the duration of the run.
* Nothing else: no deployment, service, configmap, secret or CronJob was created, patched,
  suspended, scaled or deleted, and the tier was never restarted or rolled by this work.

## 14. The tier changed underneath us after the run - disclosure

While the run's aftermath was being checked, `lolstats-go-web` was rolled by another lane. This did
not touch the run (the roll landed ~2 minutes after the final restore and the last publish), but it
changes what the live site serves, so it is recorded here rather than omitted.

ReplicaSet evidence, which shows precisely what changed:

| `deployment.kubernetes.io/revision` | created | image digest | tier `LOLSTATS_AGG_FIXTURES` env |
| --- | --- | --- | --- |
| 6 | 2026-09-17T16:57:11Z | `51775acc8f8e...` | `off` |
| 7 | 2026-09-17T17:05:47Z | `820c9e628251...` | `off` |
| **8 - the revision measured here** | 2026-09-17T17:16:29Z | `d4e136ddda52...` | **`off`** |
| 9 - created after the run | 2026-09-17T17:51:25Z | `d4e136ddda52...` | *(absent -> inherits the configMap's `only`)* |

* Revision 9 runs the **same image digest** as revision 8. The only difference relevant to this
  measurement is that the `LOLSTATS_AGG_FIXTURES=off` operator override was dropped, so the tier now
  resolves `LOLSTATS_AGG_FIXTURES=only` from `base/config.yaml` and `candidateRoots` returns only the
  in-image fixtures (`internal/webtier/artifacts.go:157`). The served site is therefore
  `data-state="demo"`, `source: demo`, `generated_at=2026-09-15T04:10:00Z`, 2 partitions, and its
  served `/agg/v1/manifest.json` sha256 is `dfabdeffd656...` - deliberately **not** the volume's
  `3099458535...`, which is the cleanest single number showing that the tier is no longer reading the
  PVC at all right now.
* That is the posture the committed manifests intend, and it was a deliberate, reasoned change, not
  drift: commit `1f464dc` ("Do not publish the real snapshot from the edge-facing tier",
  2026-09-17T17:51:04Z, i.e. the roll that produced revision 9) removes the aggregate fixtures
  override because the tier is publicly reachable through namespace `web` and risk R2 requires a
  labelled preview until the Riot production key is approved. `deploy/base/web/go-deployment.yaml:91`
  says the tier deliberately does **not** set `LOLSTATS_AGG_FIXTURES`, and
  `internal/webtier/deploy_posture_test.go` asserts it. The revisions that carried `off` were the
  override, not revision 9.
* **This does not invalidate the measurement.** The pods, image and binary that were measured are
  revision 8's, and the mechanism proven (`rename(2)` -> new `size`/`mtime` -> new cache key -> re-derived
  `Site`) lives in that image, which is the image revision 9 still runs. What has changed is only
  which root the tier reads, and the script now refuses to measure anything else.
* No change to any deployment was made by this work; the roll is another lane's.

## 15. Independent re-verification of the volume, after the roll

Because the site is served from this volume, the baseline sha256s were re-taken independently after
revision 9 appeared, using a fresh probe pod (`publish-liveness-probe`), which was then deleted:

```
tree sha256 ad16e15429f02197a560cf05aeb1dcff40c66d2986baf208e9b03dbf8368e450 180 files
manifest sha256 3099458535c8544ffab395fa6a6ee5ca645322e4df62aa2ee8142049237ef1a2
hidden entries []
source riot-match-v5 generated_at 2026-09-17T15:48:33.230338338Z partitions 1
```

* Tree sha256 `ad16e154...` over 180 files: **identical to the baseline** in §4.
* `v1/manifest.json` sha256 `3099458535...`: **identical to the baseline** in §4 and still
  `source: riot-match-v5`.
* No hidden entry (`.staging-...`, `.baseline-...`, `.trash-...`, `.incoming-...`) exists at the top of the
  aggregate root (`hidden entries` is `[]`); the traversal itself skips hidden directories by
  construction. The run-3 script additionally compared the top of the mount
  (`/var/lib/lolstats`) and the top of the aggregate root by name and found both unchanged.
* `kubectl -n lolstats get pod publish-liveness-probe` -> `NotFound`, i.e. zero residue in the
  namespace as well.

## 16. Reproducing this

```sh
bash scripts/verify-publish-liveness.sh            # read-only dry run: everything but the writes
bash scripts/verify-publish-liveness.sh --apply    # the full run (~17 min; writes, then restores)
```

Prerequisite, and it is a real one: the tier must be reading the aggregate root. With the deployment
resolving `LOLSTATS_AGG_FIXTURES=only` (the current posture, §14) the script's `preflight` stops
immediately with

```
the tier is not reading the aggregate root (fixtures=only); this test would measure fixtures
```

which is the intended behaviour - the alternative is a green run that proves nothing. Re-running it
against a fixtures-served tier is not something this test can arrange for itself: `deploy/base/config.yaml`
is pinned at `only`, and the per-Deployment `off` override that revision 8 carried was deliberately
removed by `1f464dc` because the tier is edge-facing (risk R2). Doing so again would be an operator
decision with a reason, not a favour to this test, so this document does not ask for it. Exit codes
are 0 ok, 3 control failed, 4 hot reload failed, 5 a restart was detected, 6 restoration failed.

## 17. Artefacts

| Path | What it is |
| --- | --- |
| `scripts/verify-publish-liveness.sh` | the instrument; `bash -n` clean, exit 0 on the run quoted above |
| `docs/PUBLISH-LIVENESS-EVIDENCE.md` | this document; the excerpts above are verbatim from the transcripts |
| `.agent-artifacts/publish-liveness/transcript.txt` | run 3, the canonical run, `EXIT=0` (git-ignored scratch) |
| `.agent-artifacts/publish-liveness/transcript-run1-failed-instrument.txt` | run 1, exit 6, the instrument bugs of §12 |
| `.agent-artifacts/publish-liveness/transcript-run2-pass.txt` | run 2, first full pass |
| `.agent-artifacts/publish-liveness/plan.json` | the plan the injector and the assertions both read their expected values from |

Everything under `.agent-artifacts/` is git-ignored scratch, so it is not part of the commit and will
not survive a clean checkout. The excerpts quoted above are the durable record; nothing in this
document depends on a file that is not either committed or quoted in full here.
