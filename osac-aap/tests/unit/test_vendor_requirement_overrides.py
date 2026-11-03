import importlib.util
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
