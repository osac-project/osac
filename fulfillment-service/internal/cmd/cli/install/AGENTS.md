# osac install

This directory is a deliberate, documented exception to the parent
[`../AGENTS.md`](../AGENTS.md) principle that the CLI is "tenant-facing, not
a Kubernetes administration interface."

`osac install discover` and `osac install validate` run against the target
Hub cluster *before* OSAC (and therefore fulfillment-service) exists on it.
Their audience is whoever sets up a Hub cluster — a cluster admin or SRE —
not an OSAC tenant, and Kubernetes literacy is a reasonable requirement for
that audience and that phase. This is a different persona and a different
lifecycle phase than every other verb in this CLI, bolted onto the same
binary for distribution convenience (same release pipeline, same
`osac_<os>_<arch>` download), not because the audiences or mental models are
the same.

Consequences of this exception:

- Command help text must say plainly that these commands need
  kubectl/oc-level cluster access (a working kubeconfig with sufficient
  RBAC), not soften or hide that requirement to match the rest of the CLI's
  tenant-facing tone.
- These commands must not reuse `osac login`'s JWT session — see
  `internal/install/client.go` — since that authenticates against
  fulfillment-service's own API, a different credential entirely.
- Checks logic lives in `internal/install/`, not here: this package is a
  thin Cobra wrapper only.
