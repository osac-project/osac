# CLI UX Guidelines

The OSAC CLI (`fulfillment-service/internal/cmd/cli/`) is a tenant-facing tool for managing infrastructure resources. It is NOT a Kubernetes admin tool - end users should not need to know k8s.

## Design Inspiration

- **Primary reference**: kubectl - OSAC APIs are modeled after k8s, so the CLI naturally follows kubectl patterns
- **Per-feature reference**: when implementing new commands, also look at cloud CLIs (AWS, GCP, Rafay, VCD) for inspiration on how they solved the same problem
- The goal is not to copy any single CLI, but to use kubectl as the default and evaluate alternatives per-feature

## Existing Verbs

`create`, `get`, `describe`, `edit`, `delete`, `annotate`, `label`, `login`, `logout`, `lookup`, `console`, `whoami`, `version`

## When Adding New CLI Commands

- Check if kubectl has an equivalent command and follow its UX patterns
- If kubectl doesn't have one, survey cloud CLIs (az, gcloud, aws) for the closest equivalent
- Keep commands non-interactive and scriptable by default
- All new commands must work without assuming k8s knowledge from the user
