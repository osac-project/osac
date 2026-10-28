"""Inventory backend routing for the BMaaS E2E suites.

Two layers are kept apart:

- **Inventory** (this module's backend abstraction, selected by
  ``OSAC_INVENTORY_BACKEND`` — ``metal3`` default, ``netbox``): finding,
  counting, claiming, and releasing hosts. metal3's inventory *is* the set of
  BareMetalHosts; netbox's is the pool of ``osac_managed`` dcim.device
  records.
- **Provisioning** (shared, backend-agnostic): once the inventory allocates a
  host, the operator hands over to BMH/metal3 in every mode — the BMI's
  ``spec.externalHostID`` is the namespace-qualified BMH name (on netbox the
  operator derives it from the device id as ``netbox-<id>`` and creates that
  BMH). All provisioning assertions (provisioned state, image URL,
  consumerRef, online, power, inspected NICs) therefore run against the BMH
  identically on both backends, through the ``wait_for_host_*`` /
  ``host_*`` helpers below.

NetBox credentials and URL are read from the deployed bare-metal-fulfillment
configuration (the Secrets rendered by the BMF chart in netbox mode); env
vars are debug overrides only. Backend selection itself is env-var-only —
the cluster's inventory ``type`` is deliberately not consulted for routing.
"""

from __future__ import annotations

import base64
import contextlib
import json
import subprocess
from dataclasses import dataclass
from typing import Any, Protocol

import yaml

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    DEFAULT_BMI_HOST_SELECTOR,
    HostRef,
    wait_for_bmh_available,
    wait_for_bmi_cr,
    wait_for_host_allocated,
    wait_for_host_powered,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.netbox_client import NetBoxClient
from tests.e2e.core.runner import env, poll_until

VALID_INVENTORY_BACKENDS = ("metal3", "netbox")

# Secret names are the BMF chart's defaults (secrets.inventoryConfig,
# secrets.netboxToken, secrets.netboxCA, secrets.managementConfig) — the
# deployed contract the tests read. kind's `*-metal3-test` repointing is a
# component-test-only concern e2e never runs against.
_INVENTORY_CONFIG_SECRET = "osac-inventory-config"
_INVENTORY_CONFIG_KEY = "inventory.yaml"
_NETBOX_TOKEN_SECRET = "osac-netbox-token"
_NETBOX_TOKEN_KEY = "token"
_NETBOX_CA_SECRET = "osac-netbox-ca"
_NETBOX_CA_KEY = "ca.crt"
_MANAGEMENT_CONFIG_SECRET = "osac-management-config"
_MANAGEMENT_CONFIG_KEY = "management.yaml"


@dataclass(frozen=True)
class NetBoxSettings:
    """Resolved NetBox connection settings plus the deployed BMH namespace."""

    url: str
    token: str
    ca_pem: str | None
    # OSAC_NETBOX_INSECURE only: the deployed allowInsecureHTTP setting is
    # http://-scheme permission, not a TLS-verification switch (the operator's
    # netboxclient keeps certificate verification enabled either way).
    skip_tls_verify: bool
    # Best-effort from osac-management-config: the namespace the operator
    # places BMHs in (valid in metal3 and netbox mode). Empty when the Secret
    # is absent — used only to cross-check externalHostID, never required.
    bmh_namespace: str


# Inventory backends ---------------------------------------------------------------


class InventoryBackend(Protocol):
    """Inventory-layer operations; provisioning is BMH/metal3 in every mode.

    Once the inventory allocates a host, ``host_for_bmi`` returns the BMH the
    operator handed over to, and every provisioning assertion runs against
    that BMH through the shared ``wait_for_host_*`` / ``host_*`` helpers.
    """

    def host_for_bmi(self, *, bmi_cr_name: str) -> HostRef:
        """Wait until the inventory assigned a host; return the BMH ref."""
        ...

    def count_available(self) -> int:
        """Return how many hosts are free for allocation right now."""
        ...

    def default_host_selector(self) -> dict[str, str]:
        """Return the host selector for the suite's auto-created instance type.

        metal3: keys match BMH labels (osac.openshift.io/host-type). netbox:
        keys must be valid device custom-field names, since the operator
        queries them as cf_<key>.
        """
        ...

    def wait_for_released(self, *, host: HostRef) -> None:
        """Wait until the host is free again after BMI deprovisioning."""
        ...

    def ensure_available_hosts(self) -> None:
        """Make at least one host available for provisioning (idempotent)."""
        ...

    def release_created_hosts(self) -> None:
        """Tear down hosts this backend created in ensure_available_hosts."""
        ...

    def close(self) -> None:
        """Release client-side resources (temp CA bundles)."""
        ...


class Metal3Inventory:
    """metal3 backend: the BareMetalHosts themselves are the inventory."""

    def __init__(self, *, k8s: K8sClient, bmh_namespace: str) -> None:
        self.k8s = k8s
        self.bmh_namespace = bmh_namespace

    def host_for_bmi(self, *, bmi_cr_name: str) -> HostRef:
        external_host_id: str = self.k8s.get_baremetal_instance_external_host_id(name=bmi_cr_name)
        assert "/" in external_host_id, f"Expected namespace/name format, got: {external_host_id}"
        bmh_ns, bmh_name = external_host_id.split("/", 1)
        assert bmh_ns == self.bmh_namespace, f"BMH landed in {bmh_ns}, expected {self.bmh_namespace}"
        return HostRef(namespace=bmh_ns, name=bmh_name)

    def count_available(self) -> int:
        return self.k8s.count_bmhs_by_provisioning_state(
            bmh_namespace=self.bmh_namespace, states={"available", "ready"}
        )

    def default_host_selector(self) -> dict[str, str]:
        # Matches the osac.openshift.io/host-type label the infra backend
        # stamps on the enrolled BMHs.
        return dict(DEFAULT_BMI_HOST_SELECTOR)

    def wait_for_released(self, *, host: HostRef) -> None:
        wait_for_bmh_available(k8s=self.k8s, name=host.name, bmh_namespace=host.namespace)

        image_url: str = self.k8s.get_bmh_image_url(name=host.name, bmh_namespace=host.namespace)
        assert image_url == "", f"BMH {host.name} image not cleared after deprovision: {image_url}"

        consumer_ref: str = self.k8s.get_bmh_consumer_ref(name=host.name, bmh_namespace=host.namespace)
        assert consumer_ref == "", f"BMH {host.name} consumerRef not cleared after deprovision: {consumer_ref}"

    def ensure_available_hosts(self) -> None:
        available: int = self.count_available()
        assert available >= 1, (
            f"Need at least 1 available BMH in {self.bmh_namespace}; found {available} "
            "(metal3 hosts are enrolled by the infra backend or an admin, not by the tests)"
        )

    def release_created_hosts(self) -> None:
        # metal3 hosts are infra-provided; the tests never create them.
        return

    def close(self) -> None:
        return


class NetBoxInventory:
    """netbox backend: OSAC-managed dcim.device records are the inventory.

    Device claim state (status ``active`` + ``osac_instance_id``) is
    netbox-specific; provisioning hands over to the BMH the operator derives
    from the device id (``netbox-<id>``, recorded in the BMI's
    externalHostID), so all provisioning assertions use the shared BMH
    helpers. On release the operator deletes that BMH and restores the device
    to ``staged``.
    """

    def __init__(
        self,
        *,
        k8s: K8sClient,
        netbox: NetBoxClient,
        fixture_spec: list[dict[str, Any]],
        name_prefix: str,
        bmh_namespace: str = "",
    ) -> None:
        self.k8s = k8s
        self.netbox = netbox
        self.fixture_spec = fixture_spec
        self.name_prefix = name_prefix
        self.bmh_namespace = bmh_namespace
        self._created_device_ids: list[str] = []

    def host_for_bmi(self, *, bmi_cr_name: str) -> HostRef:
        external_host_id: str = self.k8s.get_baremetal_instance_external_host_id(name=bmi_cr_name)
        assert "/" in external_host_id, f"Expected namespace/name format, got: {external_host_id}"
        bmh_ns, bmh_name = external_host_id.split("/", 1)
        assert bmh_name.startswith("netbox-"), (
            f"Expected a NetBox-derived BMH name (netbox-<device-id>), got: {bmh_name}"
        )
        device_id = bmh_name.removeprefix("netbox-")
        assert device_id.isdigit(), f"Expected a NetBox device id in the BMH name, got: {bmh_name}"
        if self.bmh_namespace:
            assert bmh_ns == self.bmh_namespace, (
                f"BMH landed in {bmh_ns}, expected {self.bmh_namespace} (from {_MANAGEMENT_CONFIG_SECRET})"
            )

        # The inventory claim: the device must be active and held before the
        # operator proceeds with BMH provisioning.
        def _claimed() -> bool:
            device: dict[str, Any] = self.netbox.get_device(device_id)
            instance_id = device.get("custom_fields", {}).get("osac_instance_id") or ""
            return self.netbox.get_device_status(device_id) == "active" and instance_id != ""

        poll_until(
            fn=_claimed,
            until=lambda v: v is True,
            retries=120,
            delay=10,
            description=f"NetBox device {device_id} claimed (active, held)",
        )
        return HostRef(namespace=bmh_ns, name=bmh_name)

    def count_available(self) -> int:
        # Free-device query convention: managed, staged, and unclaimed.
        return len(
            self.netbox.list_devices(
                status="staged", custom_fields={"osac_managed": True, "osac_instance_id__empty": True}
            )
        )

    def default_host_selector(self) -> dict[str, str]:
        # The netbox equivalent of the BMH host-type label: a capability
        # custom field the operator queries as cf_host_type. Fixture devices
        # are stamped with it (see _fixture_custom_fields) the same way the
        # infra backend labels BMHs for metal3.
        return {"host_type": "default"}

    def wait_for_released(self, *, host: HostRef) -> None:
        device_id = host.name.removeprefix("netbox-")

        def _check() -> bool:
            bmh_gone = not self.k8s.is_bmh_present(name=host.name, bmh_namespace=host.namespace)
            device: dict[str, Any] = self.netbox.get_device(device_id)
            instance_id = device.get("custom_fields", {}).get("osac_instance_id") or ""
            return bmh_gone and self.netbox.get_device_status(device_id) == "staged" and instance_id == ""

        poll_until(
            fn=_check,
            until=lambda v: v is True,
            retries=120,
            delay=10,
            description=f"NetBox device {device_id} released (BMH deleted, device staged)",
        )

    def ensure_available_hosts(self) -> None:
        if not self.fixture_spec:
            raise RuntimeError(
                "OSAC_NETBOX_FIXTURE_HOSTS is empty: the netbox backend provisions its "
                "inventory through fixture devices and needs at least one host spec"
            )
        site: str = env("OSAC_NETBOX_SITE", "").strip()
        if not site:
            raise RuntimeError("OSAC_NETBOX_SITE must name the NetBox site slug for fixture devices")
        device_type: str = env("OSAC_NETBOX_DEVICE_TYPE", "").strip()
        if not device_type:
            raise RuntimeError("OSAC_NETBOX_DEVICE_TYPE must name the NetBox device type slug for fixture devices")
        role: str = env("OSAC_NETBOX_ROLE", "server")
        try:
            for index, spec in enumerate(self.fixture_spec, start=1):
                name = f"{self.name_prefix}-netbox-{index}"
                if self.netbox.list_devices(name=name):
                    continue
                device_id = self.netbox.create_device(
                    name=name,
                    role=role,
                    site=site,
                    device_type=device_type,
                    custom_fields=self._fixture_custom_fields(spec),
                )
                self._created_device_ids.append(device_id)
        except BaseException:
            # Fixture setup failed partway: the yield-based pytest fixture
            # never reaches teardown, so clean up here before re-raising.
            with contextlib.suppress(Exception):
                self.release_created_hosts()
            raise

    def _fixture_custom_fields(self, spec: dict[str, Any]) -> dict[str, Any]:
        custom_fields: dict[str, Any] = {
            "osac_managed": True,
            "osac_bmc_address": spec["bmc_address"],
            "osac_boot_mac": spec["boot_mac"],
        }
        # Capability fields matching the instance type's host selector — the
        # netbox equivalent of the BMH labels the infra backend stamps.
        custom_fields.update(self.default_host_selector())
        interface_macs = spec.get("interface_macs")
        if interface_macs:
            custom_fields["osac_interface_macs"] = interface_macs
        custom_fields.update(spec.get("custom_fields") or {})
        return custom_fields

    def release_created_hosts(self) -> None:
        errors: list[Exception] = []
        for device_id in self._created_device_ids:
            try:
                self.netbox.delete_device(device_id)
            except Exception as exc:
                errors.append(exc)
        self._created_device_ids = []
        if errors:
            raise RuntimeError(f"{len(errors)} NetBox fixture device deletion failure(s)") from errors[0]

    def close(self) -> None:
        self.netbox.close()


def assert_bmi_lifecycle_on_running(
    *,
    grpc: GRPCClient,
    k8s: K8sClient,
    inventory: InventoryBackend,
    bmi_id: str,
    expected_image_url: str | None = None,
    power_cycle: bool = True,
) -> tuple[str, HostRef]:
    """Assert host allocation and power cycling on an already-RUNNING BMI.

    Does not create or delete the instance. Inventory exhaust calls this after all
    claim BMIs reach RUNNING so provisioning is not blocked by interleaved checks.

    Returns (bmi_cr_name, host): the inventory hands over to a BMH in every
    mode, so the provisioning assertions run against it identically.
    """
    assert bmi_id in grpc.list_baremetal_instance_ids()
    bmi_cr_name: str = wait_for_bmi_cr(k8s=k8s, uuid=bmi_id)

    host: HostRef = inventory.host_for_bmi(bmi_cr_name=bmi_cr_name)

    wait_for_host_allocated(k8s=k8s, host=host, expected_image_url=expected_image_url)

    if power_cycle:
        grpc.update_baremetal_instance_run_strategy(
            bmi_id=bmi_id, run_strategy="BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED"
        )
        wait_for_host_powered(k8s=k8s, host=host, on=False)
        grpc.update_baremetal_instance_run_strategy(
            bmi_id=bmi_id, run_strategy="BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS"
        )
        wait_for_host_powered(k8s=k8s, host=host, on=True)

    return bmi_cr_name, host


def _secret_data(k8s: K8sClient, *, namespace: str, name: str) -> dict[str, str]:
    secret: dict[str, Any] = k8s.get_json(resource="secret", name=name, namespace=namespace)
    return {key: base64.b64decode(value).decode() for key, value in (secret.get("data") or {}).items()}


def _optional_secret_data(k8s: K8sClient, *, namespace: str, name: str) -> dict[str, str]:
    try:
        return _secret_data(k8s, namespace=namespace, name=name)
    except subprocess.CalledProcessError as exc:
        output = (exc.stderr or "") + (exc.stdout or "")
        if "NotFound" in output:
            return {}
        raise


def _required_secret_value(k8s: K8sClient, *, namespace: str, name: str, key: str) -> str:
    try:
        data = _secret_data(k8s, namespace=namespace, name=name)
    except subprocess.CalledProcessError as exc:
        # Suppress the subprocess exception: its command and output may contain
        # secrets. The last kubectl stderr line (e.g. the NotFound message) is
        # safe detail and names the resource.
        detail = (exc.stderr or "").strip().splitlines()[-1] if (exc.stderr or "").strip() else f"exit {exc.returncode}"
        raise RuntimeError(f"Secret {name} in namespace {namespace} is not readable: {detail}") from None
    if key not in data:
        raise RuntimeError(f"Secret {name} in namespace {namespace} has no key {key!r}")
    return data[key]


def _required_yaml_secret(k8s: K8sClient, *, namespace: str, name: str, key: str) -> dict[str, Any]:
    raw = _required_secret_value(k8s, namespace=namespace, name=name, key=key)
    parsed = yaml.safe_load(raw)
    if not isinstance(parsed, dict):
        raise RuntimeError(f"Secret {name} key {key!r} in namespace {namespace} is not a YAML mapping")
    return parsed


def netbox_settings_from_bmf(*, k8s: K8sClient, namespace: str) -> NetBoxSettings:
    """Resolve NetBox settings from env overrides and the deployed BMF config.

    The OSAC_NETBOX_* env vars are local-debugging / external-NetBox overrides
    that win when set. The URL and token Secrets are read only when their
    overrides are unset, and the CA Secret applies only when the URL comes
    from the deployed config — an override URL uses system roots unless
    OSAC_NETBOX_CA_FILE is set, so the deployed NetBox's private CA never
    leaks into an external connection. The osac-management-config cross-check
    is cluster-scoped and read best-effort in every mode. The token is never
    logged.
    """
    url: str = env("OSAC_NETBOX_URL", "").strip()
    url_from_secret = not url
    token: str = env("OSAC_NETBOX_TOKEN", "").strip()
    ca_file: str = env("OSAC_NETBOX_CA_FILE", "").strip()
    skip_tls_verify = env("OSAC_NETBOX_INSECURE", "").strip().lower() in ("1", "true", "yes")

    if not url:
        inventory = _required_yaml_secret(
            k8s, namespace=namespace, name=_INVENTORY_CONFIG_SECRET, key=_INVENTORY_CONFIG_KEY
        )
        options = inventory.get("options", {}).get("netbox", {})
        url = options.get("url", "")
        # Mirror the deployed transport contract: plain HTTP needs the
        # config's explicit opt-in (the BMF chart and the operator's
        # netboxclient enforce the same). Env-override URLs are the
        # tester's explicit choice and are not constrained here.
        if url.startswith("http://") and not bool(options.get("allowInsecureHTTP", False)):
            raise RuntimeError(
                f"NetBox URL {url!r} uses http:// but Secret {_INVENTORY_CONFIG_SECRET} "
                f"(key {_INVENTORY_CONFIG_KEY}) does not set allowInsecureHTTP"
            )
    if not token:
        token = _required_secret_value(k8s, namespace=namespace, name=_NETBOX_TOKEN_SECRET, key=_NETBOX_TOKEN_KEY)

    ca_pem: str | None = None
    if ca_file:
        with open(ca_file) as handle:
            ca_pem = handle.read()
    elif url_from_secret:
        # The CA Secret belongs to the deployed NetBox (paired with the URL
        # from its inventory config); an override URL uses system roots.
        # Absent means default TLS verification.
        ca_data = _optional_secret_data(k8s, namespace=namespace, name=_NETBOX_CA_SECRET)
        ca_pem = ca_data.get(_NETBOX_CA_KEY)

    # Best-effort cross-check source: the namespace the operator places BMHs
    # in (options.metal3.namespace, valid in metal3 and netbox mode). Absent
    # just skips the externalHostID namespace check.
    bmh_namespace = ""
    management_data = _optional_secret_data(k8s, namespace=namespace, name=_MANAGEMENT_CONFIG_SECRET)
    management_raw = management_data.get(_MANAGEMENT_CONFIG_KEY)
    if management_raw:
        management = yaml.safe_load(management_raw)
        if isinstance(management, dict):
            bmh_namespace = management.get("options", {}).get("metal3", {}).get("namespace", "")

    if not url:
        raise RuntimeError(
            f"NetBox URL is empty: read options.netbox.url from Secret {_INVENTORY_CONFIG_SECRET} "
            f"(key {_INVENTORY_CONFIG_KEY}) in namespace {namespace}, or set OSAC_NETBOX_URL"
        )
    if not token:
        raise RuntimeError(
            f"NetBox token is empty: read Secret {_NETBOX_TOKEN_SECRET} (key {_NETBOX_TOKEN_KEY}) "
            f"in namespace {namespace}, or set OSAC_NETBOX_TOKEN"
        )
    return NetBoxSettings(
        url=url, token=token, ca_pem=ca_pem, skip_tls_verify=skip_tls_verify, bmh_namespace=bmh_namespace
    )


def load_netbox_fixture_spec() -> list[dict[str, Any]]:
    """Parse OSAC_NETBOX_FIXTURE_HOSTS: a JSON list of virtual host specs.

    Each entry is ``{"bmc_address": ..., "boot_mac": ..., "interface_macs"?: {...},
    "custom_fields"?: {...}}`` — one per lab host backed by the sushy/redfish
    emulator; ``custom_fields`` carries capability fields matching the instance
    type's host selector.
    """
    raw = env("OSAC_NETBOX_FIXTURE_HOSTS", "").strip()
    if not raw:
        return []
    try:
        spec = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"OSAC_NETBOX_FIXTURE_HOSTS is not valid JSON: {exc}") from exc
    if not isinstance(spec, list) or not all(isinstance(item, dict) for item in spec):
        raise RuntimeError("OSAC_NETBOX_FIXTURE_HOSTS must be a JSON list of host objects")
    for index, item in enumerate(spec, start=1):
        for key in ("bmc_address", "boot_mac"):
            if not item.get(key):
                raise RuntimeError(f"OSAC_NETBOX_FIXTURE_HOSTS entry {index} is missing {key!r}")
    return spec


def make_inventory_backend(*, k8s: K8sClient, namespace: str, bmh_namespace: str, test_run_id: str) -> InventoryBackend:
    """Build the inventory backend selected by OSAC_INVENTORY_BACKEND."""
    backend = env("OSAC_INVENTORY_BACKEND", "metal3")
    if backend == "metal3":
        return Metal3Inventory(k8s=k8s, bmh_namespace=bmh_namespace)
    if backend == "netbox":
        settings = netbox_settings_from_bmf(k8s=k8s, namespace=namespace)
        netbox = NetBoxClient(
            url=settings.url, token=settings.token, ca_pem=settings.ca_pem, insecure=settings.skip_tls_verify
        )
        return NetBoxInventory(
            k8s=k8s,
            netbox=netbox,
            fixture_spec=load_netbox_fixture_spec(),
            name_prefix=test_run_id,
            bmh_namespace=settings.bmh_namespace,
        )
    raise RuntimeError(
        f"Unknown OSAC_INVENTORY_BACKEND {backend!r}; valid values: {', '.join(VALID_INVENTORY_BACKENDS)}"
    )
