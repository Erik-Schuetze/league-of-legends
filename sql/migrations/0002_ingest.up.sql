-- Additive changes the ingest side needs from the schema in 0001.
--
-- Three things, all discovered while implementing the crawler:
--
-- 1. The queue vocabulary. contract.JobStatus has five values and 0001's CHECK
--    admits 'failed' instead of 'retry', so a retryable fetch failure had
--    nowhere legal to go: writing it as 'failed' means the claim query cannot
--    tell it apart from a dead letter, and writing it as 'pending' loses the
--    fact that it already failed. 'retry' is added and 'failed' is kept for
--    rows written by anything older.
--
-- 2. The foreign key from fetch_queue to matches. Queue rows are *work*, and the
--    work predates the payload: a match id discovered on a history page has no
--    match row yet, and inventing one would mean a matches row whose patch,
--    queue_id and raw_uri are lies until the fetch succeeds. Dedupe does not
--    depend on the FK - fetch_queue.match_id is UNIQUE and matches.match_id is
--    the primary key - and nothing deletes match rows, so ON DELETE CASCADE was
--    protecting an operation that does not happen.
--
-- 3. Diagnostics. RetryJob and DeadLetterJob take a cause and MarkFrontierDead
--    takes one too; the schema had nowhere to keep them, which makes a stalled
--    queue undiagnosable from the database. They are nullable: an old row with
--    no cause is not an error, and a job that is retried again overwrites the
--    previous cause with the newer one, because the newer one is the one the
--    operator is looking at.
--
-- crawl_seeds.queue moves from integer to text because contract.SeedRun.Queue is
-- a string naming the ladder (RANKED_SOLO_5x5), and the seed audit is worthless
-- if it cannot say which ladder was paged.

BEGIN;

ALTER TABLE fetch_queue DROP CONSTRAINT IF EXISTS fetch_queue_match_id_fkey;

ALTER TABLE fetch_queue DROP CONSTRAINT IF EXISTS fetch_queue_status_check;
ALTER TABLE fetch_queue ADD CONSTRAINT fetch_queue_status_check
    CHECK (status IN ('pending', 'claimed', 'done', 'retry', 'failed', 'dead'));

-- Re-claimable rows are read exactly like fresh ones, so the claim index has to
-- cover both statuses; 0001's index is partial on 'pending' only and would leave
-- a retry backlog to a sequential scan.
CREATE INDEX fetch_queue_retry_idx
    ON fetch_queue (status, not_before, priority, id)
    WHERE status IN ('pending', 'retry');

-- Dead letters are read by an operator asking "what is dying, and when did it
-- stop moving", which is a scan ordered by time rather than by priority.
CREATE INDEX fetch_queue_dead_idx
    ON fetch_queue (claimed_at)
    WHERE status = 'dead';

ALTER TABLE fetch_queue ADD COLUMN IF NOT EXISTS last_cause text;

ALTER TABLE crawl_frontier ADD COLUMN IF NOT EXISTS dead_cause text;

ALTER TABLE build_runs ADD COLUMN IF NOT EXISTS error text;

ALTER TABLE crawl_seeds ALTER COLUMN queue TYPE text USING queue::text;

COMMIT;
