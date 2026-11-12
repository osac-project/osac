"""Reserve and release CI guest ingress VIPs in a hub ConfigMap registry.

The CI provider owns one IPv4 range that is reachable from guest workers, the
hub, and the test runner. Every CaaS cluster needs exactly one address from that
range for its guest MetalLB pool, so the claim must be atomic across concurrent
ClusterOrders. The registry is a single ConfigMap on the hub, keyed by
ClusterOrder UID; writes carry the read ``resourceVersion`` so a losing writer
gets a conflict instead of overwriting another cluster's claim.

A claim is retained until the owning flow releases it, so a failed install or a
failed cleanup never hands a still-advertised address to another cluster.
"""

from __future__ import annotations

import ipaddress
import re
import time

CLAIM_LABEL = "osac.openshift.io/guest-ingress-vip-claim"
DEFAULT_REGISTRY_NAME = "osac-ci-guest-ingress-vip-claims"

MAX_ATTEMPTS = 5
RETRY_DELAY_SECONDS = 1.0

_RANGE = re.compile(r"^(\d{1,3}(?:\.\d{1,3}){3})-(\d{1,3}(?:\.\d{1,3}){3})$")


class ClaimError(ValueError):
    """The reservation could not be completed or verified."""

    category = "claim_error"


class InvalidRangeError(ClaimError):
    """The configured range is not a usable IPv4 host range."""

    category = "invalid_range"


class ClaimConflictError(ClaimError):
    """Another writer changed the registry first; the operation is retryable."""

    category = "conflict"


class ClaimExhaustedError(ClaimError):
    """No address in the configured range is available."""

    category = "exhausted"


class ForeignClaimError(ClaimError):
    """The registry records a different owner for this allocation."""

    category = "foreign_claim"


def parse_range(spec: str) -> list[str]:
    """Return the host addresses of a ``first-last`` IPv4 range in order."""
    if not isinstance(spec, str):
        raise InvalidRangeError("the guest ingress VIP range must be a string")
    match = _RANGE.match(spec.strip())
    if match is None:
        raise InvalidRangeError(
            f"{spec!r} is not an IPv4 range of the form <first>-<last>"
        )
    try:
        first = ipaddress.IPv4Address(match.group(1))
        last = ipaddress.IPv4Address(match.group(2))
    except ValueError as error:
        raise InvalidRangeError(f"{spec!r} is not a valid IPv4 range") from error
    if first > last:
        raise InvalidRangeError(f"{spec!r} starts after it ends")
    if first.packed[:3] != last.packed[:3]:
        raise InvalidRangeError(
            f"{spec!r} spans more than one /24; CI guests share one L2 network"
        )
    return [str(first + offset) for offset in range(int(last) - int(first) + 1)]


def _validated_owner(owner: dict) -> dict:
    """Return the owner identity, or raise ValueError for incomplete input."""
    if not isinstance(owner, dict):
        raise ClaimError("the claim owner must be a mapping")
    validated = {}
    for key in ("uid", "namespace", "name"):
        value = owner.get(key)
        if not isinstance(value, str) or not value.strip():
            raise ClaimError(f"the claim owner requires a non-empty {key}")
        validated[key] = value.strip()
    return validated


def _parse_record(raw: str, uid: str) -> tuple[str, str]:
    """Split a registry record into its address and ``namespace/name`` owner."""
    if not isinstance(raw, str):
        raise ClaimError(f"registry record for {uid} is not a string")
    parts = raw.split()
    if len(parts) != 2 or "/" not in parts[1]:
        raise ClaimError(
            f"registry record for {uid} is malformed; expected '<address> <namespace>/<name>'"
        )
    try:
        address = str(ipaddress.IPv4Address(parts[0]))
    except ValueError as error:
        raise ClaimError(f"registry record for {uid} has an invalid address") from error
    return address, parts[1]


def _owner_name(owner: dict) -> str:
    return f"{owner['namespace']}/{owner['name']}"


def _read(store, spec: list[str]):
    """Read and validate the registry, returning claims and resourceVersion."""
    data, resource_version = store.read()
    claims = {}
    for uid, raw in (data or {}).items():
        address, name = _parse_record(raw, uid)
        if str(ipaddress.IPv4Address(address)) not in spec:
            raise ClaimError(
                f"registry record for {uid} ({address}) is outside the configured range"
            )
        claims[uid] = {"address": address, "owner": name}
    return claims, resource_version


def _record(claims: dict) -> dict:
    return {uid: f"{claim['address']} {claim['owner']}" for uid, claim in claims.items()}


def _write(store, claims: dict, resource_version) -> None:
    store.write(_record(claims), resource_version)


def claim_address(
    ip_range: str,
    owner: dict,
    store,
    max_attempts: int = MAX_ATTEMPTS,
    retry_delay: float = RETRY_DELAY_SECONDS,
) -> tuple[str, bool]:
    """Return the address reserved for ``owner``, allocating one if needed.

    Retries of the same ClusterOrder return the same address without writing.
    Claims remain reserved until the teardown flow explicitly releases them.
    """
    spec = parse_range(ip_range)
    wanted = _validated_owner(owner)
    if max_attempts < 1:
        raise ClaimError("max_attempts must be at least 1")

    for attempt in range(max_attempts):
        claims, resource_version = _read(store, spec)
        current = claims.get(wanted["uid"])
        if current is not None:
            if current["owner"] != _owner_name(wanted):
                raise ForeignClaimError(
                    f"{wanted['uid']} is already reserved by {current['owner']}, "
                    f"not by {_owner_name(wanted)}"
                )
            return current["address"], False

        used = {claim["address"] for claim in claims.values()}
        free = [address for address in spec if address not in used]
        if not free:
            raise ClaimExhaustedError(
                f"no free guest ingress VIP in {ip_range}: {len(claims)} addresses "
                f"are reserved for ClusterOrders {', '.join(sorted(claims)) or 'none'}"
            )

        claims[wanted["uid"]] = {"address": free[0], "owner": _owner_name(wanted)}
        try:
            _write(store, claims, resource_version)
        except ClaimConflictError:
            _back_off(attempt, max_attempts, retry_delay)
            continue
        return free[0], True

    raise ClaimConflictError(
        f"could not reserve a guest ingress VIP in {ip_range} after {max_attempts} attempts"
    )


def release_address(
    owner: dict,
    store,
    max_attempts: int = MAX_ATTEMPTS,
    retry_delay: float = RETRY_DELAY_SECONDS,
) -> tuple[str | None, bool]:
    """Release ``owner``'s address; an absent claim is an idempotent success."""
    wanted = _validated_owner(owner)

    for attempt in range(max_attempts):
        data, resource_version = store.read()
        claims = {}
        for uid, raw in (data or {}).items():
            address, name = _parse_record(raw, uid)
            claims[uid] = {"address": address, "owner": name}

        current = claims.get(wanted["uid"])
        if current is None:
            return None, False
        if current["owner"] != _owner_name(wanted):
            raise ForeignClaimError(
                f"{wanted['uid']} is reserved by {current['owner']}, "
                f"not by {_owner_name(wanted)}; refusing to release it"
            )

        released = current["address"]
        claims.pop(wanted["uid"])
        try:
            _write(store, claims, resource_version)
        except ClaimConflictError:
            _back_off(attempt, max_attempts, retry_delay)
            continue
        return released, True

    raise ClaimConflictError(
        f"could not release the guest ingress VIP after {max_attempts} attempts"
    )


def _back_off(attempt: int, max_attempts: int, retry_delay: float) -> None:
    """Sleep between attempts, unless this was the last one."""
    if retry_delay and attempt + 1 < max_attempts:
        time.sleep(retry_delay)


class ConfigMapClaimStore:
    """Claim registry backed by one hub ConfigMap and its resourceVersion."""

    def __init__(self, api, namespace: str, name: str = DEFAULT_REGISTRY_NAME):
        self.api = api
        self.namespace = namespace
        self.name = name

    def read(self) -> tuple[dict, str | None]:
        from kubernetes.client.rest import ApiException

        try:
            config_map = self.api.read_namespaced_config_map(self.name, self.namespace)
        except ApiException as error:
            if error.status == 404:
                return {}, None
            raise ClaimError(
                f"cannot read the guest ingress VIP registry "
                f"{self.namespace}/{self.name}: {error.status} {error.reason}"
            ) from error
        return dict(config_map.data or {}), config_map.metadata.resource_version

    def write(self, data: dict, resource_version: str | None) -> None:
        from kubernetes.client.rest import ApiException

        metadata = {"name": self.name, "labels": {CLAIM_LABEL: "true"}}
        try:
            if resource_version is None:
                self.api.create_namespaced_config_map(
                    self.namespace,
                    {
                        "apiVersion": "v1",
                        "kind": "ConfigMap",
                        "metadata": metadata,
                        "data": data,
                    },
                )
                return
            self.api.replace_namespaced_config_map(
                self.name,
                self.namespace,
                {
                    "apiVersion": "v1",
                    "kind": "ConfigMap",
                    "metadata": dict(metadata, resourceVersion=resource_version),
                    "data": data,
                },
            )
        except ApiException as error:
            if error.status == 409:
                raise ClaimConflictError(
                    f"the guest ingress VIP registry {self.namespace}/{self.name} "
                    "changed during the reservation"
                ) from error
            raise ClaimError(
                f"cannot write the guest ingress VIP registry "
                f"{self.namespace}/{self.name}: {error.status} {error.reason}"
            ) from error
