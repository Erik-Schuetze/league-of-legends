# Aggregation fixture

A tiny, hand-authored raw archive plus the numbers a human computed from it by
hand, for the tests of `internal/aggregate`. It is the only raw archive the
build step is ever verified against in this repository, because there is no real
Riot data here and there must not be.

## Provenance and legality

**Nothing in this directory is real Riot data.** Every payload was written by
hand to match the documented MATCH-V5 shape, using the field set frozen in
`internal/riot/dto.go`. There is no captured payload, no scraped response and no
player data of any kind.

The identifiers make that obvious on sight: match ids are `EUW1_0000000001`
through `EUW1_0000000014`, PUUIDs are `fixture-puuid-01` through
`fixture-puuid-10`, and every `riotIdGameName` starts with `fixture-`. Champion
ids, item ids, rune ids and summoner spell ids are the **real** Riot ids,
because those are public static data and a fixture that invents them would test
nothing.

`corrupt/` is the same archive with one extra match whose payload is a valid
JSON document carrying no envelope at all (`{}`). It is the input for the
fail-closed test. It is deliberately *readable*: a fixture that fails to parse
would test the JSON parser, while a row that parses and is not a match is the
corruption only a gate can catch.

## Layout

```
fixtures/agg/raw/riot/match-v5/dt=<YYYY-MM-DD>/matches.jsonl      the valid archive
fixtures/agg/corrupt/riot/match-v5/dt=<YYYY-MM-DD>/matches.jsonl  the same, plus one bare row
```

The payloads are JSONL, one MATCH-V5 response body per line, rather than parquet:
they are the specification the expected cell table was computed from, and a
human has to be able to read them next to the numbers they produce. The build
itself reads **parquet** parts (`part-*.parquet[.zst]`, see `docs/contracts.md`
section 4), so the test converts the JSONL with the pinned DuckDB client at test
time, into a scratch directory. Two consequences, both deliberate:

- a checked-in binary part can never drift away from the JSONL that explains it;
- the conversion is itself a test of the client version that `docs/aggregation.md`
  pins, so a mismatched local engine shows up as a fixture failure rather than as
  a subtly different aggregation.

`TestFixtureSourcesAreCurrent` compares the files on disk against the match set
declared in `internal/aggregate/fixture_test.go` and fails when they disagree.
After changing the declared archive, regenerate:

```
LOLSTATS_AGG_WRITE_FIXTURE=1 go test ./internal/aggregate/ -run TestFixtureSources
```

## What the archive is designed to make observable

Fourteen matches: nine in the window on the selected patch, and one per negative
case.

| Match | Partition | Played | In the window | What it is for |
| --- | --- | --- | --- | --- |
| `EUW1_0000000001` | 2026-09-10 | 2026-09-09 | yes | the base case; one ban per team |
| `EUW1_0000000002` | 2026-09-10 | 2026-09-09 | yes | a zero ban id ("no ban") that must never reach `ban_rate` |
| `EUW1_0000000003` | 2026-09-10 | 2026-09-10 | yes | two bans per team |
| `EUW1_0000000004` | 2026-09-10 | 2026-09-10 | yes | champion 11 played once in the top lane: a suppressed cell; champion 24 sits the match out, so it can be banned; one participant bought no items, so an item group is smaller than its cell |
| `EUW1_0000000005` | 2026-09-10 | 2026-09-10 | yes | a rune page in an undocumented order: no rune build may be published for that participant |
| `EUW1_0000000006` | 2026-09-14 | 2026-09-13 | yes | champion 13 played here and in match 7: a cell exactly on `min_cell_n`, and both are losses |
| `EUW1_0000000007` | 2026-09-14 | 2026-09-13 | yes | the other half of that boundary; a participant with no second summoner spell |
| `EUW1_0000000008` | 2026-09-14 | 2026-09-13 | yes | champion 12 played once, with the position present only in `individualPosition`: the second suppressed cell, and the role fallback |
| `EUW1_0000000009` | 2026-09-14 | 2026-09-13 | yes | the previous patch: counted in the window, must not reach the published patch; champion 99 plays only here, so a stale patch filter shows up as an extra champion |
| `EUW1_0000000010` | 2026-09-14 | 2026-08-30 | **no** | crawled inside the window, played before it: the window filter is on `gameCreation`, not on the partition |
| `EUW1_0000000011` | 2026-09-14 | 2026-09-12 | **no** | `NA1`: champion 97 plays only on the other platform |
| `EUW1_0000000012` | 2026-09-14 | 2026-09-12 | **no** | queue 440: champion 96 plays only in the other queue |
| `EUW1_0000000013` | 2026-09-14 | 2026-09-14 23:59 | yes | the last minute of the window end date |
| `EUW1_0000000014` | 2026-09-15 | 2026-09-15 00:01 | **no** | the first minute after it; champion 98 plays only here |

The window the tests build is 2026-09-01 to 2026-09-14 (14 days ending on the
fixture's window end), `min_cell_n` is 2, and a build that is fed this archive
has to produce exactly:

| Quantity | Value |
| --- | --- |
| archive rows | 14 |
| malformed rows | 0 (the `corrupt` set has 1) |
| matches used | 9 |
| participant rows | 90 |
| rejected rows | 0 |
| classified rows | 90 |
| cells computed | 13 |
| cells published | 11 |
| cells suppressed | 2 |
| `sum(n)` over all cells | 90 |
| window baseline win rate | 0.50 |

Those numbers are hand-computed from the table above and asserted in
`TestBuildAgainstHandComputedFixture`. `TestFixtureSuppressionBoundary` pins the
two sides of the floor: a cell with `n = min_cell_n` publishes, `n = min_cell_n
- 1` suppresses.

## Adding to the fixture

Keep it small, keep it hand-authored, and keep the expectations hand-computed. A
match added to make a test pass by construction is worse than no match at all.
Every row above earns its place by making one boundary observable; if a new row
does not say which boundary, it does not belong here.
