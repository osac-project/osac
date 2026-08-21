# OSAC Installer Instructions

This is a **Helm-based infrastructure/deployment repository** in the `osac`
mono-repo. It assembles component charts (osac-operator, fulfillment-service,
osac-aap, bare-metal-fulfillment-operator, osac-csi-driver, osac-metering) via
`file://` references and deploys **osac-ui** from an external OCI chart. There is
no Go code, no container builds, and no unit tests in this directory. All
validation is structural.

## Validation Commands

After making changes, run the following commands from the installer root
in order. Every command must pass -- CI enforces all of them on every PR.

1. **YAML lint** (strict mode, repo-level `.yamllint.yaml` config):

   ```bash
   yamllint --strict .
   ```

2. **Pre-commit hooks** (trailing whitespace, merge conflicts, large
   files, private key detection, YAML lint):

   ```bash
   pre-commit run --all-files
   ```

3. **Helm lint** (validates chart structure and templates -- see `Makefile` for full command):

   ```bash
   make helm-lint
   ```

4. **Helm template render** (validates against all values files -- see `Makefile` for full command):

   ```bash
   make helm-validate
   ```

## Repository Structure

```text
charts/osac/                     # Helm umbrella chart
  Chart.yaml                     # Dependencies on subchart repos
  values.yaml                    # Default values
  values.schema.json             # JSON Schema for values validation
  templates/                     # Deployment templates

values/
  development/values.yaml        # All controllers, latest images
  vmaas-ci/values.yaml           # VMaaS CI: pinned images
  caas-ci/values.yaml            # CaaS CI: pinned images

prerequisites/                   # Cluster-wide operator manifests
scripts/                         # Automation scripts (setup, teardown, sync)
```

Component source lives in sibling directories at the `osac` repo root (not under
`osac-installer/`). **osac-ui** is external (OCI chart dependency).

## Coding Conventions

- All YAML files must pass `yamllint --strict` with the repo's
  `.yamllint.yaml` config (line-length disabled, document-start disabled,
  indent-sequences: whatever).
- Shell scripts must use `set -euo pipefail`. Source `scripts/lib.sh` for shared functions
  (`retry_until`, `wait_for_resource`, `wait_for_namespace_cleanup`).
- Always use explicit `-n <namespace>` flags in `oc` commands -- never
  rely on the current context namespace.
- Every new Helm value must have a matching entry in
  `charts/osac/values.schema.json`.

## What Not to Modify

- Do not edit component implementation under sibling mono-repo directories
  (e.g. `../fulfillment-service/`) from an `osac-installer/`-only mindset —
  land one PR at the `osac` repo root. **osac-ui** is external; chart version
  bumps belong in `charts/osac/Chart.yaml` or release workflows, not ad-hoc
  edits in a checkout of `osac-ui` from here.
