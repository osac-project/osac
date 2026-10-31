"""API-owned cleanup for the storage scenarios' disposable tenants."""

from __future__ import annotations

import re
import subprocess

from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.helpers import wait_for_tenant_deletion
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import poll_until

_NOT_FOUND = re.compile(r"(?m)^\s*Code:\s*NotFound\s*$")


def delete_storage_tenant(*, grpc: GRPCClient, k8s: K8sClient, tenant_name: str) -> None:
    """Wait for API-owned defaults and namespace cleanup, not just CR removal."""
    try:
        tenant_id = grpc.find_tenant_id(name=tenant_name)
        if tenant_id:
            grpc.call(service=f"{PRIVATE_API}.Tenants/Delete", data={"id": tenant_id})
            wait_for_tenant_deletion(k8s=k8s, name=tenant_name)

            # Storage cleanup can outlive the operator's deletion feedback.
            # Wake onboarding after the CR is gone, as API integration cleanup
            # does; this test does not cover autonomous deletion feedback.
            output, rc = grpc.call_unchecked(service=f"{PRIVATE_API}.Tenants/Signal", data={"id": tenant_id})
            if rc != 0 and not _NOT_FOUND.search(output):
                raise RuntimeError(f"Tenant {tenant_name} cleanup signal failed (exit {rc})")

            def api_absent() -> bool:
                output, rc = grpc.call_unchecked(service=f"{PRIVATE_API}.Tenants/Get", data={"id": tenant_id})
                if rc == 0:
                    return False
                if _NOT_FOUND.search(output):
                    return True
                raise RuntimeError(f"Tenant {tenant_name} cleanup lookup failed (exit {rc})")

            poll_until(
                fn=api_absent,
                until=lambda gone: gone,
                retries=120,
                delay=5,
                description=f"API tenant {tenant_name} deletion",
            )
        poll_until(
            fn=lambda: k8s.is_absent(resource="namespaces", name=tenant_name),
            until=lambda gone: gone,
            retries=120,
            delay=5,
            description=f"Tenant namespace {tenant_name} deletion",
        )
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
        # grpcurl command arguments include credentials; do not expose them.
        raise RuntimeError(f"Tenant {tenant_name} cleanup request failed") from None
