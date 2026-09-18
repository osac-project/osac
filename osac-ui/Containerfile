# Multi-stage build: one image runs the Go proxy + serves the SPA bundle
# Usage:
#   podman build -t osac:latest -f Containerfile .
#   podman run --rm -p 8080:8080 -e FULFILLMENT_API_URL=https://fulfillment.example.com osac:latest

# ── Stage 1: install SPA workspace dependencies ───────────────────────────
FROM registry.access.redhat.com/ubi9/nodejs-22-minimal:9.8 AS deps
USER root
WORKDIR /app

RUN npm install -g pnpm@9

COPY package.json pnpm-workspace.yaml pnpm-lock.yaml* tsconfig.base.json ./
COPY libs/i18n/package.json ./libs/i18n/
COPY libs/types/package.json ./libs/types/
COPY libs/ui-components/package.json ./libs/ui-components/
COPY apps/app-frontend/package.json ./apps/app-frontend/

RUN pnpm install --frozen-lockfile

# ── Stage 2: build SPA ────────────────────────────────────────────────────
FROM deps AS spa-builder
WORKDIR /app

COPY libs/ ./libs/
COPY apps/app-frontend/ ./apps/app-frontend/

# SPA output goes to apps/app-frontend/dist (configured in vite.config.ts outDir)
RUN pnpm --filter @osac/app-frontend run build

# ── Stage 3: build Go proxy ───────────────────────────────────────────────
FROM registry.access.redhat.com/ubi9/go-toolset:9.8-1789040808 AS proxy-builder
USER root
WORKDIR /build

COPY proxy/go.mod proxy/go.sum ./
RUN go mod download

COPY proxy/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o osac-proxy .

# ── Stage 4: production image ──────────────────────────────────────────────
FROM registry.access.redhat.com/ubi9/ubi-minimal:9.5 AS production
WORKDIR /app

ENV PORT=8080
ENV HOST=0.0.0.0
ENV LOG_LEVEL=info

# Copy the compiled Go binary
COPY --from=proxy-builder /build/osac-proxy ./osac-proxy

# Copy the built SPA (vite outDir = apps/app-frontend/dist)
COPY --from=spa-builder /app/apps/app-frontend/dist ./public

EXPOSE 8080
USER 1001

CMD ["./osac-proxy"]
