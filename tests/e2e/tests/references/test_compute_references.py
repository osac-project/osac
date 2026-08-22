from __future__ import annotations

import logging
import subprocess
from collections.abc import Generator
from typing import Any
from uuid import uuid4

import pytest

from tests.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.core.helpers import (
    assert_grpc_field_violation,
    wait_for_cr,
    wait_for_deletion,
    wait_for_provision,
    wait_for_running,
)
from tests.core.k8s_client import K8sClient
from tests.core.runner import env

logger = logging.getLogger(__name__)


@pytest.fixture(scope="module")
def compute_template() -> str:
    return env("OSAC_VM_TEMPLATE", "ocp-virt-vm")


@pytest.fixture(scope="module")
def ref_instance_type(private_grpc: GRPCClient) -> Generator[str, None, None]:
    tag = uuid4().hex[:8]
    name = f"ref-it-{tag}"
    private_grpc.create_instance_type(name=name, cores=2, memory_gib=4)
    yield name
    try:
        private_grpc.delete_instance_type(name=name)
    except subprocess.CalledProcessError:
        logger.warning("Failed to cleanup instance type %s", name)


@pytest.fixture(scope="module")
def ref_ci_catalog_item(private_grpc: GRPCClient, compute_template: str) -> Generator[str, None, None]:
    tag = uuid4().hex[:8]
    name = f"ref-ci-cat-{tag}"
    cat_id = private_grpc.create_compute_instance_catalog_item(name=name, template=compute_template)
    yield cat_id
    try:
        private_grpc.delete_compute_instance_catalog_item(catalog_item_id=cat_id)
    except subprocess.CalledProcessError:
        logger.warning("Failed to cleanup catalog item %s", cat_id)


def _ci_create_data(
    name: str, cat_item_name: str, subnet_name: str, sg_name: str, instance_type: str
) -> dict[str, Any]:
    return {
        "object": {
            "metadata": {"name": name},
            "spec": {
                "catalog_item": {"name": cat_item_name},
                "instance_type": {"name": instance_type},
                "network_attachments": [{"subnet": {"name": subnet_name}, "security_groups": [{"name": sg_name}]}],
            },
        }
    }


class TestComputeReferences:
    """OSAC-3100: Compute resource reference tests."""

    def test_compute_instance_full_chain_by_name(
        self,
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        ref_subnet: dict[str, str],
        ref_security_group: dict[str, str],
        ref_ci_catalog_item: str,
        ref_instance_type: str,
    ):
        tag = uuid4().hex[:8]
        ci_name = f"ref-ci-chain-{tag}"
        cat_item = grpc.get_compute_instance_catalog_item(catalog_item_id=ref_ci_catalog_item)
        cat_item_name = cat_item["object"]["metadata"]["name"]

        response: dict[str, Any] = grpc.call(
            service=f"{PUBLIC_API}.ComputeInstances/Create",
            data=_ci_create_data(
                ci_name, cat_item_name, ref_subnet["name"], ref_security_group["name"], ref_instance_type
            ),
        )
        ci_id = response["object"]["id"]
        try:
            spec = response["object"]["spec"]
            cat_ref = spec.get("catalog_item", spec.get("catalogItem", {}))
            assert cat_ref.get("name") == cat_item_name
            assert cat_ref.get("id") == ref_ci_catalog_item

            att = spec.get("network_attachments", spec.get("networkAttachments", [{}]))[0]
            subnet_ref = att.get("subnet", {})
            assert subnet_ref.get("name") == ref_subnet["name"]
            assert subnet_ref.get("id") == ref_subnet["id"]

            sg_ref = att.get("security_groups", att.get("securityGroups", [{}]))[0]
            assert sg_ref.get("name") == ref_security_group["name"]
            assert sg_ref.get("id") == ref_security_group["id"]
        finally:
            grpc.delete_compute_instance(ci_id=ci_id)
            try:
                cr_name = wait_for_cr(k8s=k8s_hub_client, uuid=ci_id)
                wait_for_deletion(k8s=k8s_hub_client, name=cr_name)
            except (subprocess.CalledProcessError, AssertionError, TimeoutError):
                logger.warning("Cleanup wait failed for compute instance %s", ci_id)

    def test_compute_instance_reaches_running_with_name_refs(
        self,
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        ref_subnet: dict[str, str],
        ref_security_group: dict[str, str],
        ref_ci_catalog_item: str,
        ref_instance_type: str,
    ):
        tag = uuid4().hex[:8]
        ci_name = f"ref-ci-run-{tag}"
        cat_item = grpc.get_compute_instance_catalog_item(catalog_item_id=ref_ci_catalog_item)
        cat_item_name = cat_item["object"]["metadata"]["name"]

        response: dict[str, Any] = grpc.call(
            service=f"{PUBLIC_API}.ComputeInstances/Create",
            data=_ci_create_data(
                ci_name, cat_item_name, ref_subnet["name"], ref_security_group["name"], ref_instance_type
            ),
        )
        ci_id = response["object"]["id"]
        cr_name = None
        try:
            cr_name = wait_for_cr(k8s=k8s_hub_client, uuid=ci_id)
            wait_for_provision(k8s=k8s_hub_client, name=cr_name)
            wait_for_running(k8s=k8s_hub_client, name=cr_name)

            phase = k8s_hub_client.get_compute_instance_phase(name=cr_name)
            assert phase == "Running"
        finally:
            grpc.delete_compute_instance(ci_id=ci_id)
            if cr_name:
                try:
                    wait_for_deletion(k8s=k8s_hub_client, name=cr_name)
                except (subprocess.CalledProcessError, AssertionError, TimeoutError):
                    logger.warning("Cleanup wait failed for compute instance %s", ci_id)

    def test_invalid_subnet_name_returns_array_indexed_field_path(
        self, grpc: GRPCClient, ref_subnet: dict[str, str], ref_ci_catalog_item: str, ref_instance_type: str
    ):
        tag = uuid4().hex[:8]
        cat_item = grpc.get_compute_instance_catalog_item(catalog_item_id=ref_ci_catalog_item)
        cat_item_name = cat_item["object"]["metadata"]["name"]

        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.call(
                service=f"{PUBLIC_API}.ComputeInstances/Create",
                data={
                    "object": {
                        "metadata": {"name": f"ref-ci-bad-sg-{tag}"},
                        "spec": {
                            "catalog_item": {"name": cat_item_name},
                            "instance_type": {"name": ref_instance_type},
                            "network_attachments": [
                                {
                                    "subnet": {"name": ref_subnet["name"]},
                                    "security_groups": [{"name": "nonexistent-sg"}],
                                }
                            ],
                        },
                    }
                },
            )
        assert_grpc_field_violation(exc_info, field_path="security_groups")

    def test_cross_tenant_template_reference(
        self,
        jwt_grpc_tenant1: GRPCClient,
        private_grpc: GRPCClient,
        compute_template: str,
        ref_subnet: dict[str, str],
        ref_security_group: dict[str, str],
        ref_instance_type: str,
    ):
        tag = uuid4().hex[:8]
        cat_name = f"ref-xt-cat-{tag}"
        cat_id = private_grpc.create_compute_instance_catalog_item(name=cat_name, template=compute_template)
        ci_id = None
        try:
            response: dict[str, Any] = jwt_grpc_tenant1.call(
                service=f"{PUBLIC_API}.ComputeInstances/Create",
                data=_ci_create_data(
                    f"ref-ci-xt-{tag}", cat_name, ref_subnet["name"], ref_security_group["name"], ref_instance_type
                ),
            )
            ci_id = response["object"]["id"]
            spec = response["object"]["spec"]
            cat_ref = spec.get("catalog_item", spec.get("catalogItem", {}))
            assert cat_ref.get("name") == cat_name
            assert cat_ref.get("id") == cat_id
        finally:
            if ci_id:
                try:
                    jwt_grpc_tenant1.delete_compute_instance(ci_id=ci_id)
                except subprocess.CalledProcessError:
                    logger.warning("Failed to cleanup compute instance %s", ci_id)
            try:
                private_grpc.delete_compute_instance_catalog_item(catalog_item_id=cat_id)
            except subprocess.CalledProcessError:
                logger.warning("Failed to cleanup cross-tenant catalog item %s", cat_id)

    def test_instance_type_deprecation_replacement_resolves(self, private_grpc: GRPCClient):
        tag = uuid4().hex[:8]
        active_name = f"ref-it-active-{tag}"
        deprecated_name = f"ref-it-depr-{tag}"

        active_id = private_grpc.create_instance_type(name=active_name, cores=2, memory_gib=4)
        private_grpc.create_instance_type(name=deprecated_name, cores=2, memory_gib=4)
        try:
            private_grpc.call(
                service=f"{PRIVATE_API}.InstanceTypes/Update",
                data={
                    "object": {
                        "id": deprecated_name,
                        "spec": {
                            "state": "INSTANCE_TYPE_STATE_DEPRECATED",
                            "deprecation": {"replacement": {"name": active_name}},
                        },
                    },
                    "updateMask": {"paths": ["spec.state", "spec.deprecation"]},
                },
            )

            result = private_grpc.get_instance_type(name=deprecated_name)
            deprecation = result["object"]["spec"]["deprecation"]
            replacement = deprecation["replacement"]
            assert replacement.get("name") == active_name
            assert replacement.get("id") == active_id
        finally:
            try:
                private_grpc.delete_instance_type(name=deprecated_name)
            except subprocess.CalledProcessError:
                logger.warning("Failed to cleanup deprecated instance type %s", deprecated_name)
            try:
                private_grpc.delete_instance_type(name=active_name)
            except subprocess.CalledProcessError:
                logger.warning("Failed to cleanup active instance type %s", active_name)
