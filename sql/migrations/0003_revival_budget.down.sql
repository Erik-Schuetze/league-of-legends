-- Reverses 0003_revival_budget.up.sql.
--
-- The counter is dropped and the crawl's revival goes back to being unbounded,
-- which is the behaviour 0002 shipped. The reverse is lossless in the only
-- direction that matters: queue progress lives in status, attempts, not_before
-- and claimed_at, none of which this migration touches, so a down/up cycle costs
-- the revival history and not a single row of work.

BEGIN;

ALTER TABLE fetch_queue DROP COLUMN IF EXISTS revivals;

COMMIT;
