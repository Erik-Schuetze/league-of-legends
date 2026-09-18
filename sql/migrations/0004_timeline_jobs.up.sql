-- fetch_queue learns what kind of payload a row is for.
--
-- A match summary and a match timeline are two Riot requests for the same match
-- id. They fail for different reasons (a timeline ages out after one year while
-- the summary lives two), they are rate-limited against separate buckets, and a
-- crawler can legitimately want one without the other. Giving the timeline its
-- own queue table would duplicate the whole claim/retry/backoff/dead-letter/
-- revival state machine; a kind column costs one index and no code.
--
-- The column defaults to 'match', which is what every existing row means, so
-- this migration is a no-op for the summary backlog.

BEGIN;

ALTER TABLE fetch_queue
    ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'match';

ALTER TABLE fetch_queue
    DROP CONSTRAINT IF EXISTS fetch_queue_kind_check;
ALTER TABLE fetch_queue
    ADD CONSTRAINT fetch_queue_kind_check CHECK (kind IN ('match', 'timeline'));

-- The unique key becomes the pair. 0001 made match_id unique and 0002 dropped
-- its foreign key to matches, so a timeline row can now exist beside a summary
-- row for the same match without disturbing it. Keeping match_id alone unique
-- would make timeline rows unrepresentable.
ALTER TABLE fetch_queue
    DROP CONSTRAINT IF EXISTS fetch_queue_match_id_key;

ALTER TABLE fetch_queue
    DROP CONSTRAINT IF EXISTS fetch_queue_match_kind_key;
ALTER TABLE fetch_queue
    ADD CONSTRAINT fetch_queue_match_kind_key UNIQUE (match_id, kind);

-- The claim query filters on kind and then orders by priority, not_before and
-- id, so kind leads the index. A kind-blind index would make the timeline claim
-- a scan over the summary backlog, which is the larger of the two by an order
-- of magnitude.
DROP INDEX IF EXISTS fetch_queue_retry_idx;

CREATE INDEX fetch_queue_kind_ready_idx
    ON fetch_queue (kind, status, not_before, priority, id)
    WHERE status IN ('pending', 'retry');

COMMENT ON COLUMN fetch_queue.kind IS
    'Payload the row requests: match (MATCH-V5 summary) or timeline (MATCH-V5 timeline). Attempt budgets, retries and revivals are per row, so the two kinds cannot consume each other''s budget.';

COMMIT;
