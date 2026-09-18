# Runbook: ingest down

Written from the ingest side. It covers `cmd/lolstats-ingest` only; the build and
the site have their own runbooks.

## Symptom: the worker is up but nothing is being fetched

Work down this list in order. Each step is a question with a cheap answer.

1. **Is there a key?** `LOLSTATS_RIOT_API_KEY` is optional by design, and the
   binary starts without it: it serves `/healthz` and `/metrics` and does
   nothing else, leaving `fetch_queue` and `crawl_frontier` untouched rather
   than spending the attempts of rows it cannot fetch. That state is one warn
   line, `no Riot API key configured: crawling is disabled, health and metrics
   continue`, followed by the loop's own note at info level, `crawler idle: no
   Riot API key yet`. A healthy process with a flat
   `lolstats_riot_requests_total` is almost always this.
2. **Is the key age past the rotation warning?** A development key expires
   after 24 hours. `lolstats_riot_key_age_seconds` above 12h means the swap is
   due. Point `LOLSTATS_RIOT_API_KEY_FILE` at the key file and rotate that file
   rather than restarting: the key provider re-reads it, so a 24h development
   key can be swapped under a running worker. Read the number from `/readyz`
   (`riot_key_age_seconds`; a `0` there means no key is held, not a fresh key)
   and treat the metric as a cross-check: it was a constant `0` until the gauge
   was repaired on 2026-09-18, it now tracks `/readyz` (measured
   `1023.833030043` -> `1099.692236706`), refreshes only once per report, and -
   like `/readyz` - measures *this process's* key lifetime, so both reset on
   every deploy and neither is an expiry clock.
3. **Is the circuit open?** Repeated 403 or 429 responses open the breaker.
   `lolstats_riot_requests_total{status="403"}` climbing while other statuses
   are flat is an expired or revoked key, not a rate problem.
4. **Is the queue stalled?** `maintain` reports it:
   `lolstats-ingest maintain -dry-run` prints frontier size, dead frontier,
   queue depths by status and the age of the oldest pending row. A large
   `retry` bucket means the fetches are failing; a large `claimed` bucket that
   does not move means a worker died holding a claim.
5. **Is the frontier empty?** A crawl with an empty frontier has nothing to
   walk. Seed it: `lolstats-ingest discover-seeds -tier GOLD -division I`. The
   pass opens a `crawl_seeds` row, so an empty frontier is traceable to "the
   seeding found nothing" rather than to "the seeding did not run".

## Symptom: claims are stuck

A worker that is killed mid-fetch leaves its rows `claimed`. Two things reclaim
them, both under the same grace window:

```
lolstats-ingest worker                   # reclaims expired claims on boot
lolstats-ingest maintain                 # reclaims claims older than 15m
lolstats-ingest maintain -dry-run        # what would be reclaimed, changed nothing
```

The worker's own pass runs before it claims anything, so a crash and restart is
a recovery and not a wait: before it, a restart recovered nothing and the run
stalled until the hourly `maintain` job's grace had passed, which put a crash as
much as seventy-five minutes behind. The boot pass hands back at most 1000 rows
(`claimRecoveryLimit`); a backlog larger than that is drained by the next boot
or by `maintain`. `maintain` is still the periodic safety net, and it logs
`maintain: reclaimed abandoned claims`.

A reclaimed row whose match is already in `matches` is closed without a fetch:
the row's work is finished, and fetching it again would append a second record to
an archive that has no key. The worker logs `match already archived; row closed
without a fetch` at debug level for each one. A match the control plane cannot be
asked about is fetched as usual, so a store outage costs a duplicate record
rather than a skipped match.

`-claim-grace` moves the window for both. It should stay comfortably above the
client timeout (`DefaultJobTimeout`, 25s): a short grace reclaims claims that are
still being worked, which duplicates work rather than losing it - a worker closes
a row whose match is already in `matches` without fetching it, and the aggregate
reads the archive one row per match, so a duplicate cannot move a published
number - but it spends rate-limit budget twice and appends a record to an archive
that has no key.

A graceful `TERM` does not leave claims behind for either pass: the worker stops
claiming, releases the rows of the batch it had not started, and schedules the
rows that were in flight, so only a `SIGKILL` produces the stranded claims above.
The half of the batch that was already archived is finished on the way out - the
final flush and the updates that close those rows run on a context that outlives
the stop, because the writer rejects a flush on a cancelled one - so the process
log ends with `released unstarted rows at shutdown` and, when a batch had already
archived something, `closed the archived rows of the interrupted batch`. A batch
whose flush fails for a real reason is left claimed on purpose: the archive holds
nothing for those rows yet, and a boot's pass (or `maintain`) hands them back.

## Symptom: rows retire with cause `archive`

The archive is written before the control-plane row (`worker.go`), and a write
that fails - a read-only or mis-mounted raw volume - puts the row back on the
queue. That retry is bounded like any other failure now: each row climbs to
`-max-attempts` and then goes `dead` with `last_cause = 'archive'`, with the
process log carrying one `match requeued ... "cause":"archive"` line per
attempt and one `match dead-lettered` line at the end. Before, the archive path
bypassed the attempt ceiling and requeued the same rows with no growth, which
turned a bad mount into an unbounded hot loop (measured: 2107 requeues in 60s).

`fetch_queue.revivals` counts how often a retired row has been given back to the
crawl. The queue revives a `dead` row on its own at most three times; after
that the row is terminal and moving it again is a deliberate act:

```
lolstats-ingest maintain -replay-dead-letters   # returns dead rows to the queue
```

Nothing is dropped: a row that is `dead` still holds its `match_id` and cause,
and a re-fetch of it is a no-op in the control plane because the archive is
content-addressed and `matches` is keyed by `match_id`.

A `429` with a `Retry-After` longer than one call's timeout is a wait, not a
shutdown: the worker classifies it from the response, not from the deadline that
expired while it was holding the call back, and schedules the row after at least
the `Retry-After`. If the process log shows rows in `job released before
shutdown` with no shutdown in progress, that classification is what regressed.

## Symptom: the archive is being written but rows are missing

The worker archives a payload before it writes the control-plane row, so a row
can be missing while the bytes are present. In that order the recovery is a
backfill of the same keys; in the other order the payload would be lost.
Re-run the keys rather than re-crawling the player:

```
lolstats-ingest backfill -match-ids EUW1_0000000000,EUW1_0000000001
lolstats-ingest backfill -puuid <puuid> -from 2026-03-01T00:00:00Z -to 2026-03-08T00:00:00Z -limit 200
```

A backfill is bounded by `-limit` and skips keys the control plane already knows
unless `-force` is given, so a repeated run is cheap. Failures are collected and
reported at the end rather than aborting the run.

## Symptom: static data is missing for a patch

The aggregates join champion, item, rune and summoner spell ids against the raw
archive. A patch whose static documents were never mirrored cannot be built:

```
lolstats-ingest static-sync               # newest patch from versions.json
lolstats-ingest static-sync -version 16.20.1
```

`versions.json` is always retained, even when a patch is pinned, because it is
the list that patch came from. The pass does not touch the control plane.

## Recovery order

When more than one thing is wrong, do it in this order: migrate, static, seeds,
maintain, worker.

`migrate up` first, because a worker against the old schema writes rows the new
one cannot read. Then `static-sync`, because it is independent of the crawl and
cheap. Then `discover-seeds` to give the crawl ground, then `maintain` to clear
whatever the previous run left behind, and only then start the worker.

## What to check before paging anyone

- `curl -s localhost:9090/healthz` - liveness only. It answers without a key.
- `curl -s localhost:9090/metrics | grep lolstats_riot` - request counts by
  method and status, retries by reason, and the age of the key in use.
- `lolstats-ingest maintain -dry-run` - the pipeline numbers, changed nothing.

The crawler is deliberately allowed to be idle. An idle crawler with a valid key
and a non-empty frontier is the only state worth escalating.
