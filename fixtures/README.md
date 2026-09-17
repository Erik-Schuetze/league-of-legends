# Fixtures

Small, hand-authored payloads for tests and for reasoning about shapes.

## Provenance and legality

**Nothing in this directory is real Riot data.** Every file here was written by
hand to match the documented MATCH-V5 shape, using the field set frozen in
`internal/riot/dto.go`. There is no captured payload, no scraped response and no
player data of any kind.

That is deliberate, for three reasons:

1. **Match payloads contain player PUUIDs**, which are personal identifiers
   attached to real accounts. A committed capture is a privacy problem that outlives
   whoever committed it, and it is not needed to test a shape.
2. **Riot's terms cover derived data as well as API access.** Committing published
   captures into a public repository is the kind of thing that is easier to avoid
   than to argue about afterwards.
3. **A hand-authored fixture is a specification, not a recording.** It states what
   the code is allowed to read. A capture that happens to contain a field the code
   does not use just invites someone to start using it without an ADR.

The identifiers make the provenance obvious on sight: match ids are
`EUW1_0000000000` and PUUIDs are `fixture-puuid-01` through `fixture-puuid-10`.
Champion ids, item ids, rune ids and summoner spell ids are the real Riot ids,
because those are public static data and a fixture that invents them would test
nothing.

When a real payload is needed to check field coverage against the live API, that
is a Phase 0 gate (G0.1 in `docs/data-sources.md`) and the payload is inspected
locally, never committed.

## What is here

| File | Shape | Notes |
| --- | --- | --- |
| `match-v5/synthetic-ranked-solo.json` | `riot.MatchDTO` | Ranked solo queue, two teams of five, all five canonical roles present, item slots partly empty, and three zero-champion ban entries |
| `riot/league-entries-gold-i.json` | `[]riot.LeagueEntryDTO` | One LEAGUE-V4 division page: three GOLD I entries with `fixture-puuid-01`..`03`, used by the ladder seeding tests |
| `riot/league-challenger.json` | `riot.ApexLeagueDTO` | One apex league response. The entries omit `tier` and `rank` on purpose, because Riot does: the client fills them in from the request |
| `riot/match-ids-ranked-solo.json` | `[]string` | Three MATCH-V5 ids in the `EUW1_0000000000` form, used by the history-walk tests |
| `ddragon/versions.json` | `[]string` | A patch list in deliberately non-sorted order, so a version picker that trusts the list order rather than comparing patch numbers fails |
| `ddragon/champion.json` | Data Dragon champion document | Two champions, in the `{"type","format","version","data"}` envelope |
| `ddragon/item.json` | Data Dragon item document | Two items, one of them a starting item and one a completed one |
| `ddragon/runesReforged.json` | Data Dragon rune tree | One tree with one slot, because the shape is what the join needs, not the content |
| `ddragon/summoner.json` | Data Dragon summoner spell document | Two spells. The document is still named `summoner.json` upstream, which is why the path is spelled out in `internal/crawl` |
| `agg/` | raw archive in the frozen `riot/match-v5/dt=<date>/` layout, as JSONL | The aggregation fixture: fourteen hand-authored matches plus a `corrupt/` variant for the fail-closed test, and the hand-computed cell table they have to produce. See `agg/README.md` |

The Data Dragon files carry `version: 16.20.1`, which is the newest entry of
`versions.json`. `latestVersion` picks the newest by comparing patch numbers, so
that pairing is what the static-sync test asserts on.

The ban entries with `championId: 0` and the empty item slots are in there on
purpose. Both are real shapes that a naive aggregation counts as data, so a
fixture that omits them would let that bug through. `internal/riot/dto_test.go`
asserts that this file decodes into the frozen DTO with no field left unread.

## Adding a fixture

Keep it small, keep it hand-authored, and give it identifiers that cannot be
mistaken for real ones. If a fixture exists to pin down a field, say so in the
table above - a fixture whose purpose is not written down gets deleted by the
next person who cannot tell whether it matters.
