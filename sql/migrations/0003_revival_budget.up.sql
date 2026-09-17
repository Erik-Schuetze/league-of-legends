-- A budget for the one thing that can bring a dead letter back.
--
-- 0002 left dead letters terminal with two ways back: EnqueueMatches revives a
-- dead row when the crawl rediscovers the match, and maintain
-- -replay-dead-letters revives a batch on an operator's instruction. Both of
-- them reset `attempts` to zero, which is right for the condition they were
-- written for - a revoked key retires rows that are fine, and the crawl should
-- be able to pick them up again - but together they make the cycle
--
--     dead -> pending -> claimed -> retry -> claimed -> dead
--
-- unendable, because nothing on the row records that it has already been
-- through it. A match that fails for its own reasons is rediscovered by every
-- crawl of every player who played it, so "the crawl rediscovered it" is not
-- evidence that anything has changed. The verification run showed the loop
-- directly: one poison row was re-fetched for every walk, three walks in a row,
-- each time returning to 'dead' with its attempt budget reset to the same two.
-- A permanently failing row therefore consumed fetch budget and limiter budget
-- forever without ever being retired.
--
-- `revivals` counts how many times the *crawl* has given the row a fresh
-- attempt budget, not how many times the row has failed: a row can be revived
-- once and fail the budget-many times inside that revival, and those are
-- different numbers. EnqueueMatches is bounded by it (store.revivalBudget);
-- ReplayDeadLettered is not, because an operator replaying dead letters is
-- asserting that the global condition which retired them has passed, which is a
-- claim only a human can make and is exactly the case where a row may
-- legitimately need a new budget.
--
-- The column is NOT NULL DEFAULT 0 rather than nullable: "never revived" and
-- "revived zero times" are the same state, and a null counter would make every
-- comparison in the revival predicate a three-valued one.
--
-- Nothing else about the queue changes. A row's status, attempts, not_before and
-- claimed_at keep their meaning, so this migration neither releases nor retires
-- a single row that was already in flight.

BEGIN;

ALTER TABLE fetch_queue ADD COLUMN IF NOT EXISTS revivals integer NOT NULL DEFAULT 0;

COMMIT;
