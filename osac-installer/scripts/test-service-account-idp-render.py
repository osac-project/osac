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
    assert consumers == {"fulfillment-controller", "fulfillment-grpc-server"}, consumers
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
    assert 'curl --cacert "${KEYCLOAK_CA_FILE}" "$@"' in setup_script
    assert "curl -k" not in setup_script
    subprocess.run(["bash", "-n"], input=setup_script, check=True, text=True)
    setup_jobs = [d for d in documents if "kind: Job\n" in d and "  name: keycloak-set-passwords\n" in d]
    assert len(setup_jobs) == 1
    assert "mountPath: /etc/keycloak/ca" in setup_jobs[0]
    assert "secretName: keycloak-tls-cert" in setup_jobs[0]
    print("Service-account IDP rendering passed.")


if __name__ == "__main__":
    check()
