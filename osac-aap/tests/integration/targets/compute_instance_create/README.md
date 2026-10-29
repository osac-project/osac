# ComputeInstance storage mapping integration

`storage_tier_mapping` runs the real top-level ComputeInstance create playbook
against Kind. It checks boot and additional-disk StorageClasses, sizes, sources,
access modes on persisted DataVolumes, including
retries. Missing, empty, incomplete and ambiguous mappings must fail before any
DataVolume or VM exists.
Shared StorageClasses must remain unchanged. A mixed case also exercises the
real storage-provider dispatcher and LVMS StorageClass creation.
Identical mappings returned again by JIT remain valid; different class names
for one requested tier are rejected.

The fixture supplies `osac_job_vars.tenant_storage_classes` as `{name, tier}`
entries, with no CSP tier definitions or backend credentials. It verifies that
this input bypasses provider dispatch. Operator-side input construction and
credential filtering remain owned by
[OSAC-6596](https://redhat.atlassian.net/browse/OSAC-6596).

Ansible, the Kubernetes API, StorageClasses, ComputeInstance CRs, DataVolumes
and VM API objects are real. Only the template's wait/annotate step is replaced
with a no-op. No AAP server, operator, CDI/KubeVirt controllers, LVMS provisioner,
Trident, NetApp, PVC binding or guest I/O is exercised.

## Existing integration environment

From `osac-aap/`, `make test` registers this target without requiring
`STORAGE_TESTS_ENABLED=true`. That flag adds the separate provider test targets.

After the standard integration setup, the focused command from `osac-aap/` is:

```bash
KUBECONFIG="$PWD/tests/integration/kubeconfig-osac-test" \
  uv run ansible-playbook \
  tests/integration/targets/compute_instance_create/tasks/storage_tier_mapping.yml
```

The target requires the ComputeInstance, DataVolume and VirtualMachine CRDs.
Use an API-only test environment: active controllers or admission webhooks can
introduce provider behavior outside this test's scope.
The standard setup removes workload admission configurations after stopping
CDI/KubeVirt, so writes do not call their unavailable services. CRD schema
validation stays enabled.

## Minimal isolated environment

For faster iteration, use a separate disposable Kind cluster with no provider
controllers. The test-only workload CRDs below preserve specs but do not validate
the full CDI/KubeVirt schema. **Install them only in this fresh test cluster**;
never apply them over existing provider CRDs.

From `osac-aap/`, with `uv sync --all-groups` and the repository's required
Ansible collections installed:

```bash
(
set -e
mkdir -p ../.artifacts/implement/OSAC-6682
export KUBECONFIG="$PWD/../.artifacts/implement/OSAC-6682/kubeconfig"
kind create cluster --name osac-6682 --kubeconfig "$KUBECONFIG"
kubectl --kubeconfig "$KUBECONFIG" apply \
  -f ../osac-operator/config/crd/bases/osac.openshift.io_computeinstances.yaml
kubectl --kubeconfig "$KUBECONFIG" apply \
  -f tests/integration/targets/compute_instance_create/files/storage_mapping_api_crds.yaml
kubectl --kubeconfig "$KUBECONFIG" wait \
  --for=condition=Established --timeout=60s \
  crd/computeinstances.osac.openshift.io \
  crd/datavolumes.cdi.kubevirt.io \
  crd/virtualmachines.kubevirt.io
uv run ansible-playbook \
  tests/integration/targets/compute_instance_create/tasks/storage_tier_mapping.yml
kind delete cluster --name osac-6682
)
```

The subshell stops on any failure. If cluster creation fails, subsequent
commands, including cluster deletion, do not run. If a later step fails, the
test cluster is retained for inspection and manual cleanup.

The verifier requires an explicit `KUBECONFIG`, creates unique namespaces and
StorageClasses, and removes only its own resources. It clears inherited
Kubernetes module overrides before invoking the provisioning playbook.

## Deferred Volume-ID coverage

This target tests StorageClass selection independently of Volume-ID propagation
and has no dependency on [PR #1907](https://github.com/osac-project/osac/pull/1907).
It supplies fixed synthetic `diskVolumeIDs` for `boot`, `disk-1` and `disk-2`
so it also works with consumers that require those inputs. No corresponding
Volume records are created, and Volume-ID propagation is not asserted.

The Volume-ID portion of [OSAC-6682](https://redhat.atlassian.net/browse/OSAC-6682)
is deferred until after the Developer Preview, when the Volume API's ownership
of DataVolume creation is established. Follow-up tests must verify per-disk
Volume identity through that workflow, including boot/additional-disk mapping
and retries. They should exercise the new owner rather than add conditional
checks to this StorageClass target.
