#!/usr/bin/env python3

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


REPOSITORY_ROOT = Path(__file__).resolve().parent.parent
GENERATOR = REPOSITORY_ROOT / "tools" / "generate-site-index.py"


class GenerateSiteIndexTest(unittest.TestCase):
    def run_generator(self, presentations_dir: Path, output_path: Path):
        return subprocess.run(
            [sys.executable, GENERATOR, presentations_dir, output_path],
            check=False,
            capture_output=True,
            text=True,
        )

    def test_generates_cards_for_valid_marp_presentations_only(self):
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            presentations_dir = temporary_path / "presentations"
            presentations_dir.mkdir()
            (presentations_dir / "valid.md").write_text(
                "---\nmarp: true\ntitle: 'A & B'\ndescription: 'Use <care>'\n---\n"
            )
            (presentations_dir / "ignored.md").write_text(
                "---\nmarp: false\ntitle: Ignored\ndescription: Ignored\n---\n"
            )
            output_path = temporary_path / "index.html"

            result = self.run_generator(presentations_dir, output_path)

            self.assertEqual(result.returncode, 0, result.stderr)
            output = output_path.read_text()
            self.assertIn("<title>OSAC</title>", output)
            self.assertIn("<h1>OSAC</h1>", output)
            self.assertIn('href="presentations/valid.html"', output)
            self.assertIn("A &amp; B", output)
            self.assertIn("Use &lt;care&gt;", output)
            self.assertNotIn("presentations/ignored.html", output)

    def test_rejects_marp_presentations_without_required_metadata(self):
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            presentations_dir = temporary_path / "presentations"
            presentations_dir.mkdir()
            (presentations_dir / "incomplete.md").write_text(
                "---\nmarp: true\ntitle: Incomplete\n---\n"
            )
            output_path = temporary_path / "index.html"

            result = self.run_generator(presentations_dir, output_path)

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("incomplete.md is missing frontmatter: description", result.stderr)
            self.assertFalse(output_path.exists())

    def test_rejects_presentations_with_unclosed_frontmatter(self):
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            presentations_dir = temporary_path / "presentations"
            presentations_dir.mkdir()
            (presentations_dir / "unclosed.md").write_text(
                "---\nmarp: true\ntitle: Unclosed\n"
            )
            output_path = temporary_path / "index.html"

            result = self.run_generator(presentations_dir, output_path)

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("unclosed.md has an unclosed frontmatter block", result.stderr)
            self.assertFalse(output_path.exists())


if __name__ == "__main__":
    unittest.main()
