# ClusterOrder pull-secret references

The public Cluster API continues to accept `pull_secret_secret`, with the
existing tenant/project authorization and shared-template default rules.
Fulfillment resolves that authorized API Secret and projects its data into a
namespace-local `kubernetes.io/dockerconfigjson` Secret. The Secret is controlled
by the ClusterOrder UID and carries the order's tenant annotation. Existing
objects with conflicting ownership, tenant or type are not adopted or overwritten.

ClusterOrder stores `spec.pullSecretRef.name` and an opaque
`spec.pullSecretRevision`, never the projected credential value. The revision is
generated when the projection's bytes change, not on every reconciliation. It is
not a credential digest. As before, fulfillment's existing periodic sync discovers
API credential changes; no Secret event watch or shorter polling interval is added.
The revision participates in the existing desired-configuration calculation, so
credential changes still schedule provisioning through the existing lifecycle.

InfraEnv references the same namespace-local Secret instead of an independent
create-only copy. AAP reads and validates the reference during protected runtime
execution, then writes the HostedCluster namespace Secret through its existing
provisioning role. An explicit unavailable or invalid reference fails closed;
provider defaults are used only when there is no explicit reference. Runtime
template variables retain the legacy template interface, but credential values
are not written back into the ClusterOrder fact.

## Upgrade and legacy orders

Deploy the updated CRDs and compatible operator/AAP consumers before enabling
the updated fulfillment producer. The operator migrates known inline
`spec.pullSecret` and `spec.templateParameters.pull_secret` values to an owned
Secret before provisioning or deprovisioning. It removes those fields and the
ClusterOrder's last-applied annotation. When a reference already exists it wins
over stale inline fields; an invalid reference does not fall back to inline or
provider credentials. Fulfillment updates retain an existing projection reference
or reuse the legacy order-name-based Secret, so an existing InfraEnv continues
to select the updated projection. An owned legacy Secret is updated rather than
silently reused with stale data. Unrelated template parameters are retained.
The representation change updates the desired spec and can cause one provisioning
pass through the existing configuration-change lifecycle; subsequent unchanged
syncs do not change the projection revision.

The deprecated inline field remains readable for migration; this is not a promise
that direct writers can continue passing credentials in launch variables. Both
job-template and workflow-template launches reject unmigrated orders and resource
overrides after inherited/context variables have been merged. The safe launch
copy excludes the last-applied annotation. No unrelated resource is sanitized.
Known runtime credential variables and alternate `cluster_order` overrides are
also rejected at this boundary; unrelated template parameters remain supported.

Creation can briefly publish a reference before its owned Secret exists because
the order UID is required for ownership. Consumers retry rather than provisioning
with default credentials. Projection write errors are reported without payloads.
Owned projections are garbage-collected with their order; API source Secrets are
not owned or deleted by this mechanism. Removing a reference does not revoke
credentials already installed in a running cluster.

Rollback must retain reference-capable consumers while references remain in use;
do not restore plaintext propagation. Historical AAP job records, audit records,
backups, artifact sanitization and actual credential rotation are outside this
change. The launch invariant concerns pull-secret data, not every credential in
all provisioning flows (some other flows carry admin kubeconfig).

## Local regression boundaries

Synthetic fulfillment and helper tests cover projection, migration, unrelated
parameter preservation, ownership, collision rejection, authorization failures,
rotation and idempotence. Provider tests inspect complete HTTP launch bodies for
both AAP template variants after merges. AAP filter and subprocess tests exercise
settings plus extraction using a synthetic Kubernetes module, including missing,
malformed and forbidden references, and check that output contains no marker.
These tests do not establish real AAP permissions or Kubernetes garbage collection.

Generate with `make manifests generate helm-crds` in `osac-operator/`. Run the
affected Go tests and `make test` for the operator's unit/envtest boundary. In
`osac-aap/`, run `uv run pytest tests/unit/test_cluster_pull_secret.py` and
`uv run ansible-lint .`. Full component integration and deployed cross-component
create/update/delete validation remain required before publication; no live
deployment is changed by the implementation phase.
