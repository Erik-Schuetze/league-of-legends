# Runbook: enable alert delivery

Covers the last step of the alerting plan. The rules are loaded and evaluate
already; this is what makes a firing alert leave the cluster.

Two repositories are involved: the Alertmanager bundle lives in `homecluster`
(`monitoring/alertmanager/`), and the one-line change that activates it is to the
`Prometheus` CR that `homecluster` already owns.

## The gap

`monitoring/prometheus/lolstats-rules.yaml` in the `homecluster` repo defines **9
alerts in 3 groups**. Count them from the cluster rather than from this page: the list
has changed once already and will change again, and a runbook that lists rules nobody
loads is the same defect as a rule that can never fire.

```console
kubectl -n monitoring get prometheusrule lolstats-prometheus-rule -o json \
  | jq -r '[.spec.groups[] | {group: .name, alerts: [.rules[].alert], count: (.rules|length)}]
           | .[] | "\(.group)\t\(.count)\t\(.alerts|join(", "))"'
# lolstats-crawl    4  LolstatsCrawlStale, LolstatsCrawlStalled, LolstatsFrontierNotDraining, LolstatsIngestMetricsAbsent
# lolstats-build    2  LolstatsBuildNotScheduled, LolstatsBuildJobFailed
# lolstats-riot-api 3  LolstatsRiotRateLimited, LolstatsRiotAuthFailures, LolstatsRiotKeyRevoked

kubectl -n monitoring get prometheusrule lolstats-prometheus-rule -o json \
  | jq '[.spec.groups[].rules[]] | length'
# 9
```

`prometheus-persistant` sets `ruleSelector: {}`, so the operator loads all of them
and they evaluate on every interval (30s). A firing alert is visible on Prometheus'
`/alerts` page and in Grafana, and that is where it stops:

```console
kubectl -n monitoring port-forward svc/prometheus 9090:9090 &
curl -s localhost:9090/api/v1/alertmanagers
{"status":"success","data":{"activeAlertmanagers":[],"droppedAlertmanagers":[]}}

kubectl get alertmanager -A
No resources found
```

The cluster has never had an Alertmanager. Prometheus has nowhere to send
notifications, so nothing is routed: no webhook, no email, no page. Stale crawl,
stale build, elevated 403/429 and key age all fire into an empty
`activeAlertmanagers` list - delivery terminates nowhere, and the gap is in the
*route*, not in the rules. The cost of closing it is not the opt-in bundle below; it
is an `alerting.alertmanagers` block in the **shared**
`monitoring/prometheus/prometheus.yaml`, which rolls the single-replica Prometheus
that serves every site on this cluster (Step 2, and "Why this is not enabled by
default").

### Three rules were removed on 2026-09-17 - do not re-add them

They were removed because they could never fire, and a rule that cannot fire
manufactures coverage that does not exist. Each one is recoverable only with the
wiring named below.

| rule | why it could not fire |
| --- | --- |
| `LolstatsBuildStuck` | **Hold unreachable.** `lolstats-aggregate`'s Jobs carry `activeDeadlineSeconds: 7200`, and the Job API applies that deadline to the Job as a whole, so `kube_job_status_active > 0` cannot persist for any sane `for:`. Measured with 30s and 60s probe Jobs: the deadline kill drops `kube_job_status_active` to `0` within one scrape interval and sets `kube_job_status_failed = 1`, which `LolstatsBuildJobFailed` already alerts on. There is also no measured build duration to derive a hold from - the CronJob has never been scheduled. |
| `LolstatsBuildFailures` | **Inert producer.** `lolstats_build_failures_total` is a lazily-created `CounterVec` child (`internal/obs/obs.go:161`) whose only writer is the aggregate binary (`internal/aggregate/build.go:171`), and that binary runs as a CronJob that nothing scrapes: no `ports:` in its Job template, no Service, no PodMonitor (the Prometheus CR sets `serviceMonitorSelector: {}` but leaves the PodMonitor selector unset). `count(lolstats_build_failures_total)` is `0` series - it has never been scraped. Re-add it only with a Pushgateway or a long-lived aggregate worker whose `serveMetrics()` (`cmd/lolstats-aggregate/main.go:141`) is actually reachable. |
| `LolstatsRiotKeyOld` | **Dead gauge.** `lolstats_riot_key_age_seconds` is a plain Gauge (`internal/obs/obs.go:125`) written only by `SetRiotKeyAge` (`internal/obs/obs.go:206`), whose only call site sits behind a type assertion that cannot succeed: the two-value `Age() (time.Duration, bool)` assertion in `internal/crawl/worker.go` (line 891 at commit `436b0f7`) is satisfied only by the test fake (`internal/crawl/fakes_test.go:940`), never by the ingest's real client, which exposes `Keys() *KeyProvider` (`internal/riot/client.go:222`). The gauge is therefore scraped as a constant `0` while `/readyz` reports the true age (observed `138s` and `10980s` at different times, with `/metrics` reading `0` in both); CI stays green because the fake does implement the assertion. Even if it were wired up, `KeyProvider.Age()` (`internal/riot/key.go:101`) measures process key lifetime, not key age, so it could never answer the question the alert asked. The whole `lolstats-riot-key` group was removed with it. |

### Standing rule: no rule may be re-added unless it can fire

Before adding or restoring a rule in `lolstats-rules.yaml`, prove two things against
the live Prometheus: the metric it reads **exists** (a rule referencing a series no
scraped process produces can never fire), and its `for:` is **shorter than the
failure it detects and longer than healthy operation** (a hold shorter than what it
waits for fires on a healthy system; a longer one is coverage theatre).

- The key-age gauge fix is **in flight in another lane** (`internal/riot`,
  `internal/crawl`). Until it lands and the value is observed to track `/readyz`, no
  rule in this file may read `lolstats_riot_key_age_seconds`.
- `LolstatsRiotRateLimited` was re-thresholded from measurement rather than
  intuition: the old `share > 0.05` alone was unreachable (the highest 15m 429 share
  ever recorded on this key is `0.0160`), so the expression is now `share > 0.03`
  **and** total traffic `>= 0.01 req/s` - the second clause stops a nearly idle
  window from paging on a single 429. It is measured, but not trigger-proven: a real
  429 cannot be induced without asking Riot for more than the dev key allows.
- **Unproven, recorded as unproven:** whether
  `lolstats_riot_requests_total{status="401"}` increments for a real revoked
  *production* key. Proving it would mean swapping the live key for a revoked one on
  the running crawler, which is not a trade worth making. What *is* proven is that
  Riot answers **401**, not 403, for a revoked key, so both Riot auth rules match
  `status=~"401|403"`.

## Step 1 - apply the opt-in bundle

The bundle is deliberately **not** applied by ArgoCD: no `Application` in
`argocd-apps/` targets `monitoring/alertmanager`, so it only reaches the cluster
when someone runs this on purpose. Its configuration is inert - one receiver with
no integrations - so applying it changes nothing about routing yet.

```console
cd ~/workspace/homecluster
kubectl apply -f monitoring/alertmanager/
# secret/alertmanager-lolstats created
# service/alertmanager-lolstats created
# alertmanager.monitoring.coreos.com/lolstats created

kubectl -n monitoring get alertmanager lolstats
# NAME       VERSION   REPLICAS   READY   RECONCILED   AVAILABLE   AGE
# lolstats             1          1       True         True        30s

kubectl -n monitoring get sts alertmanager-lolstats
# NAME                    READY   AGE
# alertmanager-lolstats   1/1     30s
```

`VERSION` is blank because `spec.version` is left unset on purpose: the operator
picks the version it was built for. Confirm what it picked with

```console
kubectl -n monitoring get sts alertmanager-lolstats \
  -o jsonpath='{.spec.template.spec.containers[*].image}{"\n"}'
# quay.io/prometheus/alertmanager:v0.27.0 quay.io/prometheus-operator/prometheus-config-reloader:v0.76.2
```

## Step 2 - point Prometheus at it

The file is `monitoring/prometheus/prometheus.yaml` in the `homecluster` repo - the
`Prometheus` CR `prometheus-persistant`. Add this block after `ruleSelector: {}`
(currently line 28) and before the `# Prometheus only persists TSDB data when
storage is configured here.` comment that follows it:

```yaml
  alerting:
    alertmanagers:
      - namespace: monitoring
        name: alertmanager-lolstats
        port: web
```

`namespace`, `name` and `port` all matter: the operator renders this into a
`role: endpoints` discovery that keeps only endpoints whose Service and port names
match, which is why `10-service.yaml` creates a named Service instead of relying on
the operator's headless `alertmanager-operated`. `port` is the Service's port
*name*, `web`.

Check it before it goes live, then apply it the normal way for this repo - commit
and push, since ArgoCD syncs `HEAD` and has `automated: {}`:

```console
cd ~/workspace/homecluster
kubectl apply --dry-run=server -f monitoring/prometheus/prometheus.yaml
# prometheus.monitoring.coreos.com/prometheus-persistant configured (server dry run)
git add monitoring/prometheus/prometheus.yaml
git commit -m "prometheus: send alerts to the opt-in Alertmanager"
git push
```

The operator regenerates the Prometheus config Secret within seconds and rolls the
StatefulSet. Confirm it moved:

```console
kubectl -n monitoring get prometheus prometheus-persistant
# NAME                    VERSION   DESIRED   READY   RECONCILED   AVAILABLE   AGE
# prometheus-persistant             1         1       True         True        331d

kubectl -n monitoring get sts prometheus-prometheus-persistant \
  -o jsonpath='{.metadata.generation} {.status.observedGeneration}{"\n"}'
# 4 4

kubectl -n monitoring get pod prometheus-prometheus-persistant-0 \
  -o jsonpath='{.metadata.creationTimestamp}{"\n"}'
# 2026-09-17T10:20:04Z   <- a fresh timestamp is the roll. It was
#                        2026-08-13T11:08:06Z before this change, and the
#                        StatefulSet itself is 137d old.
```

`metadata.generation` on the StatefulSet was 3 before this change and
`observedGeneration` trails it until the roll finishes.

## Step 3 - confirm delivery is wired

```console
kubectl -n monitoring port-forward svc/prometheus 9090:9090 &
curl -s localhost:9090/api/v1/alertmanagers
# activeAlertmanagers now holds one entry ending in /api/v2/alerts/ (the URL is the
# endpoint address Prometheus resolved, so it is an IP, not the Service name) and
# droppedAlertmanagers is empty.

kubectl -n monitoring port-forward svc/alertmanager-lolstats 9093:9093 &
curl -s localhost:9093/api/v2/status | python3 -m json.tool
# "cluster": { ..., "status": "ready" }   (it reads "settling" for the first ~10s)
```

From here a firing rule reaches Alertmanager, is grouped by `alertname` and
`namespace`, and is dropped by the `inert` receiver - which is the point of the
default: the path is proven end to end but nobody is notified until a real receiver
is added. To deliver for real, edit the commented example at the bottom of the
`alertmanager.yaml` value in `monitoring/alertmanager/00-config-secret.yaml` (a
webhook and an email receiver are written out there) and re-apply that file:

```console
cd ~/workspace/homecluster
kubectl apply -f monitoring/alertmanager/00-config-secret.yaml
```

The operator regenerates `alertmanager-lolstats-generated` and the `config-reloader`
sidecar reloads Alertmanager; the pod is not restarted and no notification is
replayed.

## What to expect while doing it

- **Prometheus restarts.** Editing the CR changes the config hash annotation, so the
  operator rolls `prometheus-prometheus-persistant-0`. One replica, so there is a
  gap of roughly half a minute with no scraping.
- **A brief gap in the graphs.** Scrapes that fall inside that window are simply
  missed; the TSDB lives on the longhorn PVC and the WAL is replayed on start, so no
  stored sample is lost.
- **Nothing should fire because of it.** Every rule in `lolstats-rules.yaml` carries
  a `for:` of at least `5m` (the file says so at the top), which is far longer than
  the restart. `LolstatsIngestMetricsAbsent` is the one to watch if the pod does not
  come back; a restart that takes minutes is a broken roll, not a metrics gap.
- **Alertmanager is small.** Requests are 25m CPU / 64Mi and limits 200m / 128Mi: a
  rounding error against the 400Mi Prometheus asks for, so it neither competes for
  scheduling nor is the reason the node comes under pressure. The 128Mi limit is a
  backstop, not a target.

## Rollback

Remove the `alerting.alertmanagers` block from
`monitoring/prometheus/prometheus.yaml` first (commit, push), so Prometheus stops
resolving an endpoint that is about to disappear. Then delete the bundle:

```console
cd ~/workspace/homecluster
kubectl delete -f monitoring/alertmanager/
# secret "alertmanager-lolstats" deleted
# service "alertmanager-lolstats" deleted
# alertmanager.monitoring.coreos.com "lolstats" deleted

kubectl get alertmanager -A
No resources found
```

The StatefulSet, the Services and the operator's `alertmanager-lolstats-generated` /
`alertmanager-lolstats-web-config` Secrets go with it through their owner
references. There is no PVC, so nothing is left dangling on longhorn.

**State that is lost:** everything in Alertmanager itself. Storage is
`storage: {}` (an emptyDir), so silences, the notification log and the nflog live
only while the pod does - both a rollback and an ordinary reschedule during a node
drain lose them. That is the deliberate trade for a component that must stay one
command away from removal: no PVC to bind, nothing to clean up. If you are
mid-incident, keep the firing alert in mind instead of a silence created 10 minutes
ago: `curl -s localhost:9090/api/v1/alerts` is the source of truth and it lives in
Prometheus, not here.

## Why this is not enabled by default

Alert delivery is one step away, not on, because turning it on is a change to
**shared, running infrastructure**:

- The `Alertmanager` CR and its Service are ours, but wiring Prometheus to them
  means editing the cluster's only `Prometheus` CR, which every other service on the
  homecluster also reports into. That edit makes the operator regenerate the config
  and **restart a StatefulSet that has been running the whole cluster's metrics for
  137 days without interruption**.
- `monitoring/prometheus/prometheus.yaml` is shared with the rest of the homecluster,
  so a mistake there takes down metrics for every workload, not just `lolstats`. The
  rules this change serves are `lolstats`' own; the blast radius is the whole
  monitoring stack.
- Nothing is broken today. Firing alerts are visible in Prometheus and in Grafana;
  the missing delivery is a deliberate, reversible omission, and it stays reversible
  precisely because the bundle is outside ArgoCD - there is no `Application` that
  would re-apply it.

The owner's standing rule is to disturb nothing already running, so this stays an
opt-in procedure that a human runs once, with the rollback above, rather than an
`Application` that syncs itself.

## Verified before this was written

`amtool check-config` accepts the bundle's `alertmanager.yaml` verbatim (Alertmanager
0.27.0, the image the operator selects), `kubectl apply --dry-run=server -f
monitoring/alertmanager/` is clean against the live cluster, and the whole path -
real Prometheus 2.54.0 (the tag the cluster runs) firing a synthetic rule into real
Alertmanager 0.27.0 with a webhook receiver - was run in local Docker, where the sink
received the firing notification. No part of that touched the cluster.
