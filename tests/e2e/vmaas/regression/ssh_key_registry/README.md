# VMaaS SSH-key registry test coverage

| Case | Coverage | Test location |
| --- | --- | --- |
| TC-01 | SSH-key Secret lifecycle and duplicate names | Regression: [test_secret_registry.py](test_secret_registry.py), `test_ssh_key_secret_lifecycle` |
| TC-02 | Default-project SSH-key reference and VM projection | Sanity: [test_compute_instance_registered_ssh_key.py](../../sanity/test_compute_instance_registered_ssh_key.py), `test_compute_instance_resolves_registered_ssh_key` |
| TC-03, TC-04 | Named-project VM selects a default-project or named-project SSH key | Regression: [test_authorization.py](test_authorization.py), `test_named_project_vm_selects_registered_key` |
| TC-05 | Same-name project keys and cross-project VM selection | Regression: [test_authorization.py](test_authorization.py), `test_same_key_name_in_different_projects` |
| TC-06 | Catalog default, override, locked and absent SSH-key policies | Regression: [test_vm_lifecycle.py](test_vm_lifecycle.py), `test_catalog_ssh_key_policy_observation` |
| TC-07a, TC-08 | Project key access and access revocation | Regression: [test_authorization.py](test_authorization.py), `test_project_key_access_controls` |
| TC-07b | Cross-tenant key isolation and non-disclosing errors | Regression: [test_authorization.py](test_authorization.py), `test_cross_tenant_keys_are_not_disclosed` |
| TC-07c | Shared-tenant Secret and shared VM key reference rejection | Regression: [test_secret_registry.py](test_secret_registry.py), `test_shared_tenant_rejects_ssh_key`; [test_vm_lifecycle.py](test_vm_lifecycle.py), `test_shared_ssh_key_reference_is_rejected` |
| TC-09a | Invalid SSH-key Secret payloads | Regression: [test_secret_registry.py](test_secret_registry.py), `test_invalid_ssh_key_secret_is_rejected` |
| TC-09b | Missing, deleted and wrong-type VM SSH-key references; bounded hub CR absence checks | Regression: [test_vm_lifecycle.py](test_vm_lifecycle.py), `test_unusable_ssh_key_references_are_rejected` |
| TC-10 | SSH-key selectors, immutable references and in-use deletion | Regression: [test_vm_lifecycle.py](test_vm_lifecycle.py), `test_ssh_key_reference_cannot_be_changed` |
| TC-11 | Registered-key projection, keyless Linux and Windows rejection | Sanity: [test_compute_instance_registered_ssh_key.py](../../sanity/test_compute_instance_registered_ssh_key.py); regression: [test_vm_lifecycle.py](test_vm_lifecycle.py), `test_linux_vm_without_registered_key`, `test_windows_image_rejects_ssh_key` |
| TC-12 | SSH-key injection, tunnel and direct-network login, unrelated-key denial | Regression: [test_guest_access.py](test_guest_access.py), `test_registered_key_authenticates_to_guest` |
| TC-13 | SSH-key update observations and VM key projections (policy pending) | Regression: [test_vm_lifecycle.py](test_vm_lifecycle.py), `test_key_update_observation` |
| NFR-SSH-01 | SSH-key injection and material in CLI, available service and AAP job output | Regression: [test_secret_registry.py](test_secret_registry.py), `test_cli_output_hides_private_key_and_invalid_payload`; [test_vm_lifecycle.py](test_vm_lifecycle.py), `test_deployed_logs_hide_ssh_key_material` |
| NFR-SSH-02 | Non-disclosing errors for inaccessible and missing keys | Regression: [test_authorization.py](test_authorization.py), `test_project_key_access_controls`, `test_cross_tenant_keys_are_not_disclosed` |
