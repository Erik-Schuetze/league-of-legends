# syntax=docker/dockerfile:1
#
# The site build image: Node plus this repository's Astro source and its
# installed dependencies. It exists as an image at all because the nightly build
# has to run reproducibly - `npm ci` inside the job would make every run depend
# on the npm registry being up and on whatever a floating dependency resolved to
# that night.
#
# The build context is web/, not the repository root: the root .dockerignore
# excludes web/ (it is dead weight for the Go image), and this image needs
# exactly that and nothing else.

FROM node:26-alpine@sha256:ef24c5053d50fdc3e4e56eb4e7ddb7861874ab0fdc797046ba897581deb8e868

# Dependencies first, in their own layer, so that a source-only change does not
# re-download the tree. The lockfile is copied explicitly: a package.json change
# without one is a mistake that should fail here rather than resolve silently.
WORKDIR /site
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY . .

# The build output directory is created and owned by the image's node user so a
# mount point at the same path cannot change the ownership out from under it.
RUN mkdir -p /site/dist && chown -R node:node /site
USER node

# Same provenance labels as the Go image (docs/contracts.md section 5). They
# matter more here than elsewhere: this image carries a snapshot of the frontend
# source, so "which commit was the nightly site built from" is a question the
# image itself should be able to answer.
ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=1970-01-01T00:00:00Z

LABEL org.opencontainers.image.title="league-of-legends site build" \
      org.opencontainers.image.description="Node and the Astro source used by the nightly League of Legends statistics site build" \
      org.opencontainers.image.source="https://github.com/Erik-Schuetze/league-of-legends" \
      org.opencontainers.image.url="https://github.com/Erik-Schuetze/league-of-legends" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.created="${CREATED}" \
      org.opencontainers.image.base.name="node:22-alpine"
