"""Exercise the real uv bootstrap with local archives and command shims."""

import hashlib
import io
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import textwrap
import unittest
from pathlib import Path

INSTALLER = Path(__file__).resolve().parents[1] / "actions/setup-python/install-uv.sh"
CHECKSUM = "f29c3e94ffabd7e6f3ba521dba169a9b7ec12100bf3257087f0f24f6123f3645"
UV_FIXTURE = r'''#!/bin/sh
case "$1" in
  python) ;;
  venv)
    mkdir -p "$HOME/.setup-python-venv/bin"
    for tool in python python3 pip; do
      printf '#!/bin/sh\necho "venv %s"\n' "$tool" > "$HOME/.setup-python-venv/bin/$tool"
      chmod +x "$HOME/.setup-python-venv/bin/$tool"
    done ;;
  tool)
    printf '#!/bin/sh\necho "ruff fixture"\n' > "$HOME/.local/bin/ruff"
    chmod +x "$HOME/.local/bin/ruff" ;;
  *) echo 'uv fixture' ;;
esac
'''
SHIM = r'''
import json
import os
import shutil
import sys
from pathlib import Path

command = Path(sys.argv[0]).name
arguments = sys.argv[1:]
log = Path(os.environ["COMMAND_LOG"])
calls = [json.loads(line) for line in log.read_text().splitlines()]
with log.open("a") as output:
    output.write(json.dumps([command, *arguments]) + "\n")
if command == "uname":
    print(os.environ.get("TEST_OS", "Linux") if arguments == ["-s"] else os.environ.get("TEST_ARCH", "x86_64"))
elif command == "curl":
    attempt = 1 + sum(call[0] == "curl" for call in calls)
    destination = arguments[arguments.index("--output") + 1]
    if attempt <= int(os.environ.get("FAILURES", "0")):
        Path(destination).write_bytes(b"partial download")
        sys.exit(int(os.environ.get("CURL_EXIT", "28")))
    shutil.copyfile(os.environ["ARCHIVE"], destination)
elif command == "tar" and os.environ.get("FAIL_TAR"):
    sys.exit(2)
elif command != "sleep":
    os.execv(os.environ["REAL_" + command.upper()], [command, *arguments])
'''


class SetupUvTests(unittest.TestCase):
    def setUp(self):
        """Build a local archive and replace only its hash in a test copy."""
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.runner = self.root / "runner"
        self.runner.mkdir()
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.log = self.root / "commands.jsonl"
        self.log.touch()
        self.github_path = self.root / "github-path"
        self.github_path.touch()
        self.archive = self.root / "uv.tar.gz"
        with tarfile.open(self.archive, "w:gz") as archive:
            for name in ("uv", "uvx"):
                content = (UV_FIXTURE if name == "uv" else f"#!/bin/sh\necho '{name} fixture'\n").encode()
                member = tarfile.TarInfo(f"uv-x86_64-unknown-linux-gnu/{name}")
                member.size = len(content)
                member.mode = 0o755
                archive.addfile(member, io.BytesIO(content))
        self.installer = self.root / "install-uv.sh"
        source = INSTALLER.read_text()
        self.assertEqual(source.count(CHECKSUM), 1)
        self.installer.write_text(source.replace(CHECKSUM, hashlib.sha256(self.archive.read_bytes()).hexdigest()))
        self.env = {
            **os.environ,
            "PATH": f"{self.bin}:{os.environ['PATH']}",
            "RUNNER_TEMP": str(self.runner),
            "GITHUB_PATH": str(self.github_path),
            "COMMAND_LOG": str(self.log),
            "ARCHIVE": str(self.archive),
        }
        for command in ("uname", "curl", "sleep", "tar", "sha256sum"):
            self.env["REAL_" + command.upper()] = shutil.which(command)
            shim = self.bin / command
            shim.write_text(f"#!{sys.executable}\n{SHIM}")
            shim.chmod(0o755)

    def run_installer(self, **environment):
        result = subprocess.run(
            ["bash", str(self.installer)], env={**self.env, **environment}, capture_output=True, text=True, timeout=10
        )
        self.assertFalse(list(self.runner.glob("osac-uv-download.*")))
        return result

    def calls(self, command):
        return [call[1:] for call in map(json.loads, self.log.read_text().splitlines()) if call[0] == command]

    def assert_failure(self, result):
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.github_path.read_text(), "")

    def test_fast_success_and_installed_path_lifetime(self):
        result = self.run_installer()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.calls("curl")), 1)
        self.assertEqual(self.calls("sleep"), [])
        installed = Path(self.github_path.read_text().splitlines()[-1])
        for name in ("uv", "uvx"):
            result = subprocess.run([str(installed / name)], capture_output=True, text=True, check=True)
            self.assertEqual(result.stdout.strip(), f"{name} fixture")
        order = [json.loads(line)[0] for line in self.log.read_text().splitlines()]
        self.assertLess(order.index("sha256sum"), order.index("tar"))

    def test_retry_recovers_and_replaces_partial_download(self):
        result = self.run_installer(FAILURES="1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.calls("curl")), 2)
        self.assertEqual(self.calls("sleep"), [["1"]])

    def test_composite_keeps_verified_uv_ahead_of_existing_local_tools(self):
        """Run all composite shell steps with the runner's PATH-file ordering."""
        home = self.root / "home"
        local_bin = home / ".local/bin"
        local_bin.mkdir(parents=True)
        for name in ("uv", "uvx"):
            stale = local_bin / name
            stale.write_text(f"#!/bin/sh\necho 'untrusted {name}' >&2\nexit 97\n")
            stale.chmod(0o755)
        action = INSTALLER.with_name("action.yaml").read_text()
        steps = re.findall(r"^    run: (.*(?:\n      .*)*)", action, re.MULTILINE)
        self.assertEqual(len(steps), 3)
        env = {**self.env, "HOME": str(home), "ACTION_PATH": str(self.installer.parent)}
        original_path = env["PATH"]
        prepend_paths = []
        for step in steps:
            script = textwrap.dedent(step[2:]) if step.startswith("|\n") else step
            result = subprocess.run(["bash", "-e", "-c", script], env=env, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
            for entry in self.github_path.read_text().splitlines():
                if entry in prepend_paths:
                    prepend_paths.remove(entry)
                prepend_paths.append(entry)
            self.github_path.write_text("")
            env["PATH"] = os.pathsep.join([*reversed(prepend_paths), original_path])
        for name, expected in (
            ("uv", "uv fixture"), ("uvx", "uvx fixture"),
            ("python", "venv python"), ("python3", "venv python3"),
            ("pip", "venv pip"), ("ruff", "ruff fixture"),
        ):
            result = subprocess.run([name, "--version"], env=env, capture_output=True, text=True, check=True)
            self.assertEqual(result.stdout.strip(), expected)
        self.assertFalse(list(self.runner.glob("osac-uv-download.*")))

    def test_persistent_failures_stop_after_one_retry(self):
        for status in (22, 28, 35):
            with self.subTest(curl_exit=status):
                self.log.write_text("")
                result = self.run_installer(FAILURES="9", CURL_EXIT=str(status))
                self.assert_failure(result)
                self.assertEqual(result.returncode, status)
                self.assertIn(f"after 2 attempts (curl exit {status})", result.stderr)
                self.assertEqual(len(self.calls("curl")), 2)
                self.assertEqual(self.calls("sleep"), [["1"]])
                self.assertEqual(self.calls("tar"), [])

    def test_transfer_limits_and_no_nested_retry(self):
        self.assertEqual(self.run_installer().returncode, 0)
        arguments = self.calls("curl")[0]
        self.assertEqual(arguments[0], "--disable")
        for option, value in (
            ("--max-time", "15"),
            ("--max-redirs", "1"),
            ("--proto", "=https"),
            ("--proto-redir", "=https"),
        ):
            self.assertEqual(arguments[arguments.index(option) + 1], value)
        for option in ("--fail", "--location", "--show-error"):
            self.assertIn(option, arguments)
        self.assertFalse(any(argument.startswith("--retry") for argument in arguments))
        self.assertNotIn("--insecure", arguments)
        self.assertEqual(
            arguments[-1],
            "https://github.com/astral-sh/uv/releases/download/0.13.6/uv-x86_64-unknown-linux-gnu.tar.gz",
        )

    def test_bad_checksum_prevents_extraction_and_path_publication(self):
        self.archive.write_bytes(b"corrupt archive")
        result = self.run_installer()
        self.assert_failure(result)
        self.assertEqual(self.calls("tar"), [])
        self.assertEqual(len(self.calls("curl")), 1)

    def test_extraction_failure_is_terminal(self):
        self.assert_failure(self.run_installer(FAIL_TAR="1"))
        self.assertEqual(len(self.calls("curl")), 1)
        self.assertEqual(self.calls("sleep"), [])

    def test_unsupported_platform_fails_before_download(self):
        for operating_system, architecture in (("Darwin", "x86_64"), ("Linux", "aarch64")):
            with self.subTest(platform=(operating_system, architecture)):
                self.log.write_text("")
                result = self.run_installer(TEST_OS=operating_system, TEST_ARCH=architecture)
                self.assert_failure(result)
                self.assertIn("supports only Linux x86_64", result.stderr)
                self.assertEqual(self.calls("curl"), [])


if __name__ == "__main__":
    unittest.main()
