#!/usr/bin/env python3
"""Exercise cluster-version seed schema, rendered payloads, and Bash error handling.

Helm renders the real hook in a dependency-free temporary chart. Runtime tests
execute its Bash against a curl double; they do not exercise a deployed API.
"""

from __future__ import annotations

import copy
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import yaml

INSTALLER = Path(__file__).resolve().parents[1]
CHART = INSTALLER / "charts/osac"
SOURCE = "oci://quay.io/rh_ee_rpiccoli/rhcos-bmi:4.22.0"
VERSION = {
    "version": "4.22.0",
    "image": "quay.io/openshift-release-dev/ocp-release:4.22.0-multi",
    "default": True,
    "diskImage": {"name": "rhcos-4-22", "sourceRef": SOURCE},
}


class SeedTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.chart = self.root / "chart"
        templates = self.chart / "templates"
        templates.mkdir(parents=True)
        (self.chart / "Chart.yaml").write_text("apiVersion: v2\nname: osac\nversion: 0.1.0\n")
        for name in ("values.yaml", "values.schema.json"):
            shutil.copyfile(CHART / name, self.chart / name)
        shutil.copyfile(CHART / "templates/hooks/seed-cluster-versions.yaml", templates / "seed.yaml")
        (templates / "_helpers.tpl").write_text(
            '{{- define "osac.labels" -}}app: osac{{- end -}}\n'
            '{{- define "osac.waitForFulfillment" -}}- name: wait\n  image: busybox{{- end -}}\n'
            '{{- define "osac.fulfillmentCurlTLS" -}}--cacert /etc/ca-bundle/bundle.pem{{- end -}}\n'
        )

    def render(self, versions: list[dict], *, enabled: bool = True) -> subprocess.CompletedProcess:
        values = self.root / "values.yaml"
        values.write_text(yaml.safe_dump({"clusterVersions": {"enabled": enabled, "versions": versions}}))
        return subprocess.run(
            [
                "helm",
                "template",
                "osac",
                str(self.chart),
                "--namespace",
                "osac-test",
                "-f",
                str(CHART / "ci/default-values.yaml"),
                "-f",
                str(values),
            ],
            capture_output=True,
            text=True,
            check=False,
        )

    def script(self, versions: list[dict]) -> str:
        result = self.render(versions)
        self.assertEqual(result.returncode, 0, result.stderr)
        job = yaml.safe_load(result.stdout)
        self.assertEqual(job["metadata"]["namespace"], "osac-test")
        pod = job["spec"]["template"]["spec"]
        self.assertEqual(pod["serviceAccountName"], "admin")
        container = pod["containers"][0]
        self.assertEqual(container["command"][:4], ["/bin/bash", "-euo", "pipefail", "-c"])
        return container["command"][-1]

    def run_seed(self, versions: list[dict], codes: list[int]) -> tuple[subprocess.CompletedProcess, list[dict]]:
        script = self.script(versions)
        token = self.root / "token"
        token.write_text("test-token")
        script = script.replace("/var/run/secrets/kubernetes.io/serviceaccount/token", str(token))
        script = script.replace("/tmp/seed-response.json", str(self.root / "response.json"))
        log = self.root / "requests.jsonl"
        curl = self.root / "curl"
        curl.write_text(
            f"#!{sys.executable}\n"
            "import json, os, pathlib, sys\n"
            "args = sys.argv[1:]\n"
            "log = pathlib.Path(os.environ['REQUEST_LOG'])\n"
            "count = len(log.read_text().splitlines()) if log.exists() else 0\n"
            "code = json.loads(os.environ['HTTP_CODES'])[count]\n"
            "payload = json.loads(args[args.index('--data-binary') + 1])\n"
            "request = {'url': args[-1], 'payload': payload, 'args': args}\n"
            "with log.open('a') as f: f.write(json.dumps(request) + '\\n')\n"
            "if code < 0: sys.exit(-code)\n"
            "pathlib.Path(args[args.index('-o') + 1]).write_text('test API response')\n"
            "print(code, end='')\n"
        )
        curl.chmod(0o755)
        result = subprocess.run(
            ["bash", "-euo", "pipefail", "-c", script],
            capture_output=True,
            text=True,
            check=False,
            env={
                **os.environ,
                "PATH": f"{self.root}:{os.environ['PATH']}",
                "REQUEST_LOG": str(log),
                "HTTP_CODES": json.dumps(codes),
            },
        )
        requests = [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []
        return result, requests

    def test_default_without_disk_image_is_rejected(self) -> None:
        version = copy.deepcopy(VERSION)
        del version["diskImage"]
        result = self.render([version])
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("diskImage", result.stderr)

    def test_invalid_disk_images_are_rejected(self) -> None:
        for disk_image in (
            None,
            {},
            {"name": "rhcos"},
            {"name": "", "sourceRef": SOURCE},
            {"name": "rhcos", "sourceRef": ""},
            {"name": "../rhcos", "sourceRef": SOURCE},
            {"name": "rhcos", "sourceRef": "quay.io/example/rhcos:4.22.0"},
            {"name": "rhcos", "sourceRef": SOURCE, "unexpected": True},
            {"name": "rhcos", "sourceRef": SOURCE, "architecture": ["ARCHITECTURE_AMD64", "ARCHITECTURE_AMD64"]},
            {"name": "rhcos", "sourceRef": SOURCE, "architecture": []},
            {"name": "rhcos", "sourceRef": SOURCE, "architecture": ["bogus"]},
        ):
            with self.subTest(disk_image=disk_image):
                version = {**VERSION, "diskImage": disk_image}
                self.assertNotEqual(self.render([version]).returncode, 0)

    def test_non_default_without_disk_image_is_allowed(self) -> None:
        version = {"version": "4.20.0", "image": "quay.io/example/release:4.20.0"}
        result, requests = self.run_seed([version], [201])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(requests), 1)
        self.assertNotIn("disk_image", requests[0]["payload"]["spec"])
        self.assertFalse(requests[0]["payload"]["spec"]["is_default"])

    def test_disk_image_is_created_before_default_version(self) -> None:
        result, requests = self.run_seed([VERSION], [201, 201])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual([r["url"].rsplit("/", 1)[-1] for r in requests], ["disk_images", "cluster_versions"])
        self.assertEqual(
            requests[0]["payload"],
            {
                "metadata": {"name": "rhcos-4-22", "tenant": "shared"},
                "spec": {
                    "source_type": "SOURCE_TYPE_REGISTRY",
                    "source_ref": SOURCE,
                    "guest_os_family": "GUEST_OS_FAMILY_LINUX",
                    "architecture": ["ARCHITECTURE_AMD64"],
                },
            },
        )
        self.assertEqual(requests[1]["payload"]["spec"]["disk_image"], {"name": "rhcos-4-22", "shared": True})
        self.assertTrue(requests[1]["payload"]["spec"]["is_default"])
        for request in requests:
            self.assertIn("--cacert", request["args"])
            self.assertIn("Authorization: Bearer test-token", request["args"])
            self.assertNotIn("-k", request["args"])

    def test_architecture_can_be_configured(self) -> None:
        version = copy.deepcopy(VERSION)
        version["diskImage"]["architecture"] = ["ARCHITECTURE_ARM64", "ARCHITECTURE_AMD64"]
        result, requests = self.run_seed([version], [201, 201])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(requests[0]["payload"]["spec"]["architecture"], version["diskImage"]["architecture"])

    def test_existing_records_are_idempotent(self) -> None:
        for codes in ([409, 201], [201, 409], [409, 409]):
            with self.subTest(codes=codes):
                (self.root / "requests.jsonl").unlink(missing_ok=True)
                result, requests = self.run_seed([VERSION], codes)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(len(requests), 2)

    def test_disk_image_error_stops_before_version(self) -> None:
        for code in (400, 401, 403, 500):
            with self.subTest(code=code):
                (self.root / "requests.jsonl").unlink(missing_ok=True)
                result, requests = self.run_seed([VERSION], [code])
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(len(requests), 1)
                self.assertIn("test API response", result.stderr)
                self.assertIn(f"HTTP {code}", result.stderr)

    def test_curl_failure_stops_seeding(self) -> None:
        result, requests = self.run_seed([VERSION], [-7])
        self.assertEqual(result.returncode, 7)
        self.assertEqual(len(requests), 1)

    def test_non_default_can_also_reference_disk_image(self) -> None:
        version = {**VERSION, "default": False}
        result, requests = self.run_seed([version], [201, 201])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(requests[1]["payload"]["spec"]["disk_image"], {"name": "rhcos-4-22", "shared": True})
        self.assertFalse(requests[1]["payload"]["spec"]["is_default"])

    def test_disabled_seeding_emits_no_job(self) -> None:
        result = self.render([], enabled=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), "")

    def test_version_error_is_not_swallowed(self) -> None:
        result, requests = self.run_seed([VERSION], [201, 400])
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(requests), 2)
        self.assertIn("test API response", result.stderr)

    def test_payload_is_not_expanded_by_shell(self) -> None:
        version = copy.deepcopy(VERSION)
        version["image"] = "quay.io/example/release:$HOME'$(id)"
        version["diskImage"]["sourceRef"] = "oci://quay.io/example/rhcos:$HOME'$(id)"
        result, requests = self.run_seed([version], [201, 201])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(requests[0]["payload"]["spec"]["source_ref"], version["diskImage"]["sourceRef"])
        self.assertEqual(requests[1]["payload"]["spec"]["image"], version["image"])

    def test_shipped_profiles_have_backed_defaults(self) -> None:
        for profile in ("dev", "caas-ci", "full-ci", "cudn-evpn-netris-test"):
            with self.subTest(profile=profile):
                values = yaml.safe_load((INSTALLER / "values" / profile / "instance.yaml").read_text())
                versions = values["clusterVersions"]["versions"]
                self.assertEqual(self.render(versions).returncode, 0)
                defaults = [v for v in versions if v.get("default")]
                self.assertEqual(len(defaults), 1)
                self.assertEqual(defaults[0]["version"], "4.22.0")
                self.assertEqual(defaults[0]["diskImage"]["sourceRef"], SOURCE)


if __name__ == "__main__":
    unittest.main(verbosity=2)
