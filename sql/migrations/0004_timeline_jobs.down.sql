-- Reverses 0004_timeline_jobs.up.sql.
--
-- Timeline rows must be deleted before the pair key can go back to a single
-- unique match_id, because a match with both a summary row and a timeline row
-- has two rows that the old key cannot describe. That deletion is the point at
-- which this reverse is not lossless: the row records that a timeline was
-- fetched, and the payload itself stays in the archive, but the queue's memory
-- of *which* timelines are done does not survive. A down/up cycle therefore
-- re-offers every already-fetched timeline to the backfill, which then spends
-- rate-limit budget re-fetching payloads the archive already holds.
--
-- It is written this way rather than left as a documented caveat because the
-- alternative - keeping the duplicate rows and failing to restore the key -
-- would leave the schema in a state neither migration describes.
--
-- Summary rows are untouched: every row whose kind is 'match' keeps its status,
-- attempts, not_before, claimed_at and revivals.

BEGIN;

DELETE FROM fetch_queue WHERE kind = 'timeline';

DROP INDEX IF EXISTS fetch_queue_kind_ready_idx;

CREATE INDEX fetch_queue_retry_idx
    ON fetch_queue (status, not_before, priority, id)
    WHERE status IN ('pending', 'retry');

ALTER TABLE fetch_queue
    DROP CONSTRAINT IF EXISTS fetch_queue_match_kind_key;

ALTER TABLE fetch_queue
    ADD CONSTRAINT fetch_queue_match_id_key UNIQUE (match_id);

ALTER TABLE fetch_queue
    DROP CONSTRAINT IF EXISTS fetch_queue_kind_check;

ALTER TABLE fetch_queue DROP COLUMN IF EXISTS kind;

COMMIT;
