"""Read one base64-encoded data key from a private Fulfillment Secret."""

import base64
import os
from urllib.parse import quote, urlunsplit

from ansible.errors import AnsibleError
from ansible.plugins.lookup import LookupBase
from ansible_collections.osac.service.plugins.lookup.fulfillment_token import (
    HTTPRequestError,
    _request_json,
    fulfillment_ca_file,
    get_access_token_from_environment,
)
from ansible_collections.osac.service.plugins.module_utils.fulfillment_url import parse_fulfillment_api_url

# A REST response expands the gRPC message's bytes fields to base64.
_MAX_SECRET_RESPONSE_BYTES = 6 * 1024 * 1024
_READ_ERRORS = {
    401: "Fulfillment rejected the refreshed AAP credentials.",
    403: "The AAP credential does not have permission to read the Fulfillment Secret.",
    404: "The Fulfillment Secret is unavailable.",
}


def _read_secret(url: str, token: str) -> dict:
    return _request_json(
        url,
        method="GET",
        body=None,
        headers={"Authorization": f"Bearer {token}", "Accept": "application/json"},
        ca_file=fulfillment_ca_file(),
        operation="Fulfillment Secret read",
        max_response_bytes=_MAX_SECRET_RESPONSE_BYTES,
    )


def _get_secret_data(secret_id: str, data_key: str) -> dict:
    try:
        endpoint = parse_fulfillment_api_url(os.environ.get("OSAC_FULFILLMENT_INTERNAL_API_URL", ""))
        if not isinstance(secret_id, str) or not secret_id or secret_id in (".", "..") or "/" in secret_id:
            raise AnsibleError("The Fulfillment Secret ID is invalid.")
        path = f"/api/private/v1/secrets/{quote(secret_id, safe='')}"
        url = urlunsplit((endpoint.scheme, endpoint.netloc, path, "", ""))
        for refresh in (False, True):
            token = get_access_token_from_environment(force_refresh=refresh)
            try:
                response = _read_secret(url, token)
                break
            except HTTPRequestError as err:
                if refresh or err.status != 401:
                    return {"error": _READ_ERRORS.get(err.status, str(err))}
    except AnsibleError as err:
        return {"error": str(err)}

    data = response.get("data")
    value = data.get(data_key) if isinstance(data, dict) else None
    if not isinstance(value, str) or not value:
        return {"error": "The Fulfillment Secret does not contain a non-empty value for the requested data key."}
    try:
        base64.b64decode(value, validate=True)
    except ValueError:
        return {"error": "Fulfillment returned invalid base64 Secret data."}
    return {"value": value}


class LookupModule(LookupBase):
    def run(self, terms, variables=None, **kwargs):
        # The calling task keeps the result under no_log and reports only error.
        data_key = kwargs.get("data_key")
        if not isinstance(data_key, str) or not data_key:
            raise AnsibleError("The fulfillment_secret_data lookup requires a data_key.")
        return [_get_secret_data(secret_id, data_key) for secret_id in terms]
