#!/usr/bin/env python3
"""Exercise private offline validation diagnostics."""

import io
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import tarfile
import tempfile
import unittest


INSTALLER = Path(__file__).resolve().parents[1]
HELM = shutil.which(os.environ.get("HELM_BIN", "helm"))
HELM4 = shutil.which(os.environ.get("HELM4_BIN", "helm4"))


class ValidationDiagnosticsTests(unittest.TestCase):
    """Regression tests for private validation diagnostics."""

    def setUp(self):
        """Create a temporary directory and the canary values used below."""
        self.temp = tempfile.TemporaryDirectory(prefix="osac-validation-")
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.password = secrets.token_hex(24)
        self.username = secrets.token_hex(8)
        self.hostname = f"{secrets.token_hex(12)}.internal.invalid"
        self.corp_host = f"{secrets.token_hex(12)}.corp.example"
        self.url = f"https://{self.username}:{self.password}@{self.hostname}"
        self.token = secrets.token_hex(24)

    def assert_private(self, output):
        """Fail when any canary value appears in the given output."""
        for value in (self.username, self.password, self.hostname, self.token,
                      self.corp_host):
            self.assertFalse(value in output, "sensitive canary reached output")

    def test_offline_render_error_hides_set_file_path(self):
        """Check a --set-file failure does not echo the file path."""
        path = f"/srv/{self.token}/ca.pem"
        message = (
            "Error: failed parsing --set-file data: open "
            f"{path}: no such file or directory\n"
        )
        result = subprocess.run(
            ["bash", "-c", 'source "$1"; offline_render_error "$2"',
             "bash", str(INSTALLER / "scripts/lib.sh"), message],
            text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 0)
        self.assert_private(result.stdout + result.stderr)
        self.assertIn("--set-file input could not be read", result.stderr)
        self.assertNotIn(path, result.stdout + result.stderr)

    def test_offline_render_error_reports_bad_values_input_class(self):
        """Check an unreadable values input reports its class, not the path."""
        path = f"/srv/{self.token}/values.yaml"
        values_hint = "a values file (-f/--values) could not be read or parsed"
        flag_hint = "an unknown Helm flag was passed"
        cases = (
            (f"Error: open {path}: no such file or directory\n", values_hint),
            (f"Error: failed to parse {path}: error converting YAML to"
             " JSON: yaml: line 1: did not find expected node content\n",
             values_hint),
            (f"Error: unknown flag: --{self.token}\n", flag_hint),
            (f"Error: unknown shorthand flag: '{self.token[0]}' in"
             f" -{self.token[0]}\n", flag_hint),
        )
        for message, hint in cases:
            with self.subTest(hint=hint):
                result = subprocess.run(
                    ["bash", "-c", 'source "$1"; offline_render_error "$2"',
                     "bash", str(INSTALLER / "scripts/lib.sh"), message],
                    text=True, capture_output=True,
                )
                self.assertEqual(result.returncode, 0)
                self.assert_private(result.stdout + result.stderr)
                self.assertIn(hint, result.stderr)
                self.assertNotIn(path, result.stdout + result.stderr)

    def test_failure_diagnostics_do_not_echo_canaries(self):
        """Check a failed assertion's diagnostic output does not echo canaries."""
        output = io.StringIO()
        case = unittest.FunctionTestCase(lambda: self.assert_private(self.url))
        result = unittest.TextTestRunner(stream=output).run(case)
        self.assertEqual(len(result.failures), 1)
        self.assert_private(output.getvalue())

    def test_aap_validation_does_not_echo_url(self):
        """Check AAP URL validation rejects a credential URL without echoing it."""
        values = self.directory / "aap.json"
        values.write_text(json.dumps({"instanceGroups": {"networkFulfillment": {
            "config": {"NETRIS_CONTROLLER_URL": self.url}}}}))
        result = subprocess.run(
            [HELM, "template", "aap", str(INSTALLER.parent / "osac-aap/charts/aap"),
             "-f", str(values)], text=True, capture_output=True,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assert_private(result.stdout + result.stderr)
        self.assertIn("NETRIS_CONTROLLER_URL", result.stderr)
        self.assertIn("https://", result.stderr)
        self.assertNotIn("without embedded credentials", result.stderr)

    def test_aap_url_validation_policy_is_unchanged(self):
        """Check a credential-free URL still renders under the existing policy."""
        values = self.directory / "aap-valid.json"
        values.write_text(json.dumps({"instanceGroups": {"networkFulfillment": {
            "config": {"NETRIS_CONTROLLER_URL": f"https://{self.username}@{self.hostname}/api"}}}}))
        result = subprocess.run(
            [HELM, "template", "aap", str(INSTALLER.parent / "osac-aap/charts/aap"),
             "-f", str(values)], text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 0, "the existing URL policy changed")

    def test_make_gate_initial_and_retry_errors_are_private(self):
        """Check the make gate keeps a failing render's error private, with or
        without the Secret-injection retry."""
        chart = self.directory / "chart"
        (chart / "templates").mkdir(parents=True)
        (chart / "Chart.yaml").write_text("apiVersion: v2\nname: diagnostic\nversion: 0.1.0\n")
        values = self.directory / "values.json"
        values.write_text(json.dumps({"rejected": self.url}))
        (chart / "templates/failure.yaml").write_text('{{ fail .Values.rejected }}\n')
        calls = self.directory / "render-calls"
        for retry in (False, True):
            with self.subTest(retry=retry):
                binary = self.directory / "bin"
                binary.mkdir(exist_ok=True)
                wrapper = binary / "helm"
                wrapper.write_text(
                    '#!/usr/bin/env bash\nset -euo pipefail\n'
                    'if [[ "$1" == template && "$2" == osac ]]; then\n'
                    '  echo render >> "$RENDER_CALLS"\n'
                    + ('  if [[ "$*" != *"bmf.netbox.tokenValue="* ]]; then\n'
                       '    echo "netbox.tokenValue is required when Secret" >&2\n'
                       '    exit 1\n  fi\n' if retry else '')
                    + '  exec "$REAL_HELM" template diagnostic "$ERROR_CHART" -f "$ERROR_VALUES"\n'
                    'fi\nexec "$REAL_HELM" "$@"\n'
                )
                wrapper.chmod(0o755)
                env = {**os.environ, "PATH": f"{binary}:{os.environ['PATH']}",
                       "REAL_HELM": HELM, "ERROR_CHART": str(chart), "ERROR_VALUES": str(values),
                       "RENDER_CALLS": str(calls)}
                calls.write_text("")
                result = subprocess.run(
                    ["make", "--no-print-directory", "-C", str(INSTALLER),
                     "-o", "helm-osac-deps", "-o", "helm-infra-deps", "validate-inputs",
                     "PLATFORM=kind", "PROFILE=dev", "NS=osac"],
                    env=env, text=True, capture_output=True, timeout=60,
                )
                self.assertNotEqual(result.returncode, 0)
                self.assert_private(result.stdout + result.stderr)
                self.assertIn("offline chart rendering failed", result.stderr)
                self.assertIn("no cluster changes", result.stderr)
                self.assertEqual(len(calls.read_text().splitlines()), 2 if retry else 1)

    def test_offline_render_error_reports_template_location_without_message(self):
        """Check a template check failure names its location, not its value."""
        message = (
            "Error: execution error at "
            "(osac/templates/networking.yaml:42:7): invalid peer "
            f"{self.hostname}\n\nUse --debug flag to render out invalid YAML\n"
        )
        result = subprocess.run(
            ["bash", "-c", 'source "$1"; offline_render_error "$2"',
             "bash", str(INSTALLER / "scripts/lib.sh"), message],
            text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 0)
        self.assert_private(result.stdout + result.stderr)
        self.assertIn("offline chart rendering failed", result.stderr)
        self.assertIn(
            "template check failed in osac/templates/networking.yaml:42:7"
            " (message suppressed)",
            result.stderr,
        )

    def test_offline_render_error_ignores_embedded_location(self):
        """Check a path-shaped token in the message is never printed."""
        message = (
            "Error: execution error at "
            "(osac/templates/validate-values.yaml:9:1): rejected "
            f"execution error at ({self.hostname}:3:4): {self.token}\n"
        )
        result = subprocess.run(
            ["bash", "-c", 'source "$1"; offline_render_error "$2"',
             "bash", str(INSTALLER / "scripts/lib.sh"), message],
            text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 0)
        self.assert_private(result.stdout + result.stderr)
        self.assertIn(
            "template check failed in "
            "osac/templates/validate-values.yaml:9:1 (message suppressed)",
            result.stderr,
        )

    def test_offline_render_error_uses_first_error_line_only(self):
        """Check only the first Helm error line can name a location."""
        location = (
            "template check failed in "
            "osac/templates/validate-values.yaml:9:1 (message suppressed)"
        )
        for prefix in ("", "WARNING: This chart is deprecated\n"):
            with self.subTest(prefix=prefix):
                message = (
                    prefix
                    + "Error: execution error at "
                    "(osac/templates/validate-values.yaml:9:1): rejected\n"
                    f"Error: execution error at ({self.hostname}:3:4): "
                    f"{self.token}\n"
                )
                result = subprocess.run(
                    ["bash", "-c", 'source "$1"; offline_render_error "$2"',
                     "bash", str(INSTALLER / "scripts/lib.sh"), message],
                    text=True, capture_output=True,
                )
                self.assertEqual(result.returncode, 0)
                self.assert_private(result.stdout + result.stderr)
                self.assertIn(location, result.stderr)
                self.assertEqual(
                    result.stderr.count("template check failed in"), 1)
        message = (
            "Error: values don't meet the specifications of the schema(s)"
            " in the following chart(s):\n"
            f"osac:\n- at '/global/x': {self.hostname}\n"
            f"Error: execution error at ({self.hostname}:1:2): {self.token}\n"
        )
        result = subprocess.run(
            ["bash", "-c", 'source "$1"; offline_render_error "$2"',
             "bash", str(INSTALLER / "scripts/lib.sh"), message],
            text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 0)
        self.assert_private(result.stdout + result.stderr)
        self.assertNotIn("template check failed in", result.stderr)

    def test_offline_render_error_ignores_echoed_schema_paths(self):
        """Check path-shaped message text is never reported as a path."""
        cases = (
            (
                "Error: execution error at (osac/templates/x.yaml:1:3):"
                " rejected\n"
                f"- at '/{self.hostname}/path': value\n",
                "template check failed in osac/templates/x.yaml:1:3"
                " (message suppressed)",
                False,
            ),
            (
                "Error: values don't meet the specifications of the"
                " schema(s) in the following chart(s):\nosac:\n"
                "- at '/global/real': rejected\n"
                f"- at '/{self.hostname}/path': value\n",
                "global/real: violates the values schema",
                True,
            ),
        )
        for message, expected, schema_reported in cases:
            with self.subTest(expected=expected):
                result = subprocess.run(
                    ["bash", "-c", 'source "$1"; offline_render_error "$2"',
                     "bash", str(INSTALLER / "scripts/lib.sh"), message],
                    text=True, capture_output=True,
                )
                self.assertEqual(result.returncode, 0)
                self.assert_private(result.stdout + result.stderr)
                self.assertIn(expected, result.stderr)
                if not schema_reported:
                    self.assertNotIn(
                        "violates the values schema", result.stderr)

    def test_offline_render_error_stops_after_unmatched_schema_item(self):
        """Check an unrecognized first schema item ends the scan."""
        message = (
            "Error: values don't meet the specifications of the schema(s)"
            " in the following chart(s):\n"
            "osac:\n"
            "- password field: Invalid type. Expected: integer,"
            " given: string\n"
            f"- at '/{self.hostname}/echoed': got string, want integer\n"
        )
        result = subprocess.run(
            ["bash", "-c", 'source "$1"; offline_render_error "$2"',
             "bash", str(INSTALLER / "scripts/lib.sh"), message],
            text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 0)
        self.assert_private(result.stdout + result.stderr)
        self.assertNotIn("violates the values schema", result.stderr)

    def test_make_gate_infra_error_is_private(self):
        """Check a failing infra render stays private through the make gate."""
        binary = self.directory / "bin"
        binary.mkdir(exist_ok=True)
        wrapper = binary / "helm"
        wrapper.write_text(
            '#!/usr/bin/env bash\nset -euo pipefail\n'
            'if [[ "$1" == template && "$2" == osac-infra ]]; then\n'
            '  echo "Error: execution error at '
            '(osac-infra/templates/networking.yaml:7:3): $REJECTED" >&2\n'
            '  echo "Use --debug flag to render out invalid YAML" >&2\n'
            '  exit 1\n'
            'fi\nexec "$REAL_HELM" "$@"\n'
        )
        wrapper.chmod(0o755)
        env = {**os.environ, "PATH": f"{binary}:{os.environ['PATH']}",
               "REAL_HELM": HELM, "REJECTED": self.hostname}
        result = subprocess.run(
            ["make", "--no-print-directory", "-C", str(INSTALLER),
             "-o", "helm-osac-deps", "-o", "helm-infra-deps", "validate-inputs",
             "PLATFORM=kind", "PROFILE=dev", "NS=osac"],
            env=env, text=True, capture_output=True, timeout=60,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assert_private(result.stdout + result.stderr)
        self.assertIn(
            "template check failed in osac-infra/templates/networking.yaml:7:3",
            result.stderr,
        )
        self.assertIn("no cluster changes", result.stderr)

    def test_offline_render_error_reports_schema_path_without_values(self):
        """Check unmatched schema failures name the path and drop the value."""
        cases = (
            (
                "Error: values don't meet the specifications of the schema(s)"
                " in the following chart(s):\n"
                f"osac:\n- aap.bootstrap.backoffLimit: {self.password}\n",
                "aap.bootstrap.backoffLimit: violates the values schema",
            ),
            (
                "Error: values don't meet the specifications of the schema(s)"
                " in the following chart(s):\n"
                f"osac:\n- at '/global/fulfillmentTrust': {self.url}\n",
                "global/fulfillmentTrust: violates the values schema",
            ),
            (
                "Error: values don't meet the specifications of the schema(s)"
                " in the following chart(s):\n"
                f"osac:\n- at '/operator/fabricDomainInventory/{self.hostname}':"
                " must be non-empty\n",
                "operator.fabricDomainInventory: violates the values schema",
            ),
            (
                "Error: values don't meet the specifications of the schema(s)"
                " in the following chart(s):\n"
                f"osac:\n- operator.fabricDomainInventory.{self.hostname}:"
                " must be non-empty\n",
                "operator.fabricDomainInventory: violates the values schema",
            ),
            (
                "Error: values don't meet the specifications of the schema(s)"
                " in the following chart(s):\n"
                f"osac:\n- operator.vendorControllers.{self.hostname}:"
                " must be non-empty\n",
                "operator.vendorControllers: violates the values schema",
            ),
            (
                "Error: values don't meet the specifications of the schema(s)"
                " in the following chart(s):\n"
                "osac:\n- at '/operator/vendorControllers/"
                f"{self.hostname}/endpoint': got string, want integer\n",
                "operator.vendorControllers: violates the values schema",
            ),
            (
                "Error: values don't meet the specifications of the schema(s)"
                " in the following chart(s):\n"
                "osac:\n- at '/operator/networkManagers/k8sManagers/"
                f"{self.hostname}/endpoint': got string, want integer\n",
                "operator.networkManagers.k8sManagers: violates the"
                " values schema",
            ),
            (
                "Error: values don't meet the specifications of the schema(s)"
                " in the following chart(s):\n"
                "osac:\n- operator.stall.workersJoiningByInstanceType."
                f"{self.hostname}: Invalid type\n",
                "operator.stall.workersJoiningByInstanceType: violates the"
                " values schema",
            ),
            (
                "Error: values don't meet the specifications of the schema(s)"
                " in the following chart(s):\n"
                "osac:\n- metering.serviceMonitor.labels."
                f"{self.hostname}: Invalid type. Expected: string\n",
                "metering.serviceMonitor.labels: violates the values schema",
            ),
            (
                "Error: values don't meet the specifications of the schema(s)"
                " in the following chart(s):\n"
                "osac:\n- at '/metering/serviceMonitor/labels/"
                f"{self.hostname}': got number, want string\n",
                "metering.serviceMonitor.labels: violates the values schema",
            ),
            (
                "Error: values don't meet the specifications of the schema(s)"
                " in the following chart(s):\n"
                "osac:\n- metering.prometheusRule.labels."
                f"{self.hostname}: Invalid type\n",
                "metering.prometheusRule.labels: violates the values schema",
            ),
            (
                "Error: values don't meet the specifications of the schema(s)"
                " in the following chart(s):\n"
                "osac:\n- preflight.skip."
                f"{self.hostname}: Invalid type\n",
                "preflight.skip: violates the values schema",
            ),
            (
                "Error: values don't meet the specifications of the schema(s)"
                " in the following chart(s):\n"
                "osac:\n- at '/aap/instanceGroups/networkFulfillment/config/"
                f"{self.hostname}': validation failed\n",
                "aap.instanceGroups.networkFulfillment.config: violates the"
                " values schema",
            ),
            (
                "Error: values don't meet the specifications of the schema(s)"
                " in the following chart(s):\n"
                "osac:\n- aap.instanceGroups.networkFulfillment.secret."
                f"{self.hostname}: Invalid type\n",
                "aap.instanceGroups.networkFulfillment.secret: violates the"
                " values schema",
            ),
        )
        for message, expected in cases:
            with self.subTest(expected=expected):
                result = subprocess.run(
                    ["bash", "-c", 'source "$1"; offline_render_error "$2"',
                     "bash", str(INSTALLER / "scripts/lib.sh"), message],
                    text=True, capture_output=True,
                )
                self.assertEqual(result.returncode, 0)
                self.assert_private(result.stdout + result.stderr)
                self.assertIn("offline chart rendering failed", result.stderr)
                self.assertIn(expected, result.stderr)
        # The three specific diagnostics still win over the path fallback.
        specific = subprocess.run(
            ["bash", "-c", 'source "$1"; offline_render_error "$2"',
             "bash", str(INSTALLER / "scripts/lib.sh"),
             "bmf.netbox.tokenValue: Invalid type: got number, want string"],
            text=True, capture_output=True,
        )
        self.assertIn("bmf.netbox.tokenValue: Invalid type; expected string",
                      specific.stderr)

    def test_offline_render_error_reports_added_schema_constraints(self):
        """Check the constraints this installer adds are named, value-free."""
        schema = (
            "Error: values don't meet the specifications of the schema(s)"
            " in the following chart(s):\n"
        )
        minimum = "must be an integer greater than or equal to 0"
        # The two object-level paths carry their type requirement too: a
        # scalar there fails the object type check, not the rule named
        # after it, and the path alone cannot say which one failed.
        nonroot = (
            "must be an object; runAsUser must not be 0 when runAsNonRoot"
            " is true"
        )
        unknown_key = "must be an object; unknown keys are not allowed"
        cases = (
            # Helm 3.18 prints the dotted path and its own minimum wording.
            (schema + "osac:\n- aap.bootstrap.backoffLimit:"
             " Must be greater than or equal to 0\n",
             "aap.bootstrap.backoffLimit: violates the values schema: "
             + minimum),
            # A rejected type uses the same static text: the field's rule is
            # "an integer zero or greater", and the path alone cannot say
            # which half failed.
            (schema + "osac:\n- aap.bootstrap.backoffLimit:"
             " Invalid type. Expected: integer, given: string\n",
             "aap.bootstrap.backoffLimit: violates the values schema: "
             + minimum),
            # Helm 3.22 and Helm 4 print a JSON pointer and a minimum.
            (schema + "osac:\n- at '/aap/bootstrap/backoffLimit':"
             " minimum: got -1, want 0\n",
             "aap/bootstrap/backoffLimit: violates the values schema: "
             + minimum),
            (schema + "osac:\n- at '/bundledVault/podSecurityContext/"
             "runAsUser': minimum: got -1, want 0\n",
             "bundledVault/podSecurityContext/runAsUser: violates the"
             " values schema: " + minimum),
            # The `not` rule: Helm 3.18 and Helm 3.22 wording and paths.
            (schema + "osac:\n- bundledVault.podSecurityContext:"
             " Must not validate the schema (not)\n",
             "bundledVault.podSecurityContext: violates the values schema: "
             + nonroot),
            (schema + "osac:\n- at '/bundledVault/podSecurityContext':"
             " 'not' failed\n",
             "bundledVault/podSecurityContext: violates the values schema: "
             + nonroot),
            # A scalar at either object path fails its type check, and the
            # same text covers both rules the path can break.
            (schema + "osac:\n- bundledVault.podSecurityContext:"
             " Invalid type. Expected: object, given: integer\n",
             "bundledVault.podSecurityContext: violates the values schema: "
             + nonroot),
            (schema + "osac:\n- global.fulfillmentTrust:"
             " Invalid type. Expected: object, given: integer\n",
             "global.fulfillmentTrust: violates the values schema: "
             + unknown_key),
            (schema + "osac:\n- global.fulfillmentTrust: Additional property"
             " unknownKey is not allowed\n",
             "global.fulfillmentTrust: violates the values schema: "
             + unknown_key),
        )
        for message, expected in cases:
            with self.subTest(expected=expected):
                result = subprocess.run(
                    ["bash", "-c", 'source "$1"; offline_render_error "$2"',
                     "bash", str(INSTALLER / "scripts/lib.sh"), message],
                    text=True, capture_output=True,
                )
                self.assertEqual(result.returncode, 0)
                self.assert_private(result.stdout + result.stderr)
                self.assertIn(expected, result.stderr)
        # The unknown-key rule hides the caller-supplied key itself, so a
        # key-shaped canary must not reach the output.
        message = (
            schema + "osac:\n- global.fulfillmentTrust: Additional property"
            f" {self.hostname} is not allowed\n"
        )
        result = subprocess.run(
            ["bash", "-c", 'source "$1"; offline_render_error "$2"',
             "bash", str(INSTALLER / "scripts/lib.sh"), message],
            text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 0)
        self.assert_private(result.stdout + result.stderr)
        self.assertIn(
            "global.fulfillmentTrust: violates the values schema: must be an"
            " object; unknown keys are not allowed", result.stderr)

    def test_schema_constraint_texts_match_the_schemas(self):
        """Check the constraints named in lib.sh still describe the schemas."""
        osac = json.loads(
            (INSTALLER / "charts/osac/values.schema.json").read_text())
        infra = json.loads(
            (INSTALLER / "charts/osac-infra/values.schema.json").read_text())
        backoff = osac["properties"]["aap"]["properties"]["bootstrap"][
            "properties"]["backoffLimit"]
        # The printed text covers both halves of the field's rule ("an
        # integer greater than or equal to 0"), so the schema must still
        # carry both the type and the minimum.
        self.assertEqual(backoff.get("type"), "integer")
        self.assertEqual(backoff.get("minimum"), 0)
        security = infra["properties"]["bundledVault"]["properties"][
            "podSecurityContext"]
        for field in ("runAsUser", "runAsGroup", "fsGroup"):
            self.assertEqual(
                security["properties"][field].get("type"), "integer")
            self.assertEqual(
                security["properties"][field].get("minimum"), 0)
        # The `not` rule rejects runAsUser 0 with runAsNonRoot true.
        rule = security["not"]["allOf"]
        self.assertEqual(rule[0]["properties"]["runAsUser"]["const"], 0)
        self.assertEqual(rule[1]["properties"]["runAsNonRoot"]["const"],
                         True)
        trust = osac["properties"]["global"]["properties"][
            "fulfillmentTrust"]
        self.assertIs(trust.get("additionalProperties"), False)

    def test_schema_paths_of_open_maps_are_masked(self):
        """Check every validating open map in the chart schemas is listed."""
        derived = set()

        def derive(schema):
            """Add the open-map roots found in one values schema to derived."""
            defs = schema.get("$defs", {})

            def resolve(node):
                """Follow local $defs references to the node they name."""
                for _ in range(10):
                    if not (isinstance(node, dict) and "$ref" in node):
                        break
                    ref = node["$ref"]
                    if not ref.startswith("#/$defs/"):
                        break
                    node = defs.get(ref.split("/")[-1], {})
                return node

            def walk(node, prefix):
                """Walk one schema node, recording every validating open map."""
                node = resolve(node)
                if not isinstance(node, dict):
                    return
                additional = node.get("additionalProperties")
                # An open map that validates its unknown entries echoes the
                # entry key in a schema error path; one that accepts any
                # entry validates none, so no key can reach a path.
                if isinstance(additional, dict) or node.get("patternProperties"):
                    derived.add(".".join(prefix))
                for key, child in (node.get("properties") or {}).items():
                    walk(child, prefix + [key])
                if isinstance(additional, dict):
                    walk(additional, prefix + ["<key>"])
                if isinstance(node.get("items"), dict):
                    walk(node["items"], prefix + ["<item>"])
                for keyword in ("allOf", "anyOf", "oneOf"):
                    for child in node.get(keyword) or []:
                        walk(child, prefix)

            walk(schema, [])

        for path in sorted((INSTALLER / "charts").glob("*/values.schema.json")):
            derive(json.loads(path.read_text()))

        # Helm validates each dependency's own values against its own schema
        # and reports a failure there with a subchart-relative path, so the
        # schemas vendored in the dependency archives contribute their
        # open-map roots too.
        archives = sorted((INSTALLER / "charts").glob("*/charts/*.tgz"))
        self.assertTrue(
            archives,
            "chart dependency archives are missing; run"
            " `make helm-deps helm-infra-deps` first")

        def scan_archive(data):
            """Derive open-map roots from the schemas in one chart archive."""
            with tarfile.open(fileobj=io.BytesIO(data)) as archive:
                for member in archive.getmembers():
                    if not member.isfile():
                        continue
                    payload = archive.extractfile(member).read()
                    if member.name.endswith("/values.schema.json"):
                        derive(json.loads(payload))
                    elif member.name.endswith(".tgz"):
                        scan_archive(payload)

        for archive in archives:
            scan_archive(archive.read_bytes())

        listed = subprocess.run(
            ["bash", "-c",
             'source "$1"; printf "%s\\n" "${_LIB_OPEN_SCHEMA_MAPS[@]}"',
             "bash", str(INSTALLER / "scripts/lib.sh")],
            text=True, capture_output=True, check=True,
        ).stdout.split()
        self.assertEqual(sorted(derived), sorted(listed))

    def assert_real_schema_error_is_private(self, helm):
        """Feed a real Helm schema failure through the private gate."""
        chart = self.directory / "schema-chart"
        (chart / "templates").mkdir(parents=True)
        (chart / "Chart.yaml").write_text(
            "apiVersion: v2\nname: schema-chart\nversion: 0.1.0\n")
        (chart / "values.yaml").write_text("password: 1\n")
        (chart / "values.schema.json").write_text(json.dumps({
            "type": "object",
            "properties": {"password": {"type": "integer"}},
            "required": ["password"],
        }))
        (chart / "templates" / "probe.yaml").write_text(
            "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: probe\n")
        render = subprocess.run(
            [helm, "template", "release", str(chart),
             "--set", f"password={self.password}"],
            text=True, capture_output=True,
        )
        self.assertNotEqual(render.returncode, 0)
        self.assertIn("values don", render.stderr)
        result = subprocess.run(
            ["bash", "-c", 'source "$1"; offline_render_error "$2"',
             "bash", str(INSTALLER / "scripts/lib.sh"), render.stderr],
            text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 0)
        self.assert_private(result.stdout + result.stderr)
        self.assertIn("offline chart rendering failed", result.stderr)
        self.assertIn("password: violates the values schema", result.stderr)
        # A caller-named key in a validating open map is part of the path
        # Helm echoes, so it must be reported as the map root instead.
        (chart / "values.schema.json").write_text(json.dumps({
            "type": "object",
            "properties": {
                "password": {"type": "integer"},
                "metering": {"type": "object", "properties": {
                    "serviceMonitor": {"type": "object", "properties": {
                        "labels": {"type": "object", "additionalProperties": {
                            "type": "string"}}}}}},
            },
        }))
        values = self.directory / "schema-values.json"
        values.write_text(json.dumps(
            {"password": 1, "metering": {"serviceMonitor": {
                "labels": {self.hostname: 1}}}}))
        render = subprocess.run(
            [helm, "template", "release", str(chart), "-f", str(values)],
            text=True, capture_output=True,
        )
        self.assertNotEqual(render.returncode, 0)
        self.assertIn(self.hostname, render.stderr)
        result = subprocess.run(
            ["bash", "-c", 'source "$1"; offline_render_error "$2"',
             "bash", str(INSTALLER / "scripts/lib.sh"), render.stderr],
            text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 0)
        self.assert_private(result.stdout + result.stderr)
        self.assertIn("metering.serviceMonitor.labels: violates the values"
                      " schema", result.stderr)
        # Helm validates a dependency's own values against that dependency's
        # schema and reports the failure with a subchart-relative path, so a
        # caller-named key in an open map of a dependency schema must be
        # masked the same way.
        subchart = chart / "charts" / "dependency"
        (subchart / "templates").mkdir(parents=True)
        (subchart / "Chart.yaml").write_text(
            "apiVersion: v2\nname: dependency\nversion: 0.1.0\n")
        (subchart / "values.schema.json").write_text(json.dumps({
            "type": "object",
            "properties": {"apiTokenPeppers": {
                "type": "object",
                "additionalProperties": {"type": "integer"}}},
        }))
        values.write_text(json.dumps(
            {"dependency": {"apiTokenPeppers": {self.hostname: "not-an-int"}}}))
        render = subprocess.run(
            [helm, "template", "release", str(chart), "-f", str(values)],
            text=True, capture_output=True,
        )
        self.assertNotEqual(render.returncode, 0)
        self.assertIn(self.hostname, render.stderr)
        result = subprocess.run(
            ["bash", "-c", 'source "$1"; offline_render_error "$2"',
             "bash", str(INSTALLER / "scripts/lib.sh"), render.stderr],
            text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 0)
        self.assert_private(result.stdout + result.stderr)
        self.assertIn("apiTokenPeppers: violates the values schema", result.stderr)

    @unittest.skipUnless(HELM, "helm 3 is not installed")

    def test_offline_render_error_real_helm3_schema_error_is_private(self):
        """Check a real Helm 3 schema failure names the path, not the value."""
        self.assert_real_schema_error_is_private(HELM)

    @unittest.skipUnless(HELM4, "helm 4 is not installed")

    def test_offline_render_error_real_helm4_schema_error_is_private(self):
        """Check a real Helm 4 schema failure names the path, not the value."""
        self.assert_real_schema_error_is_private(HELM4)

    def test_make_gate_real_aap_error_is_private(self):
        """Check the real AAP render failure through the make gate stays private."""
        self.assertIsNotNone(HELM, "helm is not installed")
        # The gate calls the helm on PATH, so select the same binary this
        # test resolved through HELM_BIN instead of whichever helm the
        # ambient PATH happens to hold.
        helm_dir = self.directory / "helm-bin"
        helm_dir.mkdir()
        (helm_dir / "helm").symlink_to(Path(HELM).resolve())
        values = self.directory / "osac.json"
        values.write_text(json.dumps({"aap": {"instanceGroups": {"networkFulfillment": {
            "config": {"NETRIS_CONTROLLER_URL": self.url}}}}}))
        result = subprocess.run(
            ["make", "--no-print-directory", "-C", str(INSTALLER),
             "-o", "helm-osac-deps", "-o", "helm-infra-deps", "validate-inputs",
             "PLATFORM=kind", "PROFILE=dev", "NS=osac", f"INSTANCE_VALUES_EXTRA=-f {values}"],
            env={**os.environ,
                 "PATH": f"{helm_dir}{os.pathsep}{os.environ.get('PATH', '')}"},
            text=True, capture_output=True, timeout=60,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assert_private(result.stdout + result.stderr)
        self.assertIn("offline chart rendering failed", result.stderr)
        # The gate renders osac-infra before the osac chart, so an unrelated
        # infra failure would also exit nonzero with the generic line. Require
        # the OSAC-stage failure itself, whose fixed message names the field
        # and still withholds the value.
        self.assertIn(
            "instanceGroups.networkFulfillment.config.NETRIS_CONTROLLER_URL"
            " must be an https:// URL",
            result.stderr,
        )


if __name__ == "__main__":
    unittest.main()
