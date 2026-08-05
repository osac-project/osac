# OSAC Installer Instructions

This is a **Helm-based infrastructure/deployment repository**. It
assembles component submodules (osac-operator, fulfillment-service,
osac-aap, bare-metal-fulfillment-operator, osac-ui) and deploys them
via a Helm umbrella chart. There is no Go code, no container builds,
and no unit tests in this repo. All validation is structural.

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

base/                            # Git submodules (version tracking) -- discover with: git submodule status
prerequisites/                   # Cluster-wide operator manifests
scripts/                         # Automation scripts (setup, teardown, sync)
```

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

- Do not modify files inside any `base/*/` directories (discover with:
  `git submodule status`) -- these are submodules. Changes to component
  manifests belong in the component repos.
