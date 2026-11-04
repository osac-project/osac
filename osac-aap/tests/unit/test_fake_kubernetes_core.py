"""Self-tests for the test-only kubernetes.core collection.

The isolated bm_host_provisioning scenarios run the real role tasks with
the file-backed fake Kubernetes modules under
tests/integration/targets/bm_host_provisioning_patch/test_collections in
place of a real API server. The fakes' happy paths are exercised end to
end by those scenarios; what these tests pin down is the error semantics
the real API server enforces, so a lenient fake can never silently
accept an invalid role patch.
"""

import importlib.util
from pathlib import Path

import pytest

MODULES_DIR = (
    Path(__file__).resolve().parents[1]
    / "integration/targets/bm_host_provisioning_patch"
    / "test_collections/ansible_collections/kubernetes/core/plugins/modules"
)


@pytest.fixture(scope="module")
def json_patch():
    spec = importlib.util.spec_from_file_location(
        "fake_kubernetes_core_k8s_json_patch", MODULES_DIR / "k8s_json_patch.py"
    )
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_unescape_decodes_json_pointer_escapes(json_patch):
    assert json_patch.unescape("osac.test~1never-present") == "osac.test/never-present"
    assert json_patch.unescape("a~0b") == "a~b"


def test_remove_on_missing_pointer_fails(json_patch):
    # The real API server rejects an RFC 6902 remove whose target does not
    # exist; the fixture must reject it too instead of ignoring it.
    with pytest.raises(KeyError):
        json_patch.apply_patch(
            {"spec": {}}, [{"op": "remove", "path": "/spec/rootDeviceHints"}]
        )


def test_test_op_mismatch_fails(json_patch):
    resource = {"metadata": {"annotations": {"osac.test/never-present": "actual"}}}
    with pytest.raises(ValueError):
        json_patch.apply_patch(
            resource,
            [
                {
                    "op": "test",
                    "path": "/metadata/annotations/osac.test~1never-present",
                    "value": "expected",
                }
            ],
        )


def test_guarded_test_and_remove_succeed(json_patch):
    resource = {"metadata": {"annotations": {"osac.test/never-present": "x"}}}
    json_patch.apply_patch(
        resource,
        [
            {
                "op": "test",
                "path": "/metadata/annotations/osac.test~1never-present",
                "value": "x",
            },
            {"op": "remove", "path": "/metadata/annotations/osac.test~1never-present"},
        ],
    )
    assert "osac.test/never-present" not in resource["metadata"]["annotations"]


def test_unsupported_op_fails(json_patch):
    with pytest.raises(ValueError):
        json_patch.apply_patch({"spec": {}}, [{"op": "add", "path": "/spec/x"}])
