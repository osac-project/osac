import importlib.util
import os
import shutil
import stat
from pathlib import Path

import pytest


SCRIPT_PATH = Path(__file__).resolve().parents[2] / "scripts/patch_vendor_requirements.py"
SCRIPT_SPEC = importlib.util.spec_from_file_location("patch_vendor_requirements", SCRIPT_PATH)
assert SCRIPT_SPEC is not None and SCRIPT_SPEC.loader is not None
SCRIPT = importlib.util.module_from_spec(SCRIPT_SPEC)
SCRIPT_SPEC.loader.exec_module(SCRIPT)

REQUIREMENT_FLOORS = SCRIPT.REQUIREMENT_FLOORS
apply_overrides = SCRIPT.apply_overrides
sanitize_controller_example_credentials = SCRIPT.sanitize_controller_example_credentials
sanitize_kubeconfig_example_tokens = SCRIPT.sanitize_kubeconfig_example_tokens
remove_unused_test_requirements = SCRIPT.remove_unused_test_requirements
fix_amazon_inventory_error_assertion = SCRIPT.fix_amazon_inventory_error_assertion
fix_amazon_ec2_tag_examples = SCRIPT.fix_amazon_ec2_tag_examples
sanitize_flashblade_example_tokens = SCRIPT.sanitize_flashblade_example_tokens
apply_runtime_overlays = SCRIPT.apply_runtime_overlays


def _create_vendor_requirements(vendor_dir: Path) -> None:
    contents = {
        "ansible_collections/ansible/controller/requirements.txt": "awxkit\n",
        "ansible_collections/kubernetes/core/requirements.txt": ("kubernetes>=24.2.0\nrequests-oauthlib\njsonpatch\n"),
        "ansible_collections/kubernetes/core/test-requirements.txt": ("coverage==4.5.4\npytest\n"),
        "ansible_collections/openstack/cloud/requirements.txt": "openstacksdk>=1.0.0\n",
        "ansible_collections/ansible/utils/test-requirements.txt": ("pytest-ansible\nansible-navigator\n"),
        "ansible_collections/amazon/aws/test-requirements.txt": (
            "botocore\n# For testing event source plugins\nasyncmock\npytest-asyncio\naiobotocore[botocore]\n"
        ),
    }
    for relative_path, content in contents.items():
        path = vendor_dir / relative_path
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")


def test_apply_overrides_adds_all_fixed_dependency_floors(tmp_path):
    _create_vendor_requirements(tmp_path)

    assert apply_overrides(tmp_path) == len(REQUIREMENT_FLOORS)

    for relative_path, floors in REQUIREMENT_FLOORS.items():
        text = (tmp_path / relative_path).read_text(encoding="utf-8")
        for package, version in floors.items():
            assert f"{package}>={version}" in text


def test_apply_overrides_is_idempotent(tmp_path):
    _create_vendor_requirements(tmp_path)

    apply_overrides(tmp_path)
    assert apply_overrides(tmp_path) == 0


def test_apply_overrides_upgrades_a_previous_osac_floor(tmp_path):
    _create_vendor_requirements(tmp_path)
    path = tmp_path / "ansible_collections/kubernetes/core/requirements.txt"
    path.write_text(
        "kubernetes>=24.2.0\nrequests-oauthlib\njsonpatch\n\n"
        f"{SCRIPT.REQUIREMENT_FLOOR_COMMENT}\n"
        "oauthlib>=4.0.0\nurllib3>=2.8.0\n",
        encoding="utf-8",
    )

    apply_overrides(tmp_path)
    text = path.read_text(encoding="utf-8")
    assert "oauthlib>=4.0.1" in text
    assert "oauthlib>=4.0.0" not in text
    assert apply_overrides(tmp_path) == 0


def test_apply_overrides_rejects_an_unreviewed_upstream_constraint(tmp_path):
    _create_vendor_requirements(tmp_path)
    path = tmp_path / "ansible_collections/openstack/cloud/requirements.txt"
    path.write_text("openstacksdk>=1.0.0\nurllib3<2.0\n", encoding="utf-8")

    with pytest.raises(ValueError, match="review the collection update"):
        apply_overrides(tmp_path)


def test_apply_overrides_requires_all_expected_collection_manifests(tmp_path):
    with pytest.raises(FileNotFoundError, match="ansible/controller/requirements.txt"):
        apply_overrides(tmp_path)


def test_remove_unused_test_requirements_and_is_idempotent(tmp_path):
    _create_vendor_requirements(tmp_path)

    assert remove_unused_test_requirements(tmp_path) == 2
    utils = (tmp_path / "ansible_collections/ansible/utils/test-requirements.txt").read_text(encoding="utf-8")
    aws = (tmp_path / "ansible_collections/amazon/aws/test-requirements.txt").read_text(encoding="utf-8")
    assert "ansible-navigator" not in utils
    assert "pytest-ansible" in utils
    assert "aiobotocore" not in aws
    assert "For testing event source plugins" in aws
    assert "asyncmock" in aws
    assert "pytest-asyncio" in aws
    assert "botocore" in aws
    assert remove_unused_test_requirements(tmp_path) == 0


def test_remove_unused_test_requirements_rejects_changed_upstream_line(tmp_path):
    _create_vendor_requirements(tmp_path)
    path = tmp_path / "ansible_collections/ansible/utils/test-requirements.txt"
    path.write_text("pytest-ansible\nansible-navigator>=3.0\n", encoding="utf-8")

    with pytest.raises(ValueError, match="review the upstream test dependency"):
        remove_unused_test_requirements(tmp_path)


def test_fix_amazon_inventory_error_assertion_is_scoped_and_idempotent(tmp_path):
    path = tmp_path / SCRIPT.AMAZON_INVENTORY_ERROR_TEST
    path.parent.mkdir(parents=True)
    path.write_text(f"{SCRIPT.AMAZON_INVENTORY_URL_ASSERTION}\n", encoding="utf-8")

    assert fix_amazon_inventory_error_assertion(tmp_path) is True
    assert path.read_text(encoding="utf-8").splitlines() == [SCRIPT.AMAZON_INVENTORY_URL_ASSERTION_FIXED]
    assert fix_amazon_inventory_error_assertion(tmp_path) is False


def test_fix_amazon_inventory_error_assertion_rejects_upstream_drift(tmp_path):
    path = tmp_path / SCRIPT.AMAZON_INVENTORY_ERROR_TEST
    path.parent.mkdir(parents=True)
    path.write_text("assert raised.exception\n", encoding="utf-8")

    with pytest.raises(ValueError, match="review the upstream test"):
        fix_amazon_inventory_error_assertion(tmp_path)


def test_fix_amazon_ec2_tag_examples_and_is_idempotent(tmp_path):
    for index, (relative_path, (old, _)) in enumerate(SCRIPT.AMAZON_EC2_TAG_EXAMPLE_REWRITES.items()):
        path = tmp_path / relative_path
        path.parent.mkdir(parents=True, exist_ok=True)
        if index == 1:
            old = old.replace("Application tag", "Environment tag").replace(
                SCRIPT.AMAZON_EC2_TAG_SOURCE, SCRIPT.AMAZON_EC2_TAG_PREVIOUS
            )
        path.write_text(old + "\n", encoding="utf-8")

    assert fix_amazon_ec2_tag_examples(tmp_path) == 2
    for relative_path, (_, new) in SCRIPT.AMAZON_EC2_TAG_EXAMPLE_REWRITES.items():
        assert (tmp_path / relative_path).read_text(encoding="utf-8") == new + "\n"
    assert fix_amazon_ec2_tag_examples(tmp_path) == 0


def test_sanitize_flashblade_example_tokens_and_is_idempotent(tmp_path, monkeypatch):
    module_dir = tmp_path / SCRIPT.FLASHBLADE_MODULE_DIR
    module_dir.mkdir(parents=True)
    path = module_dir / "example.py"
    path.write_text(
        'EXAMPLES = r"""\n'
        "- name: First\n  api_token: first-token\n"
        '- name: Second\n  api_token: "second-token"\n'
        '"""\napi_token: outside-example\n',
        encoding="utf-8",
    )
    monkeypatch.setattr(SCRIPT, "FLASHBLADE_API_TOKEN_FIELDS", 2)

    assert sanitize_flashblade_example_tokens(tmp_path) == 2
    contents = path.read_text(encoding="utf-8")
    assert contents.count('api_token: "replace-me"') == 2
    assert "api_token: outside-example" in contents
    assert sanitize_flashblade_example_tokens(tmp_path) == 0


def test_sanitize_flashblade_example_tokens_rejects_upstream_drift(tmp_path, monkeypatch):
    module_dir = tmp_path / SCRIPT.FLASHBLADE_MODULE_DIR
    module_dir.mkdir(parents=True)
    path = module_dir / "example.py"
    path.write_text('EXAMPLES = """\n  api_token: sample\n"""\n', encoding="utf-8")
    monkeypatch.setattr(SCRIPT, "FLASHBLADE_API_TOKEN_FIELDS", 2)

    with pytest.raises(ValueError, match="review the upstream collection update"):
        sanitize_flashblade_example_tokens(tmp_path)


def test_sanitize_controller_example_credentials_is_scoped_and_idempotent(tmp_path):
    path = tmp_path / SCRIPT.CONTROLLER_SUBSCRIPTIONS_EXAMPLES
    path.parent.mkdir(parents=True)
    path.write_text(
        """EXAMPLES = '''
- subscriptions:
    client_secret: "first"
- subscriptions:
    client_secret: "second"
'''
runtime_value = {"client_secret": "unchanged"}
""",
        encoding="utf-8",
    )

    assert sanitize_controller_example_credentials(tmp_path) is True
    patched = path.read_text(encoding="utf-8")
    assert patched.count('client_secret: "replace-me"') == 2
    assert 'runtime_value = {"client_secret": "unchanged"}' in patched
    assert sanitize_controller_example_credentials(tmp_path) is False


def test_sanitize_kubeconfig_token_examples_in_source_and_docs(tmp_path):
    example_token = ".".join(("h" * 12, "p" * 12, "s" * 12))
    for relative_path in SCRIPT.KUBECONFIG_EXAMPLE_FILES:
        path = tmp_path / relative_path
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(f'token: "{example_token}"\n', encoding="utf-8")

    assert sanitize_kubeconfig_example_tokens(tmp_path) == 2

    for relative_path in SCRIPT.KUBECONFIG_EXAMPLE_FILES:
        content = (tmp_path / relative_path).read_text(encoding="utf-8")
        assert 'token: "replace-with-service-account-token"' in content

    assert sanitize_kubeconfig_example_tokens(tmp_path) == 0


def test_patch_amazon_ssm_parameter_logging_removes_secret_values_and_is_idempotent(tmp_path):
    path = tmp_path / SCRIPT.AMAZON_SSM_PARAMETER_LOOKUP
    path.parent.mkdir(parents=True)
    source = "from ansible.module_utils.common.text.converters import to_native\n"
    source += "\n".join(old for old, _ in SCRIPT.AMAZON_SSM_PARAMETER_LOG_REWRITES)
    path.write_text(source, encoding="utf-8")

    assert SCRIPT.patch_amazon_ssm_parameter_logging(tmp_path) == 4
    patched = path.read_text(encoding="utf-8")
    assert "to_native" not in patched
    assert "returned {len(paramlist)} parameter(s)" in patched
    assert "returned results for {len(ret)} path(s)" in patched
    assert "returned {len(ret)} value(s)" in patched
    assert SCRIPT.patch_amazon_ssm_parameter_logging(tmp_path) == 0


def test_patch_amazon_route53_inventory_uses_one_global_client_and_is_idempotent(tmp_path):
    path = tmp_path / SCRIPT.AMAZON_EC2_INVENTORY
    path.parent.mkdir(parents=True)
    path.write_text(SCRIPT.AMAZON_ROUTE53_MAP_OLD, encoding="utf-8")

    assert SCRIPT.patch_amazon_route53_inventory(tmp_path) == 1
    patched = path.read_text(encoding="utf-8")
    assert 'next(iter(self.all_clients("route53")), None)' in patched
    assert 'for connection, _region in self.all_clients("route53")' not in patched
    assert SCRIPT.patch_amazon_route53_inventory(tmp_path) == 0


def test_patch_vast_saml_config_redacts_all_custom_results_and_is_idempotent(tmp_path):
    path = tmp_path / SCRIPT.VAST_SAML_CONFIG_MODULE
    path.parent.mkdir(parents=True)
    path.write_text("self.module.exit_json()\n" * SCRIPT.VAST_SAML_CONFIG_EXIT_COUNT, encoding="utf-8")

    assert SCRIPT.patch_vast_saml_config_redaction(tmp_path) == SCRIPT.VAST_SAML_CONFIG_EXIT_COUNT
    patched = path.read_text(encoding="utf-8")
    assert "self.module.exit_json(" not in patched
    assert patched.count("self._exit_json_with_redaction(") == SCRIPT.VAST_SAML_CONFIG_EXIT_COUNT
    assert SCRIPT.patch_vast_saml_config_redaction(tmp_path) == 0


def test_vast_redaction_helper_upgrades_only_the_previous_overlay(tmp_path):
    path = tmp_path / SCRIPT.VAST_REDACTION_MODULE
    path.parent.mkdir(parents=True)
    path.write_text(SCRIPT.VAST_REDACTION_SOURCE_PREVIOUS, encoding="utf-8")

    assert SCRIPT._install_vast_redaction_helper(tmp_path) is True
    assert path.read_text(encoding="utf-8") == SCRIPT.VAST_REDACTION_SOURCE
    assert SCRIPT._install_vast_redaction_helper(tmp_path) is False

    path.write_text("unrecognized helper content", encoding="utf-8")
    with pytest.raises(ValueError, match="unexpected content"):
        SCRIPT._install_vast_redaction_helper(tmp_path)


def test_runtime_overlays_match_vendor_snapshot_and_are_idempotent(tmp_path):
    vendor_source = SCRIPT_PATH.parents[1] / "vendor"
    vendor_dir = tmp_path / "vendor"
    runtime_files = (
        SCRIPT.AMAZON_SECRETSMANAGER_LOOKUP,
        SCRIPT.AMAZON_SSM_PARAMETER_LOOKUP,
        SCRIPT.AMAZON_EC2_INVENTORY,
        SCRIPT.KUBECONFIG_MODULE_UTIL,
        SCRIPT.KUBECONFIG_MODULE,
        SCRIPT.KUBECONFIG_ARGS_COMMON,
        SCRIPT.VAST_REDACTION_MODULE,
        SCRIPT.VAST_CLIENT_FILE,
        SCRIPT.VAST_INFO_RESOURCE_FILE,
        SCRIPT.VAST_BASE_RESOURCE_FILE,
        SCRIPT.VAST_SUB_ENDPOINT_FILE,
        SCRIPT.VAST_KEYED_SHOW_FILE,
        SCRIPT.VAST_KEYED_COLLECTION_FILE,
        SCRIPT.VAST_WEBHOOKS_MODULE,
        SCRIPT.VAST_SAML_CONFIG_MODULE,
        SCRIPT.KUBERNETES_HELM_FILE,
        SCRIPT.CONTROLLER_API_FILE,
    )

    for relative_path in runtime_files:
        source = vendor_source / relative_path
        destination = vendor_dir / relative_path
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, destination)

    assert all(changes == 0 for changes in apply_runtime_overlays(vendor_dir).values())

    aws_lookup = (vendor_dir / SCRIPT.AMAZON_SECRETSMANAGER_LOOKUP).read_text(encoding="utf-8")
    assert "to_native(secrets)" not in aws_lookup
    assert "returned {len(secrets)} secret(s)" in aws_lookup

    ssm_lookup = (vendor_dir / SCRIPT.AMAZON_SSM_PARAMETER_LOOKUP).read_text(encoding="utf-8")
    assert "to_native(paramlist)" not in ssm_lookup
    assert "to_native(ret)" not in ssm_lookup
    assert "returned {len(paramlist)} parameter(s)" in ssm_lookup
    assert "returned {len(ret)} value(s)" in ssm_lookup

    aws_ec2 = (vendor_dir / SCRIPT.AMAZON_EC2_INVENTORY).read_text(encoding="utf-8")
    assert 'next(iter(self.all_clients("route53")), None)' in aws_ec2
    assert "for connection, _region in self.all_clients(\"route53\")" not in aws_ec2

    kubeconfig_util = (vendor_dir / SCRIPT.KUBECONFIG_MODULE_UTIL).read_text(encoding="utf-8")
    kubeconfig_module = (vendor_dir / SCRIPT.KUBECONFIG_MODULE).read_text(encoding="utf-8")
    args_common = (vendor_dir / SCRIPT.KUBECONFIG_ARGS_COMMON).read_text(encoding="utf-8")
    assert "tempfile.mkstemp(prefix=\".kubeconfig-\", dir=directory)" in kubeconfig_util
    assert "destination_is_insecure" in kubeconfig_module
    assert "old_hash != new_hash or destination_is_insecure" in kubeconfig_module
    assert '"client-secret"' in args_common
    assert '"id-token"' in args_common
    assert 'key == "env"' in args_common

    kubeconfig_spec = importlib.util.spec_from_file_location(
        "kubeconfig_util",
        vendor_dir / SCRIPT.KUBECONFIG_MODULE_UTIL,
    )
    assert kubeconfig_spec is not None and kubeconfig_spec.loader is not None
    kubeconfig_module_utils = importlib.util.module_from_spec(kubeconfig_spec)
    kubeconfig_spec.loader.exec_module(kubeconfig_module_utils)
    kubeconfig_target = tmp_path / "kubeconfig-target"
    kubeconfig_link = tmp_path / "kubeconfig-link"
    kubeconfig_target.write_text("old", encoding="utf-8")
    os.chmod(kubeconfig_target, 0o644)
    kubeconfig_link.symlink_to(kubeconfig_target)
    kubeconfig_module_utils.write_file(str(kubeconfig_link), {"apiVersion": "v1"})
    assert kubeconfig_link.is_symlink()
    assert kubeconfig_module_utils.yaml.safe_load(kubeconfig_target.read_text(encoding="utf-8")) == {
        "apiVersion": "v1"
    }
    assert stat.S_IMODE(kubeconfig_target.stat().st_mode) == 0o600

    redaction_path = vendor_dir / SCRIPT.VAST_REDACTION_MODULE
    redaction_spec = importlib.util.spec_from_file_location("vast_redaction", redaction_path)
    assert redaction_spec is not None and redaction_spec.loader is not None
    redaction = importlib.util.module_from_spec(redaction_spec)
    redaction_spec.loader.exec_module(redaction)

    secrets = {
        "iam_role_credentials": {
            "AccessKeyId": "access-key-value",
            "SecretAccessKey": "secret-key-value",
            "SessionToken": "session-token-value",
            "region": "us-east-1",
        },
        "ssl_keyfile": "private-key-file-content",
        "webhooks": {
            "headers": {
                "Authorization": "Bearer header-token",
                "Content-Type": "application/json",
                "X-API-Key": "webhook-api-key",
            }
        },
    }
    module = type("Module", (), {"no_log_values": set()})()
    redaction.redact_module_result(module, secrets)
    assert module.no_log_values == {
        "access-key-value",
        "secret-key-value",
        "session-token-value",
        "private-key-file-content",
        "Bearer header-token",
        "webhook-api-key",
    }

    saml_config = (vendor_dir / SCRIPT.VAST_SAML_CONFIG_MODULE).read_text(encoding="utf-8")
    assert "self.module.exit_json(" not in saml_config
    assert saml_config.count("self._exit_json_with_redaction(") == SCRIPT.VAST_SAML_CONFIG_EXIT_COUNT

    client = (vendor_dir / SCRIPT.VAST_CLIENT_FILE).read_text(encoding="utf-8")
    assert SCRIPT.VAST_REQUEST_TRACE_OLD not in client
    assert SCRIPT.VAST_RESPONSE_TRACE_OLD not in client
    assert SCRIPT.VAST_REQUEST_EXCEPTION_OLD not in client

    show_lookup = (vendor_dir / SCRIPT.VAST_KEYED_SHOW_FILE).read_text(encoding="utf-8")
    keyed_collection = (vendor_dir / SCRIPT.VAST_KEYED_COLLECTION_FILE).read_text(encoding="utf-8")
    assert "scope[match_field] = match_value" in show_lookup
    assert "self._api_base.get(**filters)" in keyed_collection

    helm = (vendor_dir / SCRIPT.KUBERNETES_HELM_FILE).read_text(encoding="utf-8")
    controller = (vendor_dir / SCRIPT.CONTROLLER_API_FILE).read_text(encoding="utf-8")
    assert "out.find(\"\\n\", offset)" in helm
    assert "splitlines(keepends=True)" not in helm
    assert SCRIPT.CONTROLLER_RETRY_WARNING_OLD not in controller
