#!/usr/bin/env python3
"""Check service-account management credentials and Keycloak permissions."""

import json
from pathlib import Path
import subprocess
import textwrap

INSTALLER = Path(__file__).resolve().parents[1]
SERVICE = INSTALLER.parent / "fulfillment-service/charts/service"
STATIC_REALM = INSTALLER / "prerequisites/keycloak/service/files/realm.json"


def render(chart, *args):
    return subprocess.run(
        ["helm", "template", "test", str(chart), *args],
        check=True, capture_output=True, text=True, timeout=120,
    ).stdout.split("\n---\n")


def check():
    documents = render(SERVICE, "-f", str(SERVICE / "ci-values.yaml"))
    consumers = set()
    for document in documents:
        if "kind: Deployment\n" not in document:
            continue
        if "ci-idp-credentials" not in document:
            continue
        name = document.split("  name: ", 1)[1].splitlines()[0]
        consumers.add(name)
        if name == "fulfillment-controller":
            assert (
                "  strategy:\n    type: RollingUpdate\n"
            ) in document
        assert "name: idp-credentials" in document, name
        assert f"mountPath: /etc/{name}/idp\n          readOnly: true" in document, name
        for flag in (
            "--idp-url=https://idp.ci.example.com",
            "--auth-issuer-url=https://issuer.ci.example.com",
            f"--idp-client-id-file=/etc/{name}/idp/client-id",
            f"--idp-client-secret-file=/etc/{name}/idp/client-secret",
            f"--ca-file=/etc/{name}/tls/cas",
        ):
            assert flag in document, (name, flag)
    assert consumers == {"fulfillment-controller"}, consumers
    documents = render(INSTALLER / "charts/osac-infra", "--set", "keycloak.devFixtures.enabled=true")
    realm_documents = [d for d in documents if "kind: ConfigMap\n" in d and "  name: keycloak-realm\n" in d]
    assert len(realm_documents) == 1
    realm = json.loads(textwrap.dedent(realm_documents[0].split("  realm.json: |\n", 1)[1]))
    controller = next(u for u in realm["users"] if u["username"] == "service-account-osac-controller")
    expected = {
        "manage-realm", "manage-users", "view-realm", "view-users",
        "manage-identity-providers", "view-identity-providers",
        "create-client", "view-clients",
    }
    assert set(controller["clientRoles"]["realm-management"]) == expected
    assert controller["serviceAccountClientId"] == "osac-controller"
    static_realm = json.loads(STATIC_REALM.read_text())
    static_controller = next(u for u in static_realm["users"] if u["username"] == "service-account-osac-controller")
    assert set(static_controller["clientRoles"]["realm-management"]) == expected
    for candidate in (realm, static_realm):
        registrants = {
            user["username"] for user in candidate["users"]
            if "create-client" in user.get("clientRoles", {}).get("realm-management", [])
        }
        assert registrants == {"service-account-osac-controller"}, registrants
        policies = candidate["components"]["org.keycloak.services.clientregistration.policy.ClientRegistrationPolicy"]
        authenticated = {p["providerId"]: p for p in policies if p["subType"] == "authenticated"}
        assert {
            "oidc-hardcoded-claim-mapper",
            "oidc-audience-mapper",
            "oidc-usersessionmodel-note-mapper",
        }.issubset(set(authenticated["allowed-protocol-mappers"]["config"]["allowed-protocol-mapper-types"]))
        assert "service_account" in authenticated["allowed-client-templates"]["config"]["allowed-client-scopes"]
        anonymous = {p["providerId"]: p for p in policies if p["subType"] == "anonymous"}
        assert not {
            "oidc-hardcoded-claim-mapper",
            "oidc-audience-mapper",
            "oidc-usersessionmodel-note-mapper",
        }.intersection(set(anonymous["allowed-protocol-mappers"]["config"]["allowed-protocol-mapper-types"]))
        assert "service_account" not in anonymous["allowed-client-templates"]["config"].get("allowed-client-scopes", [])
    setup_documents = [d for d in documents if "kind: ConfigMap\n" in d and "  name: keycloak-password-setup\n" in d]
    assert len(setup_documents) == 1
    setup_document = setup_documents[0]
    setup_script = textwrap.dedent(setup_document.split("  set-passwords.sh: |\n", 1)[1])
    assert "ensure_controller_idp_roles" in setup_script
    assert "role-mappings/clients/${realm_management_id}" in setup_script
    assert 'keycloak_curl -sf -X DELETE' in setup_script
    assert 'roles/view-clients' in setup_script
    assert 'roles/manage-clients' in setup_script
    assert "ensure_service_account_registration_policies" in setup_script
    assert 'admin/realms/osac/components/${component_id}' in setup_script
    assert "ensure_service_account_registration_token_policy" in setup_script
    assert "client-policies/profiles" in setup_script
    assert "registration-access-token-rotation-disabled" in setup_script
    assert "client-policies/policies" in setup_script
    assert '"condition": "client-updater-context"' in setup_script
    assert '"update-client-source": ["ByRegistrationAccessToken"]' in setup_script
    assert 'curl --cacert "${KEYCLOAK_CA_FILE}" "$@"' in setup_script
    assert "curl -k" not in setup_script
    subprocess.run(["bash", "-n"], input=setup_script, check=True, text=True)
    setup_jobs = [d for d in documents if "kind: Job\n" in d and "  name: keycloak-set-passwords\n" in d]
    assert len(setup_jobs) == 1
    assert "mountPath: /etc/keycloak/ca" in setup_jobs[0]
    assert "secretName: keycloak-tls-cert" in setup_jobs[0]
    reader_id = "osac-aap-secret-reader"
    reader = next(client for client in realm["clients"] if client["clientId"] == reader_id)
    assert reader["serviceAccountsEnabled"] is True
    assert reader["publicClient"] is False
    assert reader["fullScopeAllowed"] is False
    assert reader["standardFlowEnabled"] is False
    assert reader["directAccessGrantsEnabled"] is False
    assert reader["secret"] == "__OSAC_AAP_SECRET_READER_CLIENT_SECRET__"
    assert {"osac-api", "service_account"}.issubset(reader["defaultClientScopes"])
    reader_user = next(u for u in realm["users"] if u["username"] == "service-account-" + reader_id)
    assert reader_user["serviceAccountClientId"] == reader_id
    assert not any(reader_user.get(field) for field in ("groups", "clientRoles", "realmRoles"))

    platform = INSTALLER / "charts/osac"
    values = str(platform / "ci/default-values.yaml")
    reader_docs = render(
        platform,
        "-f", values,
        "--set", "service.auth.issuerUrl=https://issuer.example/realms/osac",
        "--set", "aap.configAsCode.fulfillmentInternalApiUrl=https://fulfillment.example",
    )
    config = next(d for d in reader_docs if "kind: ConfigMap\n" in d and "  name: aap-fulfillment-secret-reader\n" in d)
    assert "OSAC_AAP_SECRET_READER_CLIENT_ID" not in config
    assert 'OSAC_AAP_SECRET_READER_ISSUER_URL: "https://issuer.example/realms/osac"' in config
    assert 'OSAC_AAP_SECRET_READER_API_URL: "https://fulfillment.example"' in config
    assert "CLIENT_SECRET" not in config
    deployment = next(d for d in reader_docs if "kind: Deployment\n" in d and "  name: fulfillment-grpc-server\n" in d)
    assert "--aap-secret-reader-client-id" not in deployment
    assert "--grpc-authn-trusted-token-issuers=https://issuer.example/realms/osac" in deployment
    assert "--aap-secret-reader-issuer" not in deployment
    bootstrap = next(d for d in reader_docs if "kind: Job\n" in d and "name: OSAC_AAP_SECRET_READER_CLIENT_SECRET" in d)
    assert bootstrap.count("name: aap-fulfillment-secret-reader") == 2
    assert "key: client-secret" in bootstrap
    without_vmaas = render(platform, "-f", values, "--set", "global.services.vmaas.enabled=false")
    assert any("--enable-vmaas=false" in d and "  name: fulfillment-grpc-server\n" in d for d in without_vmaas)
    assert any("OSAC_AAP_SECRET_READER_ISSUER_URL" in d for d in without_vmaas)
    assert any("kind: Job\n" in d and "name: OSAC_AAP_SECRET_READER_CLIENT_SECRET" in d for d in without_vmaas)
    print("Service-account IDP rendering passed.")


if __name__ == "__main__":
    check()
