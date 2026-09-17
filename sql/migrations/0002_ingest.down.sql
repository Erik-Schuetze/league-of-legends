-- Reverses 0002_ingest.up.sql.
--
-- The reverse is deliberately lossy in one direction only: the diagnostics
-- columns (retry causes, dead-letter reasons, build errors) are dropped, because
-- 0001 has no column to move them into. Everything the crawler needs to resume -
-- status, attempts, not_before, claimed_at, the frontier fields - lives in 0001
-- and is untouched, so a down/up cycle costs history, not progress.
--
-- The fetch_queue -> matches foreign key is *not* restored: rows enqueued while
-- it was absent have no matches row to reference, so re-adding it would fail on
-- exactly the data the migration exists to allow.
--
-- Statuses are normalised before the narrower CHECK is restored, and the ladder
-- name is reduced to its numeric queue id ('RANKED_SOLO_5x5' -> 420) because
-- 0001's column has no room for the name.

BEGIN;

ALTER TABLE crawl_seeds ALTER COLUMN queue TYPE integer
    USING NULLIF(substring(queue from '[0-9]+'), '')::integer;

ALTER TABLE build_runs DROP COLUMN IF EXISTS error;

ALTER TABLE crawl_frontier DROP COLUMN IF EXISTS dead_cause;

ALTER TABLE fetch_queue DROP COLUMN IF EXISTS last_cause;

DROP INDEX IF EXISTS fetch_queue_dead_idx;
DROP INDEX IF EXISTS fetch_queue_retry_idx;

UPDATE fetch_queue SET status = 'pending' WHERE status = 'retry';

ALTER TABLE fetch_queue DROP CONSTRAINT IF EXISTS fetch_queue_status_check;
ALTER TABLE fetch_queue ADD CONSTRAINT fetch_queue_status_check
    CHECK (status IN ('pending', 'claimed', 'done', 'failed', 'dead'));

COMMIT;
