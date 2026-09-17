# Runbook: enable alert delivery

Covers the last step of the alerting plan. The rules are loaded and evaluate
already; this is what makes a firing alert leave the cluster.

Two repositories are involved: the Alertmanager bundle lives in `homecluster`
(`monitoring/alertmanager/`), and the one-line change that activates it is to the
`Prometheus` CR that `homecluster` already owns.

## The gap

`monitoring/prometheus/lolstats-rules.yaml` in the `homecluster` repo defines 12
alerts - `LolstatsCrawlStale`, `LolstatsCrawlStalled`, `LolstatsFrontierNotDraining`,
`LolstatsIngestMetricsAbsent`, `LolstatsBuildNotScheduled`, `LolstatsBuildJobFailed`,
`LolstatsBuildStuck`, `LolstatsBuildFailures`, `LolstatsRiotRateLimited`,
`LolstatsRiotAuthFailures`, `LolstatsRiotKeyRevoked`, `LolstatsRiotKeyOld`.
`prometheus-persistant` sets `ruleSelector: {}`, so the operator loads all of them
and they evaluate on every interval. A firing alert is visible on Prometheus'
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
`activeAlertmanagers` list.

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
