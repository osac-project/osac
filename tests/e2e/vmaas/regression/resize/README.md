# VM resize regression tests

Resize test coverage is split between VMaaS sanity and regression suites.

| Source case | Suite | Python coverage |
| --- | --- | --- |
| TC-NFR1-01, TC-FR1-01, TC-FR2-01 | Sanity | `sanity/test_compute_instance_instance_type.py::test_compute_instance_resize_requires_restart_and_applies_new_resources`; CLI resize directions are also exercised by `test_compute_instance_resize_via_cli` |
| TC-FR1-02 | Sanity | `sanity/test_compute_instance_instance_type.py::test_compute_instance_resize_while_stopped_applies_on_start` |
| TC-FR2-02 | Regression | `test_mixed_direction_resize_and_restart_via_cli` |
| TC-FR3-01 | Sanity | Existing `test_compute_instance_resize_to_deprecated_type_warns_via_cli` sanity case |
| TC-FR3-02 | Sanity | Existing `test_compute_instance_resize_to_obsolete_type_is_rejected_via_cli` sanity case |
| TC-FR3-03 | Regression | `test_resize_rejects_missing_instance_type_via_cli` |
| TC-FR4-01 | Sanity | Existing `test_compute_instance_resize_to_current_type_is_noop` sanity case, including CLI edit |
| TC-FR4-02 | Regression | `test_resize_to_current_obsolete_type_is_noop_via_cli` |
| TC-FR5-01, TC-FR6-01 | Sanity | Existing `test_compute_instance_resize_requires_restart_and_applies_new_resources` sanity case checks staging, restart, and live VMI resources |

**CLI coverage:** The resize lifecycle and stopped-VM sanity tests use gRPC
updates to verify functional behavior. Separate CLI tests cover upsize and
downsize staging, and the mixed-direction regression covers a CLI resize
followed by a CLI restart. The original Ansible upsize/downsize and stopped-VM
sequences are therefore not exercised entirely through `osac edit`.

Run the migrated regression cases from the repository root:

```bash
uv run pytest -n 0 tests/e2e/vmaas/regression/resize/
```

Run the existing resize sanity cases from the same checkout when validating the
full migrated coverage:

```bash
uv run pytest -n 0 tests/e2e/vmaas/sanity/test_compute_instance_instance_type.py -k resize
```

Both commands require a deployed VMaaS environment with Fulfillment, AAP,
KubeVirt, storage, a VM disk image, tenant networking, `osac`, `grpcurl`,
`kubectl`, public tenant and private API credentials, and hub and VM-cluster
kubeconfigs. They create and delete their own VMs and InstanceTypes. The
regression directory is selected by the full VMaaS tier, not the default sanity
selection. No source GitLab tests are disabled or removed by this migration.
