#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["jsonschema==4.23.0", "pyyaml==6.0.2"]
# ///
"""Unit regressions for bounded CRD readiness and localized restoration failures.

Run: uv run --script scripts/test-preflight-harness.py
"""

import importlib.util
import io
import json
import pathlib
import subprocess
import unittest
from contextlib import redirect_stdout
from unittest.mock import Mock, patch

SCRIPT = pathlib.Path(__file__).with_name("test-preflight-kind.py")
spec = importlib.util.spec_from_file_location("preflight_kind", SCRIPT)
preflight = importlib.util.module_from_spec(spec)
spec.loader.exec_module(preflight)
READY = {"status": {"conditions": [{"type": "Established", "status": "True"}]}}
FAULTS = ("default-storageclass", "nodes-schedulable", "controller-credentials", "cert-manager-crds")
SENTINEL = "fake-sensitive-payload"


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
        """Pending conditions and stale observations retry; compatible current conditions pass."""
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
        generations = [
            ({"generation": 2}, {}, {}, False),
            ({}, {"observedGeneration": 0}, {}, False),
            ({"generation": 2}, {"observedGeneration": 2}, {}, False),
            ({"generation": 2}, {"observedGeneration": 3}, {}, False),
            ({"generation": 2}, {"observedGeneration": 1}, {"observedGeneration": 3}, True),
            ({"generation": 2}, {"observedGeneration": 2}, {"observedGeneration": 1}, False),
            ({"generation": 2}, {"observedGeneration": 0}, {"observedGeneration": 3}, True),
            ({"generation": 2}, {}, {"observedGeneration": 1}, True),
            ({"generation": 2}, {}, {"observedGeneration": 2}, False),
        ]
        for invalid in (None, "2", 2.0, True, False, 2**63, -(2**63) - 1):
            generations += [
                ({"generation": 2}, {"observedGeneration": invalid}, {"observedGeneration": 1}, True),
                ({"generation": invalid}, {"observedGeneration": 0}, {}, False),
                ({"generation": 2}, {}, {"observedGeneration": invalid}, False),
            ]
        current = {
            "metadata": {"generation": 2},
            "status": {"conditions": [{"type": "Established", "status": "True", "observedGeneration": 2}]},
        }
        for metadata, condition, status, stale in generations:
            with self.subTest(metadata=metadata, condition=condition, status=status):
                self.now = 0
                self.run.reset_mock()
                self.sleep.reset_mock()
                document = {
                    "metadata": metadata,
                    "status": dict(status, conditions=[dict(condition, type="Established", status="True")]),
                }
                self.run.side_effect = [
                    subprocess.CompletedProcess([], 0, json.dumps(doc), "") for doc in (document, current)
                ]
                self.cluster.wait_for_crd("test.example.com")
                self.assertEqual(self.run.call_count, 2 if stale else 1)
                self.assertEqual(self.sleep.call_count, 1 if stale else 0)
                self.assertEqual(
                    [call.kwargs["timeout"] for call in self.run.call_args_list], [60, 59] if stale else [60]
                )

    def test_errors_are_not_retried(self):
        """API, parser and process errors propagate unchanged without another request."""
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
        """Slow fractional commands share one budget, cap sleeps and reject late readiness."""
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
    def test_every_restoration_is_attempted_and_failures_survive(self):
        """Each failed operation leaves later attempts intact and retains the original error."""
        failures = [(), *((label,) for label in preflight.RESTORATION_LABELS), preflight.RESTORATION_LABELS]
        for failed_labels in failures:
            for original_failure in (None, ValueError, AssertionError):
                with self.subTest(failed_labels=failed_labels, original_failure=original_failure):
                    self.check_restoration(failed_labels, original_failure)

    def check_restoration(self, failed_labels, original_failure):
        """Exercise the actual fault case with fixed, individually injectable restoration actions."""
        attempted = []
        errors = {label: OSError("cleanup command failed") for label in failed_labels}

        def restore(label):
            attempted.append(label)
            if label in errors:
                raise errors[label]

        cluster = Mock(spec=preflight.Cluster, ns="test")
        cluster.kubectl.side_effect = lambda *args, **kwargs: (
            restore("uncordon") if args[0] == "uncordon" else subprocess.CompletedProcess([], 0, "node", "")
        )
        cluster.install.return_value = subprocess.CompletedProcess(
            [],
            1,
            SENTINEL,
            "Nothing was changed; oc patch storageclass; Uncordon a node; "
            "Install the cert-manager Operator; Create the controller credential Secret",
        )
        cluster.releases.return_value = ["unexpected release"] if original_failure is AssertionError else []
        cluster.report_configmap.return_value = None
        cluster.apply.side_effect = lambda manifest: restore("CRD apply")
        cluster.wait_for_crd.side_effect = lambda name: restore("CRD readiness")
        original = ValueError("original case failure") if original_failure is ValueError else None
        issuer = dict(id="cert-issuer-ready", status="unknown", dependsOn=["cert-manager-crds"])
        report = {"results": [dict(id=name, blocking=True) for name in FAULTS] + [dict(issuer, blocking=False)]}
        output = io.StringIO()
        caught = []

        def case(cluster):
            try:
                preflight.case_fresh_install_lists_every_fault_once(cluster)
            except Exception as error:
                caught.append(error)
                raise

        cluster.run.return_value.stdout = "v3.22.0"
        with (
            patch.multiple(
                preflight,
                Cluster=Mock(return_value=cluster),
                CASES=[case],
                create_prerequisites=Mock(),
                cleanup=Mock(),
                default_storageclass=Mock(return_value="standard"),
                set_default_storageclass=Mock(
                    side_effect=lambda cluster, sc, value: restore("StorageClass reset") if value else None
                ),
                create_namespace_prerequisites=Mock(side_effect=lambda cluster: restore("prerequisite creation")),
                failure_report=Mock(side_effect=original, return_value=report),
            ),
            patch.object(preflight.sys, "argv", [str(SCRIPT), "--context", "kind-test"]),
            redirect_stdout(output),
        ):
            self.assertEqual(preflight.main(), 1 if failed_labels or original_failure else 0)
        self.assertIn("context=kind-test namespace=test helm=v3.22.0", output.getvalue())
        self.assertEqual(attempted, list(preflight.RESTORATION_LABELS))
        if original_failure is AssertionError:
            original = caught[0]
            self.assertIsInstance(original, AssertionError)
            self.assertIn("must not leave a release", str(original))
        if failed_labels:
            cleanup = caught[0].__cause__ if original_failure else caught[0]
            self.assertIsInstance(cleanup, ExceptionGroup)
            self.assertEqual([group.message for group in cleanup.exceptions], list(failed_labels))
            self.assertEqual([group.exceptions[0] for group in cleanup.exceptions], list(errors.values()))
            for label in failed_labels:
                self.assertIn(label, output.getvalue())
        if original_failure:
            self.assertIs(caught[0], original)
            self.assertIn(str(original), output.getvalue())
            self.assertIsNotNone(original.__traceback__)
            frame = original.__traceback__
            while frame.tb_next is not None:
                frame = frame.tb_next
            self.assertIn(
                frame.tb_frame.f_code.co_name, ("case_fresh_install_lists_every_fault_once", "_execute_mock_call")
            )
        if not failed_labels and not original_failure:
            self.assertEqual(caught, [])
        self.assertNotIn(SENTINEL, output.getvalue())


if __name__ == "__main__":
    unittest.main()
