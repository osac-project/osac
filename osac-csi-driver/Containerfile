FROM registry.access.redhat.com/ubi10/go-toolset:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /opt/app-root/src
COPY go.mod go.sum ./
RUN go mod download

COPY . ./

ARG VERSION=0.1.0
ARG GIT_COMMIT=unknown
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -a -buildvcs=false -ldflags "-X main.version=${VERSION} -X main.gitCommit=${GIT_COMMIT}" \
    -o osac-csi-driver ./cmd/osac-csi-driver

FROM registry.access.redhat.com/ubi10-minimal:10.2
COPY --from=builder /opt/app-root/src/osac-csi-driver /usr/local/bin/
USER 1001
ENTRYPOINT ["/usr/local/bin/osac-csi-driver"]
