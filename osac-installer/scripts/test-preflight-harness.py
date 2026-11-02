#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["jsonschema==4.23.0", "pyyaml==6.0.2"]
# ///
"""Unit regressions for the Kind preflight harness; no cluster required.

Run: uv run --script scripts/test-preflight-harness.py
"""

from __future__ import annotations

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

    def response(self, document):
        return subprocess.CompletedProcess([], 0, json.dumps(document), "")

    def established(self):
        return self.response({"status": {"conditions": [{"type": "Established", "status": "True"}]}})

    def test_pending_states_then_established(self):
        self.run.side_effect = [
            self.response({}),
            self.response({"status": None}),
            self.response({"status": {}}),
            self.response({"status": {"conditions": None}}),
            self.response({"status": {"conditions": []}}),
            self.response({"status": {"conditions": [{"type": "Established", "status": "False"}]}}),
            self.response({"status": {"conditions": [{"type": "Established", "status": "Unknown"}]}}),
            self.response({"status": {"conditions": [{"type": "NamesAccepted", "status": "True"}]}}),
            self.established(),
        ]
        self.cluster.wait_for_crd("test.example.com")
        self.assertEqual(self.run.call_count, 9)
        self.assertEqual(self.sleep.call_count, 8)
        for index, call in enumerate(self.run.call_args_list):
            self.assertEqual(call.kwargs["timeout"], 60 - index)
            self.assertIn(f"--request-timeout={(60 - index) * 1_000_000_000}ns", call.args[0])
            self.assertEqual(call.args[0][:4], ("kubectl", "--context", "kind-test", "get"))

    def test_permanent_command_errors_are_not_retried(self):
        for message in ("Forbidden", "NotFound", "connection refused"):
            with self.subTest(message=message):
                self.run.return_value = subprocess.CompletedProcess([], 1, "api stdout", message)
                with self.assertRaisesRegex(AssertionError, f"api stdout.*\\n{message}"):
                    self.cluster.wait_for_crd("test.example.com")
        self.assertEqual(self.run.call_count, 3)
        self.sleep.assert_not_called()

    def test_invalid_json_is_not_retried(self):
        self.run.return_value = subprocess.CompletedProcess([], 0, "not JSON", "")
        with self.assertRaises(json.JSONDecodeError):
            self.cluster.wait_for_crd("test.example.com")
        self.run.assert_called_once()

    def test_one_deadline_even_when_requests_take_time(self):
        def pending(*args, **kwargs):
            self.assertLessEqual(kwargs["timeout"], 60 - self.now)
            self.advance(min(10, kwargs["timeout"]))
            return self.response({"status": {"conditions": [{"type": "Established", "status": "False"}]}})

        self.run.side_effect = pending
        with self.assertRaisesRegex(AssertionError, "timed out.*test.example.com.*60s"):
            self.cluster.wait_for_crd("test.example.com")
        self.assertEqual(self.now, 60)
        self.assertEqual([call.kwargs["timeout"] for call in self.run.call_args_list], [60, 49, 38, 27, 16, 5])

    def test_request_timeout_is_not_retried(self):
        error = subprocess.TimeoutExpired("kubectl", 60, output="partial stdout", stderr="partial stderr")
        self.run.side_effect = error
        with self.assertRaises(subprocess.TimeoutExpired) as caught:
            self.cluster.wait_for_crd("test.example.com")
        self.assertIs(caught.exception, error)
        self.run.assert_called_once()

    def test_late_established_response_does_not_pass(self):
        def late(*args, **kwargs):
            self.advance(60)
            return self.established()

        self.run.side_effect = late
        with self.assertRaisesRegex(AssertionError, "timed out"):
            self.cluster.wait_for_crd("test.example.com")


class RestorationTests(unittest.TestCase):
    def setUp(self):
        self.cluster = Mock(spec=preflight.Cluster)
        self.cluster.ns = "test"
        self.cluster.kubectl.return_value = subprocess.CompletedProcess([], 0, "node", "")
        self.proc = subprocess.CompletedProcess([], 1, "helm stdout", "helm stderr")
        self.cluster.install.return_value = self.proc
        self.cluster.releases.return_value = []
        self.cluster.report_configmap.return_value = None
        self.enterContext(patch.object(preflight, "default_storageclass", return_value="standard"))
        self.enterContext(patch.object(preflight, "set_default_storageclass"))
        self.enterContext(patch.object(preflight, "create_namespace_prerequisites"))
        self.report = self.enterContext(patch.object(preflight, "failure_report"))
        self.report.return_value = {
            "results": [
                {"id": name, "blocking": True}
                for name in ("default-storageclass", "nodes-schedulable", "controller-credentials", "cert-manager-crds")
            ]
            + [{"id": "cert-issuer-ready", "blocking": False, "status": "unknown", "dependsOn": ["cert-manager-crds"]}]
        }
        self.proc.stderr = (
            "Nothing was changed; oc patch storageclass; Uncordon a node; "
            "Install the cert-manager Operator; Create the controller credential Secret; helm stderr"
        )

    def test_original_exception_survives_restoration_failure(self):
        original = AssertionError("original case failure")
        self.report.side_effect = original
        self.cluster.wait_for_crd.side_effect = AssertionError("restoration failed")
        with self.assertRaises(AssertionError) as caught:
            preflight.case_fresh_install_lists_every_fault_once(self.cluster)
        self.assertIs(caught.exception, original)
        notes = "\n".join(original.__notes__)
        for text in ("helm stdout", "helm stderr", "restoration failed"):
            self.assertIn(text, notes)

    def test_cleanup_only_failure_fails_case(self):
        cleanup = AssertionError("restoration failed")
        self.cluster.wait_for_crd.side_effect = cleanup
        with self.assertRaises(AssertionError) as caught:
            preflight.case_fresh_install_lists_every_fault_once(self.cluster)
        self.assertIs(caught.exception, cleanup)
        self.assertIn("helm stdout", "\n".join(cleanup.__notes__))

    def test_case_assertions_are_preserved_before_cleanup(self):
        self.cluster.releases.return_value = ["unexpected release"]
        self.cluster.wait_for_crd.side_effect = AssertionError("restoration failed")
        with self.assertRaisesRegex(AssertionError, "must not leave a release") as caught:
            preflight.case_fresh_install_lists_every_fault_once(self.cluster)
        self.assertIn("restoration failed", "\n".join(caught.exception.__notes__))

    def test_runner_prints_exception_notes(self):
        original = AssertionError("original case failure")
        self.report.side_effect = original
        self.cluster.wait_for_crd.side_effect = AssertionError("restoration failed")
        self.cluster.run.return_value.stdout = "test helm"
        output = io.StringIO()
        with (
            patch.object(preflight, "Cluster", return_value=self.cluster),
            patch.object(preflight, "create_prerequisites"),
            patch.object(preflight, "cleanup"),
            patch.object(preflight, "CASES", [preflight.case_fresh_install_lists_every_fault_once]),
            patch.object(preflight.sys, "argv", [str(SCRIPT), "--context", "kind-test"]),
            patch.object(preflight.sys, "stdout", output),
        ):
            self.assertEqual(preflight.main(), 1)
        for text in ("original case failure", "helm stdout", "helm stderr", "restoration failed", "0/1 cases passed"):
            self.assertIn(text, output.getvalue())


if __name__ == "__main__":
    unittest.main()
