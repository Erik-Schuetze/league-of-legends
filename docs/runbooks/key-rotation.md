# Runbook: rotate the Riot API key

Covers the one credential the ingest worker holds. A Riot **development** key
expires 24 hours after it is issued, so for a development key this is a daily
operation, not an incident procedure.

## Where the key lives

Two inputs, both read by `riot.NewKeyProvider()`:

| variable | source in this repo | refresh |
| --- | --- | --- |
| `LOLSTATS_RIOT_API_KEY` | env, from Secret `lolstats-riot` key `RIOT_API_KEY` | on process start only |
| `LOLSTATS_RIOT_API_KEY_FILE` | **not set anywhere** | re-read every 30s by a watcher |

The file wins when both are set, and the watcher means a file-based rotation
needs no restart. Three facts about the wiring:

- The Secret is referenced `optional: true` (`deploy/base/ingest/deployment.yaml`),
  so a cluster with no key still starts every other workload in the namespace.
  The worker itself *does* require it: `cmd/lolstats-ingest` refuses to start
  when `cfg.Riot` is empty, which is a `CrashLoopBackOff` with a clear log line
  rather than a degraded crawler.
- **Secret `lolstats-riot` is not in this repository.** `deploy/base/secret.yaml`
  contains only `lolstats-postgres` (and its committed development password).
  Nothing ArgoCD tracks holds the key, so an out-of-band `kubectl create secret`
  is *not* reverted - unlike a hand edit to something this repo does own, which
  `selfHeal: true` on the `lolstats` Application reverts within minutes.
- **The file path is not wired.** Nothing mounts a key file and nothing sets
  `LOLSTATS_RIOT_API_KEY_FILE`. The mechanism exists in the code
  (`internal/riot/key.go`) but is unreachable in this deployment, which is why
  the procedure below restarts a pod. Wiring it up is additive and specified at
  the end of this runbook.

## When to use it

- `LolstatsRiotKeyRevoked` fired - a sustained 403 with no successful request
  beside it. See `docs/runbooks/ingest-down.md` if the frontier is also stalled.
- `LolstatsRiotAuthFailures` fired, or 403s are climbing in the logs.
- Daily, for a development key. Nothing in the cluster can warn you first: see
  "the one thing that cannot tell you" below.
- Immediately, if the key leaked. Rotate first, investigate second.

## Establish that the key is the problem

```
# 1. Is the worker running at all? A missing key is a start-up failure, and the
#    log says so in the first few lines.
kubectl -n lolstats get pods -l app.kubernetes.io/component=ingest
kubectl -n lolstats logs deploy/lolstats-ingest --tail=80

# 2. What Riot is answering. There is no shell in the worker image - it is
#    distroless, so `kubectl exec ... -- sh` fails with an exec-format error -
#    and the metrics listener is the only port it opens. Port-forward instead.
kubectl -n lolstats port-forward deploy/lolstats-ingest 9090:9090 &
curl -s http://localhost:9090/metrics \
  | grep -E 'lolstats_riot_(requests_total|key_age_seconds)'
kill %1
```

`lolstats_riot_requests_total` carries a `status` label whose value is the HTTP
response code, or `0` for a transport failure. Read the rates, not the counters:

- `403` climbing with `2xx` at zero -> the key is expired or revoked. Rotate.
- `403` a small fraction with `2xx` normal -> one endpoint or an over-long
  response, not the key. Leave the credential alone.
- `0` with everything else quiet -> DNS or egress, not the key. The namespace is
  under `default-deny`, so this is a NetworkPolicy symptom
  (`deploy/base/network/allow.yaml` opens Riot over 443 for `ingest`,
  `discover-seeds`, `static-sync` and `backfill`; nothing else).

## The one thing that cannot tell you the key is expiring

`lolstats_riot_key_age_seconds` is **not the age of the key**. It is the time
since *this process* first saw the key that is currently configured, it is `0`
when no key is configured, and it resets to `0` on every restart. `KeyWarnAge` is
12 hours and `LolstatsRiotKeyOld` fires on it, but:

- a restart clears it, so it can be silenced by the very action that does not fix
  the key;
- a key that was already 20 hours old when the pod started will not trip it at
  all, because the pod will be restarted by the next deploy before the gauge
  reaches 12 hours.

A real expiry alert needs a metric derived from the key's own `expires_at`.
`LOLSTATS_RIOT_API_KEY_EXPIRES_AT` is parsed by `internal/config`
(`config.Config.Riot.KeyExpiresAt`) and reaches no metric - but it is not
"consumed by nothing": `cmd/lolstats-ingest` reads it in three places (it refuses
to start once the declared deadline has passed, the worker carries it as
`KeyExpiry`, and `/readyz` reports it), and it is wired from the Secret's
optional `RIOT_API_KEY_EXPIRES_AT` key in `deploy/base/ingest/deployment.yaml`,
`deploy/base/jobs/discover-seeds.yaml` and `deploy/base/jobs/backfill.yaml`. What
is missing is a *metric*; the deadline itself still has to be written down by
hand, and Riot does not tell this stack when a development key dies.

It has never been written down here: Secret `lolstats-riot` carries
`RIOT_API_KEY` and no `RIOT_API_KEY_EXPIRES_AT` (verified 2026-09-17), so no
process can fail early on a passed deadline and a dead key is discovered exactly
as the code comment says it is - a 401 on the first call. Until the ingestion
workstream emits an expiry metric, the symptom you can actually observe is a
rising 403 rate - which is what `LolstatsRiotKeyRevoked` watches - and the
practical control is a calendar alarm.

## Rotate, the path that works today

```
# 1. Issue a new key in the Riot developer portal, with the old one still valid.
#    This repository is public: never put the value in a manifest, a commit, a
#    ticket or a shell where history is kept.

# 2. Replace the Secret in place. `--dry-run=client -o yaml | apply` updates an
#    existing Secret and creates a missing one without a read-modify-write race,
#    and the value never lands in a file.
kubectl -n lolstats create secret generic lolstats-riot \
  --from-literal=RIOT_API_KEY='RGAPI-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx' \
  --dry-run=client -o yaml | kubectl apply -f -

# 3. Roll the worker. The key reached it as an environment variable and an
#    environment variable cannot change under a running process.
kubectl -n lolstats rollout restart deploy/lolstats-ingest
kubectl -n lolstats rollout status deploy/lolstats-ingest --timeout=180s
```

Step 3 is a rolling update, so the metrics listener and the queue stay served and
the frontier does not move backwards; the crawl pauses for the few seconds the
new pod needs to become Ready. With a 24-hour key that is a daily interruption -
which is the argument for the file path below.

## How to tell it worked

```
# 403s decay to zero within a few minutes, 2xx take over, matches start landing.
kubectl -n lolstats port-forward deploy/lolstats-ingest 9090:9090 &
curl -s http://localhost:9090/metrics | grep -E \
  'lolstats_riot_requests_total|lolstats_matches_persisted_total|lolstats_pipeline_staleness_seconds'
kill %1

# The alert itself, evaluated by the Prometheus that owns the rule. Alerts are
# delivered nowhere (there is no Alertmanager in this cluster), so this page is
# the only place they can be seen.
kubectl -n monitoring port-forward svc/prometheus 9090:9090 &
open http://localhost:9090/alerts   # or curl -s localhost:9090/api/v1/alerts
kill %1
```

It worked when `rate(lolstats_riot_requests_total{status="403"}[5m])` is zero,
`lolstats_matches_persisted_total` is increasing again, and
`lolstats_pipeline_staleness_seconds{stage="crawl"}` is falling back towards the
15-second poll interval. `LolstatsRiotKeyRevoked` clears on its own; it has no
acknowledgement state, because there is nowhere to acknowledge.

## Roll back

- **Typo in the new value.** Re-apply the previous value with the same command -
  `kubectl rollout undo deploy/lolstats-ingest` is *not* a remedy here: it
  reverts the container template, and the key is not in the container template.
- **The old key still exists and the new one is worse.** Re-apply the old one.
  Its remaining lifetime is whatever it is; for a development key that may be
  minutes.
- **The old key was revoked or has expired.** There is no rollback. Get a new key
  and repeat step 2. Nothing else in the namespace needs touching, which is the
  point of the Secret being untracked.

## Wiring the no-restart path (owned by the deployment workstream)

Both changes are additive and neither is in this repository today:

1. In the `lolstats-ingest` Deployment, add a *projected Secret volume* for
   `lolstats-riot`, `optional: true`, mounted read-only, and set
   `LOLSTATS_RIOT_API_KEY_FILE` to the file inside it. Do **not** use `subPath`
   for this: a Secret volume mounted with `subPath` is never updated in place,
   whereas a plain Secret volume is refreshed by the kubelet within about a
   minute - which is exactly the property the 30-second watcher exists to use.
2. Nothing else. Rotation then becomes `kubectl create secret ... | kubectl
   apply -f -` and a 30-second wait, with no restart and no crawl pause.

Do **not** put the key on the `lolstats-data` volume instead. It is a
ReadWriteMany NFS export mounted by the web tier (a file server), by both
aggregate jobs and by both backup jobs, so the key would be readable by every pod
in the namespace. The restic job snapshots only `LOLSTATS_RAW_ROOT`
(`raw/` under that volume), so a key at the volume root would not be in the
snapshot - but that is a property of today's snapshot path, not a reason to rely
on it.

## What is destructive here

- `kubectl rollout restart deploy/lolstats-ingest` interrupts a running crawl for
  the length of a rolling update. It cannot lose queue state: the queue is a
  Postgres table, and a claimed-but-unfinished item is reclaimed after its lease
  expires. It **does** delay the frontier.
- `kubectl delete secret lolstats-riot` with no key anywhere else stops the
  crawler at its next restart. There is no way to read the old value back out of
  the cluster once it is gone.
- `kubectl apply -f-` against an existing Secret merges by key. It does not
  delete other keys - which matters if you later add `RESTIC_PASSWORD`-style
  extra keys to a shared Secret. Keep `lolstats-riot` holding the key and nothing
  else.
