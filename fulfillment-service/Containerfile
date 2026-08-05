# First stage is to build the image that contains the image with all the development tools needed to build the binary.
FROM registry.access.redhat.com/ubi10/go-toolset:1.26 AS builder

# Set this to 'true' to build the binary with debugging symbols and disabling optimizations.
ARG DEBUG=false

# Copy only the 'go.mod' and 'go.sum' files and try to download the required modules, so that hopefully this will be
# in a layer that can be cached reused for builds that don't change the dependencies.
WORKDIR /opt/app-root/src
COPY go.mod go.sum /opt/app-root/src/
RUN \
  set -e; \
  go mod download

# Version can be passed as a build arg to avoid requiring git inside the container.
# This is necessary when building from a git worktree, where the .git reference
# cannot be resolved inside the container context.
ARG VERSION=""

# Copy the rest of the source and build the binary:
COPY --chown=1001:1001 . /opt/app-root/src/
RUN \
  set -e; \
  version="${VERSION}"; \
  if [[ -z "${version}" ]]; then \
    version=$(git describe --tags --always 2>/dev/null || echo "dev"); \
  fi; \
  gcflags=""; \
  ldflags="-X github.com/osac-project/fulfillment-service/internal/version.id=${version}"; \
  if [[ "${DEBUG}" == "true" ]]; then \
    gcflags="all=-N -l"; \
  fi; \
  go build -gcflags="${gcflags}" -ldflags="${ldflags}" ./cmd/fulfillment-service

# dlv stage — only pulled into the build graph when targeting runtime-debug.
FROM builder AS dlv-builder
RUN go install github.com/go-delve/delve/cmd/dlv@latest

# Common runtime base with the service binary.
FROM registry.access.redhat.com/ubi10-minimal:10.2 AS runtime-base
COPY --from=builder /opt/app-root/src/fulfillment-service /usr/local/bin
USER 1001

# Debug variant — adds dlv on top of the base; build with: podman build --target runtime-debug .
FROM runtime-base AS runtime-debug
COPY --from=dlv-builder /opt/app-root/src/go/bin/dlv /usr/local/bin

# Default (non-debug) runtime — must stay last so plain `podman build .` targets it.
FROM runtime-base AS runtime
