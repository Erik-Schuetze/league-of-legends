# ADR-005: Simulated artifact sets are labelled inside the artifact

- Status: accepted
- Date: 2026-09-17
- Decision: aggregation provenance

## Context

The published `agg/v1` tree has two legitimate producers and one illegitimate
one:

1. **the real build**, which reads the immutable raw archive of crawled Riot
   MATCH-V5 payloads and is the only thing that may publish a real tier list;
2. **a simulated set**, needed because no Riot API key exists in development or
   CI, and the frontend needs a shape-correct artifact set - every frozen path,
   every envelope, every optional field - to render against at all;
3. **a real-looking set that is not real**, which must be impossible.

The danger is not that someone deliberately ships demo numbers. It is that a
demo tree, a screenshot of a demo tree, or a demo partition copied into the
aggregate root is *indistinguishable* from real data after one copy: same
filenames, same schema, same structure. A label in a directory name or in the
command that produced it does not survive that copy, and a filename convention
is not a contract. Nothing in the artifact tree currently says which producer
wrote it, so `verify` cannot tell either.

## Decision

**Every artifact set declares its provenance in a `source` field that is part of
the artifact, and the only two legal values are `"riot-match-v5"` and
`"demo"`.**

- `source` is a **required** field on `manifest.json` and on every envelope
  (`tierlist.json`, `champions/<id>.json`, `matchups/<role>.json`), and it is in
  the JSON Schema `cmd/gen-types` emits, so a frontend build breaks loudly if
  the field is ever removed.
- A real build writes `"riot-match-v5"`. The string is a constant in
  `internal/aggregate/build.go`, and `demo` is the only code path that ever
  writes `"demo"`.
- `demo` additionally writes `README-DEMO.txt` at the root of the tree it
  produces, stating in plain language that the numbers are simulated, how they
  were generated (fixed seed, 2000 synthetic matches) and that they must not be
  published or cited.
- `generated_at` on a demo tree is the close of the *simulated* window
  (`2026-09-17T00:00:00Z`) unless `--generated-at` says otherwise, not the wall
  clock. Two demo runs are therefore byte-identical, which is what makes the
  demo usable as a snapshot test.
- The two producers refuse each other's territory:
  - `demo` refuses to write into a tree that already holds a manifest from
    another source (`refuseRealTree`), so it cannot quietly overwrite published
    Riot data;
  - `build` refuses to publish over a tree whose manifest says `"demo"`
    (`refuseDemoTree`), so a real build cannot inherit a simulated partition.
- `verify --source demo|riot-match-v5` checks the declared source of every
  document in a tree, and the nightly runbook passes the real one. A demo set
  that has been copied into the published root fails that check.

This is an **additive contract change**: a new required field with a closed
value set, one new file in the demo tree only, and no change to any path, any
existing field, or any published document's shape. `internal/aggmodel` is the
single source of truth, so the schema, the TypeScript declaration and the
validator all move together: `internal/aggmodel.Source`, `SourceRiotMatchV5`
and `SourceDemo` are the definitions, and `cmd/gen-types` regenerates
`web/src/types/agg.schema.json` and `agg.d.ts` from them.

## What a demo artifact is, and where it may live

A **demo artifact** is a complete, shape-correct artifact set written by
`lolstats-aggregate demo` into a directory the operator names, generated from a
fixed seed and synthetic matches. It is a *test fixture*, not a data source: it
exists only because no Riot API key is available in development or CI, and the
frontend cannot be built or reviewed against nothing. Everything after the
synthetic generation stage is the production path - the same cell policy, the
same gates, the same document assembly, the same atomic publish - which is
exactly why a demo set is worth rendering against.

**A demo set is never published.** `demo` has no default output root: `--out` is
required, so no invocation can write into the live aggregate root by accident,
and it refuses outright to write into a tree whose manifest declares another
source (`refuseRealTree`). The reverse holds too: `build` refuses a tree whose
manifest says `demo` (`refuseDemoTree`). No CronJob, Kustomization or deployment
manifest invokes `demo`; the only caller is a human, a test, or CI. A demo set
that has been copied into the published root is therefore a publish bug on
someone's part, and `verify --source riot-match-v5` fails on it.

**How a reader tells which state they are looking at.** Two things, both on the
document in hand:

1. `source` on `manifest.json` and on every envelope - `"demo"` or
   `"riot-match-v5"`. This is the machine-readable answer and the one `verify`
   enforces.
2. The banner every page renders. `web/src/layouts/StateBanner.astro` is driven
   from the manifest alone, on every route, and says which of the three states
   the reader is in: `"Published snapshot"` for a real manifest, `"Preview
   build"` for a demo set, `"Preview build - snapshot source not declared"` when
   the source is missing or unrecognised, and an explicit empty state when
   nothing has been published. So a reader never has to know which directory the
   page was built from, and a page cannot be mistaken for real statistics by
   being visited directly.

## Alternatives considered

- **A separate root, e.g. `agg/demo/v1/`.** Rejected: the demo would then not
  exercise the frozen paths, which is the entire reason it exists. A demo set
  that renders from a different layout proves nothing about the real one.
- **A marker file only (`README-DEMO.txt`).** Rejected as the *only* mechanism:
  a copied partition directory does not bring the readme with it, and a checker
  cannot validate a comment. The readme stays as a human-facing extra.
- **A `demo/` prefix in the partition path.** Rejected: it would be a path
  outside the frozen layout, and a directory name is not validated by anything.
- **A boolean `simulated: true`.** Rejected in favour of naming the real source
  as well: `source` answers "where did this come from" for both producers, which
  is the question a reviewer actually asks, and it leaves room for a future real
  source (for example a ranked-ladder export) without a second field.
- **Not shipping a demo mode at all.** Rejected: the frontend cannot be built or
  reviewed without an artifact set, and hand-writing 27 documents by hand in a
  fixture directory - which is what the alternative amounts to - drifts from the
  structs the moment a field is added.
- **A `--allow-demo-in-prod` style override.** Rejected: the two refusals above
  are not a convenience, they are the safety property.

## Consequences

- Any consumer can tell real from simulated by reading one field of the document
  it already has open, and `verify` enforces it in CI.
- The provenance of a published number travels with the number, which is also
  what makes `manifest.source` a useful audit datum: a partition whose source is
  not `riot-match-v5` in the live root is a publish bug, not a data question.
- Every envelope carries one more field. The frontend reads it to decide whether
  to show the "simulated data" banner, so the banner cannot be forgotten by a
  page that forgot to check a global.
- Adding a third real source later means adding a value to a closed set, and the
  schema, the validator and the banner all follow from `internal/aggmodel`.
- The demo remains a *test* fixture, not a product feature: it is deterministic,
  it says so in the artifact, and it is not wired into any CronJob or deployment
  manifest.

## Migration path

None required: the field is additive and both producers write it in the same
release. A consumer that ignores it keeps working unchanged. If the demo mode is
ever removed, the `"demo"` value stays in the vocabulary and in the schema so
that a historical demo snapshot can still be validated and recognised.
