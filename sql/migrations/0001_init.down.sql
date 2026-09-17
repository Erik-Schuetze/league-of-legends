-- Reverses 0001_init.up.sql.
--
-- Dropping these tables discards dedupe and provenance state but never the raw
-- archive or the derived artifacts, both of which live outside Postgres. A
-- rebuild after a down/up cycle therefore re-fetches nothing it already has.

BEGIN;

DROP TABLE IF EXISTS source_toggles;
DROP TABLE IF EXISTS build_runs;
DROP TABLE IF EXISTS crawl_seeds;
DROP TABLE IF EXISTS crawl_frontier;
DROP TABLE IF EXISTS fetch_queue;
DROP TABLE IF EXISTS matches;

COMMIT;
