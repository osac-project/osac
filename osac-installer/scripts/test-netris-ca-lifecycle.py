#!/usr/bin/env python3
"""Check Netris CA validation, RBAC, and admission-policy contracts."""

from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import textwrap
import unittest

import yaml


INSTALLER = Path(__file__).resolve().parents[1]
CHART = INSTALLER / "charts/osac"
SCRIPT = CHART / "files/validate-netris-ca.sh"

VALID_PEM = """\
-----BEGIN CERTIFICATE-----
MIICujCCAaKgAwIBAgIUI5hCs85gtax190iEEthHNCH6OjEwDQYJKoZIhvcNAQEL
BQAwFzEVMBMGA1UEAwwMb3NhYy10ZXN0LWNhMB4XDTI2MTAyMjExMzIyOVoXDTI2
MTAyMzExMzIyOVowFzEVMBMGA1UEAwwMb3NhYy10ZXN0LWNhMIIBIjANBgkqhkiG
9w0BAQEFAAOCAQ8AMIIBCgKCAQEAsXOaiGO4DZdfTuDH7HmY8Vg80KbqyqGCjYEC
FFdlFNSLZIYcjVBNDH+81tDcV4f+QZj1zXkAsCF8gxdeoF6rbFO2TYaaLyhYjXG6
uf/o0heMmPudNvm5GJMhx6nWOZ0w2Iiy+Ut4QmqmInwrndIdKNlolf9RD4OXSGzs
o2o4cK29qZpdtpaPb5fRnpEHKzPmX2KruACGRU4uo1WoL169Tws7TV4lwVtX7b5Y
qOP8rJ8DgfzVerD64NvN5HTemuX+mrQckfcNo8esU07eL5JgW8yMAouqR/tBRHBV
2LFWD2z7YngSfNsag+/pI1M9sQ6lp7mdetpovabCyRVetdcYrwIDAQABMA0GCSqG
SIb3DQEBCwUAA4IBAQANWMrUms1PcE94Vh5yIar6Eu7RxaolqJtO7QpOQjqCLvB1
RF8BZmEMhEtQzBymTuwr2oy9fGFKzg6EyeZZKwQdhH90231Kj3ffSocmPTZl/sS/
cpQkHYu3TlsJT/ovwtxZTYyIyrpIekIvG7oQLm0Q1PQO3zWq2YaK1KAfIyArNNwr
ULKdaH227+yyTwMvdpqkwCYQ1zMNJDroR+dsfkzVhLiWA73SUtoobklzdpqaMmwK
6Z1iln+DSy30st1WqV73Ic+cBX9J4kGS5oQ7p5Ach7k8UjKzSveE+xuZDSlOxbmi
1/tDMvEtwoPOrekTuQXLSQDZ7WW+WeUNJyUfbMnz
-----END CERTIFICATE-----
"""

NETRIS_ARGS = [
    "--set",
    "global.networking.fabricManager=netris",
    "--set",
    "global.networking.k8sManager=",
    "--set-string",
    "global.networking.netris.controllerUrl=https://netris.example.test",
    "--set-string",
    "global.networking.netris.credentials.username=test",
    "--set-string",
    "global.networking.netris.credentials.password=test",
    "--set-string",
    "global.networking.netris.siteId=1",
    "--set-string",
    "global.networking.netris.tenantId=1",
    "--set-string",
    "global.networking.netris.tenantName=test",
]

CA_ARGS = [
    "--set-string",
    "global.networking.netris.caBundle.configMap=customer-netris-ca",
]


def render(*extra_args: str) -> str:
    command = [
        "helm",
        "template",
        "osac",
        str(CHART),
        "--values",
        str(CHART / "ci/default-values.yaml"),
        *extra_args,
    ]
    return subprocess.run(
        command, check=True, capture_output=True, text=True
    ).stdout


def documents(rendered: str) -> list[dict]:
    return [doc for doc in yaml.safe_load_all(rendered) if isinstance(doc, dict)]


def find(docs: list[dict], kind: str, name: str) -> dict:
    for doc in docs:
        if doc.get("kind") == kind and doc.get("metadata", {}).get("name") == name:
            return doc
    raise AssertionError(f"{kind} {name} not rendered")


def command_of(job: dict) -> str:
    container = job["spec"]["template"]["spec"]["containers"][0]
    return "\n".join(container.get("command", []))


def env_of(job: dict) -> dict[str, str]:
    container = job["spec"]["template"]["spec"]["containers"][0]
    return {item["name"]: item["value"] for item in container.get("env", [])}


class RenderContractTests(unittest.TestCase):
    def test_no_ca_resources_when_config_map_is_empty(self) -> None:
        docs = documents(render(*NETRIS_ARGS))
        names = {doc.get("metadata", {}).get("name", "") for doc in docs}
        self.assertFalse([name for name in names if "netris-ca" in name])

    def test_validation_disabled_omits_validator_resources(self) -> None:
        docs = documents(
            render(*NETRIS_ARGS, *CA_ARGS, "--set", "validation.enabled=false")
        )
        names = {doc.get("metadata", {}).get("name", "") for doc in docs}
        self.assertFalse([name for name in names if "netris-ca-validate" in name])
        self.assertFalse([name for name in names if "identity-validate" in name])

    def test_validation_hooks_and_validator_rbac(self) -> None:
        docs = documents(render(*NETRIS_ARGS, *CA_ARGS))

        pre_install = find(docs, "Job", "osac-pre-install-validate")
        pre_command = command_of(pre_install)
        pre_env = env_of(pre_install)
        self.assertEqual(pre_env["CA_CONFIGMAP_NAME"], "customer-netris-ca")
        self.assertEqual(pre_env["AAP_NAMESPACE"], "default")
        self.assertEqual(
            pre_env["CA_EXECUTION_IDENTITY"], "system:serviceaccount:default:osac-sa"
        )
        self.assertEqual(pre_env["CHECK_IDENTITY"], "false")
        self.assertIn("NETRIS_CA_", pre_command)

        identity = find(docs, "Job", "osac-netris-ca-identity-validate")
        self.assertEqual(
            identity["metadata"]["annotations"]["helm.sh/hook"],
            "post-install,post-upgrade",
        )
        self.assertEqual(
            identity["metadata"]["annotations"]["helm.sh/hook-weight"], "0"
        )
        self.assertEqual(env_of(identity)["CHECK_IDENTITY"], "true")

        hook_role = find(docs, "ClusterRole", "osac-validate-default")
        self.assertFalse(
            [
                rule
                for rule in hook_role["rules"]
                if "configmaps" in rule.get("resources", [])
                or "subjectaccessreviews" in rule.get("resources", [])
            ]
        )

        pre_install_role = find(docs, "Role", "osac-validate-netris-ca")
        self.assertEqual(
            pre_install_role["rules"],
            [
                {
                    "apiGroups": [""],
                    "resources": ["configmaps"],
                    "resourceNames": ["customer-netris-ca"],
                    "verbs": ["get"],
                }
            ],
        )
        pre_install_binding = find(docs, "RoleBinding", "osac-validate-netris-ca")
        self.assertEqual(
            pre_install_binding["subjects"],
            [{"kind": "ServiceAccount", "name": "osac-validate", "namespace": "default"}],
        )

        find(docs, "ServiceAccount", "osac-netris-ca-validate")
        validator_role = find(docs, "Role", "osac-netris-ca-validate")
        self.assertEqual(
            validator_role["rules"][0]["resourceNames"], ["customer-netris-ca"]
        )

    def test_ca_read_grant_is_scoped_to_osac_sa(self) -> None:
        docs = documents(render(*NETRIS_ARGS, *CA_ARGS))
        role = find(docs, "Role", "osac-netris-ca-read")
        self.assertEqual(role["rules"][0]["resourceNames"], ["customer-netris-ca"])
        self.assertEqual(role["rules"][0]["verbs"], ["get"])

        binding = find(docs, "RoleBinding", "osac-netris-ca-read")
        self.assertEqual(
            binding["subjects"],
            [{"kind": "ServiceAccount", "name": "osac-sa", "namespace": "default"}],
        )

    def test_configured_aap_service_account_is_used(self) -> None:
        docs = documents(
            render(*NETRIS_ARGS, *CA_ARGS, "--set", "aap.serviceAccounts.osacSa=custom-sa")
        )
        binding = find(docs, "RoleBinding", "osac-netris-ca-read")
        self.assertEqual(binding["subjects"][0]["name"], "custom-sa")
        identity = find(docs, "Job", "osac-netris-ca-identity-validate")
        self.assertEqual(
            env_of(identity)["CA_EXECUTION_IDENTITY"],
            "system:serviceaccount:default:custom-sa",
        )
        pre_install = find(docs, "Job", "osac-pre-install-validate")
        self.assertEqual(
            env_of(pre_install)["CA_EXECUTION_IDENTITY"],
            "system:serviceaccount:default:custom-sa",
        )

    def test_admission_policy_protects_the_active_name(self) -> None:
        docs = documents(render(*NETRIS_ARGS, *CA_ARGS))
        policy = find(
            docs, "ValidatingAdmissionPolicy", "osac-netris-ca-protection-default"
        )
        self.assertEqual(policy["spec"]["failurePolicy"], "Fail")
        rule = policy["spec"]["matchConstraints"]["resourceRules"][0]
        self.assertEqual(rule["operations"], ["CREATE", "UPDATE", "DELETE"])
        self.assertEqual(rule["resources"], ["configmaps"])

        variables = {item["name"]: item["expression"] for item in policy["spec"]["variables"]}
        self.assertEqual(json.loads(variables["activeName"]), "customer-netris-ca")
        self.assertEqual(json.loads(variables["adminGroups"]), ["system:masters"])
        self.assertIn(
            "request.userInfo.groups.exists", policy["spec"]["validations"][0]["expression"]
        )
        self.assertIn(
            "system:serviceaccount:kube-system:namespace-controller",
            policy["spec"]["validations"][0]["expression"],
        )

        binding = find(
            docs,
            "ValidatingAdmissionPolicyBinding",
            "osac-netris-ca-protection-default",
        )
        self.assertEqual(binding["spec"]["validationActions"], ["Deny"])
        self.assertEqual(
            binding["spec"]["policyName"], "osac-netris-ca-protection-default"
        )

    def test_admin_groups_can_be_overridden(self) -> None:
        docs = documents(
            render(
                *NETRIS_ARGS,
                *CA_ARGS,
                "--set",
                "global.networking.netris.caBundle.adminGroups[0]=platform-admins",
            )
        )
        policy = find(
            docs, "ValidatingAdmissionPolicy", "osac-netris-ca-protection-default"
        )
        variables = {item["name"]: item["expression"] for item in policy["spec"]["variables"]}
        self.assertEqual(json.loads(variables["adminGroups"]), ["platform-admins"])

    def test_empty_admin_groups_fail_schema_validation(self) -> None:
        with self.assertRaises(subprocess.CalledProcessError):
            render(
                *NETRIS_ARGS,
                *CA_ARGS,
                "--set-json",
                "global.networking.netris.caBundle.adminGroups=[]",
            )


class ValidationScriptTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory(prefix="osac-netris-ca-test-")
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.bin = self.directory / "bin"
        self.bin.mkdir()
        self.write_stubs()

    def write_stubs(self) -> None:
        kubectl = self.bin / "kubectl"
        kubectl.write_text(
            textwrap.dedent(
                """\
                #!/usr/bin/env python3
                import json
                import os
                import sys

                args = sys.argv[1:]
                mode = os.environ.get("STUB_MODE", "valid")

                if args[:2] == ["get", "configmap"]:
                    if mode == "missing":
                        sys.exit(1)
                    data = {}
                    if mode != "missing-key":
                        if mode == "empty":
                            data["bundle.pem"] = ""
                        elif mode == "invalid":
                            data["bundle.pem"] = "not a certificate"
                        else:
                            data["bundle.pem"] = os.environ["STUB_BUNDLE"]
                    immutable = mode != "mutable"
                    output = args[args.index("-o") + 1] if "-o" in args else ""
                    if output == "json":
                        print(json.dumps({"data": data, "immutable": immutable}))
                        sys.exit(0)
                    if output == "jsonpath={.immutable}":
                        print("true" if immutable else "false")
                        sys.exit(0)
                    if output.startswith("jsonpath={.data.bundle"):
                        print(data.get("bundle.pem", ""))
                        sys.exit(0)
                    sys.exit(1)

                if args[:1] == ["create"]:
                    request = sys.stdin.read()
                    if "kind: SubjectAccessReview" not in request:
                        sys.exit(1)
                    print("false" if mode == "denied" else "true")
                    sys.exit(0)

                sys.exit(1)
                """
            )
        )
        openssl = self.bin / "openssl"
        openssl.write_text(
            textwrap.dedent(
                """\
                #!/usr/bin/env python3
                import ssl
                import sys

                args = sys.argv[1:]
                if args[:1] == ["crl2pkcs7"]:
                    path = args[args.index("-certfile") + 1]
                    try:
                        ssl._ssl._test_decode_cert(path)
                    except Exception:
                        sys.exit(1)
                    print("VALID")
                    sys.exit(0)
                if args[:1] == ["pkcs7"]:
                    if "VALID" not in sys.stdin.read():
                        sys.exit(1)
                    print("subject=CN=osac-test-ca")
                    sys.exit(0)
                sys.exit(1)
                """
            )
        )
        for path in (kubectl, openssl):
            path.chmod(0o755)

    def run_script(self, mode: str, *, check_identity: bool = True) -> subprocess.CompletedProcess:
        env = {
            **os.environ,
            "PATH": f"{self.bin}:{Path(sys.executable).parent}:/usr/bin:/bin",
            "STUB_MODE": mode,
            "STUB_BUNDLE": VALID_PEM,
            "CA_CONFIGMAP_NAME": "customer-netris-ca",
            "AAP_NAMESPACE": "default",
            "CA_EXECUTION_IDENTITY": "system:serviceaccount:default:osac-sa",
            "CHECK_IDENTITY": "true" if check_identity else "false",
        }
        return subprocess.run(
            ["sh", str(SCRIPT)], env=env, capture_output=True, text=True
        )

    def assert_failure(self, result: subprocess.CompletedProcess, code: str) -> None:
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(f"ERROR: NETRIS_CA_{code}", result.stderr)

    def test_valid_bundle_and_identity_succeeds(self) -> None:
        result = self.run_script("valid")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Netris CA ConfigMap validated", result.stdout)

    def test_missing_configmap_fails(self) -> None:
        self.assert_failure(self.run_script("missing"), "CONFIGMAP_MISSING")

    def test_missing_bundle_key_fails(self) -> None:
        self.assert_failure(self.run_script("missing-key"), "BUNDLE_KEY_MISSING")

    def test_empty_bundle_fails(self) -> None:
        self.assert_failure(self.run_script("empty"), "BUNDLE_EMPTY")

    def test_invalid_pem_fails(self) -> None:
        self.assert_failure(self.run_script("invalid"), "BUNDLE_INVALID")

    def test_mutable_configmap_fails(self) -> None:
        self.assert_failure(self.run_script("mutable"), "CONFIGMAP_MUTABLE")

    def test_identity_denied_fails(self) -> None:
        self.assert_failure(self.run_script("denied"), "IDENTITY_DENIED")

    def test_identity_is_not_checked_when_disabled(self) -> None:
        result = self.run_script("denied", check_identity=False)
        self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()
