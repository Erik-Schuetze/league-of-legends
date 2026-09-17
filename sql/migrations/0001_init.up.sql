-- Control plane for the LoL statistics pipeline.
--
-- This migration owns only tables whose write volume is bounded by pipeline
-- events: one row per match, per queue item, per PUUID, per build. Per-participant
-- feature rows never live here; they go to Parquet and are read by DuckDB, so the
-- aggregate shape can change without a migration (see docs/decisions/ADR-002).

BEGIN;

CREATE TABLE matches (
    match_id         text        PRIMARY KEY,
    region           text        NOT NULL,
    queue_id         integer     NOT NULL,
    patch            text        NOT NULL,
    game_version     text        NOT NULL,
    game_creation    timestamptz NOT NULL,
    game_duration_s  integer     NOT NULL,
    payload_version  integer     NOT NULL,
    raw_uri          text        NOT NULL,
    status           text        NOT NULL,
    fetched_at       timestamptz,
    parsed_at        timestamptz,
    error            text,
    CONSTRAINT matches_status_check CHECK (status IN ('queued', 'fetched', 'parsed', 'failed', 'dead'))
);

-- Crawl, patch and staleness queries all filter on this tuple before ordering by
-- time, so the index matches the column order the queries use.
CREATE INDEX matches_region_queue_patch_idx
    ON matches (region, queue_id, patch, game_creation DESC);

-- Partial index keeps the "what still needs work" scan proportional to the
-- backlog rather than to the whole table.
CREATE INDEX matches_pending_idx
    ON matches (status, game_creation)
    WHERE status IN ('queued', 'fetched', 'failed');

CREATE TABLE fetch_queue (
    id         bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    match_id   text        NOT NULL UNIQUE REFERENCES matches (match_id) ON DELETE CASCADE,
    priority   integer     NOT NULL DEFAULT 100,
    attempts   integer     NOT NULL DEFAULT 0,
    not_before timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    status     text        NOT NULL DEFAULT 'pending',
    CONSTRAINT fetch_queue_status_check CHECK (status IN ('pending', 'claimed', 'done', 'failed', 'dead')),
    CONSTRAINT fetch_queue_attempts_check CHECK (attempts >= 0)
);

-- The worker's claim query is "lowest priority, then oldest ready row", which is
-- exactly this index order.
CREATE INDEX fetch_queue_claim_idx
    ON fetch_queue (status, not_before, priority, id)
    WHERE status = 'pending';

CREATE INDEX fetch_queue_claimed_idx
    ON fetch_queue (claimed_at)
    WHERE status = 'claimed';

CREATE TABLE crawl_frontier (
    puuid              text        PRIMARY KEY,
    region             text        NOT NULL,
    seed_tier          text        NOT NULL,
    seed_division      text        NOT NULL,
    last_seen_at       timestamptz,
    last_fetched_at    timestamptz,
    consecutive_empty  integer     NOT NULL DEFAULT 0,
    priority           integer     NOT NULL DEFAULT 100,
    dead               boolean     NOT NULL DEFAULT false,
    CONSTRAINT crawl_frontier_consecutive_empty_check CHECK (consecutive_empty >= 0)
);

-- Frontier selection is "not dead, ordered by priority then least recently
-- fetched"; NULLS FIRST puts never-fetched PUUIDs at the head of the queue.
CREATE INDEX crawl_frontier_pick_idx
    ON crawl_frontier (region, priority, last_fetched_at ASC NULLS FIRST)
    WHERE dead = false;

CREATE TABLE crawl_seeds (
    id            bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tier          text        NOT NULL,
    division      text        NOT NULL,
    queue         integer     NOT NULL,
    region        text        NOT NULL,
    started_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz,
    entries_found integer     NOT NULL DEFAULT 0,
    CONSTRAINT crawl_seeds_entries_found_check CHECK (entries_found >= 0)
);

CREATE INDEX crawl_seeds_recent_idx ON crawl_seeds (region, tier, division, started_at DESC);

CREATE TABLE build_runs (
    id               bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    patch            text        NOT NULL,
    region           text        NOT NULL,
    queue            integer     NOT NULL,
    bracket          text        NOT NULL,
    started_at       timestamptz NOT NULL DEFAULT now(),
    finished_at      timestamptz,
    status           text        NOT NULL DEFAULT 'running',
    cells_total      integer     NOT NULL DEFAULT 0,
    cells_published  integer     NOT NULL DEFAULT 0,
    cells_suppressed integer     NOT NULL DEFAULT 0,
    git_sha          text        NOT NULL,
    artifact_uri     text,
    CONSTRAINT build_runs_status_check CHECK (status IN ('running', 'succeeded', 'failed')),
    -- Published plus suppressed can never exceed the total the build counted, so a
    -- run whose counts disagree with itself is rejected at write time instead of
    -- being surfaced later as a suspiciously thin patch.
    CONSTRAINT build_runs_totals_check CHECK (cells_published + cells_suppressed <= cells_total)
);

CREATE INDEX build_runs_segment_idx
    ON build_runs (patch, region, queue, bracket, started_at DESC);

CREATE TABLE source_toggles (
    source        text        PRIMARY KEY,
    enabled       boolean     NOT NULL DEFAULT false,
    decided_by    text        NOT NULL,
    decided_at    timestamptz NOT NULL DEFAULT now(),
    review_due_at timestamptz,
    notes         text
);

CREATE INDEX source_toggles_review_idx
    ON source_toggles (review_due_at)
    WHERE enabled = true;

COMMIT;
