#!/usr/bin/env python3
"""Check the real umbrella schema and AAP storage-worker ConfigMap rendering."""

import json
import pathlib
import subprocess
import tempfile
import unittest

import yaml


INSTALLER = pathlib.Path(__file__).resolve().parents[1]
CHART = INSTALLER / "charts/osac"
KEY = "OSAC_STORAGE_BLOCK_FSTYPE"


def render(config):
    with tempfile.NamedTemporaryFile("w", suffix=".json") as extra:
        json.dump({"aap": {"instanceGroups": {"storageFulfillment": {"enabled": True, "config": config}}}}, extra)
        extra.flush()
        return subprocess.run(
            [
                "helm",
                "template",
                "osac",
                str(CHART),
                "--namespace",
                "osac",
                "--values",
                str(CHART / "ci/default-values.yaml"),
                "--values",
                extra.name,
            ],
            capture_output=True,
            text=True,
            check=False,
        )


class StorageFilesystemTest(unittest.TestCase):
    def test_default_and_opaque_override(self):
        for override, expected in (({}, "ext4"), ({KEY: " Opaque:FS/9 "}, " Opaque:FS/9 "), ({KEY: "   "}, "   ")):
            with self.subTest(override=override):
                result = render(override)
                self.assertEqual(result.returncode, 0, result.stderr)
                configs = [
                    doc
                    for doc in yaml.safe_load_all(result.stdout)
                    if doc and doc.get("kind") == "ConfigMap" and doc["metadata"]["name"] == "storage-operations-ig"
                ]
                self.assertEqual(len(configs), 1)
                self.assertEqual(configs[0]["data"][KEY], expected)

    def test_schema_rejects_non_string(self):
        result = render({KEY: 42})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(KEY, result.stderr)
        self.assertIn("string", result.stderr)


if __name__ == "__main__":
    unittest.main()
