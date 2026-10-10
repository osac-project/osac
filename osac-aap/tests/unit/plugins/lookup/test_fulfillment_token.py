from __future__ import annotations

import base64
import io
import json
from urllib.error import HTTPError

import pytest
from ansible.errors import AnsibleError

import fulfillment_token as plugin

ISSUER = "https://keycloak.example/realms/osac"
CLIENT_ID = "test-client"
TEST_CLIENT_SECRET = "test-secret"


class FakeResponse:
    def __init__(self, payload: dict):
        self._payload = json.dumps(payload).encode()

    def read(self, limit: int) -> bytes:
        return self._payload[:limit]

    def close(self) -> None:
        pass


class FakeOpener:
    def __init__(self, *, token_status: int = 200, token_body: dict | None = None):
        self.calls = []
        self.token_status = token_status
        self.token_body = token_body
        self.token_requests = 0

    def open(self, request, timeout: int):
        self.calls.append(request)
        if request.full_url.endswith("/.well-known/openid-configuration"):
            return FakeResponse(
                {
                    "issuer": ISSUER,
                    "token_endpoint": f"{ISSUER}/protocol/openid-connect/token",
                }
            )

        self.token_requests += 1
        assert request.get_method() == "POST"
        assert request.get_header("Content-type") == "application/x-www-form-urlencoded"
        assert request.get_header("Authorization").startswith("Basic ")
        assert timeout == plugin._REQUEST_TIMEOUT_SECONDS
        if self.token_status != 200:
            raise HTTPError(
                request.full_url,
                self.token_status,
                "Unauthorized",
                {},
                io.BytesIO(TEST_CLIENT_SECRET.encode()),
            )
        if self.token_body is not None:
            return FakeResponse(self.token_body)
        return FakeResponse({"access_token": f"access-token-{self.token_requests}", "expires_in": 300})


@pytest.fixture(autouse=True)
def set_credentials(monkeypatch):
    plugin._TOKEN_CACHE.clear()
    monkeypatch.setenv("OSAC_FULFILLMENT_ISSUER_URL", ISSUER)
    monkeypatch.setenv("OSAC_FULFILLMENT_CLIENT_ID", CLIENT_ID)
    monkeypatch.setenv("OSAC_FULFILLMENT_CLIENT_SECRET", TEST_CLIENT_SECRET)
    monkeypatch.delenv("SSL_CERT_FILE", raising=False)


def _install_opener(monkeypatch, opener: FakeOpener) -> None:
    monkeypatch.setattr(plugin, "build_opener", lambda *handlers: opener)


def _lookup() -> str:
    return plugin.LookupModule().run([], variables={})[0]


def test_requests_and_caches_client_credentials_token(monkeypatch):
    opener = FakeOpener()
    _install_opener(monkeypatch, opener)

    assert _lookup() == "access-token-1"
    assert _lookup() == "access-token-1"

    token_request = next(call for call in opener.calls if call.get_method() == "POST")
    expected_basic = base64.b64encode(f"{CLIENT_ID}:{TEST_CLIENT_SECRET}".encode()).decode()
    assert token_request.get_header("Authorization") == f"Basic {expected_basic}"
    assert opener.token_requests == 1


def test_form_encodes_client_credentials_for_basic_auth(monkeypatch):
    opener = FakeOpener()
    _install_opener(monkeypatch, opener)
    monkeypatch.setenv("OSAC_FULFILLMENT_CLIENT_ID", "client+id")
    monkeypatch.setenv("OSAC_FULFILLMENT_CLIENT_SECRET", "secret value&part")

    assert _lookup() == "access-token-1"

    token_request = next(call for call in opener.calls if call.get_method() == "POST")
    expected_basic = base64.b64encode(b"client%2Bid:secret+value%26part").decode()
    assert token_request.get_header("Authorization") == f"Basic {expected_basic}"


def test_uses_the_configured_ca_bundle_for_keycloak_requests(monkeypatch):
    opener = FakeOpener()
    _install_opener(monkeypatch, opener)
    monkeypatch.setenv("SSL_CERT_FILE", "/certs/bundle.pem")
    ca_files = []

    def create_context(*, cafile=None):
        ca_files.append(cafile)
        return plugin.ssl.SSLContext(plugin.ssl.PROTOCOL_TLS_CLIENT)

    monkeypatch.setattr(plugin.ssl, "create_default_context", create_context)

    assert _lookup() == "access-token-1"
    assert ca_files == ["/certs/bundle.pem", "/certs/bundle.pem"]


def test_refreshes_cached_token_before_expiry(monkeypatch):
    opener = FakeOpener()
    _install_opener(monkeypatch, opener)
    clock = {"now": 100.0}
    monkeypatch.setattr(plugin, "monotonic", lambda: clock["now"])

    assert _lookup() == "access-token-1"
    clock["now"] = 339.9
    assert _lookup() == "access-token-1"
    clock["now"] = 340.0
    assert _lookup() == "access-token-2"
    assert opener.token_requests == 2


def test_fetches_a_new_token_after_client_secret_rotation(monkeypatch):
    opener = FakeOpener()
    _install_opener(monkeypatch, opener)

    assert _lookup() == "access-token-1"
    monkeypatch.setenv("OSAC_FULFILLMENT_CLIENT_SECRET", "rotated-secret")
    assert _lookup() == "access-token-2"
    assert opener.token_requests == 2


def test_rejects_non_https_issuer_before_network_request(monkeypatch):
    opener = FakeOpener()
    _install_opener(monkeypatch, opener)
    monkeypatch.setenv("OSAC_FULFILLMENT_ISSUER_URL", "http://keycloak.example/realms/osac")

    with pytest.raises(AnsibleError, match="HTTPS"):
        _lookup()
    assert opener.calls == []


def test_rejects_mismatched_discovery_issuer(monkeypatch):
    opener = FakeOpener()
    _install_opener(monkeypatch, opener)
    opener.open = lambda request, timeout: FakeResponse(
        {
            "issuer": "https://other.example/realms/osac",
            "token_endpoint": f"{ISSUER}/protocol/openid-connect/token",
        }
    )

    with pytest.raises(AnsibleError, match="does not match"):
        _lookup()
    assert opener.token_requests == 0


def test_rejects_token_endpoint_on_another_origin(monkeypatch):
    opener = FakeOpener()
    _install_opener(monkeypatch, opener)
    opener.open = lambda request, timeout: FakeResponse(
        {
            "issuer": ISSUER,
            "token_endpoint": "https://other.example/token",
        }
    )

    with pytest.raises(AnsibleError, match="outside the configured issuer origin"):
        _lookup()
    assert opener.token_requests == 0


def test_token_request_failure_does_not_expose_credentials_or_response_body(monkeypatch):
    opener = FakeOpener(token_status=401)
    _install_opener(monkeypatch, opener)

    with pytest.raises(AnsibleError) as exc_info:
        _lookup()

    assert "401" in str(exc_info.value)
    assert TEST_CLIENT_SECRET not in str(exc_info.value)
    assert "access-token" not in str(exc_info.value)


def test_requires_all_client_credentials_before_network_request(monkeypatch):
    opener = FakeOpener()
    _install_opener(monkeypatch, opener)
    monkeypatch.delenv("OSAC_FULFILLMENT_CLIENT_SECRET")

    with pytest.raises(AnsibleError, match="OSAC_FULFILLMENT_CLIENT_SECRET"):
        _lookup()

    assert opener.calls == []


def test_rejects_token_response_without_a_valid_expiry(monkeypatch):
    opener = FakeOpener(token_body={"access_token": "access-token", "expires_in": 0})
    _install_opener(monkeypatch, opener)

    with pytest.raises(AnsibleError, match="valid expiry"):
        _lookup()
