# Build the manager and console-proxy binaries
FROM registry.access.redhat.com/ubi10/go-toolset:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /opt/app-root/src
# Copy the Go Modules manifests (including the nested api module)
COPY go.mod go.sum ./
COPY api/go.mod api/go.sum api/
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY . ./

# Build
# the GOARCH has not a default value to allow the binary be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager cmd/main.go
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o console-proxy cmd/console-proxy/main.go

FROM registry.access.redhat.com/ubi10-minimal:10.2
COPY --from=builder /opt/app-root/src/manager /usr/local/bin/
COPY --from=builder /opt/app-root/src/console-proxy /usr/local/bin/
USER 1001
ENTRYPOINT ["/usr/local/bin/manager"]
