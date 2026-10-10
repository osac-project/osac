# CLI UX guidelines

The `osac` CLI serves tenants managing infrastructure resources. Users should
not need Kubernetes knowledge to use it.

When adding or changing a command:

- Check whether kubectl has an equivalent command and follow its UX patterns by
  default. OSAC APIs use Kubernetes conventions, but the CLI should use terms
  that tenants understand.
- When kubectl has no equivalent, compare similar commands in cloud CLIs such
  as `az`, `gcloud`, and `aws`, then choose the behavior that fits OSAC.
- Keep the command non-interactive and scriptable by default.
- Follow the existing verbs and command structure in [root_cmd.go](root_cmd.go)
  rather than introducing a synonym for an existing operation.
