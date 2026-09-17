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
   key can be swapped under a running worker.
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

A worker that is killed mid-fetch leaves its rows `claimed`. They are reclaimed
by the grace window, not by hand:

```
lolstats-ingest maintain                 # reclaims claims older than 15m
lolstats-ingest maintain -dry-run        # what would be reclaimed, changed nothing
```

`-claim-grace` moves the window. It should stay comfortably above the client
timeout (`DefaultJobTimeout`, 25s): a short grace reclaims claims that are still
being worked, which duplicates work rather than losing it - the archive and the
`match_id` upsert both make the duplicate harmless, but it spends rate-limit
budget twice.

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
