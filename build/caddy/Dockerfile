# syntax=docker/dockerfile:1
#
# The site tier image. Gate G0.5 in the plan is "Caddy built with xcaddy and the
# cache-handler module", and the reason it is a gate is that the upstream Caddy
# images cannot do response caching: cache-handler is a third-party module with
# no storage of its own, so the binary has to be compiled together with a
# storage implementation. `otter` is the in-memory one, which suits a tier that
# serves a pre-rendered directory and is replaced by a rolling restart whenever
# its configuration changes.
#
# Digests, not tags: a tag is a mutable ref, so a rebuild of this file months
# from now must produce the same binary or the diff is invisible.

FROM caddy:2.10-builder-alpine@sha256:4cff3ae272ea05842adb4546e35961e4947aeea65f80fa1eb62a35b960e84971 AS builder

# Pinned to the module version, not to latest, for the same reason as the base
# images. cache-handler and storages are separate modules with separate release
# cycles, so both are pinned independently.
#
# storages is the one pin that is not free to float. Its `otter/caddy` module
# requires a *minimum* Caddy version, and a version whose minimum is above the
# builder's own is not a warning but a resolution failure. v0.0.20 and v0.0.19
# both require v2.11.2, which the Caddy 2.10 builder cannot satisfy; v0.0.18
# requires v2.8.4, so MVS resolves to the builder's v2.10.2 and the build is
# consistent. Bumping either the base image to Caddy 2.11 or this pin to
# v0.0.19+ is therefore a single change, not two: they move together.
ARG CACHE_HANDLER_VERSION=v0.16.0
ARG STORAGES_VERSION=v0.0.18

# The builder image ships the Go release that was current when Caddy 2.10 was
# cut, and a pinned module may require a newer one: storages v0.0.20 declares
# `go >= 1.26.1` against the builder's 1.26.0, and with the default
# GOTOOLCHAIN=local that is a hard failure rather than a warning. `auto` lets
# the toolchain directive do what it is for - download the exact version the
# module asks for - so the module pins below stay honest and the fix is not to
# loosen one of them. The download happens at build time, in CI, where network
# access is expected.
ENV GOTOOLCHAIN=auto

# --output is explicit because the default is the working directory, which is
# not part of the image's contract with this file.
RUN xcaddy build \
      --output /usr/bin/caddy \
      --with github.com/caddyserver/cache-handler@${CACHE_HANDLER_VERSION} \
      --with github.com/darkweak/storages/otter/caddy@${STORAGES_VERSION}

FROM caddy:2.10-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d

COPY --from=builder /usr/bin/caddy /usr/bin/caddy

# The upstream image runs as root and owns /data and /config. The namespace
# enforces the restricted Pod Security profile, which forbids that, so the
# runtime gets a fixed unprivileged uid instead. The uid is named rather than
# anonymous so an operator reading `ps` on a node can tell whose process it is.
RUN addgroup -S -g 1000 caddy \
 && adduser -S -D -H -u 1000 -G caddy caddy \
 && mkdir -p /data /config /srv \
 && chown -R caddy:caddy /data /config /srv

# Caddy's documented behaviour is to serve on :80, which a non-root user cannot
# bind on Linux without CAP_NET_BIND_SERVICE. The Kubernetes Service maps port 80
# onto 8080 instead, so the image keeps the unprivileged uid and drops the
# capability rather than asking the cluster for it.
USER 1000:1000

# The Caddyfile is not baked in: it is a ConfigMap mounted at /etc/caddy/Caddyfile,
# so changing a header or a cache TTL is a configuration change and a rollout,
# not an image build.

# Same provenance labels as the Go image (docs/contracts.md section 5), supplied
# by the workflow from the commit rather than the build clock, so two builds of
# one commit produce identical labels. The defaults keep a local `docker build`
# honest instead of claiming a version it does not have.
ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=1970-01-01T00:00:00Z

LABEL org.opencontainers.image.title="league-of-legends site tier" \
      org.opencontainers.image.description="Caddy with the cache-handler module, serving the rendered League of Legends statistics site" \
      org.opencontainers.image.source="https://github.com/Erik-Schuetze/league-of-legends" \
      org.opencontainers.image.url="https://github.com/Erik-Schuetze/league-of-legends" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.created="${CREATED}" \
      org.opencontainers.image.base.name="caddy:2.10-alpine"
