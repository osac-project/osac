#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["jsonschema==4.23.0", "pyyaml==6.0.2"]
# ///
"""Unit regressions for the Kind preflight harness; no cluster required.

Run: uv run --script scripts/test-preflight-harness.py
"""

import importlib.util
import io
import json
import pathlib
import subprocess
import unittest
from unittest.mock import Mock, patch

SCRIPT = pathlib.Path(__file__).with_name("test-preflight-kind.py")
spec = importlib.util.spec_from_file_location("preflight_kind", SCRIPT)
preflight = importlib.util.module_from_spec(spec)
spec.loader.exec_module(preflight)
READY = {"status": {"conditions": [{"type": "Established", "status": "True"}]}}
FAULTS = ("default-storageclass", "nodes-schedulable", "controller-credentials", "cert-manager-crds")


class CrdReadinessTests(unittest.TestCase):
    def setUp(self):
        self.cluster = object.__new__(preflight.Cluster)
        self.cluster.context = "kind-test"
        self.now = 0.0
        self.enterContext(patch.object(preflight.time, "monotonic", side_effect=lambda: self.now))
        self.sleep = self.enterContext(patch.object(preflight.time, "sleep", side_effect=self.advance))
        self.run = self.enterContext(patch.object(preflight.subprocess, "run"))

    def advance(self, seconds):
        self.now += seconds

    def test_pending_then_established(self):
        pending = [
            {},
            {"status": None},
            {"status": {}},
            {"status": {"conditions": None}},
            {"status": {"conditions": []}},
        ]
        pending += [
            {"status": {"conditions": [{"type": "Established", "status": status}]}} for status in ("False", "Unknown")
        ]
        pending += [{"status": {"conditions": [{"type": "NamesAccepted", "status": "True"}]}}]
        self.run.side_effect = [subprocess.CompletedProcess([], 0, json.dumps(doc), "") for doc in [*pending, READY]]
        self.cluster.wait_for_crd("test.example.com")
        self.assertEqual(self.run.call_count, len(pending) + 1)
        self.assertEqual(self.sleep.call_count, len(pending))
        for index, call in enumerate(self.run.call_args_list):
            self.assertEqual(call.kwargs["timeout"], 60 - index)
            self.assertEqual(
                call.args[0], ("kubectl", "--context", "kind-test", "get", "crd", "test.example.com", "-o", "json")
            )

    def test_errors_are_not_retried(self):
        timeout = subprocess.TimeoutExpired("kubectl", 60, output="partial stdout", stderr="partial stderr")
        outcomes = [
            (subprocess.CompletedProcess([], 1, "api stdout", message), AssertionError)
            for message in ("Forbidden", "NotFound", "connection refused")
        ]
        outcomes += [
            (subprocess.CompletedProcess([], 0, "not JSON", ""), json.JSONDecodeError),
            (timeout, subprocess.TimeoutExpired),
            (FileNotFoundError("kubectl"), FileNotFoundError),
        ]
        for outcome, error_type in outcomes:
            with self.subTest(outcome=outcome):
                self.run.reset_mock()
                self.run.side_effect = [outcome]
                with self.assertRaises(error_type) as caught:
                    self.cluster.wait_for_crd("test.example.com")
                if isinstance(outcome, Exception):
                    self.assertIs(caught.exception, outcome)
                elif outcome.returncode:
                    self.assertIn(outcome.stdout, str(caught.exception))
                    self.assertIn(outcome.stderr, str(caught.exception))
                self.run.assert_called_once()
                self.sleep.assert_not_called()

    def test_deadline_bounds_requests_sleep_and_late_success(self):
        for duration, late_success in ((10.25, False), (60, True), (59.75, False)):
            with self.subTest(duration=duration, late_success=late_success):
                self.now = 0
                self.run.reset_mock()

                def response(*args, **kwargs):
                    self.assertEqual(kwargs["timeout"], 60 - self.now)
                    self.advance(min(duration, kwargs["timeout"]))
                    doc = (
                        READY
                        if late_success
                        else {"status": {"conditions": [{"type": "Established", "status": "False"}]}}
                    )
                    return subprocess.CompletedProcess([], 0, json.dumps(doc), "")

                self.run.side_effect = response
                with self.assertRaisesRegex(AssertionError, "timed out.*test.example.com.*60s"):
                    self.cluster.wait_for_crd("test.example.com")
                self.assertEqual(self.now, 60)
                self.assertEqual(
                    [call.kwargs["timeout"] for call in self.run.call_args_list],
                    [60, 48.75, 37.5, 26.25, 15, 3.75] if duration == 10.25 else [60],
                )


class RestorationTests(unittest.TestCase):
    def test_runner_preserves_case_cleanup_and_helm_diagnostics(self):
        for scenario in ("case only", "cleanup only", "both", "assertion and cleanup"):
            with self.subTest(scenario=scenario):
                cluster = Mock(spec=preflight.Cluster, ns="test")
                cluster.kubectl.return_value = subprocess.CompletedProcess([], 0, "node", "")
                stderr = (
                    "helm stderr Nothing was changed; oc patch storageclass; Uncordon a node; "
                    "Install the cert-manager Operator; Create the controller credential Secret"
                )
                cluster.install.return_value = subprocess.CompletedProcess([], 1, "helm stdout", stderr)
                cluster.releases.return_value = ["unexpected release"] if scenario == "assertion and cleanup" else []
                cluster.report_configmap.return_value = None
                original, cleanup = AssertionError("original case failure"), AssertionError("restoration failed")
                cluster.wait_for_crd.side_effect = None if scenario == "case only" else cleanup
                report = Mock(side_effect=original if scenario in ("case only", "both") else None)
                issuer = dict(id="cert-issuer-ready", blocking=False, status="unknown", dependsOn=["cert-manager-crds"])
                report.return_value = {"results": [dict(id=name, blocking=True) for name in FAULTS] + [issuer]}
                output = io.StringIO()
                with (
                    patch.multiple(
                        preflight,
                        Cluster=Mock(return_value=cluster),
                        failure_report=report,
                        create_prerequisites=Mock(),
                        cleanup=Mock(),
                        default_storageclass=Mock(return_value="standard"),
                        CASES=[preflight.case_fresh_install_lists_every_fault_once],
                    ),
                    patch.object(preflight.sys, "argv", [str(SCRIPT), "--context", "kind-test"]),
                    patch.object(preflight.sys, "stdout", output),
                ):
                    self.assertEqual(preflight.main(), 1)
                rendered = output.getvalue()
                for text in ("Helm stdout:\nhelm stdout", "Helm stderr:\nhelm stderr", "0/1 cases passed"):
                    self.assertIn(text, rendered)
                if scenario in ("case only", "both"):
                    self.assertIn("original case failure", rendered)
                if scenario != "case only":
                    self.assertIn("restoration failed", rendered)
                if scenario == "both":
                    self.assertIs(cleanup.__context__, original)
                if scenario == "assertion and cleanup":
                    self.assertIn("must not leave a release", rendered)


if __name__ == "__main__":
    unittest.main()
