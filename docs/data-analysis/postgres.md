# PostgreSQL

Our database schema, column by column. It is the control plane: crawl state and build
history.

**There is no match content in any of these tables.** No champion, item, rune, participant,
outcome or timeline column exists here. The payloads are in the raw archive; this database
records what has been fetched and what has been built.

Last reviewed: 2026-09-18. Next review due: 2026-12-17.

Seven tables, 65 columns, as created by `sql/migrations/0001` through `0004`.

## `matches`

One row per match the crawl knows about.

| Column | Type | Null / default | What it is |
| --- | --- | --- | --- |
| `match_id` | text | PK | Riot match id. A real identifier, **not publishable** |
| `region` | text | not null | Platform routing value, e.g. `EUW1` |
| `queue_id` | integer | not null | Riot queue id; `420` is ranked solo |
| `patch` | text | not null | Major.minor from `gameVersion`, e.g. `16.18` |
| `game_version` | text | not null | Full Riot `gameVersion` |
| `game_creation` | timestamptz | not null | From `info.gameCreation` |
| `game_duration_s` | integer | not null | From `info.gameDuration`, in **seconds** |
| `payload_version` | integer | not null | Archive payload schema version |
| `raw_uri` | text | not null | Archive partition holding the payload |
| `status` | text | not null | `queued`, `fetched`, `parsed`, `failed`, `dead` |
| `fetched_at` | timestamptz | null until fetched | |
| `parsed_at` | timestamptz | null until parsed | |
| `error` | text | null | Last failure cause |

## `fetch_queue`

One row per unit of fetch work. Work can exist before the match row does.

| Column | Type | Null / default | What it is |
| --- | --- | --- | --- |
| `id` | bigint | PK, identity | |
| `match_id` | text | not null | Riot match id. Unique together with `kind` |
| `priority` | integer | not null, `100` | Lower is claimed first |
| `attempts` | integer | not null, `0` | Attempts spent in the current budget |
| `not_before` | timestamptz | not null, `now()` | Earliest time the row may be claimed |
| `claimed_at` | timestamptz | null | Set while a worker holds the row |
| `status` | text | not null, `pending` | `pending`, `claimed`, `done`, `retry`, `failed`, `dead` |
| `last_cause` | text | null | Cause of the last retry or dead-lettering |
| `revivals` | integer | not null, `0` | How many times the crawl has granted a fresh attempt budget. Operator replays do not count |
| `kind` | text | not null, `match` | Payload requested: `match` (summary) or `timeline` |

## `crawl_frontier`

One row per player the crawl is walking. This is the only table keyed on a person.

| Column | Type | Null / default | What it is |
| --- | --- | --- | --- |
| `puuid` | text | PK | A real PUUID. **Per-player, not publishable** |
| `region` | text | not null | Platform routing value |
| `seed_tier` | text | not null | Tier the PUUID was first seen at |
| `seed_division` | text | not null | Division it was first seen in |
| `last_seen_at` | timestamptz | null | Last time the ladder mentioned it |
| `last_fetched_at` | timestamptz | null | Last time its match history was read |
| `consecutive_empty` | integer | not null, `0` | Consecutive fetches that found nothing new |
| `priority` | integer | not null, `100` | Lower is fetched sooner |
| `dead` | boolean | not null, `false` | Retired from the walk |
| `dead_cause` | text | null | Why it was retired |

## `crawl_seeds`

One row per ladder page pulled, which is the audit trail for where the frontier came from.

| Column | Type | Null / default | What it is |
| --- | --- | --- | --- |
| `id` | bigint | PK, identity | |
| `tier` | text | not null | Tier paged |
| `division` | text | not null | Division paged |
| `queue` | text | not null | Ladder name, e.g. `RANKED_SOLO_5x5` |
| `region` | text | not null | Platform routing value |
| `started_at` | timestamptz | not null, `now()` | |
| `finished_at` | timestamptz | null | |
| `entries_found` | integer | not null, `0` | Entries the page returned |

## `build_runs`

One row per aggregate build.

| Column | Type | Null / default | What it is |
| --- | --- | --- | --- |
| `id` | bigint | PK, identity | |
| `patch` | text | not null | Patch built |
| `region` | text | not null | Region built |
| `queue` | integer | not null | Queue built |
| `bracket` | text | not null | Rank bracket built |
| `started_at` | timestamptz | not null, `now()` | |
| `finished_at` | timestamptz | null | |
| `status` | text | not null, `running` | `running`, `succeeded`, `failed` |
| `cells_total` | integer | not null, `0` | Cells the build considered |
| `cells_published` | integer | not null, `0` | |
| `cells_suppressed` | integer | not null, `0` | Cells suppressed for being thin |
| `git_sha` | text | not null | Commit the build ran from |
| `artifact_uri` | text | null | Where the artifacts were written |
| `error` | text | null | Failure cause |

## `source_toggles`

One row per optional data source. **No row is seeded**, and the default is off, so every
optional source - including third-party scraping - is disabled by absence.

| Column | Type | Null / default | What it is |
| --- | --- | --- | --- |
| `source` | text | PK | Source name |
| `enabled` | boolean | not null, `false` | |
| `decided_by` | text | not null | Who made the call |
| `decided_at` | timestamptz | not null, `now()` | |
| `review_due_at` | timestamptz | null | Date the decision must be re-taken |
| `notes` | text | null | |

## `schema_migrations`

The migration ledger, written by `internal/store/migrate.go`.

| Column | Type | Null / default | What it is |
| --- | --- | --- | --- |
| `version` | bigint | PK | Migration number |
| `name` | text | not null | Migration name |
| `checksum` | text | not null | Body checksum; a changed body is rejected |
| `applied_at` | timestamptz | not null, `now()` | |

## Constraints and indexes

A query writer needs the legal values, so they are listed here rather than left to the
migration files.

| Table | Constraint or index | Columns / values |
| --- | --- | --- |
| `matches` | `matches_status_check` | `queued`, `fetched`, `parsed`, `failed`, `dead` |
| `matches` | `matches_region_queue_patch_idx` | `region, queue_id, patch, game_creation DESC` |
| `matches` | `matches_pending_idx` | Partial on `status, game_creation`, where `status IN ('queued','fetched','failed')` |
| `fetch_queue` | `fetch_queue_status_check` | `pending`, `claimed`, `done`, `retry`, `failed`, `dead` |
| `fetch_queue` | `fetch_queue_kind_check` | `match`, `timeline` |
| `fetch_queue` | `fetch_queue_attempts_check` | `attempts >= 0` |
| `fetch_queue` | `fetch_queue_match_kind_key` | `UNIQUE (match_id, kind)` |
| `fetch_queue` | `fetch_queue_kind_ready_idx` | Partial on `kind, status, not_before, priority, id`, where `status IN ('pending','retry')`. The current claim index |
| `fetch_queue` | `fetch_queue_claim_idx` | Partial on `status, not_before, priority, id`, where `status = 'pending'`. Superseded by `fetch_queue_kind_ready_idx` and left in place |
| `fetch_queue` | `fetch_queue_claimed_idx` | Partial on `claimed_at`, where `status = 'claimed'` |
| `fetch_queue` | `fetch_queue_dead_idx` | Partial on `claimed_at`, where `status = 'dead'` |
| `crawl_frontier` | `crawl_frontier_consecutive_empty_check` | `consecutive_empty >= 0` |
| `crawl_frontier` | `crawl_frontier_pick_idx` | Partial on `region, priority, last_fetched_at ASC NULLS FIRST`, where `dead = false` |
| `crawl_seeds` | `crawl_seeds_entries_found_check` | `entries_found >= 0` |
| `crawl_seeds` | `crawl_seeds_recent_idx` | `region, tier, division, started_at DESC` |
| `build_runs` | `build_runs_status_check` | `running`, `succeeded`, `failed` |
| `build_runs` | `build_runs_totals_check` | `cells_published + cells_suppressed <= cells_total` |
| `build_runs` | `build_runs_segment_idx` | `patch, region, queue, bracket, started_at DESC` |
| `source_toggles` | `source_toggles_review_idx` | Partial on `review_due_at`, where `enabled = true` |

`fetch_queue.revivals` carries no `CHECK`; the only bound on it is the crawl's own revival
budget, not a database constraint.

## What this set can and cannot answer

**Can.** Crawl and build questions: how many matches are known per patch, region and queue;
how deep the fetch queue is and in what state; which PUUIDs are known and when they were last
fetched; which builds ran, for which bracket, and how thin they were.

**Cannot.** Anything about champions, items, runes, participants, outcomes or timelines. No
such column exists in any of the seven tables. Those live in the raw archive and in the
derived datasets.
