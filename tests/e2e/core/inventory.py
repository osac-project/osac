"""Inventory backend routing for the BMaaS E2E suites.

Routes every inventory CRUD / state call made by test setup and assertions to
the correct codepath based on ``OSAC_INVENTORY_BACKEND`` (``metal3`` default,
``netbox``), mirroring the operator's inventory backend selection. The same
suites run against any backend; host references are opaque strings (a
``ns/name`` BareMetalHost name on metal3, a NetBox device id on netbox) that
only the owning backend interprets.

NetBox credentials and URL are read from the deployed bare-metal-fulfillment
configuration (the Secrets rendered by the BMF chart in netbox mode); env
vars are debug overrides only. Backend selection itself is env-var-only —
the cluster's inventory ``type`` is deliberately not consulted for routing.
"""

from __future__ import annotations

import base64
import json
import subprocess
from dataclasses import dataclass
from typing import Any, Protocol

import yaml

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import wait_for_bmh_available, wait_for_bmh_provisioned
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
    """Resolved NetBox connection settings plus the shared BMH namespace."""

    url: str
    token: str
    ca_pem: str | None
    allow_insecure_http: bool
    bmh_namespace: str


class InventoryBackend(Protocol):
    """Backend-agnostic inventory operations sized to the BMaaS call sites."""

    def host_for_bmi(self, *, bmi_cr_name: str) -> str:
        """Return the opaque host ref a RUNNING BMI's ExternalHostID points at."""
        ...

    def count_available(self) -> int:
        """Return how many hosts are free for allocation right now."""
        ...

    def wait_for_allocated(self, *, ref: str, expected_image_url: str | None = None) -> None:
        """Wait until the host is allocated to a BMI.

        The image URL check applies to backends where the host carries it
        (metal3 BMH spec.image.url); it is ignored where it is not
        observable (a NetBox device).
        """
        ...

    def consumer_ref(self, *, ref: str) -> str:
        """Return the backend's BMI claim marker on the host ("" when free)."""
        ...

    def wait_for_powered(self, *, ref: str, on: bool, bmi_id: str) -> None:
        """Wait until the host power state matches ``on``."""
        ...

    def hardware_macs(self, *, ref: str) -> set[str]:
        """Return the authoritative lowercased MAC set of the host's NICs."""
        ...

    def wait_for_released(self, *, ref: str) -> None:
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
    """metal3 backend: BareMetalHosts in the management namespace.

    Pure delegation to the existing K8sClient BMH queries and helpers —
    zero behavior change on the default path.
    """

    def __init__(self, *, k8s: K8sClient, bmh_namespace: str) -> None:
        self.k8s = k8s
        self.bmh_namespace = bmh_namespace

    def host_for_bmi(self, *, bmi_cr_name: str) -> str:
        external_host_id: str = self.k8s.get_baremetal_instance_external_host_id(name=bmi_cr_name)
        assert "/" in external_host_id, f"Expected namespace/name format, got: {external_host_id}"
        bmh_ns, bmh_name = external_host_id.split("/", 1)
        assert bmh_ns == self.bmh_namespace, f"BMH landed in {bmh_ns}, expected {self.bmh_namespace}"
        return bmh_name

    def count_available(self) -> int:
        return self.k8s.count_bmhs_by_provisioning_state(
            bmh_namespace=self.bmh_namespace, states={"available", "ready"}
        )

    def wait_for_allocated(self, *, ref: str, expected_image_url: str | None = None) -> None:
        wait_for_bmh_provisioned(k8s=self.k8s, name=ref, bmh_namespace=self.bmh_namespace)

        image_url: str = self.k8s.get_bmh_image_url(name=ref, bmh_namespace=self.bmh_namespace)
        if expected_image_url is not None:
            assert image_url == expected_image_url, (
                f"BMH {ref} image URL {image_url!r} does not match the expected {expected_image_url!r}"
            )
        else:
            assert image_url != "", f"BMH {ref} has no image URL after provisioning"

        consumer_ref: str = self.k8s.get_bmh_consumer_ref(name=ref, bmh_namespace=self.bmh_namespace)
        assert consumer_ref != "", f"BMH {ref} has no consumerRef after allocation"

        online: str = self.k8s.get_bmh_online(name=ref, bmh_namespace=self.bmh_namespace)
        assert online == "true", f"BMH {ref} should be online after provisioning, got: {online}"

    def consumer_ref(self, *, ref: str) -> str:
        return self.k8s.get_bmh_consumer_ref(name=ref, bmh_namespace=self.bmh_namespace)

    def wait_for_powered(self, *, ref: str, on: bool, bmi_id: str) -> None:
        # bmi_id is unused here: metal3 observes power on the BMH itself.
        poll_until(
            fn=lambda: self.k8s.get_bmh_powered_on(name=ref, bmh_namespace=self.bmh_namespace),
            until=lambda v: v == ("true" if on else "false"),
            retries=60,
            delay=5,
            description=f"{ref} powered {'on' if on else 'off'}",
        )

    def hardware_macs(self, *, ref: str) -> set[str]:
        return set(self.k8s.get_bmh_hardware_nics(name=ref, bmh_namespace=self.bmh_namespace))

    def wait_for_released(self, *, ref: str) -> None:
        wait_for_bmh_available(k8s=self.k8s, name=ref, bmh_namespace=self.bmh_namespace)

        image_url: str = self.k8s.get_bmh_image_url(name=ref, bmh_namespace=self.bmh_namespace)
        assert image_url == "", f"BMH {ref} image not cleared after deprovision: {image_url}"

        consumer_ref: str = self.k8s.get_bmh_consumer_ref(name=ref, bmh_namespace=self.bmh_namespace)
        assert consumer_ref == "", f"BMH {ref} consumerRef not cleared after deprovision: {consumer_ref}"

    def ensure_available_hosts(self) -> None:
        available: int = self.count_available()
        assert available >= 1, (
            f"Need at least 1 available BMH in {self.bmh_namespace}; found {available} "
            "(metal3 inventory is provisioned out-of-band by the infra backend)"
        )

    def release_created_hosts(self) -> None:
        # metal3 hosts are infra-provided; the tests never create them.
        return

    def close(self) -> None:
        return


class NetBoxInventory:
    """netbox backend: OSAC-managed dcim.device records.

    Device state lives in NetBox (custom fields + status); BMI-level facts
    (state, conditions, status.hardware.nics) come from the existing gRPC
    client, which is already backend-agnostic. The operator's BMH naming is
    not stable API surface, so the netbox path never resolves BMHs.
    """

    def __init__(
        self,
        *,
        k8s: K8sClient,
        netbox: NetBoxClient,
        grpc: GRPCClient,
        fixture_spec: list[dict[str, Any]],
        name_prefix: str,
    ) -> None:
        self.k8s = k8s
        self.netbox = netbox
        self.grpc = grpc
        self.fixture_spec = fixture_spec
        self.name_prefix = name_prefix
        self._created_device_ids: list[str] = []

    def host_for_bmi(self, *, bmi_cr_name: str) -> str:
        # The persisted ExternalHostID is the NetBox device id on this backend.
        device_id: str = self.k8s.get_baremetal_instance_external_host_id(name=bmi_cr_name)
        assert device_id != "", f"BareMetalInstance {bmi_cr_name} has no externalHostID"
        assert device_id.isdigit(), f"Expected a NetBox device id, got: {device_id}"
        return device_id

    def count_available(self) -> int:
        # Free-device query convention: managed, staged, and unclaimed.
        return len(
            self.netbox.list_devices(
                status="staged", custom_fields={"osac_managed": True, "osac_instance_id__empty": True}
            )
        )

    def wait_for_allocated(self, *, ref: str, expected_image_url: str | None = None) -> None:
        # expected_image_url is not observable on a NetBox device; image
        # selection is already covered by the backend-agnostic BMI surface.
        def _check() -> bool:
            device: dict[str, Any] = self.netbox.get_device(ref)
            instance_id = device.get("custom_fields", {}).get("osac_instance_id") or ""
            return self.netbox.get_device_status(ref) == "active" and instance_id != ""

        poll_until(
            fn=_check,
            until=lambda v: v is True,
            retries=120,
            delay=10,
            description=f"NetBox device {ref} allocated (active, claimed)",
        )

    def consumer_ref(self, *, ref: str) -> str:
        device: dict[str, Any] = self.netbox.get_device(ref)
        return str(device.get("custom_fields", {}).get("osac_instance_id") or "")

    def wait_for_powered(self, *, ref: str, on: bool, bmi_id: str) -> None:
        # Power is not a NetBox device field; observe it through the BMI API.
        expected = "BARE_METAL_INSTANCE_STATE_RUNNING" if on else "BARE_METAL_INSTANCE_STATE_STOPPED"
        poll_until(
            fn=lambda: self.grpc.get_baremetal_instance_state(bmi_id=bmi_id),
            until=lambda v: v == expected,
            retries=60,
            delay=5,
            description=f"{bmi_id} powered {'on' if on else 'off'} (BMI state)",
        )

    def hardware_macs(self, *, ref: str) -> set[str]:
        device: dict[str, Any] = self.netbox.get_device(ref)
        mapping = device.get("custom_fields", {}).get("osac_interface_macs") or {}
        if isinstance(mapping, str):
            mapping = json.loads(mapping)
        return {str(mac).lower() for mac in mapping.values()}

    def wait_for_released(self, *, ref: str) -> None:
        def _check() -> bool:
            device: dict[str, Any] = self.netbox.get_device(ref)
            instance_id = device.get("custom_fields", {}).get("osac_instance_id") or ""
            return self.netbox.get_device_status(ref) == "staged" and instance_id == ""

        poll_until(
            fn=_check,
            until=lambda v: v is True,
            retries=120,
            delay=10,
            description=f"NetBox device {ref} released (staged, unclaimed)",
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
        role: str = env("OSAC_NETBOX_ROLE", "server")
        for index, spec in enumerate(self.fixture_spec, start=1):
            name = f"{self.name_prefix}-netbox-{index}"
            if self.netbox.list_devices(name=name):
                continue
            device_id = self.netbox.create_device(
                name=name, role=role, site=site, custom_fields=self._fixture_custom_fields(spec)
            )
            self._created_device_ids.append(device_id)

    def _fixture_custom_fields(self, spec: dict[str, Any]) -> dict[str, Any]:
        custom_fields: dict[str, Any] = {
            "osac_managed": True,
            "osac_bmc_address": spec["bmc_address"],
            "osac_boot_mac": spec["boot_mac"],
        }
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
    """Resolve NetBox settings from the deployed BMF configuration.

    Primary source is the BMF release's Secrets in the OSAC namespace; the
    OSAC_NETBOX_* env vars are local-debugging / external-NetBox overrides
    that win when set. The token is never logged.
    """
    inventory = _required_yaml_secret(
        k8s, namespace=namespace, name=_INVENTORY_CONFIG_SECRET, key=_INVENTORY_CONFIG_KEY
    )
    options = inventory.get("options", {}).get("netbox", {})
    url: str = options.get("url", "")
    allow_insecure_http = bool(options.get("allowInsecureHTTP", False))

    token = _required_secret_value(k8s, namespace=namespace, name=_NETBOX_TOKEN_SECRET, key=_NETBOX_TOKEN_KEY)

    # The CA Secret is optional: absent means default TLS verification.
    ca_data = _optional_secret_data(k8s, namespace=namespace, name=_NETBOX_CA_SECRET)
    ca_pem: str | None = ca_data.get(_NETBOX_CA_KEY)

    management = _required_yaml_secret(
        k8s, namespace=namespace, name=_MANAGEMENT_CONFIG_SECRET, key=_MANAGEMENT_CONFIG_KEY
    )
    bmh_namespace: str = management.get("options", {}).get("metal3", {}).get("namespace", "")

    url = env("OSAC_NETBOX_URL", "").strip() or url
    token = env("OSAC_NETBOX_TOKEN", "").strip() or token
    ca_file = env("OSAC_NETBOX_CA_FILE", "").strip()
    if ca_file:
        with open(ca_file) as handle:
            ca_pem = handle.read()
    if env("OSAC_NETBOX_INSECURE", "").strip().lower() in ("1", "true", "yes"):
        allow_insecure_http = True

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
    if not bmh_namespace:
        raise RuntimeError(
            f"BMH namespace is empty: read options.metal3.namespace from Secret "
            f"{_MANAGEMENT_CONFIG_SECRET} (key {_MANAGEMENT_CONFIG_KEY}) in namespace {namespace}"
        )
    return NetBoxSettings(
        url=url, token=token, ca_pem=ca_pem, allow_insecure_http=allow_insecure_http, bmh_namespace=bmh_namespace
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


def make_inventory_backend(
    *, k8s: K8sClient, namespace: str, grpc: GRPCClient, bmh_namespace: str, test_run_id: str
) -> InventoryBackend:
    """Build the inventory backend selected by OSAC_INVENTORY_BACKEND."""
    backend = env("OSAC_INVENTORY_BACKEND", "metal3")
    if backend == "metal3":
        return Metal3Inventory(k8s=k8s, bmh_namespace=bmh_namespace)
    if backend == "netbox":
        settings = netbox_settings_from_bmf(k8s=k8s, namespace=namespace)
        netbox = NetBoxClient(
            url=settings.url, token=settings.token, ca_pem=settings.ca_pem, insecure=settings.allow_insecure_http
        )
        return NetBoxInventory(
            k8s=k8s, netbox=netbox, grpc=grpc, fixture_spec=load_netbox_fixture_spec(), name_prefix=test_run_id
        )
    raise RuntimeError(
        f"Unknown OSAC_INVENTORY_BACKEND {backend!r}; valid values: {', '.join(VALID_INVENTORY_BACKENDS)}"
    )
