"""Get and cache a Keycloak token within one Ansible task worker process.

The cache can serve multiple loop items in a task, but separate tasks run in
separate workers and each may perform its own issuer discovery and token request.
"""

from __future__ import annotations

import base64
import hashlib
import json
import os
import ssl
import threading
from time import monotonic
from urllib.error import HTTPError, URLError
from urllib.parse import quote_plus, urlencode, urlsplit, urlunsplit
from urllib.request import HTTPSHandler, HTTPRedirectHandler, Request, build_opener

from ansible.errors import AnsibleError
from ansible.plugins.lookup import LookupBase

_TOKEN_CACHE: dict[str, tuple[str, float]] = {}
_TOKEN_CACHE_LOCK = threading.Lock()
_TOKEN_REFRESH_SKEW_SECONDS = 60
_REQUEST_TIMEOUT_SECONDS = 10
_MAX_RESPONSE_BYTES = 1024 * 1024


class _NoRedirectHandler(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def _parse_https_url(raw_url: str, label: str):
    try:
        parsed = urlsplit(raw_url.strip())
        hostname = parsed.hostname
        port = parsed.port
    except ValueError:
        raise AnsibleError(f"{label} is not a valid HTTPS URL.") from None

    if (
        parsed.scheme.lower() != "https"
        or not parsed.netloc
        or not hostname
        or parsed.username is not None
        or parsed.password is not None
        or parsed.query
        or parsed.fragment
    ):
        raise AnsibleError(f"{label} must be an HTTPS URL without credentials, query, or fragment.")

    return parsed, (hostname.lower(), port if port is not None else 443)


def _normalized_issuer(parsed, origin: tuple[str, int]) -> tuple[tuple[str, int], str]:
    return origin, parsed.path.rstrip("/")


def _request_json(
    url: str,
    *,
    method: str,
    body: bytes | None,
    headers: dict[str, str],
    ca_file: str,
    operation: str,
) -> dict:
    try:
        context = ssl.create_default_context(cafile=ca_file or None)
    except (OSError, ssl.SSLError, ValueError):
        raise AnsibleError("The configured Fulfillment CA bundle could not be loaded.") from None

    opener = build_opener(_NoRedirectHandler(), HTTPSHandler(context=context))
    request = Request(url, data=body, headers=headers, method=method)
    try:
        response = opener.open(request, timeout=_REQUEST_TIMEOUT_SECONDS)
    except HTTPError as err:
        status = err.code
        err.close()
        raise AnsibleError(f"Keycloak {operation} request failed with HTTP {status}.") from None
    except (URLError, OSError, TimeoutError, ssl.SSLError):
        raise AnsibleError(f"Keycloak {operation} request could not be completed.") from None

    try:
        payload = response.read(_MAX_RESPONSE_BYTES + 1)
    except (OSError, TimeoutError):
        raise AnsibleError(f"Keycloak {operation} response could not be read.") from None
    finally:
        response.close()

    if len(payload) > _MAX_RESPONSE_BYTES:
        raise AnsibleError(f"Keycloak {operation} response was too large.")

    try:
        decoded = json.loads(payload)
    except (UnicodeDecodeError, json.JSONDecodeError):
        raise AnsibleError(f"Keycloak {operation} response was not valid JSON.") from None
    if not isinstance(decoded, dict):
        raise AnsibleError(f"Keycloak {operation} response was not a JSON object.")
    return decoded


def _get_access_token(issuer_url: str, client_id: str, client_secret: str, ca_file: str) -> str:
    issuer, issuer_origin = _parse_https_url(issuer_url, "Fulfillment issuer URL")
    cache_material = "\0".join((issuer_url.strip(), client_id, client_secret)).encode()
    cache_key = hashlib.sha256(cache_material).hexdigest()

    with _TOKEN_CACHE_LOCK:
        now = monotonic()
        cached = _TOKEN_CACHE.get(cache_key)
        if cached and now < cached[1]:
            return cached[0]

        discovery_path = issuer.path.rstrip("/") + "/.well-known/openid-configuration"
        discovery_url = urlunsplit(
            (issuer.scheme, issuer.netloc, discovery_path, "", "")
        )
        metadata = _request_json(
            discovery_url,
            method="GET",
            body=None,
            headers={"Accept": "application/json"},
            ca_file=ca_file,
            operation="issuer discovery",
        )

        discovered_issuer = metadata.get("issuer")
        token_endpoint = metadata.get("token_endpoint")
        if not isinstance(discovered_issuer, str) or not isinstance(token_endpoint, str):
            raise AnsibleError("Keycloak issuer metadata is missing required endpoints.")

        parsed_metadata_issuer, metadata_origin = _parse_https_url(
            discovered_issuer, "Discovered issuer URL"
        )
        if _normalized_issuer(parsed_metadata_issuer, metadata_origin) != _normalized_issuer(
            issuer, issuer_origin
        ):
            raise AnsibleError("Discovered Keycloak issuer does not match the configured issuer.")

        parsed_token_endpoint, token_origin = _parse_https_url(
            token_endpoint, "Discovered token endpoint"
        )
        if token_origin != issuer_origin or not parsed_token_endpoint.path:
            raise AnsibleError("Keycloak advertised a token endpoint outside the configured issuer origin.")

        credentials = f"{quote_plus(client_id)}:{quote_plus(client_secret)}".encode()
        basic_auth = base64.b64encode(credentials).decode("ascii")
        token_response = _request_json(
            token_endpoint,
            method="POST",
            body=urlencode({"grant_type": "client_credentials"}).encode(),
            headers={
                "Accept": "application/json",
                "Authorization": f"Basic {basic_auth}",
                "Content-Type": "application/x-www-form-urlencoded",
            },
            ca_file=ca_file,
            operation="client-credentials token",
        )

        access_token = token_response.get("access_token")
        expires_in = token_response.get("expires_in")
        if not isinstance(access_token, str) or not access_token:
            raise AnsibleError("Keycloak token response did not contain an access token.")
        if type(expires_in) is not int or expires_in <= 0:
            raise AnsibleError("Keycloak token response did not contain a valid expiry.")

        refresh_at = monotonic() + max(0, expires_in - _TOKEN_REFRESH_SKEW_SECONDS)
        _TOKEN_CACHE[cache_key] = (access_token, refresh_at)
        return access_token


class LookupModule(LookupBase):
    """Return a cached token, refreshing it before it is close to expiry."""

    def run(self, terms, variables=None, **kwargs):
        issuer_url = os.environ.get("OSAC_FULFILLMENT_ISSUER_URL", "").strip()
        client_id = os.environ.get("OSAC_FULFILLMENT_CLIENT_ID", "").strip()
        client_secret = os.environ.get("OSAC_FULFILLMENT_CLIENT_SECRET", "")
        if not issuer_url or not client_id or not client_secret:
            raise AnsibleError(
                "OSAC_FULFILLMENT_ISSUER_URL, OSAC_FULFILLMENT_CLIENT_ID, and "
                "OSAC_FULFILLMENT_CLIENT_SECRET are required for Fulfillment authentication."
            )

        token = _get_access_token(
            issuer_url,
            client_id,
            client_secret,
            os.environ.get("SSL_CERT_FILE", "").strip(),
        )
        return [token]
