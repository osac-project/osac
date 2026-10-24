"""Contract tests for atomic output publication."""

import os
import tempfile
import unittest
from unittest import mock
from pathlib import Path

import path_setup  # noqa: F401  (adds the package root to sys.path)

import coverage_report.publish as publish_module
from coverage_report.errors import PublishError
from coverage_report.publish import publish


class _Boom(Exception):
    pass


class TestPublish(unittest.TestCase):
    def _publish(self, output_dir, value, fault_hook=None):
        return publish(
            str(output_dir),
            "OSAC-2540",
            json_artifacts={"report.json": {"value": value}},
            text_artifacts={"report.md": "# v{0}\n".format(value)},
            fault_hook=fault_hook,
        )

    def test_publishes_expected_layout(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            target = self._publish(tmpdir, 1)

            self.assertTrue(os.path.isfile(os.path.join(target, "report.json")))
            self.assertTrue(os.path.isfile(os.path.join(target, "report.md")))
            self.assertEqual(
                Path(target, "report.md").read_text(encoding="utf-8"), "# v1\n"
            )

    def test_republish_replaces_previous_content(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            first = self._publish(tmpdir, 1)
            second = self._publish(tmpdir, 2)

            self.assertEqual(first, second)
            self.assertEqual(
                Path(second, "report.md").read_text(encoding="utf-8"), "# v2\n"
            )

    def test_failure_before_swap_keeps_the_previous_report(self):
        def hook(stage):
            if stage == "after_write":
                raise _Boom(stage)

        with tempfile.TemporaryDirectory() as tmpdir:
            target = self._publish(tmpdir, 1)
            with self.assertRaises(PublishError) as raised:
                self._publish(tmpdir, 2, fault_hook=hook)

            self.assertEqual(raised.exception.code, "PUBLISH_FAILED")
            self.assertEqual(
                Path(target, "report.md").read_text(encoding="utf-8"), "# v1\n"
            )
            self._assert_no_leftovers(tmpdir)

    def test_failure_after_backup_restores_the_previous_report(self):
        def hook(stage):
            if stage == "after_backup":
                raise _Boom(stage)

        with tempfile.TemporaryDirectory() as tmpdir:
            target = self._publish(tmpdir, 1)
            with self.assertRaises(PublishError):
                self._publish(tmpdir, 2, fault_hook=hook)

            self.assertEqual(
                Path(target, "report.md").read_text(encoding="utf-8"), "# v1\n"
            )
            self._assert_no_leftovers(tmpdir)

    def test_failure_after_swap_rolls_back_the_previous_report(self):
        def hook(stage):
            if stage == "after_swap":
                raise _Boom(stage)

        with tempfile.TemporaryDirectory() as tmpdir:
            target = self._publish(tmpdir, 1)
            with self.assertRaises(PublishError):
                self._publish(tmpdir, 2, fault_hook=hook)

            self.assertEqual(
                Path(target, "report.md").read_text(encoding="utf-8"), "# v1\n"
            )
            self._assert_no_leftovers(tmpdir)

    def test_failed_restore_keeps_the_backup_and_names_it(self):
        def hook(stage):
            if stage == "after_swap":
                raise _Boom(stage)

        with tempfile.TemporaryDirectory() as tmpdir:
            target = self._publish(tmpdir, 1)
            real_rename = os.rename

            def flaky_rename(source, destination):
                if os.path.basename(str(source)).startswith(".OSAC-2540.bak-"):
                    raise OSError("restore blocked")
                return real_rename(source, destination)

            with mock.patch.object(
                publish_module.os, "rename", side_effect=flaky_rename
            ):
                with self.assertRaises(PublishError) as raised:
                    self._publish(tmpdir, 2, fault_hook=hook)

            self.assertEqual(raised.exception.code, "PUBLISH_RESTORE_FAILED")
            backups = [
                name
                for name in os.listdir(tmpdir)
                if name.startswith(".OSAC-2540.bak-")
            ]
            self.assertEqual(len(backups), 1)
            backup_path = os.path.abspath(os.path.join(tmpdir, backups[0]))
            self.assertIn(backup_path, raised.exception.recovery)
            self.assertEqual(
                Path(backup_path, "report.md").read_text(encoding="utf-8"),
                "# v1\n",
            )
            self.assertFalse(os.path.exists(target))

    def test_failure_on_first_publish_leaves_no_target(self):
        def hook(stage):
            if stage == "after_backup":
                raise _Boom(stage)

        with tempfile.TemporaryDirectory() as tmpdir:
            with self.assertRaises(PublishError):
                self._publish(tmpdir, 1, fault_hook=hook)

            self.assertFalse(os.path.exists(os.path.join(tmpdir, "OSAC-2540")))
            self._assert_no_leftovers(tmpdir)

    def test_rejects_artifacts_that_escape_the_output_directory(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            with self.assertRaises(PublishError):
                publish(
                    tmpdir,
                    "OSAC-2540",
                    json_artifacts={},
                    text_artifacts={"../escape.md": "x"},
                )

    def _assert_no_leftovers(self, tmpdir):
        leftovers = [
            name
            for name in os.listdir(tmpdir)
            if name.startswith(".OSAC-2540.tmp-") or name.startswith(".OSAC-2540.bak-")
        ]
        self.assertEqual(leftovers, [])


if __name__ == "__main__":
    unittest.main()
