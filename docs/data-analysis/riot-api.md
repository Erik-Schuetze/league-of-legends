# Riot API

What this project reads from the Riot API, and the fields each response returns. The
requests are the ones `internal/riot/client.go` makes; the fields are the ones
`internal/riot/dto.go` and `internal/riot/timeline.go` model.

Last reviewed: 2026-09-18. Next review due: 2026-12-17.

## The endpoints we call

| Family | Endpoint | Parameters | Returns | Reference |
| --- | --- | --- | --- | --- |
| MATCH-V5 | `GET /lol/match/v5/matches/{matchId}` | path only | Match summary object | [MATCH-V5 reference](https://developer.riotgames.com/apis#match-v5) |
| MATCH-V5 | `GET /lol/match/v5/matches/{matchId}/timeline` | path only - a timeline is a whole-match resource with no paging | Timeline object | [MATCH-V5 reference](https://developer.riotgames.com/apis#match-v5) |
| MATCH-V5 | `GET /lol/match/v5/matches/by-puuid/{puuid}/ids` | `start`, `count`, `queue`, `startTime`, `endTime` | Array of match id strings, newest first | [MATCH-V5 reference](https://developer.riotgames.com/apis#match-v5) |
| LEAGUE-V4 | `GET /lol/league/v4/entries/{queue}/{tier}/{division}` | `page` | Array of ladder entries | [LEAGUE-V4 reference](https://developer.riotgames.com/apis#league-v4) |
| LEAGUE-V4 | `GET /lol/league/v4/{tier}leagues/by-queue/{queue}` | path only. `{tier}` is `challenger`, `grandmaster` or `master` | One apex league with its entries | [LEAGUE-V4 reference](https://developer.riotgames.com/apis#league-v4) |
| ACCOUNT-V1 | `GET /riot/account/v1/accounts/by-riot-id/{gameName}/{tagLine}` | path only | Account object | [ACCOUNT-V1 reference](https://developer.riotgames.com/apis#account-v1) |
| ACCOUNT-V1 | `GET /riot/account/v1/accounts/by-puuid/{puuid}` | path only | Account object | [ACCOUNT-V1 reference](https://developer.riotgames.com/apis#account-v1) |

`start`, `count`, `queue`, `startTime` and `endTime` are only sent when set, so the history
call's query string is a subset of that list. `type` (ranked, normal, tourney) is a valid Riot
parameter this project does not send.

Everything else is region-scoped through a platform routing value (`EUW1`) or a regional one
(`EUROPE`); see [routing values](https://developer.riotgames.com/docs/lol#routing-values).
The `/apis` anchors above are client-rendered, so they jump correctly in a browser but are not
in the served HTML; the link text names the family so the link still lands on the right page.

## Match summary fields

`GET /lol/match/v5/matches/{matchId}`. The load-bearing payload: most aggregate statistics
are a transform over this response.

| Field | Type | Notes |
| --- | --- | --- |
| `metadata.matchId` | string | Riot match id, `<platform>_<gameId>` |
| `metadata.participants[]` | string[] | One PUUID per participant, in participant order. **Per-player, not publishable** |
| `info.gameCreation` | integer | Epoch **milliseconds** |
| `info.gameDuration` | integer | **Seconds**. Riot changed this field's unit once already |
| `info.gameVersion` | string | e.g. `16.18.612.9234`. The patch is the first two components |
| `info.queueId` | integer | Riot queue id; `420` is ranked solo |
| `info.participants[]` | object[] | Ten entries; see the next table |
| `info.teams[]` | object[] | `teamId` (`100` or `200`), `bans[]` (`championId`, `pickTurn`) |

### `info.participants[]`

| Field | Type | Notes |
| --- | --- | --- |
| `puuid` | string | **Per-player, not publishable** |
| `championId` | integer | Resolve through Data Dragon |
| `championName` | string | Riot's display name, which is not always the Data Dragon key |
| `teamId` | integer | `100` or `200` |
| `teamPosition` | string | `TOP`, `JUNGLE`, `MIDDLE`, `BOTTOM`, `UTILITY`, or empty in modes without roles |
| `individualPosition` | string | Riot's own guess; can disagree with `teamPosition` |
| `win` | boolean | |
| `item0`..`item6` | integer x7 | Six item slots plus the trinket in `item6`. `0` means the slot is empty |
| `summoner1Id`, `summoner2Id` | integer | Resolve through Data Dragon |
| `perks.styles[]` | object[] | `description`, `style` (rune tree id; `0` on the stat-shard pseudo-tree, which has no tree of its own), `selections[]` (`perk` id). The array order is the rune page's own order, so position is the reliable read |

## Match timeline fields

`GET /lol/match/v5/matches/{matchId}/timeline`. A separate request for the same match id, not
a field of the summary.

| Field | Type | Notes |
| --- | --- | --- |
| `metadata.matchId` | string | |
| `metadata.participants[]` | string[] | PUUIDs, **per-player, not publishable**. Join to the summary by PUUID, not by index |
| `info.frameInterval` | integer | Milliseconds. `60000` normally, `0` for an aborted game. It is the interval Riot intended, not the spacing observed |
| `info.frames[]` | object[] | Kept verbatim rather than typed. Each frame carries a timestamp, `participantFrames`, and `events[]` |

## Ladder fields

`GET /lol/league/v4/entries/{queue}/{tier}/{division}` returns an array of entries:

| Field | Type | Notes |
| --- | --- | --- |
| `puuid` | string | **Per-player, not publishable** |
| `queueType` | string | e.g. `RANKED_SOLO_5x5` |
| `tier` | string | `IRON` through `CHALLENGER` |
| `rank` | string | Division, `I` through `IV` |
| `leaguePoints` | integer | |

`GET /lol/league/v4/{tier}leagues/by-queue/{queue}` returns the whole apex league in one
response: `tier`, `queue`, `name`, and `entries[]` in the shape above, except that each entry
omits `tier` and `rank`, which the caller fills in from the request.

## Account fields

Both account endpoints return the same three fields.

| Field | Type | Notes |
| --- | --- | --- |
| `puuid` | string | **Per-player, not publishable** |
| `gameName` | string | The name part of a Riot ID. **Not publishable** |
| `tagLine` | string | The tag part, e.g. `EUW`. **Not publishable** |

## Fields that are in the payload but not modelled

A timeline's `frames[].events[]` carries roughly twenty event types with per-type fields, and
`participantFrames` is a JSON object keyed by the *string* form of the 1-based `participantId`
rather than an array. Neither is typed in Go: the aggregation reads the stored payload with
DuckDB's `json_extract` instead, so a Riot addition does not need a code change.

The archive stores the whole response body, so **any field Riot returns and this page does not
name is still retrievable** - it is simply not in the typed model.

## Data Dragon

Static data - champion, item, rune and summoner-spell identity plus patch versions. Names and
icons only; no descriptions, lore or artwork text is carried.

| Document | Local file | Fields |
| --- | --- | --- |
| Champions | `projection/champions.json` | `ddragon_version`, `champions[]`: `id`, `key`, `slug`, `name`, `icon`, `roles` |
| Items | `projection/items.json` | id to `name`, `icon` |
| Runes | `projection/runes.json` | id to `name`, `icon` |
| Summoner spells | `projection/spells.json` | id to `name`, `icon` |

The projection is checked in rather than fetched at build time, and the CDN path it comes
from is `ddragon.leagueoflegends.com/cdn/<version>/data/<locale>/<document>.json`. Documented
at [Data Dragon](https://developer.riotgames.com/docs/lol#data-dragon).
