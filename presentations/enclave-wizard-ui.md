---
marp: true
theme: redhat
paginate: true
title: Enclave Wizard UI
description: Schema-driven installation wizard for Open Sovereign AI Cloud — architecture, component pipeline, and deployment workflow.
---

<style>
section.pipeline h2 { margin-bottom: 10px; }
section.pipeline { font-size: 24px; }
section.mapping { font-size: 24px; }
section.mapping table { margin-top: 10px; }
section.workflow { font-size: 22px; }
section.workflow li { margin-bottom: 4px; }
</style>

<!-- _class: title -->
<!-- _paginate: false -->

# Enclave Wizard UI

### Schema-driven installation for Open Sovereign AI Cloud

---

## What is OSAC?

**Open Sovereign AI Cloud** — a fulfillment system for provisioning OpenShift clusters, bare metal hosts, virtual machines, and networking on sovereign infrastructure.

- Multi-tenant: cloud providers host isolated environments for tenant organizations
- Multiple components: fulfillment-service, osac-operator, osac-aap, hub clusters, and their dependencies

Installing OSAC is **complex** — a Helm umbrella chart, prerequisites, hub registration, and many configuration parameters.

---

## What is Enclave?

**Enclave** is a tool for provisioning opinionated OpenShift installations — primarily targeting **disconnected environments** but also helping with complex multi-component deployments like OSAC.

| Concern | What Enclave does |
|---------|-------------------|
| **Disconnected / air-gapped** | Deploys OpenShift where there is no internet access |
| **Complex installations** | Orchestrates hub clusters, dependencies, and platform components |
| **Guided experience** | Wizard UI walks operators through configuration step by step |

For OSAC, Enclave handles the full stack: hub cluster, prerequisites (AAP, cert-manager, storage), and OSAC itself.

---

<!-- _class: divider -->

# The Wizard

---

## The Problem

An Enclave + OSAC installation spans **many moving parts**:

- Hub cluster provisioning and registration
- Prerequisites: AAP, cert-manager, storage classes
- OSAC components: fulfillment-service, operator, AAP config
- Per-component settings: database, OIDC, DNS, networking

Getting all of this right by editing YAML files is daunting — especially in disconnected environments where trial-and-error is costly.

The Wizard makes this **human-friendly**: a guided, step-by-step experience with validation and sensible defaults.

---

<!-- _class: pipeline -->

## How the Wizard Works

The Enclave Wizard **renders UI controls automatically** from the Helm chart's JSON Schema — no custom UI code required for each field.

```text
┌──────────────────────┐      ┌──────────────────┐      ┌──────────────┐
│   osac-installer     │      │  Enclave Plugin  │      │  Enclave UI  │
│                      │      │                  │      │              │
│  values.schema.json  │─────>│  reads schema,   │─────>│  Wizard      │
│  (Helm JSON Schema)  │      │  exposes params  │      │  renders     │
│                      │      │                  │      │  controls    │
└──────────────────────┘      └──────────────────┘      └──────────────┘
```

Adding a new field to the Wizard starts with a schema change in `osac-installer`.

---

<!-- _class: mapping -->

## Schema → UI Control Mapping

The JSON Schema construct determines which control the Wizard renders:

| Schema construct | UI control | Example |
|-------------|-----------|---------|
| `enum` | Dropdown | DNS provider: `route53`, `infoblox` |
| `boolean` | Checkbox | Enable bundled PostgreSQL |
| `string` | Text input | External hostname |
| `integer` / `number` | Numeric input | Worker node count |

Validation rules, default values, and descriptions come from the schema — the Wizard enforces them automatically.

---

<!-- _class: workflow -->

## Adding a New Config Item

Three coordinated actions bring a new field into the Wizard:

**1. `osac` PR** — add the value to `osac-installer/values.yaml` and its schema to `osac-installer/values.schema.json`
```yaml
# values.yaml                      # values.schema.json
dns:                                "dns":
  provider: route53                   "properties":
                                        "provider":
                                          "type": "string"
                                          "enum": ["route53", "infoblox"]
                                          "default": "route53"
```

**2. Enclave plugin** — picks up the change and exposes the parameter

**3. Enclave UI** — renders the appropriate control in the Wizard

---

## Benefits

- **Minimal custom UI per field** — schema drives most of the Wizard automatically
- **Validation built-in** — required fields, types, enums enforced at input time
- **Single source of truth** — `values.schema.json` documents and validates
- **OpenShift catalog ready** — same schema powers the OpenShift Software Catalog form
- **Extensible** — new Helm values appear in the Wizard without UI code changes

---

<!-- _class: divider -->

# Demo

**[Watch the demo](https://drive.google.com/file/d/1CoUxa2KdannKGUkEWS5MZ_RPL8Hc0lHD/view?usp=drive_link)**

---

<!-- _class: dark -->

# Schema in, Wizard out.

One JSON change — three teams coordinate — the user gets a guided experience.

---

<!-- _class: title -->
<!-- _paginate: false -->

# Thank You

### Questions?
