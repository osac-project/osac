---
marp: true
theme: redhat
paginate: true
title: Enclave Configuration Wizard
description: Handover documentation covering architecture, development setup, and testing for the Enclave Configuration Wizard.
---

<!-- _class: title -->

# Enclave Configuration Wizard

**Handover Documentation**

Architecture • Development • Testing

---

## What is Enclave Wizard?

Web-based tool for managing Enclave deployment configurations

**Purpose:**
- Runs on the Landing Zone (LZ)
- Manages config files: `global.yaml`, `certificates.yaml`, `cloud_infra.yaml`
- Schema-driven UI with auto-validation

**Tech Stack:**
- **Backend:** Go + Huma framework (OpenAPI-first)
- **Frontend:** React + TypeScript + PatternFly
- **Deployment:** RPM-based installation on Fedora/CentOS

---

<!-- _class: divider -->

# Architecture Overview

---

## System Components

```text
┌─────────────────────────────────────────────────┐
│  Enclave Wizard (deployed on LZ)                │
│                                                  │
│  ┌──────────────────┐      ┌─────────────────┐  │
│  │  wizard-api      │      │  wizard-ui      │  │
│  │  (Go binary)     │◄─────┤  (React SPA)    │  │
│  │  systemd service │      │  podman quadlet │  │
│  │  :8080           │      │  :3443 HTTPS    │  │
│  │                  │      │  :3001 HTTP     │  │
│  └──────────────────┘      └─────────────────┘  │
│         │                                        │
│         ▼                                        │
│  /opt/enclave/config/                            │
│    ├── global.yaml                               │
│    ├── certificates.yaml                         │
│    └── cloud_infra.yaml                          │
└─────────────────────────────────────────────────┘
```

<!-- _footer: "Self-signed TLS cert generated at /etc/enclave-wizard/tls/" -->

---

## Architecture: Schema-Driven Design

**OpenAPI 3.1** auto-generated from Go struct tags

```go
type ClusterConfig struct {
    ProxyURL *string `json:"proxyURL,omitempty"
                      yaml:"proxyURL,omitempty"
                      doc:"HTTP proxy URL"
                      pattern:"^https?://"`
}
```

**Flow:**
1. Backend defines schema via struct tags
2. Huma generates OpenAPI spec at `/openapi.json`
3. Frontend fetches schema and renders forms dynamically
4. No manual schema sync needed

---

## Key Directories

| Path | Purpose |
|------|---------|
| `internal/api/` | HTTP handlers (config, tasks, plugins) |
| `internal/models/` | Go types (config, plugin, task) |
| `internal/config/` | YAML reader/writer |
| `internal/deploy/` | Ansible playbook runner |
| `ui/apps/wizard/src/wizard/steps/` | Step components |
| `ui/apps/wizard/src/schema/` | Schema rendering logic |
| `docs/` | How-to guides |

---

<!-- _class: divider -->

# Adding a New Field

---

## Field Addition Flow (7 Steps)

**Schema-driven:** Add once in Go, UI renders automatically

1. Add Go struct field with tags (`internal/models/config.go`)
2. Rebuild (`make build`)
3. (Optional) Add label override (`ui/.../schemaUtils.ts`)
4. Add field to step component (`ui/.../steps/<Step>.tsx`)
5. (Optional) Add placeholder hint (`SchemaFormRenderer.tsx`)
6. (Optional) Mark as required (`stepFields.ts`)
7. (Optional) Add custom validation (`useStepValidation.ts`)

<!-- _footer: "See docs/howto-add-field.md for full details" -->

---

## Example: Adding a Proxy URL Field

**Step 1 — Add to Go model:**

```go
type ClusterConfig struct {
    // ... existing fields ...
    ProxyURL *string `json:"proxyURL,omitempty"
                      yaml:"proxyURL,omitempty"
                      doc:"HTTP proxy URL for outbound traffic"
                      pattern:"^https?://"`
}
```

**Step 2 — Rebuild:**

```bash
make build  # Regenerates OpenAPI spec
```

---

## Example: Wire to UI (cont.)

**Step 4 — Add to step component:**

```typescript
const NETWORK_FIELDS = [
  "global.machineNetwork",
  "global.apiVIP",
  "global.ingressVIP",
  "global.proxyURL",  // <-- add here
];
```

**Result:** Field auto-renders as text input with pattern validation

**Widget selection** (automatic):
- `string` with `enum` → dropdown
- `boolean` → checkbox
- `integer` → number input
- `array` → add/remove list

---

## Page Object Model Playbooks

**Playwright helpers** encapsulate all UI interactions:

**`wizard-page.ts`** — High-level wizard methods:
- `goto()`, `clickGetStarted()`, `selectFlavor()`
- `fillLandingZone()`, `fillHubCluster()`, `fillOsacConfig()`
- `selectGpuPlugin()`, `getYamlContent()`, `clickValidate()`
- `clickWriteConfiguration()`, `waitForWriteSuccess()`

**`wizard-api.ts`** — API client methods:
- `writeConfig()`, `getConfig()`, `validateConfig()`
- `triggerProvision()`, `getProvisionStatus()` (mocked)

**Tests never use raw Playwright selectors** — all interactions via page objects

---

<!-- _class: divider -->

# Testing Strategy

---

## Testing Layers

Three complementary test approaches:

1. **Unit tests** — Go tests for models, config I/O, validation
2. **Bash E2E tests** — API contract verification (curl inside VM)
3. **Playwright E2E tests** — Browser-driven UI flows

**Test environment:** Fedora 42 VM on remote libvirt host

---

## Local Development

```bash
# Start API server locally
make run
# or: go run . --port 8080 --enclave-dir ../enclave

# Access at http://localhost:8080/docs
```

**Mock mode** for frontend development:

```bash
cd ui/apps/wizard
yarn dev  # Runs with mock API responses
```

---

## VM Deployment for E2E Tests

```bash
# Build RPM
make rpm

# Deploy to target host (creates VM, installs RPM)
make deploy TARGET=root@myserver.example.com

# Access wizard at https://myserver.example.com:3443/wizard
```

**What happens:**
1. Fedora 42 VM created (4GB RAM, 2 vCPUs)
2. RPM installed inside VM
3. iptables DNAT forwards ports 3001/3443 from host → VM

---

## Running E2E Tests

**Bash API tests:**

```bash
# Full pipeline: build → deploy → test → teardown
make e2e TARGET=root@myserver.example.com

# Re-run tests on existing deployment
make e2e-rerun TARGET=root@myserver.example.com

# Single test
hack/e2e/run-e2e.sh --host root@myserver \
  --test round_trip --skip-deploy --skip-teardown
```

---

## Running E2E Tests (cont.)

**Playwright browser tests:**

```bash
# Headless
make e2e-browser WIZARD_URL=https://myserver:3443

# Headed (visible browser)
WIZARD_URL=https://myserver:3443 yarn e2e:headed

# Full pipeline (bash + browser tests)
make e2e-full TARGET=root@myserver.example.com
```

---

## Test Coverage

**Bash tests** (`hack/e2e/test_*.sh`):
- `provision_config` — Disconnected + LVMS
- `connected_lvms` — Connected mode
- `disconnected_odf_gpu` — ODF + GPU plugin
- `round_trip` — Full field fidelity verification
- `invalid_combinations` — Plugin validation
- `config_preview` — Preview endpoint

**Playwright tests** (`ui/apps/wizard/e2e/tests/`):
- `connected-lvms.spec.ts` — Full wizard flow
- `disconnected-odf-gpu.spec.ts` — ODF + GPU flavor
- `connected-rhoai.spec.ts` — RHOAI + GPU plugin
- `provision.spec.ts` — Provision trigger (mocked)
- `deploy-demo.spec.ts` — Demo recording playbook

---

## Demo Recording

**Playwright video capture** for UI demos:

```bash
# Run demo playbook (Playwright records video automatically)
cd ui/apps/wizard
WIZARD_URL=https://host:3443 yarn e2e tests/deploy-demo.spec.ts

# Video saved to test-results/
```

**For advanced editing** (speed up, trim, title cards):
- Use `browser-demo-recording` skill (available in workspace)
- Runs in podman container with Playwright + ffmpeg
- Combines video capture + editing workflow

---

<!-- _class: divider -->

# Key Documentation

---

## Essential Docs

| Doc | Purpose |
|-----|---------|
| `README.md` | Quick start, API endpoints |
| `docs/howto-add-field.md` | Field addition workflow |
| `docs/howto-add-plugin.md` | Plugin integration |
| `docs/howto-add-experience.md` | Wizard flow customization |
| `docs/testing.md` | E2E test setup and architecture |
| `docs/deployment.md` | RPM build and VM deployment |
| `docs/CONTRIBUTING.md` | Developer guide |

---

## Quick Reference Commands

```bash
# Development
make run                    # Start API server
make test                   # Run Go tests
make lint                   # Go vet

# Build
make build                  # Local binary
make rpm                    # RPM package

# Deployment
make deploy TARGET=host     # Deploy to VM
make teardown TARGET=host   # Destroy VM

# E2E tests
make e2e TARGET=host        # Full test pipeline
make e2e-browser WIZARD_URL=url  # Playwright tests
```

---

<!-- _class: title -->

# Questions?

**Documentation:** `docs/` directory
**Skills:** Available in the workspace for common tasks
