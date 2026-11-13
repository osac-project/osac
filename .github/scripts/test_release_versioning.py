"""Run the release pipeline's real version-resolution shell against synthetic tag histories."""

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = yaml.safe_load((ROOT / ".github/workflows/osac-build-and-publish.yaml").read_text())
PREPARE_STEPS = {step.get("id"): step for step in WORKFLOW["jobs"]["prepare"]["steps"]}
COMPONENTS = (
    "osac-operator",
    "fulfillment-service",
    "osac-aap",
    "bare-metal-fulfillment-operator",
    "osac-metering",
    "osac-csi-driver",
    "osac-ui",
)
CHART_PIPELINES = sorted((ROOT / ".tekton").glob("*-chart/release.yaml"))
# Git honors GIT_DIR and friends over -C and the working directory; inherited
# from a hook, they would point these commands at the real repository.
ENV = {name: value for name, value in os.environ.items() if not name.startswith("GIT_")}


class CaaSImageStamping(unittest.TestCase):
    def test_caas_built_and_pinned_images_preserve_unrelated_values(self) -> None:
        step = next(
            step for step in WORKFLOW["jobs"]["prepare"]["steps"] if step.get("name") == "Stamp charts and values"
        )
        script = step["run"].split("needs_build_for()", 1)[1]
        script = "needs_build_for()" + script
        for built in (True, False):
            with self.subTest(built=built), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                shutil.copytree(ROOT / "osac-installer/scripts", root / "osac-installer/scripts")
                overlay = root / "osac-installer/values/caas-ci/instance.yaml"
                overlay.parent.mkdir(parents=True)
                shutil.copyfile(ROOT / "osac-installer/values/caas-ci/instance.yaml", overlay)
                original = yaml.safe_load(overlay.read_text())
                components = (
                    "osac-operator",
                    "fulfillment-service",
                    "osac-aap",
                    "bare-metal-fulfillment-operator",
                    "osac-metering",
                    "osac-ui",
                )
                versions = dict.fromkeys(components, "0.5.1")
                result = subprocess.run(
                    ["bash", "-euc", "source osac-installer/scripts/nightly-charts.sh\n" + script],
                    cwd=root,
                    env={
                        **ENV,
                        "COMPONENT_VERSIONS": json.dumps(versions),
                        "COMPONENTS_NEEDING_BUILD": json.dumps(components if built else []),
                        "PROVISIONAL_TAG": "sha-abcdef0",
                        "GITHUB_SHA": "abcdef0",
                        "GITHUB_REPOSITORY": "osac-project/osac",
                    },
                    capture_output=True,
                    text=True,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                actual = yaml.safe_load(overlay.read_text())
                tag = "sha-abcdef0" if built else "0.5.1"
                expected = original
                for path, value in (
                    (("operator", "image", "tag"), tag),
                    (("service", "images", "service", "tag"), tag),
                    (("bmf", "image", "tag"), tag),
                    (("aap", "bootstrap", "image"), f"ghcr.io/osac-project/osac-aap:{tag}"),
                    (("aap", "configAsCode", "eeImage"), f"ghcr.io/osac-project/osac-aap:{tag}"),
                    (("aap", "configAsCode", "projectGitBranch"), "abcdef0"),
                    (("aap", "configAsCode", "projectGitUri"), "https://github.com/osac-project/osac"),
                    (("metering", "image", "tag"), tag),
                    (("metering", "echoAdapter", "image", "tag"), tag),
                    (("metering", "m360Adapter", "image", "tag"), tag),
                    (("ui", "images", "ui"), f"ghcr.io/osac-project/osac-ui:{tag}"),
                ):
                    parent = expected
                    for key in path[:-1]:
                        parent = parent.get(key, {})
                    if path[-1] in parent and parent[path[-1]] is not None:
                        parent[path[-1]] = value
                self.assertEqual(actual, expected)


class Repo:
    """A throwaway git repository carrying the real release scripts."""

    def __init__(self, path: Path, line: str = "0.5") -> None:
        self.path = path
        shutil.copytree(ROOT / "osac-installer/scripts", self.path / "osac-installer/scripts")
        (self.path / "RELEASE_LINE").write_text(line + "\n")
        self.git("init", "-q")
        self.git("-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--allow-empty", "-qm", "initial")

    def git(self, *arguments: str) -> None:
        subprocess.run(["git", "-C", str(self.path), *arguments], check=True, capture_output=True, env=ENV)

    def tag(self, *tags: str) -> None:
        for tag in tags:
            self.git("tag", tag)

    def tag_all(self, version: str) -> None:
        self.tag(f"osac/v{version}", *(f"{component}/v{version}" for component in COMPONENTS))

    def run_step(self, step_id: str, cwd: str = ".", **env: str) -> tuple[subprocess.CompletedProcess[str], dict[str, str]]:
        output = self.path / f"{step_id}.out"
        output.write_text("")
        result = subprocess.run(
            ["bash", "-c", PREPARE_STEPS[step_id]["run"]],
            cwd=self.path / cwd,
            env={
                **ENV,
                "GITHUB_OUTPUT": str(output),
                "GITHUB_RUN_NUMBER": "7",
                "GITHUB_RUN_ATTEMPT": "1",
                **env,
            },
            capture_output=True,
            text=True,
            check=False,
        )
        outputs = dict(line.split("=", 1) for line in output.read_text().splitlines() if "=" in line)
        return result, outputs

    def release(self, version: str, components: str = "") -> tuple[subprocess.CompletedProcess[str], dict[str, str]]:
        return self.run_step("versions", MODE="release", RELEASE_VERSION=version, COMPONENT_VERSIONS_OVERRIDE=components)

    def nightly(self) -> tuple[subprocess.CompletedProcess[str], dict[str, str]]:
        return self.run_step("versions", MODE="nightly", RELEASE_VERSION="", COMPONENT_VERSIONS_OVERRIDE="")


def all_at(version: str) -> str:
    return ",".join(f"{component}={version}" for component in COMPONENTS)


class TemporaryRepos(unittest.TestCase):
    def directory(self) -> Path:
        path = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, path, ignore_errors=True)
        return path

    def repo(self, line: str = "0.5") -> Repo:
        return Repo(self.directory() / "repo", line)


class ReleaseVersionResolution(TemporaryRepos):
    def assert_rejected(self, result: subprocess.CompletedProcess[str], message: str) -> None:
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn(message, result.stderr)

    def test_first_candidate_builds_and_tags_every_component(self) -> None:
        result, outputs = self.repo().release("0.5.0-rc.1", all_at("0.5.0-rc.1"))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(outputs["release-line"], "0.5")
        self.assertEqual(set(json.loads(outputs["component-versions"]).values()), {"0.5.0-rc.1"})
        self.assertEqual(sorted(json.loads(outputs["components-to-tag"])), sorted(COMPONENTS))

    def test_one_component_bump_pins_the_rest_inside_the_line(self) -> None:
        repo = self.repo()
        repo.tag_all("0.5.0-rc.1")
        # Other lines must never be picked up, however high they sort.
        repo.tag_all("0.6.0-rc.4")
        repo.tag("osac-ui/v0.4.9", "osac-ui/v0.5.0-rc.2")
        result, outputs = repo.release("0.5.0-rc.2", "osac-operator=0.5.0-rc.2")
        self.assertEqual(result.returncode, 0, result.stderr)
        versions = json.loads(outputs["component-versions"])
        self.assertEqual(versions["osac-operator"], "0.5.0-rc.2")
        self.assertEqual(versions["osac-ui"], "0.5.0-rc.2")
        self.assertEqual(versions["fulfillment-service"], "0.5.0-rc.1")
        self.assertEqual(json.loads(outputs["components-needing-build"]), ["osac-operator"])
        self.assertEqual(json.loads(outputs["components-to-tag"]), ["osac-operator"])

    def test_stable_outranks_candidates_and_rc_numbers_compare_numerically(self) -> None:
        repo = self.repo()
        repo.tag_all("0.5.0")
        repo.tag("osac-operator/v0.5.1-rc.9", "osac-operator/v0.5.1-rc.10", "osac-ui/v0.5.0-rc.3")
        result, outputs = repo.release("0.5.1-rc.1")
        self.assertEqual(result.returncode, 0, result.stderr)
        versions = json.loads(outputs["component-versions"])
        self.assertEqual(versions["osac-operator"], "0.5.1-rc.10")
        self.assertEqual(versions["osac-ui"], "0.5.0")

    def test_existing_version_is_only_pinnable_when_it_is_the_latest(self) -> None:
        # The chart pipelines publish a chart's highest version in the line and never see a pin.
        repo = self.repo()
        repo.tag_all("0.5.1")
        repo.tag("osac-ui/v0.5.2-rc.1")
        self.assert_rejected(repo.release("0.5.2", "osac-ui=0.5.1")[0], "is not its latest version in line 0.5.x (0.5.2-rc.1)")
        self.assert_rejected(repo.release("0.5.2")[0], "an older stable version cannot be pinned")
        result, outputs = repo.release("0.5.2-rc.1", "osac-ui=0.5.2-rc.1,osac-operator=0.5.1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(outputs["components-needing-build"]), [])
        versions = json.loads(outputs["component-versions"])
        self.assertEqual((versions["osac-ui"], versions["osac-operator"]), ("0.5.2-rc.1", "0.5.1"))

    def test_patch_after_ga_moves_one_component(self) -> None:
        repo = self.repo()
        repo.tag_all("0.5.0")
        result, outputs = repo.release("0.5.1", "osac-operator=0.5.1")
        self.assertEqual(result.returncode, 0, result.stderr)
        versions = json.loads(outputs["component-versions"])
        self.assertEqual(versions["osac-operator"], "0.5.1")
        self.assertEqual(versions["osac-csi-driver"], "0.5.0")

    def test_versions_outside_the_branch_line_are_rejected(self) -> None:
        repo = self.repo()
        repo.tag_all("0.5.0-rc.1")
        self.assert_rejected(repo.release("0.6.0-rc.1")[0], "outside this branch's release line 0.5.x")
        self.assert_rejected(repo.release("0.5.0-rc.2", "osac-ui=0.4.1")[0], "outside this branch's release line 0.5.x")

    def test_versions_outside_the_grammar_are_rejected(self) -> None:
        repo = self.repo()
        repo.tag_all("0.5.0-rc.1")
        for version in (
            "0.5.0-rc1",
            "0.5.0-rc.0",
            "0.5.0-rc.01",
            "0.5.01",
            "0.5.1234567",
            "0.5.0-beta.1",
            "0.5.0-nightly.1",
            "0.5",
            "v0.5.0",
            "0.5.0+build",
        ):
            with self.subTest(version=version):
                self.assert_rejected(repo.release(version)[0], "must be X.Y.Z or X.Y.Z-rc.N")
        self.assert_rejected(repo.release("0.5.0-rc.2", "osac-ui=0.5.0-alpha.1")[0], "must be X.Y.Z or X.Y.Z-rc.N")

    def test_versions_must_move_forward(self) -> None:
        repo = self.repo()
        repo.tag_all("0.5.0-rc.3")
        self.assert_rejected(repo.release("0.5.0-rc.2")[0], "must be higher than osac/v0.5.0-rc.3")
        self.assert_rejected(repo.release("0.5.0-rc.3")[0], "already exists")
        self.assert_rejected(
            repo.release("0.5.0-rc.4", "osac-ui=0.5.0-rc.2")[0], "must be higher than osac-ui/v0.5.0-rc.3"
        )

    def test_maturity_of_umbrella_and_components_must_agree(self) -> None:
        repo = self.repo()
        repo.tag_all("0.5.0-rc.1")
        self.assert_rejected(repo.release("0.5.0-rc.2", "osac-ui=0.5.0")[0], "use an -rc.N component version")
        self.assert_rejected(repo.release("0.5.0", "osac-ui=0.5.0")[0], "cannot contain osac-operator 0.5.0-rc.1")
        result, outputs = repo.release("0.5.0", all_at("0.5.0"))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(sorted(json.loads(outputs["components-to-tag"])), sorted(COMPONENTS))

    def test_component_without_a_version_in_the_line_must_be_named(self) -> None:
        repo = self.repo()
        repo.tag_all("0.4.0")
        self.assert_rejected(repo.release("0.5.0-rc.1")[0], "has no released version in line 0.5.x")

    def test_missing_or_malformed_release_line_fails(self) -> None:
        repo = self.repo()
        (repo.path / "RELEASE_LINE").write_text("0.5.0\n")
        self.assert_rejected(repo.nightly()[0], "must contain a release line of the form X.Y")
        (repo.path / "RELEASE_LINE").unlink()
        self.assert_rejected(repo.nightly()[0], "RELEASE_LINE not found")

    def test_nightly_tracks_the_branch_line(self) -> None:
        repo = self.repo()
        repo.tag_all("0.4.0")
        repo.tag_all("0.0.65")
        result, outputs = repo.nightly()
        self.assertEqual(result.returncode, 0, result.stderr)
        versions = json.loads(outputs["component-versions"])
        self.assertEqual(len(versions), len(COMPONENTS))
        for version in versions.values():
            self.assertRegex(version, r"^0\.5\.0-nightly\.\d{8}\.[0-9a-f]{7}\.7\.1$")
        self.assertEqual(json.loads(outputs["components-to-tag"]), [])
        umbrella, umbrella_outputs = repo.run_step(
            "umbrella-version",
            cwd="osac-installer",
            MODE="nightly",
            RELEASE_VERSION="",
            RELEASE_LINE=outputs["release-line"],
            NIGHTLY_SUFFIX=outputs["nightly-suffix"],
        )
        self.assertEqual(umbrella.returncode, 0, umbrella.stderr)
        self.assertEqual(umbrella_outputs["version"], f"0.5.0-{outputs['nightly-suffix']}")

    def test_nightly_after_a_stable_release_uses_the_next_patch(self) -> None:
        # Above the latest stable, and by the documented nightly < rc order
        # below a candidate of that same patch (0.5.2-rc.1 here).
        repo = self.repo()
        repo.tag_all("0.5.0")
        repo.tag("osac-operator/v0.5.1", "osac-operator/v0.5.2-rc.1")
        versions = json.loads(repo.nightly()[1]["component-versions"])
        self.assertTrue(versions["osac-operator"].startswith("0.5.2-nightly."))
        self.assertTrue(versions["osac-ui"].startswith("0.5.1-nightly."))


class ChartVersionResolution(TemporaryRepos):
    """The Tekton chart pipelines pick a chart version from tags; it must stay inside the release line."""

    TAGS = ("0.0.42", "0.4.0-rc.1", "0.4.0-rc.3", "0.4.0-rc.10", "0.5.0-rc.1", "0.5.0", "0.5.2", "0.6.0-rc.2")
    # Outside the release grammar: each would win its line if the resolver accepted it.
    MALFORMED = ("0.4.1-rc.0", "0.5.3-rc.0", "0.5.03", "0.5.3-nightly.20261112.abcdef0.1.1", "0.6.0-rc.03", "0.6.1-beta.1")

    def resolve(self, pipeline: Path, trigger: str) -> tuple[int, str]:
        document = yaml.safe_load(pipeline.read_text())
        tasks = document["spec"]["pipelineSpec"]["tasks"]
        task = next(task for task in tasks if task["name"] == "resolve-chart-version")
        self.assertIn({"name": "trigger-ref", "value": "{{target_branch}}"}, task["params"])
        step = task["taskSpec"]["steps"][0]
        self.assertEqual(step["env"], [{"name": "TRIGGER_REF", "value": "$(params.trigger-ref)"}])
        prefix = next(param["value"] for param in document["spec"]["params"] if param["name"] == "tag-prefix")

        repo = self.repo()
        repo.tag(*(f"{prefix}{version}" for version in (*self.TAGS, *self.MALFORMED)))
        work = self.directory()
        script = (
            step["script"]
            .replace("$(params.tag-prefix)", prefix)
            .replace("$(params.git-url)", str(repo.path))
            .replace("$(results.CHART_VERSION.path)", str(work / "version"))
            .replace("/tmp/repo", str(work / "repo"))
        )
        result = subprocess.run(
            ["bash", "-c", script],
            env={**ENV, "TRIGGER_REF": trigger},
            capture_output=True,
            text=True,
            check=False,
        )
        version = (work / "version").read_text() if (work / "version").exists() else ""
        return result.returncode, version

    def test_every_chart_pipeline_resolves_inside_the_trigger_line(self) -> None:
        self.assertEqual(len(CHART_PIPELINES), 11)
        cases = {
            "refs/tags/osac-charts-release/v0.4.0-rc.11": "0.4.0-rc.10",
            "refs/tags/osac-charts-release/v0.4.0-rc.2-retry1": "0.4.0-rc.10",
            "refs/tags/osac-charts-release/v0-4-0-rc-1-prod-r12a1": "0.4.0-rc.10",
            "refs/tags/osac-charts-release/v0.5.3": "0.5.2",
            "refs/tags/osac-charts-release/v0.6.0-rc.4": "0.6.0-rc.2",
        }
        for pipeline in CHART_PIPELINES:
            for trigger, expected in cases.items():
                with self.subTest(pipeline=pipeline.parent.name, trigger=trigger):
                    self.assertEqual(self.resolve(pipeline, trigger), (0, expected))

    def test_unknown_line_or_trigger_fails_instead_of_guessing(self) -> None:
        pipeline = CHART_PIPELINES[0]
        for trigger in ("refs/tags/osac-charts-release/v0.7.0", "refs/heads/main", "refs/tags/osac-charts-release/vnext"):
            with self.subTest(trigger=trigger):
                code, version = self.resolve(pipeline, trigger)
                self.assertNotEqual(code, 0)
                self.assertEqual(version, "")


if __name__ == "__main__":
    unittest.main()
