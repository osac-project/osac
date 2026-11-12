#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["jsonschema==4.23.0", "pyyaml==6.0.2"]
# ///
"""Mocked harness behavior checks; the Kind suite validates the real API race.

Run: uv run pytest scripts/helm-validate/tests/test_preflight-harness.py
"""

import importlib.util
import io
import json
import pathlib
import subprocess
import unittest
from contextlib import redirect_stdout
from unittest.mock import Mock, patch

SCRIPT = pathlib.Path(__file__).resolve().parents[1] / "preflight-kind.py"
spec = importlib.util.spec_from_file_location("preflight_kind", SCRIPT)
preflight = importlib.util.module_from_spec(spec)
spec.loader.exec_module(preflight)
READY = {
    "metadata": {"generation": 2},
    "status": {"conditions": [{"type": "Established", "status": "True", "observedGeneration": 2}]},
}


def reply(document):
    return subprocess.CompletedProcess([], 0, json.dumps(document), "")


class CrdReadinessTests(unittest.TestCase):
    def setUp(self):
        self.cluster = object.__new__(preflight.Cluster)
        self.cluster.context = "kind-test"
        self.now = 0
        self.enterContext(patch.object(preflight.time, "monotonic", side_effect=lambda: self.now))
        self.enterContext(patch.object(preflight.time, "sleep", side_effect=self.advance))
        self.subprocess_run = self.enterContext(patch.object(preflight.subprocess, "run"))

    def advance(self, seconds):
        self.now += seconds

    def test_pending_then_established(self):
        """Missing, null, pending and stale conditions must not finish readiness."""
        documents = iter(
            [
                {},
                {"status": None},
                {"status": {"conditions": None}},
                {"status": {"conditions": []}},
                {"status": {"conditions": [{"type": "Established", "status": "False"}]}},
                {
                    "metadata": {"generation": 2},
                    "status": {"conditions": [{"type": "Established", "status": "True", "observedGeneration": 1}]},
                },
                READY,
            ]
        )
        observed = None

        def request(*args, **kwargs):
            nonlocal observed
            observed = next(documents)
            return reply(observed)

        self.subprocess_run.side_effect = request
        self.cluster.wait_for_crd("test.example.com")
        self.assertEqual(observed, READY)

    def test_deadline_bounds_requests_and_late_success(self):
        """Requests share the total budget; readiness at the deadline is too late."""
        for late_ready in (False, True):
            with self.subTest(late_ready=late_ready):
                self.now = 0

                def request(*args, **kwargs):
                    budget = kwargs["timeout"]
                    self.assertIsInstance(budget, (int, float))
                    self.assertGreater(budget, 0)
                    self.assertLessEqual(budget, 60 - self.now)
                    self.advance(min(20, budget))
                    return reply(READY if late_ready and self.now >= 60 else {})

                self.subprocess_run.side_effect = request
                with self.assertRaisesRegex(AssertionError, "timed out.*test.example.com"):
                    self.cluster.wait_for_crd("test.example.com")
                self.assertEqual(self.now, 60)

    def test_errors_are_not_retried(self):
        """Command and JSON errors remain visible instead of becoming retries."""
        for response, error in (
            (subprocess.CompletedProcess([], 1, "", "Forbidden"), AssertionError),
            (subprocess.CompletedProcess([], 0, "not JSON", ""), json.JSONDecodeError),
        ):
            with self.subTest(error=error):
                self.subprocess_run.reset_mock()
                self.subprocess_run.side_effect = [response]
                with self.assertRaises(error) as caught:
                    self.cluster.wait_for_crd("test.example.com")
                self.assertTrue(str(caught.exception))
                if response.returncode:
                    self.assertIn("Forbidden", str(caught.exception))
                self.subprocess_run.assert_called_once()


class RestorationTests(unittest.TestCase):
    def test_injection_failures_restore_fixture(self):
        """Attempt every restoration and retain useful original and cleanup diagnostics."""
        for stage in ("annotation", "cordon", "secret", "crd"):
            with self.subTest(stage=stage):
                restored = []

                def inject(label):
                    if label == stage:
                        raise ValueError(f"{stage} injection failed")

                def restore(label):
                    restored.append(label)
                    if stage == "annotation" and label == "StorageClass reset":
                        raise OSError("restoration failed")

                def kubectl(*args, **kwargs):
                    if args[0] == "uncordon":
                        restore("uncordon")
                    elif args[0] == "cordon":
                        inject("cordon")
                    elif args[:2] in (("delete", "secret"), ("delete", "crd")):
                        inject(args[1])
                    return subprocess.CompletedProcess([], 0, "node", "")

                cluster = Mock(spec=preflight.Cluster, ns="test")
                cluster.kubectl.side_effect = kubectl
                cluster.apply.side_effect = lambda manifest, **kwargs: restore("CRD apply")
                cluster.wait_for_crd.side_effect = lambda name: restore("CRD readiness")
                output = io.StringIO()
                with (
                    patch.object(preflight, "default_storageclass", return_value="standard"),
                    patch.object(
                        preflight,
                        "set_default_storageclass",
                        side_effect=lambda cluster, sc, value, **kwargs: (
                            restore("StorageClass reset") if value else inject("annotation")
                        ),
                    ),
                    patch.object(
                        preflight,
                        "create_namespace_prerequisites",
                        side_effect=lambda cluster, **kwargs: restore("prerequisite creation"),
                    ),
                    redirect_stdout(output),
                    self.assertRaises(Exception) as caught,
                ):
                    preflight.case_fresh_install_lists_every_fault_once(cluster)
                self.assertIsInstance(caught.exception, ValueError)
                self.assertIn(f"{stage} injection failed", str(caught.exception))
                self.assertEqual(restored, list(preflight.RESTORATION_LABELS))
                cluster.install.assert_not_called()
                if stage == "annotation":
                    self.assertIsInstance(caught.exception.__cause__, ExceptionGroup)
                    self.assertIn("StorageClass reset", str(caught.exception.__cause__))
                    self.assertIn("StorageClass reset", output.getvalue())

    def test_restoration_timeouts_preserve_original_failure(self):
        """Bound actual helper commands and continue restoration after timeouts."""
        cluster = object.__new__(preflight.Cluster)
        cluster.context, cluster.ns = "kind-test", "test"
        restoring = False
        attempted = []

        def run(cmd, **kwargs):
            nonlocal restoring
            if cmd[0] == "kubectl":
                operation = cmd[3]
                if restoring:
                    attempted.append(operation)
                    self.assertGreater(kwargs["timeout"], 0)
                    self.assertLessEqual(kwargs["timeout"], 60)
                    if operation != "get":
                        raise subprocess.TimeoutExpired(cmd, kwargs["timeout"])
                    return reply(READY)
                if operation == "cordon":
                    restoring = True
                    raise ValueError("cordon injection failed")
            return subprocess.CompletedProcess(cmd, 0, "node", "")

        output = io.StringIO()
        with (
            patch.object(preflight, "default_storageclass", return_value="standard"),
            patch.object(preflight.subprocess, "run", side_effect=run),
            redirect_stdout(output),
            self.assertRaisesRegex(ValueError, "cordon injection failed") as caught,
        ):
            preflight.case_fresh_install_lists_every_fault_once(cluster)
        self.assertEqual(attempted, ["uncordon", "annotate", "apply", "apply", "get"])
        cleanup = caught.exception.__cause__
        self.assertIsInstance(cleanup, ExceptionGroup)
        self.assertEqual([error.message for error in cleanup.exceptions], list(preflight.RESTORATION_LABELS[:-1]))
        for error in cleanup.exceptions:
            self.assertIsInstance(error.exceptions[0], subprocess.TimeoutExpired)
            self.assertIn(error.message, output.getvalue())
