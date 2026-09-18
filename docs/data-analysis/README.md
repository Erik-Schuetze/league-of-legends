# Data analysis

A field-level inventory of the data this project can obtain and store: what the Riot API
returns, and what our own database holds. It lists columns. It does not explain, rank or
sample them.

Last reviewed: 2026-09-18. Next review due: 2026-12-17.

## Where the data is

| Store | Holds match content? | In this directory? | What it is |
| --- | --- | --- | --- |
| Riot API | yes - it is the source | `riot-api.md` | MATCH-V5, LEAGUE-V4, ACCOUNT-V1 and Data Dragon, as read by `internal/riot` |
| Raw Parquet archive (`raw/riot/**`) | yes - verbatim payloads | no | Primary data. Append-only zstd Parquet, partitioned by fetch date, and the only copy once a payload ages out of Riot's retention |
| PostgreSQL | **no** | `postgres.md` | Control-plane state: crawl queue, crawl frontier, build history |
| Derived datasets (`agg/v1`, `timeline-v1`) | derived from the archive | no | Published artifacts, rebuilt from the archive; their shape is in `internal/aggmodel/schema.go` and `internal/aggregate/features_schema.go` |

Two things this table is here to say:

- **Postgres holds no match content.** No champion, item, rune, participant, outcome or
  timeline column exists in any of its seven tables.
- **The archive is a superset of everything typed.** It stores the whole response body, so
  any field Riot returns and `riot-api.md` does not name is still retrievable.

## Access

Nothing in this directory requires a Postgres credential or a Riot API key. Both files are
the schema, not a connection. A future request for actual data would need either a
read-only database role or a copy of the archive tree; neither exists yet.

## Fields that must not be published

`puuid`, `gameName` and `tagLine` are per-player identifiers. They appear in the Riot
payloads and in one database column, `crawl_frontier.puuid`, and they must not be published
(obligation 9 in `docs/compliance.md`). The column tables mark every field in this category.

## See also

- [`riot-api.md`](riot-api.md) - every Riot field this project models, per endpoint
- [`postgres.md`](postgres.md) - every column of every table
- [`../data-sources.md`](../data-sources.md) - where each source comes from, under what terms,
  and how long Riot retains it
- [`../architecture.md`](../architecture.md) - how the stores relate and what writes to each
- [`../aggregation.md`](../aggregation.md) - what is computed from the archive
