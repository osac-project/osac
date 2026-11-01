"""Netris ExternalIP reservation, owned by the ExternalIP API ID."""

from __future__ import annotations

import ipaddress
import json
import re
import time
from http.cookiejar import CookieJar, DefaultCookiePolicy

from ansible.module_utils.urls import fetch_url

OPERATIONS = ("external_ip.allocate", "external_ip.release")

_UUID = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\Z")

RESERVATION_PURPOSE = "nat"

MAX_ALLOCATE_ATTEMPTS = 5
MAX_RELEASE_OBSERVATIONS = 5
RETRY_DELAY_SECONDS = 1.0

SUBNETS_PATH = "/api/v2/ipam/subnets"
SUBNET_PATH = "/api/v2/ipam/subnet"


class NetrisExternalIPError(ValueError):
    """A Netris ExternalIP provider operation failed or could not be verified."""

    def __init__(self, message, category="provider_error", http_status=None):
        """Store the failure category and optional HTTP status."""
        super().__init__(message)
        self.category = category
        self.http_status = http_status


def _api_id(value, context: str) -> str:
    """Return the value, or raise ValueError unless it is a lowercase UUID API ID."""
    if not isinstance(value, str) or not _UUID.match(value):
        raise ValueError(f"{context} must be a lowercase UUID API ID")
    return value


def _canonical_ipv4(value, context: str) -> ipaddress.IPv4Network:
    """Parse a strict canonical IPv4 CIDR string, or raise ValueError."""
    if not isinstance(value, str) or "/" not in value:
        raise ValueError(f"{context} must be a canonical IPv4 CIDR")
    try:
        network = ipaddress.ip_network(value, strict=True)
    except ValueError as error:
        raise ValueError(f"{context} must be a canonical IPv4 CIDR") from error
    if not isinstance(network, ipaddress.IPv4Network):
        raise ValueError(f"{context} must be an IPv4 CIDR")
    return network


def validate_request(resource: dict, pool: dict, operation: str, reservation_name: str) -> dict:
    """Normalize and validate the contract snapshots before any provider I/O.

    ``resource`` is the ExternalIP snapshot (its ``id`` is the API ID that owns
    the reservation) and ``pool`` the ExternalIPPool snapshot (``id`` and
    ``spec.cidrs`` with exactly one CIDR). ``reservation_name`` is the name the
    role built from the ExternalIP API ID (netris_contract_names).
    """
    if operation not in OPERATIONS:
        raise ValueError(f"unsupported ExternalIP operation {operation!r}")
    if not isinstance(resource, dict):
        raise ValueError("ExternalIP resource snapshot must be a dictionary")
    if not isinstance(pool, dict):
        raise ValueError("ExternalIP pool snapshot must be a dictionary")

    if not isinstance(reservation_name, str) or not reservation_name.strip() or reservation_name != reservation_name.strip():
        raise ValueError("reservation_name must be a non-empty string")
    resource_id = _api_id(resource.get("id"), "ExternalIP resource.id")
    pool_id = _api_id(pool.get("id"), "ExternalIPPool id")
    spec = pool.get("spec")
    cidrs = spec.get("cidrs") if isinstance(spec, dict) else None
    if not isinstance(cidrs, list) or len(cidrs) != 1:
        raise ValueError("ExternalIPPool spec.cidrs must hold exactly one CIDR")
    pool_network = _canonical_ipv4(cidrs[0], "ExternalIPPool CIDR")

    return {
        "operation": operation,
        "reservation_name": reservation_name,
        "resource_id": resource_id,
        "pool_id": pool_id,
        "cidr": str(pool_network),
    }


def _occupied_intervals(cidr: ipaddress.IPv4Network, reserved_prefixes) -> list[tuple[int, int]]:
    """Merge reserved prefixes into sorted inclusive integer intervals."""
    intervals: list[tuple[int, int]] = []
    for prefix in reserved_prefixes:
        network = _canonical_ipv4(prefix, "provider subnet prefix")
        intervals.append(
            (int(network.network_address), int(network.broadcast_address))
        )
    intervals.sort()
    merged: list[tuple[int, int]] = []
    for start, end in intervals:
        if merged and start <= merged[-1][1] + 1:
            merged[-1] = (merged[-1][0], max(merged[-1][1], end))
        else:
            merged.append((start, end))
    return merged


def select_address(cidr: str, reserved_prefixes: list[str]) -> str:
    """Return the first unused eligible IPv4 host, or raise ValueError on exhaustion."""
    network = _canonical_ipv4(cidr, "pool CIDR")
    merged = _occupied_intervals(network, reserved_prefixes)

    first_host = int(network.network_address)
    last_host = int(network.broadcast_address)
    if network.prefixlen <= 30:
        first_host += 1
        last_host -= 1

    current = first_host
    for start, end in merged:
        if end < current:
            continue
        if start > last_host:
            break
        if start > current:
            break
        if end >= last_host:
            raise ValueError("pool CIDR has no free host address")
        current = end + 1
    if current > last_host:
        raise ValueError("pool CIDR has no free host address")
    return str(ipaddress.IPv4Address(current))


def owned_address(
    request: dict, subnets: list[dict], site_id: int, tenant_id: int
) -> str | None:
    """Validate/reuse one reservation, or return None if it is absent."""
    record = _owned_record(request, subnets, site_id, tenant_id)
    if record is None:
        return None
    return _reservation_address(record)


def _owned_record(
    request: dict, subnets: list[dict], site_id: int, tenant_id: int
) -> dict | None:
    """Return the validated reservation record, or None if absent."""
    matches = []
    for record in subnets:
        if not isinstance(record, dict):
            raise ValueError("provider subnet record must be a dictionary")
        name = record.get("name")
        if not isinstance(name, str) or not name:
            raise ValueError("provider subnet record omits its name")
        if name == request["reservation_name"]:
            matches.append(record)

    if not matches:
        return None
    if len(matches) > 1:
        raise ValueError(
            f"provider has {len(matches)} reservations named"
            f" {request['reservation_name']}"
        )

    record = matches[0]
    prefix = record.get("prefix")
    if not isinstance(prefix, str):
        raise ValueError("provider reservation prefix must be a string")
    network = _canonical_ipv4(prefix, "provider reservation prefix")
    if network.prefixlen != 32:
        raise ValueError("provider reservation must be a canonical /32")
    if not network.subnet_of(ipaddress.ip_network(request["cidr"])):
        raise ValueError("provider reservation is outside the pool CIDR")
    if record.get("purpose") != RESERVATION_PURPOSE:
        raise ValueError("provider reservation purpose must be nat")
    if record.get("site_id") != site_id or record.get("tenant_id") != tenant_id:
        raise ValueError("provider reservation does not match the configured site or tenant")
    return record


def _reservation_address(record: dict) -> str:
    """Return the network address of a reservation's /32 prefix."""
    return str(ipaddress.ip_network(record["prefix"]).network_address)


def _normalize_subnet_record(record) -> dict:
    """Map one raw provider JSON record into the documented normalized shape."""
    if not isinstance(record, dict):
        raise NetrisExternalIPError(
            "Netris API returned a malformed subnet record",
            category="malformed_response",
        )
    subnet_id = record.get("id")
    name = record.get("name")
    prefix = record.get("prefix")
    purpose = record.get("purpose")
    sites = record.get("sites")
    tenant = record.get("tenant")
    if isinstance(subnet_id, bool) or not isinstance(subnet_id, int):
        raise NetrisExternalIPError(
            "Netris API subnet record omits its numeric id",
            category="malformed_response",
        )
    if not isinstance(name, str) or not name:
        raise NetrisExternalIPError(
            "Netris API subnet record omits its name",
            category="malformed_response",
        )
    if not isinstance(prefix, str) or not prefix:
        raise NetrisExternalIPError(
            "Netris API subnet record omits its prefix",
            category="malformed_response",
        )
    if not isinstance(purpose, str) or not purpose:
        raise NetrisExternalIPError(
            "Netris API subnet record omits its purpose",
            category="malformed_response",
        )
    if not isinstance(sites, list) or not sites or not isinstance(sites[0], dict):
        raise NetrisExternalIPError(
            "Netris API subnet record omits its sites",
            category="malformed_response",
        )
    site_id = sites[0].get("id")
    if isinstance(site_id, bool) or not isinstance(site_id, int):
        raise NetrisExternalIPError(
            "Netris API subnet record omits its site id",
            category="malformed_response",
        )
    if not isinstance(tenant, dict):
        raise NetrisExternalIPError(
            "Netris API subnet record omits its tenant",
            category="malformed_response",
        )
    tenant_id = tenant.get("id")
    if isinstance(tenant_id, bool) or not isinstance(tenant_id, int):
        raise NetrisExternalIPError(
            "Netris API subnet record omits its tenant id",
            category="malformed_response",
        )
    try:
        network = ipaddress.ip_network(prefix, strict=True)
    except ValueError as error:
        raise NetrisExternalIPError(
            "Netris API subnet record carries a malformed prefix",
            category="malformed_response",
        ) from error
    if not isinstance(network, ipaddress.IPv4Network):
        raise NetrisExternalIPError(
            "Netris API subnet record must be an IPv4 prefix",
            category="malformed_response",
        )
    return {
        "id": subnet_id,
        "name": name,
        "prefix": str(network),
        "purpose": purpose,
        "site_id": site_id,
        "tenant_id": tenant_id,
    }


class NetrisSubnetClient:
    """GET/POST/DELETE client for the Netris IPAM subnet endpoints."""

    def __init__(
        self,
        module,
        controller_url: str,
        session_cookie: str,
        timeout: int = 30,
        validate_certs: bool = True,
        ca_path: str | None = None,
    ):
        """Store the controller URL, cookie transport, and TLS options."""
        self._module = module
        self._controller_url = controller_url.rstrip("/")
        self._session_cookie = session_cookie
        self._timeout = timeout
        self._validate_certs = validate_certs
        self._ca_path = ca_path

    def _request(self, method: str, path: str, body=None, success_codes=(200,)) -> dict:
        """Send one JSON request; raise on unexpected status or malformed body."""
        params = getattr(self._module, "params", None)
        if params is not None:
            params["validate_certs"] = self._validate_certs
        headers = {"Cookie": f"connect.sid={self._session_cookie}"}
        if body is not None:
            headers["Content-Type"] = "application/json"
        response, info = fetch_url(
            self._module,
            f"{self._controller_url}{path}",
            data=json.dumps(body).encode() if body is not None else None,
            headers=headers,
            unredirected_headers=["Cookie"],
            # An empty allowlist prevents response cookies from reappearing on redirects.
            cookies=CookieJar(policy=DefaultCookiePolicy(allowed_domains=[])),
            method=method,
            timeout=self._timeout,
            use_proxy=True,
            force=True,
            ca_path=self._ca_path,
        )
        status = info.get("status")
        if status not in success_codes:
            category = "transport_error" if status == -1 else "provider_error"
            raise NetrisExternalIPError(
                f"Netris API {method} request failed",
                category=category,
                http_status=status,
            )
        content = response.read() if response is not None else b""
        if not content:
            return {}
        try:
            parsed = json.loads(content)
        except (UnicodeDecodeError, json.JSONDecodeError) as error:
            raise NetrisExternalIPError(
                "Netris API response is not valid JSON",
                category="malformed_response",
            ) from error
        if not isinstance(parsed, dict):
            raise NetrisExternalIPError(
                "Netris API response is not a JSON object",
                category="malformed_response",
            )
        return parsed

    def list_subnets(self) -> list[dict]:
        """Return normalized subnet records from the Netris IPAM listing."""
        parsed = self._request("GET", SUBNETS_PATH, success_codes=(200,))
        data = parsed.get("data")
        if not isinstance(data, list):
            raise NetrisExternalIPError(
                "Netris API subnets response omits its data list",
                category="malformed_response",
            )
        return [_normalize_subnet_record(record) for record in data]

    def create_subnet(self, name: str, prefix: str, site_id: int, tenant_id: int):
        """Create a nat reservation subnet and return its provider id."""
        body = {
            "name": name,
            "prefix": prefix,
            "purpose": RESERVATION_PURPOSE,
            "sites": [{"id": int(site_id)}],
            "tenant": {"id": int(tenant_id)},
        }
        parsed = self._request("POST", SUBNET_PATH, body=body, success_codes=(200, 201))
        data = parsed.get("data")
        if isinstance(data, dict):
            data = data.get("id")
        if isinstance(data, bool) or not isinstance(data, (int, str)):
            raise NetrisExternalIPError(
                "Netris API subnet create response omits its id",
                category="malformed_response",
            )
        return data

    def delete_subnet(self, subnet_id: int) -> None:
        """Delete one subnet by its provider id."""
        self._request("DELETE", f"{SUBNET_PATH}/{subnet_id}", success_codes=(200, 204))


def _read_owned(request: dict, client, site_id: int, tenant_id: int):
    """List provider subnets and resolve the request's owned reservation."""
    subnets = client.list_subnets()
    try:
        owned = _owned_record(request, subnets, site_id, tenant_id)
    except ValueError as error:
        # The reservation of this ExternalIP exists but is not ours (ambiguous, or
        # another pool, site, tenant or purpose): a retry cannot fix that.
        raise NetrisExternalIPError(str(error), category="foreign_object") from error
    return subnets, owned


def _pause(delay_seconds: float, attempt: int, max_attempts: int) -> None:
    """Sleep between retry attempts until the attempt bound is reached."""
    if attempt < max_attempts and delay_seconds > 0:
        time.sleep(delay_seconds)


def _reconcile_allocate(request, client, site_id, tenant_id, delay_seconds) -> dict:
    """Allocate or reuse the reservation; return changed + address."""
    wrote = False
    last_error = None
    attempt = 0
    while attempt < MAX_ALLOCATE_ATTEMPTS:
        attempt += 1
        subnets, owned = _read_owned(request, client, site_id, tenant_id)
        if owned is not None:
            return {"changed": wrote, "address": _reservation_address(owned)}
        try:
            pool_network = ipaddress.ip_network(request["cidr"])
            occupied = [
                r["prefix"]
                for r in subnets
                if r["purpose"] == RESERVATION_PURPOSE
                or not pool_network.subnet_of(ipaddress.ip_network(r["prefix"]))
            ]
            address = select_address(request["cidr"], occupied)
        except ValueError as error:
            raise NetrisExternalIPError(str(error), category="exhausted") from error
        wrote = True
        try:
            client.create_subnet(
                request["reservation_name"], f"{address}/32", site_id, tenant_id
            )
        except NetrisExternalIPError as error:
            last_error = error
            _pause(delay_seconds, attempt, MAX_ALLOCATE_ATTEMPTS)
            continue
        subnets, owned = _read_owned(request, client, site_id, tenant_id)
        if owned is not None:
            observed = _reservation_address(owned)
            if observed != address:
                raise NetrisExternalIPError(
                    "Netris returned a reservation at an unexpected address",
                    category="uncertain_state",
                )
            return {"changed": True, "address": address}
        _pause(delay_seconds, attempt, MAX_ALLOCATE_ATTEMPTS)
    if last_error is not None:
        raise last_error
    raise NetrisExternalIPError(
        "Netris ExternalIP allocation did not converge within the attempt bound",
        category="exhausted_attempts",
    )


def _reconcile_release(request, client, site_id, tenant_id, delay_seconds) -> dict:
    """Delete the reservation and confirm absence within the bound."""
    changed = False
    for observation in range(1, MAX_RELEASE_OBSERVATIONS + 1):
        subnets, owned = _read_owned(request, client, site_id, tenant_id)
        if owned is None:
            return {"changed": changed, "address": ""}
        changed = True
        try:
            client.delete_subnet(owned["id"])
        except NetrisExternalIPError as error:
            if error.http_status != 404:
                raise
        _pause(delay_seconds, observation, MAX_RELEASE_OBSERVATIONS)
    raise NetrisExternalIPError(
        "Netris reservation persists after the observation bound",
        category="exhausted_observations",
    )


def reconcile(
    request: dict, client, site_id: int, tenant_id: int,
    delay_seconds: float = RETRY_DELAY_SECONDS,
) -> dict:
    """Run allocation/release against the Netris API; return changed + address."""
    if request["operation"] == "external_ip.allocate":
        return _reconcile_allocate(request, client, site_id, tenant_id, delay_seconds)
    return _reconcile_release(request, client, site_id, tenant_id, delay_seconds)
