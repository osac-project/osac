"""Test extraction of the runtime CLI from a pinned service image."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


EXTRACT = Path(__file__).resolve().parents[1] / "extract-osac-cli.sh"
IMAGE_ID = "sha256:" + "a" * 64
DIGEST_IMAGE = "registry.example/service@sha256:" + "b" * 64


class ExtractCliTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.output = self.root / "osac"
        self.log = self.root / "podman.log"
        self.bin = self.root / "bin"
        self.bin.mkdir()
        podman = self.bin / "podman"
        podman.write_text(
            '#!/usr/bin/env bash\n'
            'printf "%s\\n" "$*" >> "$PODMAN_LOG"\n'
            'case "$1 $2" in\n'
            '  "image exists") [[ "$IMAGE_LOCAL" == 1 ]] ;;\n'
            '  "image inspect") printf "%s\\n" "$IMAGE_ID" ;;\n'
            '  "pull "*) exit 0 ;;\n'
            '  "create "*) printf "container-id\\n" ;;\n'
            '  "cp "*) [[ "$FAIL_COPY" != 1 ]] || exit 1; printf "test binary" > "$3" ;;\n'
            '  "rm "*) exit 0 ;;\n'
            '  *) exit 9 ;;\n'
            'esac\n'
        )
        podman.chmod(0o755)

    def run_extract(self, image, *, local=True, fail_copy=False, image_id=IMAGE_ID):
        env = os.environ.copy()
        env.update(
            PATH=f"{self.bin}:{env['PATH']}",
            PODMAN_LOG=str(self.log),
            IMAGE_LOCAL="1" if local else "0",
            IMAGE_ID=image_id.removeprefix("sha256:"),
            FAIL_COPY="1" if fail_copy else "0",
        )
        return subprocess.run(
            ["bash", str(EXTRACT), image, str(self.output)],
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )

    def test_local_tag_is_resolved_to_image_id(self):
        result = self.run_extract("localhost/service:ci")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.output.read_text(), "test binary")
        self.assertTrue(os.access(self.output, os.X_OK))
        self.assertIn(f"create --pull=never {IMAGE_ID}", self.log.read_text())
        self.assertIn("rm container-id", self.log.read_text())

    def test_missing_digest_image_is_pulled(self):
        result = self.run_extract(DIGEST_IMAGE, local=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"pull {DIGEST_IMAGE}", self.log.read_text())

    def test_missing_mutable_tag_is_not_pulled(self):
        result = self.run_extract("registry.example/service:latest", local=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.output.exists())
        self.assertNotIn("pull ", self.log.read_text())

    def test_copy_failure_cleans_temporary_container_and_file(self):
        result = self.run_extract(DIGEST_IMAGE, fail_copy=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.output.exists())
        self.assertFalse(list(self.root.glob("osac.*")))
        self.assertIn("rm container-id", self.log.read_text())

    def test_existing_output_is_not_replaced(self):
        self.output.write_text("existing")
        result = self.run_extract(DIGEST_IMAGE)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.output.read_text(), "existing")
        self.assertFalse(self.log.exists())

    def test_invalid_image_id_is_rejected(self):
        result = self.run_extract(DIGEST_IMAGE, image_id="invalid")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.output.exists())
        self.assertNotIn("create ", self.log.read_text())

    def test_existing_symlink_is_not_replaced(self):
        self.output.symlink_to(self.root / "missing")
        result = self.run_extract(DIGEST_IMAGE)
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(self.output.is_symlink())
        self.assertFalse(self.log.exists())


if __name__ == "__main__":
    unittest.main()
