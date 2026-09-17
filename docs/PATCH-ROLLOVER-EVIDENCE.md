# PATCH-ROLLOVER-EVIDENCE - a whole patch rollover, exercised end to end

Measured 2026-09-17 22:34:16-22:38:40 UTC in a **private** namespace
(`lolstats-rollover`) against a **private** NFS-backed RWX volume, by a verification agent that
owns no production code. The instrument is `scripts/verify-patch-rollover.sh`, which builds the
whole drill from the repository's own fixture archive and deletes every object it creates.

**This document records measurements. It changes no product behaviour.**

**The transition in this document is fixture-driven, not a live patch rollover.** Riot had not
shipped a new patch at the time of measurement, so the next patch was derived from the checked-in
fixture archive by mechanical edits (§4), and the "crawl" that makes new data arrive is an
`initContainer` that copies a prepared day into the raw archive. What is real is the *mechanism*
under test - the same binary, the same config keys, the same code path, the same NFS semantics,
the same tier Deployment shape as production. What is synthetic is the data and the calendar. No
live patch rollover was observed and none is claimed.

## 1. The claim, and where in the code lives

DOD-13 / P6 asks that a **patch transition works as a whole**, not that one step of it does: a new
patch's data arrives, it is ingested, aggregated into `agg/v1`, published, and the tier serves the
new patch **without a restart and without a gap** - including the edge cases that only appear when
the patch genuinely changes. Four items are demanded, and each is answered below:

| Brief item | Where it is answered |
| --- | --- |
| 1. The transition window: what protects the *set* of files that must agree | §8 |
| 2. A brand-new partition with no prior data: honest empty state, never the previous patch's numbers under the new label | §6, §9 |
| 3. Rollback: the operator's revert, and the old patch still served afterwards | §10 |
| 4. Idempotence: a re-run must not double-count or duplicate a partition | §11 |

The stages the drill drives, and where each lives (all read, none modified):

| Stage | Where | What it does |
| --- | --- | --- |
| New payloads arrive | `internal/store`'s raw archive layout, `raw/riot/match-v5/dt=<day>/part-*.parquet` | one directory per day; the drill's `initContainer` copies `staging/dt=2026-09-16` in only when the day is absent, which is what makes a re-run safe |
| Aggregation | `cmd/lolstats-aggregate build -window-end <day> -window-days 6` | reads the raw window, writes `agg/v1/p/<patch>/<region>/<queue>/<bracket>/{tierlist.json,champions/,matchups/}`, then republishes the manifest |
| Publish | `internal/aggregate/publish.go` `Publish()` | `mkdir` a `.trash-<pid>-<nano>`; `displace()` the live partition into `<trash>/old-N`; `os.Rename` the staged partition into place; **rename `v1/manifest.json` last**; best-effort `rollback()` inside one publish; `defer RemoveAll(trash)` |
| The index | `internal/aggregate/manifest.go` `UpdateManifest()` | the new manifest is disk manifest ∪ `scanPartitions` ∪ this build's partition, then `latest = latestOf(...)`. Union semantics: a rebuild keeps the existing entry for a partition that is already listed, so a rebuild is additive, never destructive |
| The reader notices | `internal/webtier/artifacts.go` (`fileCache`) | cache key is `path\|Size\|ModTime.UnixNano()` of `os.Stat("<root>/v1/manifest.json")`; a new key re-derives the whole `Site`. `rename(2)` installs a new inode, so a new mtime, so a new key - the flip is **one** invalidation, not one per file |
| The old patch stays reachable | `internal/webtier/server.go`, `/patch/<patch>/...` routes | any partition the manifest lists is addressable by name, independent of `latest` |
| Refusal instead of a guess | `internal/webtier/server.go:933` `classifyFault`, `view_error.go` | a missing advertised artifact is a 503 with `data-fault="artifact"`; a root with no manifest is a 503 with `data-fault="no-snapshot"`. Never a partial table |
| The revert | `cmd/lolstats-aggregate manifest --agg … --source riot-match-v5 --patch <old>` | re-derives the manifest from the tree and repoints `latest` without rewriting any partition |

## 2. What was measured, and what was not

| Item | Value |
| --- | --- |
| Cluster / namespace | `default` context / `lolstats-rollover` (created and deleted by this run) |
| Tier | `Deployment/rollover-web`, pod `rollover-web-77f6549f67-972kx`, uid `7e5f8d0a-edb7-4e9c-9d8b-51c720c86ff9`, created `2026-09-17T22:34:15Z` |
| Image measured | `ghcr.io/erik-schuetze/league-of-legends@sha256:109a30839762f777fbe982813d9e94465bd708b593cdb6fdc1c635c911d3a660` (the digest the live `lolstats-go-web` pods run; `:latest` is deliberately not used) |
| Reader / seed | `busybox:1.36@sha256:73aaf090…` (`rollover-seed`), sampling reader pod `docker.io/library/python@sha256:b64631e0…` |
| Volume | a PVC created by this run on StorageClass `nfs-client` (RWX), same NFS server as the live volume: `192.168.10.100:/nas-main/k3s-volumes/lolstats-rollover-…`, mounted at `/var/lib/lolstats` in the tier, aggregate root `/var/lib/lolstats/agg` |
| Reached through | the namespace's own ClusterIP Service, from inside the namespace. No port-forward, no `Ingress`, nothing shared with `web` |
| Build window | `-window-end 2026-09-15 -window-days 6` for the pre-rollover patch, `-window-end 2026-09-16` for the new one, thresholds `min_cell_n=2` (drill values, chosen so a 14-match fixture produces cells at all; production's are higher) |
| Measured | the whole transition: raw arrival → build → `agg/v1` → publish → tier serves the new patch, with the served responses, the timings, the fail-closed behaviour, idempotence, the revert lever, and non-restart |
| Not measured | **a live patch rollover** (none happened); the Riot crawl itself (the fixture archive arrives by copy); the Postgres build-run recorder (`POSTGRES_DSN` unset, so build runs are recorded as files); the static Astro tier and Caddy; sub-tick latency below the reader's 2 Hz sampling; the read that spans the two renames inside one publish (§8) |
| Not touched | namespace `web`, the shared Caddy, `deploy/base/**`, the live `lolstats` namespace and its volume, the live archive tree, `internal/store/**`, `cmd/lolstats-*/**`, the web-tier template/view files owned by other lanes |

## 3. Facts reconnaissance had to establish first

Each of these would have silently produced a meaningless PASS, so they are recorded:

1. **The manifest's `latest` is an object, not a patch string** (`internal/aggmodel/model.go:280`).
   The first version of the reader assumed a string and crashed with
   `TypeError: join() argument must be str ... not 'dict'`. Every consumer now reads `latest.patch`.
   This is also why "the manifest moved" and "the tier serves the new patch" have to be read from
   different fields.
2. **A rebuild of an existing partition is not a new partition.** `UpdateManifest` unions the disk
   manifest with what it scans, so re-running the build for a patch that is already listed leaves
   the entry in place and moves only `generated_at`. Idempotence is therefore **content** equality,
   not byte equality - a build cannot pin its clock (`--generated-at` exists on `manifest` and
   `demo`, not on `build`).
3. **`kubectl wait --for=condition=complete job/…` does not short-circuit on a failed Job**; it
   hangs to the timeout. Every job in this drill is instead polled for `status.succeeded` /
   `status.failed`.
4. **`kubectl apply` of a Job whose pod already completed is a no-op that reports Complete
   immediately**, with the *previous* run's logs. Every re-run here deletes the Job first
   (`--ignore-not-found --wait=true`); this is what step 9's restore and step 7's idempotence check
   depend on.
5. **`kubectl exec` into the tier is impossible**: the image is distroless and has no shell
   (`"sh": executable file not found`). All HTTP and volume work goes through the `rollover-seed`
   busybox pod, which is also what makes the drill read the tree from the *inside* of the
   filesystem the tier reads.
6. **The tier's selector is not `app=<name>`.** Other lanes' probe pods also carry
   `app.kubernetes.io/component=web-go`. The drill uses its own labels (`app.kubernetes.io/component:
   rollover-drill`) so before/after pod comparisons cannot pick up a foreign pod.
7. **NFS attribute caching is the reason the positive control comes first** (see §6): with
   `vers=3,… timeo=600,retrans=2,sec=sys` and the mount options recorded in
   `nfs-mount-options.txt`, "nothing changed" can be the instrument lying rather than the feature
   failing. A PASS without a control on the same instrument is not a PASS.

## 4. The fixture transition

`fixtures/agg/raw/riot/match-v5` holds 14 hand-authored matches over three days; 9 of them fall in
the six-day window. The new patch's data is derived from that archive by three mechanical edits,
all of them visible in the fixture and none of them a special case in the code:

1. match ids renumbered (`EUW1_…NN` → `EUW1_…10NN`), so the new day's rows are distinct rows,
2. `gameVersion` `16.18.612.9234` → `16.19.612.9234`, which is what makes the aggregator label the
   partition `16.19`,
3. the winner of the first three matches flipped (`100` → `200`), so the new patch's rates are
   genuinely different rather than a relabelled copy.

That third edit is what makes "the tier served the new patch" distinguishable from the failure
mode of item 2, where the *previous* patch's numbers appear under the new patch's label.

The two published partitions of the transition:

| | 16.18 (pre-rollover) | 16.19 (the new patch) |
| --- | --- | --- |
| Source window | 2026-09-10 to 2026-09-15 | 2026-09-11 to 2026-09-16 |
| Build command | `build -window-end 2026-09-15 -window-days 6` | `build -window-end 2026-09-16 -window-days 6` |
| Cells published / withheld | 11 / 3 | 11 / 1 |
| Tier-list cells sha256 | `07adb0fd7c1bf53eba44ee480ba0b53abf714a58c2a10b66b7a3948b779fbcb4` | `85aa07d2b08bdccd4f685bd1581a8482d1e8f91db21e1e9784e8dd61ba02a021` |
| Served top-3 rows | Jax B n=4 50.00%/25.00%/37.50%; Garen D n=7 42.86%/43.75%/12.50%; Ryze D n=2 0.00%/12.50%/0.00% | Jax S+ n=2 100.00%/25.00%/50.00%; Ryze S+ n=2 100.00%/25.00%/0.00%; Garen D n=3 0.00%/37.50%/25.00% |

"16.18 unchanged by the transition: True" and "16.18/16.19 cells differ: True" are both asserted in
the drill's VERDICT: the transition added a partition, and the two partitions' bytes are not equal.

## 5. Baseline - the bytes the tier had to serve, and the state it had to be in

Step 1 publishes the pre-rollover patch and verifies it before anything moves, so every later
comparison has a defined starting point. Job timings for the whole run are in §7.

```
PASS baseline: 16.18 served from its own bytes: 0 faults across 0 samples
manifest_latest=16.18  readyz_latest=16.18  readyz_patches=1
export_sha=07adb0fd7c1b… == current_partition_sha=07adb0fd7c1b…
```

The oracle is the tier's own byte-exact export: `/explore/export.json` is compared with the sha256
of `v1/p/16.18/EUW/420/all/tierlist.json` on the volume, read from the seed pod. The three
tier-list routes and `/readyz` are checked against the manifest and against the cells on disk, so a
tier that answered 200 with the wrong rows fails just as loudly as one that answered 503.

## 6. The honest empty state, and the negative control on the same instrument

Step 2 asks for the *unpublished* new patch by name, before it is published:

```
PASS unpublished 16.19 renders an honest empty state: 0 faults across 0 samples
unpublished 16.19 page: 0 data rows, 40153 bytes
negative control: the same reader FAILS on 16.19 before publication, so a later PASS is a real observation
```

The reader is run in `verify --expect-empty 16.19` mode, which asserts the *opposite* of what the
transition asserts: the page must have **no** rows, must not carry a fault, and must not answer
with the previous patch's cells. It fails if any of that is untrue. So the same instrument that
passes the transition in §7 has been shown to fail the same route in the same namespace minutes
earlier: the later PASS is an observation of a change, not a constant.

What the unpublished route actually served (200, 40153 bytes, 0 rows):

```
<title>Top tier list, patch 16.19 - LoL Stats</title>
<meta name="description" content="The archived top tier list for patch 16.19. This patch has no published snapshot.">
<main>
  <h1>Top tier list</h1>
  <p>This page will show every champion played in top as soon as a snapshot is published.</p>
  <h2>Patch</h2>
  <nav aria-label="Patch"><ul class="fallback-patch-switcher">
    <li><a href="/patch/16.18/tier-list/top" title="switch to patch 16.18">16.18</a></li>
  </ul></nav>
  <div class="fallback-empty" role="note">
    <p><strong>No sample yet</strong></p>
    <p>The published snapshot for patch 16.18 (EUW, queue 420, bracket all) has no artifact for
    this selection, so nothing is shown rather than an estimate.</p>
  </div>
```

Verdict on item 2 is in §9; the short form is that no value from 16.18 is rendered under 16.19's
label, and none of the three failures the brief names (empty table, 500, previous patch's numbers)
happened.

## 7. The transition, measured tick by tick

The reader samples the tier at 2 Hz across the whole transition while the build job runs. The
recorded run (`--soak-seconds 180 --pre-seconds 45`) covers 360 samples:

```
samples 360  fault_total 0  stale 0  incoherent 0
served patches seen: ['16.18', '16.19']
http statuses: {'200': 2880}
flip and manifest change in the same tick: True
flip_to_served_seconds 0.0
median tick 0.0537s  max 0.0954s
samples before the flip: 95  after: 265
first tick 2026-09-17T22:34:37.  last tick 2026-09-17T22:37:37.

last tick before the flip:
  2026-09-17T22:35:24. manifest=16.18 served=16.18 export=07adb0fd7c1b upcoming=not published rows=0
first tick after the flip:
  2026-09-17T22:35:24. manifest=16.19 served=16.19 export=85aa07d2b08b upcoming=16.19 rows=3
ticks between them: 1 (one tick: the change is not sub-tick observable, and no intermediate state exists)
no sample was observed with the manifest changed and the old patch still served
```

Every sample checks, in one tick: `/explore/export.json` (status, sha256, and whether that sha
matches the *current* or the *previous* partition on disk), `/readyz` (status, `latest_patch`,
`patches`), the five `/tier-list/<role>` routes (status, the patch label each renders, and whether
its cells equal the cells on disk for the manifest's patch), and `/patch/16.19/tier-list/top` asked
for by name. `0 faults` means every one of those agreed in every sample; `stale 0` means no sample
served the old bytes while the manifest named the new patch; `incoherent 0` means no sample served
bytes that matched neither partition.

The build's own log, from the same job, gives the publish latencies directly:

```
2026-09-17T22:35:24.271813387Z metrics endpoint listening
2026-09-17T22:35:24.383987639Z patch selected
2026-09-17T22:35:24.601369949Z published partition
2026-09-17T22:35:24.601912251Z published manifest
2026-09-17T22:35:24.602257622Z build published
```

So: the partition rename and the manifest rename are **543 µs** apart; the last reader tick that
saw the old patch was 0.16 s before the manifest rename and the first tick that saw the new patch
was 0.34 s after it, i.e. **≤ 0.51 s from rename to served** at 2 Hz sampling, with no intermediate
sample in between. Job timings for the whole run:

| Job | Applied (UTC) | Completed (UTC) |
| --- | --- | --- |
| `rollover-agg-a` (publish 16.18) | 22:34:16 | 22:34:21 |
| `verify-baseline` | 22:34:21 | 22:34:25 |
| `verify-empty-pre` (the honest empty state) | 22:34:25 | 22:34:30 |
| `rollover-agg-b` (the transition) | 22:35:22 | 22:35:27 |
| `verify-post` | 22:37:41 | 22:37:45 |
| `rollover-agg-b` re-run (idempotence) | 22:37:50 | 22:37:55 |
| `rollover-reindex-a` (revert to 16.18) | 22:37:56 | 22:38:01 |
| `verify-revert` | 22:38:01 | 22:38:06 |
| `rollover-reindex-b` (back to 16.19) | 22:38:06 | 22:38:10 |
| `verify-restored` | 22:38:11 | 22:38:15 |
| withdrawal + `verify-503` | 22:38:17 | 22:38:24 |
| `rollover-agg-b` restore | 22:38:24 | 22:38:31 |
| `rollover-reindex-b2` + `verify-final` | 22:38:31 | 22:38:40 |

A whole rollover is therefore **≈ 5 s of job wall time** (22:35:22 applied → 22:35:24.60 published,
2.6 s of which is pod start and container start → 22:35:27 complete), and the tier is serving the
new patch before the job that produced it has exited.

Post-transition, the same reader mode as the baseline passes again on both patches:

```
PASS post-transition: both patches served from their own bytes: 0 faults across 0 samples
manifest_latest=16.19  readyz_latest=16.19  readyz_patches=2  export_sha=85aa07d2b08b… == 16.19's cells
/patch/16.18/tier-list/top: 200, labelled 16.18, rows equal 16.18's cells on disk
```

## 8. Item 1 - what protects the rollover *set*

The set that has to agree for a reader is:

```
v1/manifest.json                       <- the index: which patches exist and which is `latest`
v1/p/<patch>/<region>/<queue>/<bracket>/tierlist.json
v1/p/<patch>/…/champions/*.json
v1/p/<patch>/…/matchups/*.json         <- the partition's own files, which all belong to one build
```

`LATEST` is not a member: there is **no `LATEST` file**. The pointer is the manifest's `latest`
field (`internal/aggmodel/model.go:280`), and `latestOf` is derived from the partitions the same
manifest lists, so the two cannot disagree inside one manifest.

Four mechanisms hold the set together, and the measurement shows each of them doing its job:

1. **The manifest is renamed last.** Within one publish, the partition directories are renamed into
   place *before* `v1/manifest.json`. A reader that looks at the manifest sees a new partition only
   after that partition's files are all at their final names. Measured: `published partition` at
   22:35:24.601369949Z, `published manifest` at 22:35:24.601912251Z.
2. **`rename(2)` is atomic per directory**, so no reader ever sees a half-written partition: the
   tree is either the old partition or the new one. No sample in 360 saw a partition in neither
   state (`stale 0`, `incoherent 0`). `publish.go` additionally stages into a `.trash-<pid>-<nano>`
   directory and `defer`s `RemoveAll`, and the re-run left **0** `.trash-*` directories behind.
3. **The manifest is an index, not a log.** The set is re-derivable from the tree
   (`scanPartitions`), which is what makes the operator's revert (§10) a one-line re-index rather
   than a tree rewrite. It is also why the union semantics of `UpdateManifest` cannot lose a
   partition that is still on disk.
4. **The tier's cache key makes the flip a single invalidation.** `fileCache` keys on
   `path|Size|ModTime.UnixNano()` of `manifest.json` alone. A publish therefore moves the whole
   `Site` from one snapshot to the other at one instant; there is no window in which some pages
   have re-read the tree and others have not, which is what the 360 samples show: 95 ticks before
   with every route on 16.18, then a single tick in which *every* route is on 16.19, with **no
   intermediate sample** in between (`flip and manifest change in the same tick: True`,
   `flip_to_served_seconds 0.0`).

The last tick before and the first tick after, as the drill's `flip-window.txt` records them:

```
last tick before the flip:
  2026-09-17T22:35:24. manifest=16.18 served=16.18 export=07adb0fd7c1b upcoming=not published rows=0
first tick after the flip:
  2026-09-17T22:35:24. manifest=16.19 served=16.19 export=85aa07d2b08b upcoming=16.19 rows=3
```

and the same two ticks from the raw sample stream, with the epochs and the manifest's own sha256
(the reader records seconds only, so the epoch is the precise part):

```
22:35:24  epoch 1789684524.442  manifest_sha d775c84d73a8a2b534cec98b10c3fe914dbe51a788b885e579634a574aeea89c  latest 16.18  served 16.18  export 07adb0fd7c1b…  upcoming not published/0 rows  readyz patches=1
22:35:24  epoch 1789684524.943  manifest_sha 3bff05a653fc57c3976340a791f5954d4ab0bfbc7d0d82f5a777a911f458abd3  latest 16.19  served 16.19  export 85aa07d2b08b…  upcoming 16.19/3 rows           readyz patches=2
```

**What is *not* protected, stated plainly:** a reader that reads the manifest and *then* reads a
partition, with the second rename landing in between, can observe the manifest of one publish and a
partition file of another. The window is bounded by the two renames 543 µs apart in this run, and
the tier cannot be caught in it because it re-reads the manifest through its cache key on each
request and derives the partition paths from that same read - but a *different* reader (an
operator's script, a future second service) that caches the manifest separately is not protected by
anything. Nothing in this repository closes that window; the honest statement is that it is
sub-millisecond, not that it is impossible. The drill did not measure it: at 2 Hz sampling it is
below one tick, and the reader's own design (one `Site` per manifest key) is what makes the tier
immune rather than the sequencing.

## 9. Item 2 - honest empty state: verdict

**Pass, with one attribution wrinkle.** For the requested-but-unpublished patch the tier answered
**HTTP 200 with 0 data rows** and no value from the previous patch (§6). The failures the brief
names did not happen: not an empty table pretending to be data (there is no table at all), not a
500, and not 16.18's numbers under 16.19's label. Independent of the fixtures, the drill asserts
this on *both* sides: pre-publication the reader is required to find `0` rows and the
`not published` label, post-publication the same route is required to render 16.19's cells and the
`16.19` label.

Three observations that are worth keeping on the record because they are where a future regression
would hide:

1. **Wrong-patch attribution in the empty-state prose.** The page's `<title>`, `og:*` and canonical
   URL say patch **16.19** ("This patch has no published snapshot"), while the page body's empty
   note says *"The published snapshot for patch **16.18** (EUW, queue 420, bracket all) has no
   artifact for this selection, so nothing is shown rather than an estimate."* That sentence comes
   from `emptyReason` (`internal/webtier/view_home.go:87`), which names `site.Latest()` - the
   *published* patch - rather than the patch that was requested. The banner above it reads
   `Published snapshot · Patch not published · generated … · source window … · cells published from
   n = 2 games · 3 cells withheld`, i.e. the site-level facts of 16.18. Read as a whole the page is
   self-consistent about what *is* published and honest about what is not, and no number leaks; but
   a reader who asked for 16.19 and sees "The published snapshot for patch 16.18" is being told
   about a different patch than the URL they are on. This is a wording defect in a file owned by
   another lane (`view_home.go`), recorded here rather than fixed here.
2. **The patch switcher is the real signal.** The switcher on the empty page lists only `16.18` -
   the patch that exists - so the "which patches can I read" answer is still correct on a page whose
   own patch does not exist yet.
3. **`?bracket=` is not a tier concept.** `internal/webtier/query.go`'s `Query` carries `sort`,
   `dir`, `filter`, `page`, `per`, `compare` and `patch` - there is no bracket selector, and the
   served bracket is always the *partition's* bracket (`view_helpers.go:34`). An unparseable or
   unknown parameter falls back to the default view rather than erroring, so `?bracket=emerald_plus`
   silently serves the all-ranks artifact. That is not a wrong answer under a bracket label - the
   page prints `bracket all` in its own metadata - but it does mean a bracket-scoped link cannot be
   expressed, which will matter when per-bracket partitions exist. Out of scope here.

## 10. Item 3 - rollback: verdict

**There is a revert lever, and it is a re-index rather than a rollback of bytes. There is no
"revert the tree" lever, and one state fails closed with a 503.** Three things were measured, and
the third is the finding worth acting on.

### 10.1 The lever that exists: re-index the manifest to the old patch

```
PASS re-index to 16.18: served from 16.18's bytes, 16.19 still reachable: 0 faults across 0 samples
manifest_latest=16.18  export_sha=07adb0fd7c1b… == 16.18's cells   readyz_latest=16.18  patches=2
/patch/16.19/tier-list/top: 200, labelled 16.19, rows equal 16.19's cells on disk
```

```
/lolstats-aggregate manifest --agg /var/lib/lolstats/agg --source riot-match-v5 --patch 16.18
manifest: ok partitions=2 latest=16.18 source=riot-match-v5 generated_at=2026-09-17T22:37:52Z path=v1/manifest.json
```

The command is a re-index of the *tree*, not a rollback of bytes. It rewrote the manifest
(sha changed from `3bff05a653fc…` to `fe71270be1ff…`), the tier served the old patch again within
one cache key, and 16.19's partition was **byte-identical afterwards** (`DISK_SHA 16.19` unchanged)
- the re-index is non-destructive, which is the property that makes it usable as a revert. Pointing
the manifest back at 16.19 (`reindex-b`) restored the new patch the same way
(`verify-restored`: export sha `09ebcfbf2d55…` == 16.19's cells on disk).

So: if the new patch's aggregation is wrong, the operator repoints `latest` at the previous patch,
the site serves the previous patch's own bytes, and the bad partition stays on disk for inspection.
The transition itself is not undone - the tree still holds both partitions - which is a deliberate
consequence of the union semantics in `UpdateManifest`.

### 10.2 The lever that also exists: rebuild with better inputs

Re-running the build for the same patch is idempotent (§11), so "fix the aggregation and run it
again" is the other revert. This is the only lever that changes bytes, and it cannot be pinned to a
fixed clock (`--generated-at` is not a `build` flag), so a rebuilt partition is never byte-identical
to its predecessor even when every cell is.

### 10.3 What does *not* exist - and the state that fails closed

`manifest` **cannot remove** a partition. Its union semantics keep any entry that is already listed,
so a stale or wrong partition cannot be deleted from the index by re-indexing: it can only be
repointed away from, or overwritten in place. Deleting the partition out of band while the manifest
still advertises it reproduces the state a half-finished publication or a bad manual "rollback"
would leave, and the drill measures what the tier does with it:

```
withdrawn: /var/lib/lolstats/agg/v1/p/16.19/EUW/420/all
503 /readyz
503 /tier-list/top
503 /patch/16.19/tier-list/top
503 /explore/export.json
fail-closed: {"explore/export.json": 503, "patch/16.19/tier-list/top": 503, "readyz": 503, "tier-list/top": 503}
every route refused with 503 while the manifest still advertised 16.19
```

All eight sampled routes (five tier-list roles, `/readyz`, `/explore/export.json`,
`/patch/16.19/tier-list/top`) returned 503, and none rendered a row. The tier does not serve the
previous patch's numbers in place of the withdrawn one, and it does not serve an empty table: it
refuses, whole.

**The 503 bodies were not captured in the cluster run.** `capture_page` uses the seed pod's
`wget`, and busybox `wget` wrote nothing to stdout for a 5xx response, so the four
`withdrawn-*.html` files in the run directory are 0 bytes. The same fault path was therefore
reproduced **locally** against the checked-in fixture tree with the release binary built from HEAD
(§16.2), which is where the body text in §16.2 comes from. This is recorded as a defect of the
instrument, not of the tier.

### 10.4 Restoring service after the withdrawal

Re-running the build job restored the partition and `reindex-b2` repointed the manifest; the final
verification is the strongest oracle in the drill:

```
PASS final state: served export bytes == bytes on disk: 0 faults across 0 samples
served export sha == disk sha  3d1a8f57e3ae4dfba76d54fb8063c6bb3e201fd64f7e0071e56925b2bc25ca4c
```

## 11. Item 4 - idempotence: verdict

**Pass, on content.** The build job was deleted and re-applied for the same patch and the same
day, and the second run changed no cell:

```
idempotence: manifest before re-run: 2 partitions, 2 distinct keys, latest 16.19
             manifest after  re-run: 2 partitions, 2 distinct keys, latest 16.19
             partition set identical: True
             top-level keys that differ between the two builds: ['generated_at']
             cells identical: True
             gate/meta identical: True
trash directories left behind: 0
crawl initContainer: "dt=2026-09-16 is already in the archive; nothing to copy"
16.19 partition sha256 after the re-run: 09ebcfbf2d5557ffe7bdd8c18be4ac0272c8e876d05964e8ca496d103b19da8f
                       (was 85aa07d2b08bdccd4f685bd1581a8482d1e8f91db21e1e9784e8dd61ba02a021 at the transition)
```

Read carefully, because the two shas differ and that difference is the point:

* **No duplicate partition.** 2 partitions before and 2 after, 2 distinct keys, `latest` unchanged,
  and the partition set identical - a double-count would show as 4 entries or 3 keys.
* **No double-counted games.** The 11-cell map compared key by key between the two builds: every
  cell identical, including `n`. Only `generated_at` moved.
* **The partition file is not byte-identical**, because the file embeds `generated_at` and a build
  cannot pin its clock (only `manifest` and `demo` take `--generated-at`). Idempotence here is
  therefore **content equality**, and any future test that compares partition shas across runs will
  fail for a reason that is not a defect. The drill asserts cells, gate metadata and partition
  identity rather than bytes, and says so.
* **The raw archive half of the transition is genuinely idempotent**: the `crawl` `initContainer`
  copies the new day in only when the day is absent, so a re-run logs
  `dt=2026-09-16 is already in the archive; nothing to copy` and the raw tree is unchanged.

## 12. Proof that no restart happened

The tier was never touched by any step of the transition; every change was made on the volume.

```
before: rollover-web-77f6549f67-972kx uid=7e5f8d0a-edb7-4e9c-9d8b-51c720c86ff9 created=2026-09-17T22:34:15Z restarts=0
after:  rollover-web-77f6549f67-972kx uid=7e5f8d0a-edb7-4e9c-9d8b-51c720c86ff9 created=2026-09-17T22:34:15Z restarts=0
```

Same pod name, same uid, same `creationTimestamp`, `restartCount` 0 on both sides, and no second
pod in any `pods-after` listing. The pod was created at 22:34:15, i.e. 69 s **before** the
transition job was applied, and it served the new patch from 22:35:24 without ever having been
recreated. If these had differed the run would say so: the drill prints the identity pair and the
verdict line `non-restart: tier pod identity before == after`, and the run fails if they differ.

## 13. Restoration and zero residue

| Object | State after the run |
| --- | --- |
| Volume / tree | never mutated by the drill outside its own copy; the final state is the published tree with both partitions, served bytes == disk bytes (§10.4) |
| Namespace `lolstats-rollover` | deleted (`kubectl delete ns lolstats-rollover`), PVC and Jobs with it - the drill deletes the namespace on exit unless `--keep` is given |
| Residue check | `kubectl get ns`, `get pvc -A`, `get jobs -A`, `get pods -A` show no drill objects in any namespace, including `default`; no `.trash-*` directory on the volume |
| Namespace `web`, the shared Caddy, `deploy/base/**` | not touched, not read as a target, not patched |
| The live `lolstats` namespace and its volume | not touched; the drill creates its own PVC on a different NFS path |
| The live raw archive | not touched: the drill copies the fixture archive into its own volume, and no restore was run anywhere |

The only thing this run leaves behind outside the repository is the recorded evidence directory
`.agent-artifacts/rollover/runs/20260917T223410Z/` (gitignored), which is what §17 refers to.

## 14. Verdict

| Brief item | Verdict | The number that decides it |
| --- | --- | --- |
| The transition works as a whole, no restart, no gap | **Pass** | 360 samples, 0 faults / 0 stale / 0 incoherent, 2880× HTTP 200; pod uid + creationTimestamp + restartCount unchanged; publish → served ≤ 0.51 s at 2 Hz sampling |
| 1. What protects the set | **Answered**: manifest renamed last, per-directory `rename(2)`, manifest-as-index, one cache key. One unprotected read across the two renames is stated, not hidden | partition rename at `…24.601369949Z`, manifest rename at `…24.601912251Z` (543 µs apart); no intermediate tick |
| 2. Honest empty state for a new partition | **Pass**, with a wrong-patch attribution wrinkle in the prose | 200 with 0 rows, `not published` label, no 16.18 value under a 16.19 label; the reader is *required* to pass the transition and to fail pre-publication on the same route |
| 3. Rollback | **Partial, and said plainly**: the re-index lever reverts `latest` non-destructively and the old patch is served; there is no revert-the-tree lever, and a partition that is withdrawn while advertised is a 503 on every route | re-index: exit 0, old patch served from its own bytes, 16.19 unchanged; withdrawal: 8/8 routes 503, 0 rows |
| 4. Idempotence | **Pass on content**; not byte equality, because `generated_at` moves | 2 partitions / 2 distinct keys before and after, every cell identical, 0 `.trash-*`, crawl no-op |

The exit criterion is met for the assertion "a new patch's data is ingested, aggregated, published
and served by the tier without a restart and without a gap", by a **fixture-driven** transition in a
private namespace. It is *not* a claim about a live Riot patch rollover; that remains unobserved.

## 15. What did not work

Instrument and method defects found while doing this, in the order they were hit. Each one produced
a wrong answer at least once before it was fixed, which is why they are listed.

1. **The manifest's `latest` is an object, not a string** (`internal/aggmodel/model.go:280`). The
   reader crashed with `TypeError: join() argument must be str ... not 'dict'` and died before
   measuring anything. Fixed with a `latest_patch()` helper that reads `latest.patch` and tolerates
   a bare string; the idempotence comparison uses it too.
2. **`load_champions()` silently returned an empty map.** It iterated `blob.values()`, but
   `internal/webtier/data/champions.json` is `{"ddragon_version": …, "champions": [...]}`. The effect
   was not a crash but a *false failure*: every route reported `tier-list/<role> rows != disk cells`,
   because the disk-side cell comparison had no champion names. Fixed to read `blob["champions"]`
   with a fallback, verified locally against a stored page and the disk partition before re-running
   in the cluster. This is the defect class this project keeps meeting: a control that cannot see
   the data it is judging.
3. **A stale expected sha made a non-destructive check fail.** `SHA_B` was captured at the
   transition, but step 7's idempotence re-run legitimately rebuilt the 16.19 partition; the later
   "the re-index must not rewrite 16.19" check then failed on a hash that was one build out of date.
   Fixed by refreshing `SHA_B` after the re-run and quoting the transition-time sha separately
   (`SHA_B_AT_TRANSITION`) in the verdict, so the two facts are not conflated. Which exposed the
   real property: only `generated_at` moves.
4. **macOS `tar` into the seed pod ships AppleDouble files.** `tar | kubectl exec -i … tar -xf -`
   transported `._*` entries and the build died with
   `unrecognised file magic "\x00\x05\x16\a"`. Fixed by using `kubectl cp` and a guard that counts
   `._*` / `.DS_Store` entries and dies if any are present.
5. **`kubectl wait --for=condition=complete job/…` hangs to the timeout on a failed Job**, hiding
   the failure behind a timeout. Replaced with polling of `status.succeeded` / `status.failed`.
6. **Re-applying a completed Job is a no-op** that reports Complete immediately and shows the
   previous run's logs, which is exactly what the idempotence and restore steps do. Every re-run now
   deletes the Job first with `--ignore-not-found --wait=true`.
7. **A thin pre-transition window.** The first clean runs sampled only 9 ticks before the flip,
   because the soak window was tied to the transition time and the "before" side was assumed rather
   than measured. Fixed with `--pre-seconds` (default 30, used at 45 for the recorded run), which
   yields 95 ticks before the flip against 265 after. A control that is not sampled on both sides of
   a change proves nothing about the change.
8. **The 503 bodies were not captured** (see §10.3): busybox `wget` wrote no body for a 5xx, so four
   evidence files in the recorded run are 0 bytes - evidence that reads as if it were missing.
   Reproduced locally instead (§16.2). Step 9 now writes the status and a note in place of the empty
   body, but no client in the drill can capture a 5xx body at all; the drill should use one, and
   does not yet.
9. **`?bracket=` is silently ignored** because the tier has no bracket selector (`query.go`, §9.3).
   Not a defect of the transition, recorded because a bracket-scoped URL is a plausible thing for a
   later lane to link to and it will not do what the link says.
10. **Not determined, and not determinable by this drill:** the read that spans the two renames of
    one publish (§8) is below the sampling floor and no client of this tier can be caught in it; a
    live patch rollover; and real-world timing for a patch whose partition is much larger than 11
    cells - all the latencies here are for a fixture-sized snapshot, and a publish-by-rename should
    not scale with partition size, but this drill does not prove that.

## 16. Reproducing this

### 16.1 The drill itself

```bash
git pull --rebase
scripts/verify-patch-rollover.sh --apply --soak-seconds 180 --pre-seconds 45
```

Requirements: `kubectl` pointing at the cluster, `python3` on the caller's machine, and
`bin/duckdb` (pinned v1.4.5) for the fixture parquet conversion. `--apply` is mandatory. The script
creates its own namespace (`lolstats-rollover`), its own RWX PVC on `nfs-client`, the tier, the seed
pod and the reader, runs the ten steps in §5-§11, writes `VERDICT.txt` and the raw evidence next to
it, and deletes the namespace on exit (`--keep` prints the delete command instead). It refuses to
run against `web`, `lolstats`, `caddy`, `default` or `kube-system`, and against any namespace not
starting with `lolstats-`.

### 16.2 The fail-closed body, reproduced locally (no cluster)

The withdrawn-partition state can be reproduced against the checked-in fixture tree, which is what
this document uses for the 503 body text, because the cluster run could not capture it:

```bash
go build -o .agent-artifacts/rollover/local-503/lolstats-web ./cmd/lolstats-web
cp -R fixtures/site .agent-artifacts/rollover/local-503/agg
mv .agent-artifacts/rollover/local-503/agg/v1/p/16.18/EUW/420/all \
   .agent-artifacts/rollover/local-503/withdrawn-16.18-all
cd .agent-artifacts/rollover/local-503
LOLSTATS_AGG_ROOT="$PWD/agg" LOLSTATS_AGG_FIXTURES=off LOLSTATS_WEB_ADDR=:18099 ./lolstats-web &
curl -s -D - -o body http://127.0.0.1:18099/tier-list/top | head -20
```

Measured with `LOLSTATS_AGG_FIXTURES=off` (the deployed posture) on
`lolstats-go-web`'s own code path:

| Route | With the partition withdrawn | With the tree intact (control) |
| --- | --- | --- |
| `/tier-list/top` | **503**, 32561 bytes, `data-fault="artifact"` | 200, 22 data rows, no `data-fault` |
| `/patch/16.18/tier-list/top` | **503**, 32597 bytes | 200 |
| `/explore/export.json` | **503** | 200 |
| `/readyz` | **503** `{"status":"unavailable","ok":false,"state":"demo","latest_patch":"16.18","patches":2,"error":"…/tierlist.json: artifact is missing"}` | 200 `{"status":"ok","ok":true,"state":"demo","latest_patch":"16.18","patches":2}` |
| `/agg/v1/manifest.json` | 200 (the manifest is still readable; the tree is intact, the *cells* are not) | 200 |

Headers on the 503: `Retry-After: 60`, `Cache-Control: no-store`, `Content-Type: text/html;
charset=utf-8`. The body is the standalone fault page:

```
The published snapshot is incomplete
The artifact this page is rendered from is missing or unreadable, so there is no table to show.
The manifest at the root of the published tree advertises this artifact, and an advertised
artifact is a promise that it is there. This site does not keep quiet about a broken promise: a
table with rows quietly missing cannot be told apart from a table with few rows, so the page is
withheld whole. Nothing has been substituted, no older copy has been served in its place, and no
rate has been estimated to fill the gap.
        …: artifact is missing     Requested path: /tier-list/top · HTTP 503 · fault artifact
What would make this page work: The snapshot is republished from the pipeline that produces it,
and this page answers normally once it has. The report below names the artifact that could not be
read.
```

Two disclosures about this local run: it uses the **checked-in demo tree**, so the banner above the
fault reads `PREVIEW - illustrative data` and `/readyz` reports `"state":"demo"`; and it is a local
process, not the cluster pod. The fault path, the fault kind, the statuses, the headers and the
prose are the tier's own, and the same classification is pinned by the repository's test
`TestUnpublishedSnapshotIs404ForArtifactsAnd503ForPages` (`internal/webtier/server_test.go:684`),
which asserts the fault kind in the rendered body.

### 16.3 What a *real* rollover will still need

When Riot ships a patch, this drill's shape transfers, but three things about it do not:

1. the *crawl* (a real `dt=<day>` partition arriving from Riot) instead of a copy from `staging`,
2. production thresholds (`min_cell_n` and the confidence gate; the drill uses 2 and a relaxed gate
   so that a 14-match fixture produces cells at all),
3. a partition of production size, which is the only part of the latency story this drill does not
   cover.

## 17. Artefacts

| Path | What it is |
| --- | --- |
| `scripts/verify-patch-rollover.sh` | the drill; the only file this work added to the product tree |
| `.agent-artifacts/rollover/runs/20260917T223410Z/VERDICT.txt` | the recorded run's verdict, with every number quoted here |
| `…/flip-window.txt`, `…/soak-post.log` | the 360 tick records and the flip window |
| `…/agg-a.log`, `…/agg-b.log`, `…/agg-b-rerun.log`, `…/agg-b-restore.log`, `…/reindex-16.18.log`, `…/reindex-16.19.log` | build and manifest logs, including the publish timings in §7 |
| `…/manifest-after-b.json`, `…/manifest-after-rerun.json`, `…/manifest-after-withdrawal.json` | the manifests at each step |
| `…/cells-16.19-before-rerun.json`, `…/cells-16.19-after-rerun.json` | the 11-cell comparison behind §11 |
| `…/empty-pre-16.19.html`, `…/served-16.18.html`, `…/served-16.19.html`, `…/served-export.json` | the served pages quoted in §6 and §9, and the byte-exact export |
| `…/withdrawn-statuses.txt`, `…/local503-*.body`, `…/local503-*.head` | the cluster statuses, and the local reproduction's bodies and headers (§10.3, §16.2) |
| `…/web-identity-before.txt`, `…/web-identity-after.txt`, `…/pods-before.txt`, `…/pods-after.txt` | §12 |
| `…/nfs-mount-options.txt`, `…/seeded.txt`, `…/reader-image.txt`, `…/trash-dirs-after-rerun.txt` | the mount options, the seeded raw days, the reader image digest, the residue check |
| `docs/PATCH-ROLLOVER-EVIDENCE.md` | this document; the excerpts above are verbatim from those files |
