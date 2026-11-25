# VM resize regression tests

OSAC-7781 migrates the existing VM resize cases from the GitLab
`osac-vmaas-deployment/e2e/roles/vmaas_resize` role, source revision
`5d8a8abe6b76f9ce2946e350acffed0979b183a2`. Existing VMaaS sanity cases
remain in place; the tests here cover scenarios missing from that suite.

| Source case | Python coverage |
| --- | --- |
| TC-NFR1-01, TC-FR1-01, TC-FR2-01 | `sanity/test_compute_instance_instance_type.py::test_compute_instance_resize_requires_restart_and_applies_new_resources`; CLI resize directions are also exercised by `test_compute_instance_resize_via_cli` |
| TC-FR1-02 | `sanity/test_compute_instance_instance_type.py::test_compute_instance_resize_while_stopped_applies_on_start` |
| TC-FR2-02 | `test_mixed_direction_resize_and_restart_via_cli` |
| TC-FR3-01 | Existing `test_compute_instance_resize_to_deprecated_type_warns_via_cli` sanity case |
| TC-FR3-02 | Existing `test_compute_instance_resize_to_obsolete_type_is_rejected_via_cli` sanity case |
| TC-FR3-03 | `test_resize_rejects_missing_instance_type_via_cli` |
| TC-FR4-01 | Existing `test_compute_instance_resize_to_current_type_is_noop` sanity case, including CLI edit |
| TC-FR4-02 | `test_resize_to_current_obsolete_type_is_noop_via_cli` |
| TC-FR5-01, TC-FR6-01 | Existing `test_compute_instance_resize_requires_restart_and_applies_new_resources` sanity case checks staging, restart, and live VMI resources |

The source role uses CLI edits for its resize and restart operations. Existing
sanity cases combine CLI coverage with public API resize/restart coverage; the
mixed-direction regression exercises both edits through the CLI. It creates its
VM directly from a template; the source case creates one from a catalog item.
The existing catalog sanity case covers catalog-based resize separately. The live VMI
checks use `status.currentCPUTopology` and `status.memory.guestCurrent`, falling
back to the VMI guest memory specification when guest-current is absent, as in
the source role. They require a configuration where a CPU resize is staged
until restart; they do not prove live migration or GPU behavior.

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
