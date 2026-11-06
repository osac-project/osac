# OSAC Helm Charts

This directory contains the Helm charts used to deploy and develop OSAC (Open Sovereign AI Cloud).

## Charts

### `osac/` — Production chart

The main OSAC chart. This is the **only chart that is published and used in production/customer environments**. It bundles:

- osac-operator
- fulfillment-service
- osac-aap
- bare-metal-fulfillment-operator
- osac-ui
- osac-metering
- csi-driver

Default values in this chart reflect production settings. Dev-only features (e.g. LVMS storage backend) are **not** enabled by default.

### `osac-deps/` — CRD prerequisites (dev/CI only)

Installs CRD providers and operator subscriptions required by OSAC. Uses OLM subscriptions on OpenShift and upstream Helm on Kind.

### `osac-infra/` — Shared infrastructure (dev/CI only)

Sets up shared infrastructure dependencies: CA chain, Keycloak, Gateway, trust-manager, shared PostgreSQL, and CRD instances.

### `osac-devstack/` — Local development stack (dev only)

A full local development stack including KubeVirt, AWX operator, UI, and a seeded catalog. Intended exclusively for local development environments.

## Values profiles

Environment-specific values overrides live in [`../values/`](../values/). Profiles such as `dev/`, `bmaas-ci/`, `caas-ci/`, `vmaas-ci/`, and `full-ci/` customize chart settings for development and CI pipelines without altering production defaults.
