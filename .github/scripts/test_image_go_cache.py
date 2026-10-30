"""Exercise the opt-in image-cache boundary without a container daemon."""

import json
import os
import re
import subprocess
import tempfile
import unittest
from contextlib import redirect_stdout
from io import StringIO
from pathlib import Path
from unittest.mock import patch

import image_go_cache as cache
import yaml

ROOT = Path(__file__).resolve().parents[2]


class CacheContracts(unittest.TestCase):
    def test_fingerprint_covers_all_workspace_inputs(self) -> None:
        """Mutating any manifest or build setting must invalidate the exact cache identity."""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for relative in cache.input_paths(ROOT):
                target = root / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes((ROOT / relative).read_bytes())
            settings = {"builder": "docker-default-1", "platform": "linux/amd64"}
            original = cache.fingerprint(root, settings)
            for relative in cache.input_paths(root):
                target = root / relative
                content = target.read_bytes()
                target.write_bytes(content + b"\n")
                self.assertNotEqual(original, cache.fingerprint(root, settings), str(relative))
                target.write_bytes(content)
            self.assertEqual(original, cache.fingerprint(root, settings))
            for field in ("builder", "platform", "toolchain", "build_flags", "schema"):
                self.assertNotEqual(original, cache.fingerprint(root, settings | {field: "changed"}))

    def test_workspace_inputs_include_every_current_module(self) -> None:
        """Every workspace and copied Containerfile manifest must participate in the key."""
        inputs = {str(path) for path in cache.input_paths(ROOT)}
        for module in (ROOT / "go.work").read_text().splitlines():
            if module.strip().startswith("./"):
                for manifest in ("go.mod", "go.sum"):
                    self.assertIn(f"{module.strip()[2:]}/{manifest}", inputs)
        for component in cache.COMPONENTS:
            containerfile = (ROOT / component / "Containerfile").read_text()
            copies = "\n".join(line for line in containerfile.splitlines() if line.startswith("COPY "))
            for manifest in re.findall(r"(?:^|\s)([^\s]+/(?:go.mod|go.sum))(?=\s)", copies):
                self.assertIn(manifest, inputs)

    def test_payload_allows_only_bounded_regular_module_and_build_data(self) -> None:
        """Reject oversize data, unexpected roots and symlinks before any Docker import."""
        with tempfile.TemporaryDirectory() as temporary:
            payload = Path(temporary)
            for name in ("pkg", "build"):
                (payload / name).mkdir()
            (payload / "pkg" / "checksum").write_bytes(b"valid")
            self.assertEqual(5, cache.payload_size(payload))
            with self.assertRaises(ValueError):
                cache.payload_size(payload, limit=4)
            (payload / "auth.json").touch()
            with self.assertRaises(ValueError):
                cache.payload_size(payload)
            (payload / "auth.json").unlink()
            (payload / "pkg" / "escape").symlink_to("/etc/passwd")
            with self.assertRaises(ValueError):
                cache.payload_size(payload)

    def test_incomplete_and_hardlinked_payloads_are_rejected(self) -> None:
        """Reject incomplete mount trees and hard links shared across payload directories."""
        with tempfile.TemporaryDirectory() as temporary:
            payload = Path(temporary)
            (payload / "pkg").mkdir()
            with self.assertRaises(ValueError):
                cache.payload_size(payload)
            (payload / "build").mkdir()
            (payload / "pkg" / "first").touch()
            os.link(payload / "pkg" / "first", payload / "build" / "second")
            with self.assertRaises(ValueError):
                cache.payload_size(payload)

    def test_transport_uses_exact_named_mounts_and_default_builder(self) -> None:
        """Generated transport must retain production mount IDs, paths and ownership."""
        for operation in ("inject", "extract", "clear"):
            dockerfile = cache.transport_file(ROOT, "example.invalid/toolset@sha256:" + "a" * 64, operation)
            for mount in cache.MOUNTS.values():
                self.assertIn(f"id={mount[0]},target={mount[1]},uid=1001,gid=0", dockerfile)
            self.assertNotIn("type=secret", dockerfile)
        self.assertIn("go mod verify", cache.transport_file(ROOT, "toolset", "verify"))

    def test_actual_installer_macro_is_invoked_without_cluster_targets(self) -> None:
        """Execute the real Make macro with fake Docker and preserve its nonzero exit."""
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            docker = directory / "docker"
            docker.write_text('#!/bin/sh\nprintf \'%s\\n\' "$@" > "$CAPTURE"\nexit 17\n')
            docker.chmod(0o755)
            environment = os.environ | {"PATH": f"{directory}:{os.environ['PATH']}", "CAPTURE": str(directory / "args")}
            result = subprocess.run(
                cache.consumer_command(ROOT, directory / "Containerfile"),
                env=environment,
                capture_output=True,
                text=True,
            )
            self.assertNotEqual(0, result.returncode)
            arguments = (directory / "args").read_text().splitlines()
            self.assertEqual("build", arguments[0])
            self.assertIn("--no-cache", arguments)
            self.assertIn("localhost/fulfillment-service:it", arguments)
            self.assertEqual("..", arguments[-1])

    def test_trust_requires_public_default_branch_and_manual_context(self) -> None:
        """Private repositories and pull-request contexts must not satisfy the save boundary."""
        trusted = {"event": "workflow_dispatch", "ref": "refs/heads/main", "default_branch": "main", "private": False}
        self.assertTrue(cache.trusted_save(trusted))
        for change in (
            {"event": "pull_request"},
            {"event": "pull_request_target"},
            {"ref": "refs/pull/1/merge"},
            {"private": True},
        ):
            self.assertFalse(cache.trusted_save(trusted | change))

    def test_workflow_defaults_and_trial_budget(self) -> None:
        """Keep opt-in inputs false and the manual experiment within six twenty-minute jobs."""
        workflows = ROOT / ".github/workflows"
        for filename in ("nightly-build.yaml", "osac-build-and-publish.yaml", "integration-tests.yml", "tests.yml"):
            workflow = yaml.safe_load((workflows / filename).read_text())
            triggers = workflow.get("on", workflow.get(True))
            trigger = "workflow_dispatch" if filename == "nightly-build.yaml" else "workflow_call"
            self.assertIs(False, triggers[trigger]["inputs"]["image_go_cache"]["default"])
        trial = yaml.safe_load((workflows / "image-go-cache-trial.yaml").read_text())
        self.assertEqual(6, len(trial["jobs"]))
        self.assertEqual(
            ["baseline", "baseline", "cold", "warm", "warm", "invalidation"],
            [job["with"]["sample"] for job in trial["jobs"].values()],
        )
        sample = yaml.safe_load((workflows / "image-go-cache-sample.yaml").read_text())
        self.assertEqual(20, sample["jobs"]["sample"]["timeout-minutes"])
        self.assertEqual("ubuntu-24.04", sample["jobs"]["sample"]["runs-on"])

    def test_build_observations_do_not_equate_restore_to_consumption(self) -> None:
        """Log-derived consumption and error counts must remain separate from cache hits."""
        observations = cache.observe(
            "#1 go: downloading example.org/m v1.0\n#2 OSAC_IMAGE_CACHE_BEFORE pkg=7 build=2\n"
        )
        self.assertEqual(1, observations["observed_download_lines"])
        self.assertEqual(0, observations["observed_dependency_error_lines"])
        self.assertEqual(["#2 OSAC_IMAGE_CACHE_BEFORE pkg=7 build=2"], observations["image_build_observations"])
        self.assertNotIn("cache_hit", observations)

    def test_dockerfile_instruction_text_is_not_consumption_evidence(self) -> None:
        """Only emitted RUN output, not Dockerfile instruction text, proves observations."""
        log = "#1 RUN echo OSAC_IMAGE_CACHE_VERIFIED\n#2 0.1 OSAC_IMAGE_CACHE_VERIFIED\n"
        self.assertEqual(["#2 0.1 OSAC_IMAGE_CACHE_VERIFIED"], cache.observe(log)["image_build_observations"])

    def test_dependency_errors_preserve_literal_hosts_case_and_go_fallback(self) -> None:
        """Literal public hosts and case-insensitive Go errors must remain observable."""
        for line in (
            "#1 https://proxy.golang.org/example/@v/v1.zip: timeout",
            "#1 https://sum.golang.org/lookup/example@v1: failed",
            "#1 HTTPS://PROXY.GOLANG.ORG/example: ERROR",
            "#1 HTTPS://SUM.GOLANG.ORG/lookup/example: RESET",
            "#1 go: module download failed",
            "#1 GO: connection TIMEOUT",
        ):
            with self.subTest(line=line):
                self.assertEqual(1, cache.observe(line)["observed_dependency_error_lines"])

    def test_dependency_errors_reject_wildcard_substituted_hostnames(self) -> None:
        """Replacing any hostname dot must not produce a matching dependency-error host."""
        for hostname in ("proxyXgolang.org", "proxy.golangXorg", "sumXgolang.org", "sum.golangXorg"):
            with self.subTest(hostname=hostname):
                self.assertEqual(
                    0, cache.observe(f"#1 https://{hostname}/example: timeout")["observed_dependency_error_lines"]
                )

    def test_download_progress_module_names_are_not_errors(self) -> None:
        """Progress with error-like module names must not hide real download or host failures."""
        for module in (
            "github.com/pkg/errors",
            "github.com/hashicorp/go-multierror",
            "example.org/timeout",
            "example.org/reset",
            "example.org/failed",
            "example.org/INTERNAL_ERROR",
        ):
            with self.subTest(module=module):
                observations = cache.observe(f"#1 0.1 go: downloading {module} v1.0.0")
                self.assertEqual(1, observations["observed_download_lines"])
                self.assertEqual(0, observations["observed_dependency_error_lines"])
        for line in (
            "#1 go: downloading example.org/module v1.0.0: failed",
            "#1 https://proxy.golang.org/example: timeout",
            "#1 go: module lookup error",
        ):
            with self.subTest(error=line):
                self.assertEqual(1, cache.observe(line)["observed_dependency_error_lines"])

    def test_extract_skips_only_verified_exact_hits_before_transport(self) -> None:
        """Avoid immutable-key re-exports only after a verified exact restore; retain all other checks."""
        exact = {"cache_hit": "true", "matched_key": "exact-key"}
        for restore, injection, skip in (
            (exact, "verified-and-injected", True),
            (exact, "cold-fallback", False),
            ({"cache_hit": "false", "matched_key": ""}, "cold-fallback", False),
            ({"cache_hit": "true", "matched_key": "other-key"}, "verified-and-injected", False),
            ({"cache_hit": True}, "verified-and-injected", False),
            ({"cache_hit": "true"}, "verified-and-injected", False),
            ("not-attempted", "not-attempted", False),
            (None, "not-attempted", False),
        ):
            with self.subTest(restore=restore, injection=injection), tempfile.TemporaryDirectory() as temporary:
                state = Path(temporary)
                (state / "state.json").write_text(
                    json.dumps({"key": "exact-key", "restore": restore, "injection": injection})
                )
                with (
                    patch.dict(os.environ, {"GITHUB_ACTIONS": "false"}),
                    patch.object(cache, "transport") as transport,
                    patch.object(cache, "payload_size", return_value=123) as size,
                    patch.object(cache, "check_ziphashes") as checksums,
                    patch.object(cache, "output") as output,
                ):
                    cache.extract(ROOT, state)
                if skip:
                    transport.assert_not_called()
                    size.assert_not_called()
                    checksums.assert_not_called()
                    output.assert_called_once_with("ready", "false")
                else:
                    self.assertEqual(["extract", "verify"], [call.args[2] for call in transport.call_args_list])
                    size.assert_called_once_with(state / "payload")
                    checksums.assert_called_once_with(ROOT, state / "payload")
                    self.assertIn(unittest.mock.call("ready", "true"), output.call_args_list)

    def test_exact_hit_does_not_bypass_save_trust_guard(self) -> None:
        """An otherwise skippable exact hit must still fail an untrusted save context."""
        with tempfile.TemporaryDirectory() as temporary:
            state = Path(temporary)
            (state / "state.json").write_text(
                json.dumps(
                    {
                        "key": "exact-key",
                        "namespace": "fulfillment-integration",
                        "restore": {"cache_hit": "true", "matched_key": "exact-key"},
                        "injection": "verified-and-injected",
                    }
                )
            )
            with (
                patch.dict(os.environ, {"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "pull_request"}),
                patch.object(cache, "transport") as transport,
                patch.object(cache, "output") as output,
                self.assertRaises(ValueError),
            ):
                cache.extract(ROOT, state)
            transport.assert_not_called()
            output.assert_not_called()

    def test_missing_and_malformed_restore_fall_back_without_injection(self) -> None:
        """Missing or malformed payloads must leave the normal build path available."""
        for malformed in (False, True):
            with self.subTest(malformed=malformed), tempfile.TemporaryDirectory() as temporary:
                state = Path(temporary)
                (state / "state.json").write_text(json.dumps({"restore_started": 0}))
                if malformed:
                    (state / "payload").mkdir()
                    (state / "payload/auth.json").touch()
                with patch.object(cache, "transport") as docker_boundary:
                    cache.inject(ROOT, state)
                docker_boundary.assert_not_called()
                self.assertEqual("cold-fallback", json.loads((state / "state.json").read_text())["injection"])

    def test_verification_failure_does_not_inject_payload(self) -> None:
        """Failed isolated Go verification must never seed the consumer cache mounts."""
        with tempfile.TemporaryDirectory() as temporary:
            state = Path(temporary)
            (state / "state.json").write_text(json.dumps({"restore_started": 0}))
            for name in cache.MOUNTS:
                (state / "payload" / name).mkdir(parents=True)
            with patch.object(cache, "transport", side_effect=subprocess.CalledProcessError(1, "docker")) as boundary:
                cache.inject(ROOT, state)
            self.assertEqual(["verify"], [call.args[2] for call in boundary.call_args_list])
            self.assertEqual("cold-fallback", json.loads((state / "state.json").read_text())["injection"])

    def test_optional_transport_cli_failures_disable_cache_without_failing_build(self) -> None:
        """Optional transport errors must emit ready=false instead of failing normal builds."""
        for operation in ("prepare", "inject", "extract"):
            with self.subTest(operation=operation), tempfile.TemporaryDirectory() as temporary:
                state = Path(temporary)
                output = state / "outputs"
                with (
                    patch("sys.argv", ["image_go_cache.py", operation, "--state", str(state)]),
                    patch.dict(os.environ, {"GITHUB_OUTPUT": str(output)}),
                    patch.object(cache, operation, side_effect=ValueError("unavailable or oversized payload")),
                    redirect_stdout(StringIO()) as warning,
                ):
                    cache.main()
                self.assertIn("normal build retained", warning.getvalue())
                self.assertEqual("ready=false\n", output.read_text())

    def test_sample_cli_does_not_swallow_build_failure(self) -> None:
        """The top-level sample command must propagate the consumer build error."""
        with (
            tempfile.TemporaryDirectory() as temporary,
            patch("sys.argv", ["image_go_cache.py", "sample", "--state", temporary]),
            patch.object(cache, "sample", side_effect=subprocess.CalledProcessError(23, "consumer")),
            self.assertRaises(subprocess.CalledProcessError),
        ):
            cache.main()

    def test_checksum_payload_must_match_committed_sums(self) -> None:
        """Reject a cached module ziphash that differs from the committed public checksum."""
        with tempfile.TemporaryDirectory() as temporary:
            payload = Path(temporary)
            module, version, checksum = next(
                line.split()
                for line in (ROOT / "fulfillment-service/go.sum").read_text().splitlines()
                if line.startswith("github.com/google/uuid ") and "/go.mod" not in line
            )
            ziphash = payload / "pkg/mod/cache/download" / module / "@v" / f"{version}.ziphash"
            ziphash.parent.mkdir(parents=True)
            ziphash.write_text(checksum)
            cache.check_ziphashes(ROOT, payload)
            ziphash.write_text("h1:wrong")
            with self.assertRaises(ValueError):
                cache.check_ziphashes(ROOT, payload)

    def test_cached_module_manifest_is_checked_not_just_ziphash(self) -> None:
        """Verify cached go.mod content independently of downloaded module ziphash files."""
        with tempfile.TemporaryDirectory() as temporary:
            payload = Path(temporary)
            manifest = payload / "pkg/mod/cache/download/github.com/google/uuid/@v/v1.6.0.mod"
            manifest.parent.mkdir(parents=True)
            manifest.write_text("module github.com/google/uuid\n")
            cache.check_ziphashes(ROOT, payload)
            manifest.write_text("module example.invalid/poison\n")
            with self.assertRaises(ValueError):
                cache.check_ziphashes(ROOT, payload)

    def test_sample_preserves_real_build_failures_and_records_measurements(self) -> None:
        """The real Make boundary must preserve exits and record source, image and timing evidence."""
        source = subprocess.check_output(["git", "-C", str(ROOT), "rev-parse", "HEAD"], text=True).strip()
        for exit_code in (0, 19):
            with self.subTest(exit_code=exit_code), tempfile.TemporaryDirectory() as temporary:
                state = Path(temporary)
                docker = state / "docker"
                docker.write_text(
                    '#!/bin/sh\nif [ "$1" = "image" ]; then\n'
                    'case "$5" in *Labels*) echo dev ;; *) echo sha256:fixture ;; esac\nexit 0\nfi\n'
                    'echo "#1 0.1 OSAC_IMAGE_CACHE_BEFORE phase=compile pkg=7 build=2"\n'
                    'echo "#1 0.1 OSAC_IMAGE_CACHE_BEFORE phase=download pkg=7 build=2"\n'
                    'echo "#1 0.2 OSAC_IMAGE_CACHE_VERIFIED"\n'
                    'echo "#1 0.3 OSAC_IMAGE_CACHE_COMPILED"\n'
                    'echo "#1 0.4 OSAC_IMAGE_CACHE_VARIANT_DOWNLOADED"\n'
                    f"exit {exit_code}\n"
                )
                docker.chmod(0o755)
                environment = {"PATH": f"{state}:{os.environ['PATH']}", "GITHUB_RUN_ATTEMPT": "1"}
                with patch.dict(os.environ, environment):
                    if exit_code:
                        with self.assertRaises(subprocess.CalledProcessError):
                            cache.sample(ROOT, state, "warm", source)
                    else:
                        cache.sample(ROOT, state, "invalidation", source)
                report = json.loads((state / "measurement.json").read_text())
                self.assertEqual(exit_code != 0, report["exit_code"] != 0)
                self.assertEqual(source, report["source_sha"])
                self.assertIn("image-cache-trial", report["consumer_argv"])
                if not exit_code:
                    self.assertEqual("dev", report["image_version"])
                    self.assertEqual("sha256:fixture", report["image_id"])
                self.assertGreaterEqual(report["build_seconds"], 0)
                self.assertTrue((state / "build.log").is_file())

    def test_sample_bounds_logs_without_masking_real_build_failure(self) -> None:
        """Drain oversized output without unbounded artifacts or losing the build exit status."""
        source = subprocess.check_output(["git", "-C", str(ROOT), "rev-parse", "HEAD"], text=True).strip()
        with tempfile.TemporaryDirectory() as temporary:
            state = Path(temporary)
            command = ["sh", "-c", "printf 'a long build log that exceeds the limit'; exit 23"]
            with (
                patch.object(cache, "consumer_command", return_value=command),
                patch.object(cache, "LOG_LIMIT", 8),
                self.assertRaises(subprocess.CalledProcessError) as failure,
            ):
                cache.sample(ROOT, state, "baseline", source)
            self.assertEqual(23, failure.exception.returncode)
            self.assertEqual(8, (state / "build.log").stat().st_size)
            self.assertTrue(json.loads((state / "measurement.json").read_text())["log_truncated"])

    def test_sample_checks_frozen_revision_and_rerun_budget_before_build(self) -> None:
        """Reject floating revisions and repeated attempts before spending another sample."""
        with tempfile.TemporaryDirectory() as temporary:
            with self.assertRaises(ValueError):
                cache.sample(ROOT, Path(temporary), "baseline", "main")
            source = subprocess.check_output(["git", "-C", str(ROOT), "rev-parse", "HEAD"], text=True).strip()
            with patch.dict(os.environ, {"GITHUB_RUN_ATTEMPT": "2"}), self.assertRaises(ValueError):
                cache.sample(ROOT, Path(temporary), "baseline", source)

    def test_sample_instruments_both_real_mount_consumers(self) -> None:
        """Require observations and ownership/path assertions in actual download and compile steps."""
        observed = cache.observed_containerfile(ROOT, "invalidation")
        self.assertIn("phase=%s pkg=%s build=%s", observed)
        self.assertIn("build=%s\\n' download", observed)
        self.assertIn("build=%s\\n' compile", observed)
        self.assertIn('test ! -f "$(go env GOMODCACHE)/cache/download/rsc.io/binaryregexp/@v/v0.2.0.zip"', observed)
        self.assertIn("OSAC_IMAGE_CACHE_VARIANT_DOWNLOADED", observed)
        self.assertIn("sha256sum -c /tmp/osac-sums", observed)
        self.assertIn('test "$(go env GOMODCACHE)" = /opt/app-root/src/go/pkg/mod', observed)
        self.assertIn('test "$(go env GOCACHE)" = /opt/app-root/src/.cache/go-build', observed)
        self.assertIn('test "$(id -u):$(id -g)" = 1001:0', observed)

    def test_trial_chain_has_no_parallel_or_seventh_runner(self) -> None:
        """Keep all six fresh samples serial with compatibility inside the first sample."""
        workflow = yaml.safe_load((ROOT / ".github/workflows/image-go-cache-trial.yaml").read_text())
        previous = None
        for name, job in workflow["jobs"].items():
            if previous:
                dependencies = job["needs"]
                self.assertIn(previous, [dependencies] if isinstance(dependencies, str) else dependencies)
            previous = name
            self.assertNotIn("runs-on", job)
        sample = yaml.safe_load((ROOT / ".github/workflows/image-go-cache-sample.yaml").read_text())
        self.assertEqual(1, len(sample["jobs"]))
        self.assertIn("github.run_attempt == 1", sample["jobs"]["sample"]["if"])

    def test_action_save_trust_and_default_builder_do_not_expand_permissions(self) -> None:
        """Restore only exact keys and retain public/manual/nightly-only save wiring."""
        action = yaml.safe_load((ROOT / ".github/actions/go-image-cache/action.yaml").read_text())
        restore = next(step for step in action["runs"]["steps"] if step.get("id") == "restore")
        self.assertNotIn("restore-keys", restore["with"])
        export = next(step for step in action["runs"]["steps"] if step.get("id") == "export")
        self.assertIn("!github.event.repository.private", export["if"])
        self.assertIn("github.event_name == 'workflow_dispatch'", export["if"])
        shared = yaml.safe_load((ROOT / ".github/workflows/osac-build-and-publish.yaml").read_text())
        self.assertIn("inputs.mode == 'nightly'", shared["jobs"]["integration-tests"]["with"]["image_go_cache"])

    def test_prepare_uses_stable_default_builder_identity_and_rejects_other_drivers(self) -> None:
        """Ignore activity timestamps in keys and reject docker-container driver substitution."""
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            docker = directory / "docker"
            image = json.dumps(
                [
                    {
                        "Config": {"Env": ["GOPROXY=https://proxy.golang.org", "GOSUMDB=sum.golang.org"]},
                        "RepoDigests": ["example.invalid/toolset@sha256:" + "a" * 64],
                    }
                ]
            )
            docker.write_text(
                '#!/bin/sh\ncase "$1 $2" in\n'
                '"context show") echo default ;;\n'
                '"buildx inspect") printf "Driver: %s\\nBuildKit version: v0.20.2\\n'
                'Platforms: linux/amd64\\nLast Activity: %s\\n" "$DRIVER" "$ACTIVITY" ;;\n'
                '"buildx version") echo buildx-v0.38.1 ;;\n'
                f"\"image inspect\") echo '{image}' ;;\n"
                '"version --format") echo engine-v28 ;;\n'
                '"pull "*) exit 0 ;;\n*) exit 1 ;;\nesac\n'
            )
            docker.chmod(0o755)
            environment = {
                "PATH": f"{directory}:{os.environ['PATH']}",
                "DRIVER": "docker",
                "ACTIVITY": "first",
                "GITHUB_ENV": str(directory / "env"),
                "GITHUB_OUTPUT": str(directory / "output"),
                "GOPRIVATE": "",
                "GONOSUMDB": "",
                "GONOPROXY": "",
            }
            with patch.dict(os.environ, environment):
                cache.prepare(ROOT, directory / "state", "fixture")
                first = json.loads((directory / "state/state.json").read_text())["key"]
                os.environ["ACTIVITY"] = "second"
                cache.prepare(ROOT, directory / "state", "fixture")
                self.assertEqual(first, json.loads((directory / "state/state.json").read_text())["key"])
                self.assertIn("BUILDX_BUILDER=default", (directory / "env").read_text())
                os.environ["DRIVER"] = "docker-container"
                with self.assertRaises(ValueError):
                    cache.prepare(ROOT, directory / "state", "fixture")


if __name__ == "__main__":
    unittest.main()
