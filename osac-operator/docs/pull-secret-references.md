# Cluster pull-secret references

## Fresh-install contract

Cluster creation requires an existing API pull-secret reference, supplied directly
or inherited through `spec_defaults.pull_secret_secret`. API tenant/project and
shared-template reference authorization remains enforced. Public protobuf fields
do not change. Inline `pull_secret` parameters and `spec.pullSecret` values are
unsupported and rejected, not converted. There is no migration of existing
clusters and no AAP provider-default credential Secret fallback. Public SSH keys
remain available through explicit API fields, template spec defaults and public
template parameters; AAP no longer reads a combined SSH/pull-secret Secret.

## Same-hub projections

Fulfillment projects authorized API bytes into an owned
`kubernetes.io/dockerconfigjson` Secret in the ClusterOrder namespace. The order
stores only `spec.pullSecretRef.name` and `spec.pullSecretRevision`. The source
Secret has a ClusterOrder controller owner reference and matching tenant/project
annotations. InfraEnv uses this namespace-local source directly.

The cluster controller creates a working namespace named
`<order-namespace>-<order-name>`, stamping the order UID, order namespace,
tenant and project. Existing namespaces must match this provenance; deleting
namespaces and foreign same-name Secrets cannot be adopted for credential copies.
Before provisioning, the controller copies the validated source to
`pullsecret-cluster-<order-name>` in that working namespace. This Secret has a
Namespace controller owner reference, not an invalid cross-namespace ClusterOrder
owner reference, and records order/tenant/project provenance.

AAP passes only that target Secret name to HostedCluster. It never looks up,
decodes, copies or stores pull-secret data in facts. The checked-in `ocp_small`
and `ocp_ci_small` adapters use names; NICo and AI MaaS delegate to `ocp_small`.
The cluster controller uses its existing local hub client. Off-hub management
clusters and external AAP target credentials are not supported by this flow.

## Rotation, failures and teardown

Opaque UUID revisions change only when projected credential bytes change.
Private SHA-256 digests are compared in constant time and are never persisted as
revisions. Fulfillment's existing synchronization discovers API Secret changes;
there is no new Secret event watch or shorter polling interval. Revision changes
participate in the established configuration-change provisioning lifecycle.

Source and order writes are separate: a source may temporarily precede the
published order revision. Consumers fail closed until they agree. Failed target
writes retry before launch; missing, malformed, unauthorized, wrong-owner or
wrong-tenant/project references do not fall back to other credentials. Errors
do not contain credential values. Unchanged retries do not rotate the revision.

Deletion does not resolve source credentials. The existing lifecycle waits for
HostedCluster teardown before deleting the provenance-checked working namespace,
which garbage-collects its target Secret. The source is garbage-collected with
its ClusterOrder. API source Secrets are neither adopted nor deleted. Manual and
no-provider deletion paths do not gain a credential-read prerequisite.

## Launch boundary and validation

Both final serialized AAP job and workflow requests are checked after inherited
and context merges. Recognized credential slots are rejected at the top level
and inside `osac_job_vars`; resource overrides must match the supplied order.
The launch copy omits the last-applied annotation. Other resource kinds and
unrelated nested template parameters are not broadly sanitized.

Synthetic Ginkgo tests cover ownership, namespace collisions, partial updates,
rotation and fail-closed resolution. Launch tests inspect actual HTTP bodies for
raw and encoded fixture markers. AAP tests run the settings/extraction roles
without Secret modules and inspect all direct template adapters. These isolated
checks do not establish deployed permissions or Kubernetes garbage collection.

Generate from `osac-operator/` with `make manifests generate helm-crds`, then
verify with `make check-helm-crds`. Run the affected Go suites and `make test`;
run `uv run pytest tests/unit` and `make lint` from `osac-aap/`. Full image builds
and deployed component integration remain separate required validation phases.
Historical records, artifact sanitization, backups and real credential rotation
are outside this change.
