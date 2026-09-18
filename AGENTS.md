# AGENTS.md

Guidance for AI assistants and automated contributors working in this
repository. It is about how changes are written down, not what they do - the
README and the code cover the rest.

## The README stays a README

`README.md` is the front door. Someone reads it once, top to bottom, to decide
whether to use this project and then to get it working. Add a feature and
document the feature - do not let it accumulate warnings, caveats, and asides.

- Say what a thing does and how to use it. Put *why* it works that way - the bug
  it prevents, the trade-off, the alternative that was rejected - in the code
  comment, the commit message, or the `CHANGELOG.md` entry.
- A short caveat earns its place when it changes what the reader should do. If it
  only matters to someone modifying the code, it belongs next to the code.
- When a change makes an existing paragraph wrong, rewrite or delete that
  paragraph instead of appending a correction.
- Material that is genuinely long - reasoning, threat models, audits - goes in
  its own document and gets one line in the README linking to it.

The test: the README answers "how do I use this?". Anything that answers "why is
it like this?" belongs somewhere else.

## Where everything else goes

| Content | Home |
|---|---|
| What a subcommand, flag or field does, how to turn it on | `README.md` |
| Why it works that way; the bug it prevents | the code, as a comment |
| What changed, and whether it is breaking | `CHANGELOG.md` |
| A decision with a rejected alternative behind it | `docs/decisions/ADR-nnn-*.md` |
| What every datum comes from, and its legal basis | `docs/data-sources.md` |
| Riot policy conformance and its evidence | `docs/compliance.md` |
| Scratch: tool downloads, captured pages, gate output | `.agent-artifacts/` (gitignored) |

## Frozen contracts

`docs/contracts.md` is normative. It fixes the aggregate artifact shapes, the
route table, the Go interfaces in `internal/contract` and the image contract,
because the ingest, aggregate and serving tiers are written against them and
cannot all be held in one head at once.

**Changing anything in a frozen section requires an ADR first.** Not a
"compatible" tweak, and not a rename that only looks local. If you believe a
contract is wrong, write the ADR, state the rejected alternative, and change the
code after. Where `docs/contracts.md` and the Go code disagree, the code is the
source of truth and the document is the bug.

## House style

- Plain ASCII hyphens, not em dashes. Sentence case headings. Match the prose
  around you rather than importing a different voice.
- Comments explain reasoning that is not obvious from the code, not what the next
  line does.
- Keep `CHANGELOG.md` entries factual and short. "Breaking" means something that
  used to work no longer does.
- Tool versions are pinned in the `Makefile` so a local run and a CI run are the
  same run. Do not invoke an unpinned tool from a target.
- Docker images are pinned by digest with a readable tag in front. `CGO_ENABLED=0`,
  static Go binary, distroless nonroot runtime - the `cc` variant of distroless,
  because the pinned DuckDB CLI is a glibc binary.

## Before you say it works

Run `make vet`, `make test`, `make lint` (and `make vuln` for dependency changes,
`make compliance` for changes to the served pages or the approved wording) - or say plainly that you could
not. `make test` skips the DuckDB-dependent analytics tests when the pinned
client is absent, so `make duckdb && make test-build` is what proves the
aggregation path actually ran. Never describe a change as tested, working or
verified on the strength of having read it carefully.

If a tool cannot be made to run locally, say so explicitly rather than reporting a
pass you did not observe. A false pass costs more than a known gap.

## Scope

`deploy/**` holds the Kustomize output this repository owns; the ArgoCD
`Application` lives in `homecluster`. The Riot DTO carries no field beyond what
Match-V5 summaries need (see `docs/contracts.md` section 2).

Never commit secrets. The Riot key is an environment variable; `deploy/*/secret.yaml`
and `.env` are gitignored.
