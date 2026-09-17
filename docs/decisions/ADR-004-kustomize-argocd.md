# ADR-004: Kustomize for our manifests, ArgoCD for reconciliation

- Status: accepted
- Date: 2026-09-17
- Decision: D8

## Context

The project runs on an existing k3s homelab managed as GitOps, with the cluster's
own resources living in a separate `homecluster` repository reconciled by ArgoCD.
This repository has to fit that pattern rather than invent a parallel one, and it
has to do so for a handful of resources: one Deployment, a few CronJobs, a
Service, an Ingress, two PVCs and one Secret.

## Decision

Kustomize for this project's own manifests, with a `base/` and an
`overlays/homelab/`. ArgoCD owns reconciliation, through an `Application` that
lives in the `homecluster` repository and points at this one.

Third-party charts, if one is ever introduced, use Helm. Our own resources do not.

**`deploy/` is not in this scaffold.** The layout is described here so the
contract is visible, but the manifests themselves belong to the deployment work,
and nothing in this repository writes to the cluster.

## Alternatives considered

**Helm charts for our own resources.** Rejected: templating is a solution to
distributing a chart to strangers who need to vary it. Here there is one consumer
and one environment, so parameters would be indirection with no caller.
Kustomize's overlay model matches "one base, a small homelab patch" exactly.

**Raw `kubectl apply` from CI.** Rejected: no reconciliation, so cluster drift is
invisible and a manual edit survives until someone notices. It also puts cluster
credentials in this repository's CI rather than in the cluster's GitOps tooling.

**A second ArgoCD instance or a separate GitOps root for this project.** Rejected:
one reconciler with two sources is the pattern already in use, and a second one
multiplies the operational surface for no isolation this project needs.

**Terraform or Pulumi.** Rejected for a stateful-set-of-two and six Kubernetes
objects; the state file would be a new thing to store and back up for no gain.

## Consequences

- **The image is the interface between this repository and the cluster.** This
  repository publishes an image and never applies anything. Tags, labels and
  platforms are therefore frozen in `docs/contracts.md` section 5, because the
  manifests in `homecluster` pin to a tag that has to exist.
- Cluster-specific values - hostnames, storage classes, ingress, replica counts -
  live in the overlay and never in this repository.
- The Riot API key is a Kubernetes `Secret`, referenced by environment variable
  name only. `deploy/*/secret.yaml` is gitignored and secrets are never baked into
  an image.
- `docs/runbooks/` holds the operational procedures the cluster work will need:
  ingest-down, rebuild-aggregates, key-rotation and restore-raw.
- Public exposure is a deliberate, reviewed step rather than a side effect of
  adding an Ingress, because the cluster hardening runbook is not finished.

## Reversal trigger

A second environment that genuinely needs different base values would justify a
second overlay, not a different tool. Reconsider the tool only if this project
outgrows "one base, one overlay" or if the cluster moves off ArgoCD.

**Verify in Phase 0:** the current Helm major version and its support window, if
a third-party chart is actually introduced.
