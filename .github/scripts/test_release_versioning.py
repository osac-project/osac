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
CHARTS = [path.parent.name for path in CHART_PIPELINES]
IMAGES = WORKFLOW["jobs"]["wait-for-konflux-build"]["strategy"]["matrix"]["component"]
METERING = ("metering-service", "metering-m360-adapter", "metering-echo-adapter", "osac-metering-chart")
CSI = ("osac-csi-driver", "osac-csi-driver-chart", "osac-csi-backends-chart")


def konflux_names(component: str) -> tuple[str, ...]:
    if component == "osac-metering":
        return METERING
    if component == "osac-csi-driver":
        return CSI
    return (component, *(chart for chart in CHARTS if chart in (f"{component}-chart", f"{component}-crds-chart")))
# Git honors GIT_DIR and friends over -C and the working directory; inherited
# from a hook, they would point these commands at the real repository.
ENV = {name: value for name, value in os.environ.items() if not name.startswith("GIT_")}


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

    def tag(self, *tags: str, staged: bool = True) -> None:
        """Create tags; a component version also gets its staging records unless staged is False."""
        for tag in tags:
            self.git("tag", tag)
            component, _, version = tag.partition("/v")
            if staged and component in COMPONENTS:
                for name in konflux_names(component):
                    self.git("tag", f"konflux-staged/{name}-v{version}")

    def tag_all(self, version: str, staged: bool = True) -> None:
        self.tag(f"osac/v{version}", *(f"{component}/v{version}" for component in COMPONENTS), staged=staged)

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

    def test_konflux_releases_only_what_the_run_builds(self) -> None:
        repo = self.repo()
        repo.tag_all("0.5.0")
        cases = {
            "osac-operator=0.5.1": (["osac-operator"], ["osac-chart", "osac-operator-chart", "osac-operator-crds-chart"]),
            "osac-metering=0.5.1": (
                ["metering-service", "metering-m360-adapter", "metering-echo-adapter"],
                ["osac-chart", "osac-metering-chart"],
            ),
            "osac-aap=0.5.1": (["osac-aap"], ["osac-chart", "osac-aap-chart"]),
            "": ([], ["osac-chart"]),
        }
        for components, (images, charts) in cases.items():
            with self.subTest(components=components):
                result, outputs = repo.release("0.5.1", components)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(json.loads(outputs["konflux-images"]), images)
                self.assertEqual(json.loads(outputs["konflux-charts"]), charts)
                self.assertEqual(json.loads(outputs["konflux-chart-versions"]), {chart: "0.5.1" for chart in charts})
                self.assertEqual(json.loads(outputs["konflux-image-versions"]), {image: "0.5.1" for image in images})

    def test_pinned_version_needs_a_staging_record(self) -> None:
        # A failed Konflux build or staging release leaves the version tags
        # behind; nothing is rebuilt under an existing version to make up for it.
        repo = self.repo()
        repo.tag_all("0.5.0")
        repo.tag("osac-operator/v0.5.1", staged=False)
        repo.tag("konflux-staged/osac-operator-v0.5.1", "konflux-staged/osac-operator-crds-chart-v0.5.1")
        result, _ = repo.release("0.5.2", "osac-ui=0.5.1")
        self.assert_rejected(result, "have no konflux-staged/ record")
        self.assertIn("  osac-operator-chart 0.5.1 (osac-operator)\n", result.stderr)
        self.assertNotIn("  osac-operator 0.5.1", result.stderr)
        self.assertNotIn("osac-operator-crds-chart", result.stderr)
        self.assertIn("push the tag konflux-staged/<name>-v<version>", result.stderr)

        repo.tag("konflux-staged/osac-operator-chart-v0.5.1")
        result, outputs = repo.release("0.5.2", "osac-ui=0.5.1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(outputs["konflux-images"]), ["osac-ui"])
        self.assertEqual(json.loads(outputs["konflux-charts"]), ["osac-chart", "osac-ui-chart"])

        # Releasing a new version of the component is the other way out.
        unstaged = self.repo()
        unstaged.tag_all("0.5.0", staged=False)
        self.assert_rejected(unstaged.release("0.5.1", "osac-ui=0.5.1")[0], "osac-operator 0.5.0 (osac-operator)")
        self.assertEqual(unstaged.release("0.5.1", all_at("0.5.1"))[0].returncode, 0)

    def test_nightly_has_no_konflux_release_set(self) -> None:
        outputs = self.repo().nightly()[1]
        self.assertEqual(json.loads(outputs["konflux-images"]), [])
        self.assertEqual(json.loads(outputs["konflux-chart-versions"]), {})
        self.assertEqual(json.loads(outputs["konflux-image-versions"]), {})

    def test_full_release_covers_every_konflux_wait_row(self) -> None:
        # A name prepare emits with no matrix row would never be waited for.
        _, outputs = self.repo().release("0.5.0-rc.1", all_at("0.5.0-rc.1"))
        jobs = WORKFLOW["jobs"]
        promote = yaml.safe_load((ROOT / ".github/workflows/osac-promote-to-prod.yaml").read_text())["jobs"]
        image_rows = jobs["wait-for-konflux-build"]["strategy"]["matrix"]["component"]
        chart_rows = jobs["wait-for-konflux-chart-build"]["strategy"]["matrix"]["component"]
        self.assertCountEqual(json.loads(outputs["konflux-images"]), image_rows)
        self.assertCountEqual(json.loads(outputs["konflux-charts"]), chart_rows)
        self.assertCountEqual(promote["wait-for-prod-chart-build"]["strategy"]["matrix"]["component"], chart_rows)
        self.assertCountEqual(CHARTS, chart_rows)
        self.assertEqual(json.loads(outputs["konflux-chart-versions"]), {chart: "0.5.0-rc.1" for chart in chart_rows})

    def test_konflux_jobs_act_only_on_the_released_set(self) -> None:
        jobs = WORKFLOW["jobs"]
        promote = yaml.safe_load((ROOT / ".github/workflows/osac-promote-to-prod.yaml").read_text())["jobs"]
        gates = {
            "wait-for-konflux-build": (jobs, "contains(fromJSON(needs.prepare.outputs.konflux-images), matrix.component)"),
            "wait-for-konflux-chart-build": (jobs, "contains(fromJSON(needs.prepare.outputs.konflux-charts), matrix.component)"),
            "wait-for-prod-chart-build": (
                promote,
                "contains(fromJSON(needs.resolve-and-verify.outputs.chart-components), matrix.component)",
            ),
        }
        for job, (source, gate) in gates.items():
            with self.subTest(job=job):
                acting = [step for step in source[job]["steps"] if step["name"].startswith(("Log in", "Wait for", "Upload"))]
                self.assertEqual(len(acting), 3)
                for step in acting:
                    self.assertEqual(step["if"], gate)
        acting = [step for step in jobs["trigger-konflux-staging-release"]["steps"] if step.get("id") != "staged-token" and "staged" not in step["name"]]
        for step in acting[1:]:
            self.assertEqual(step["if"], "needs.prepare.outputs.konflux-images != '[]'")
        for job, source, variable in (
            ("trigger-konflux-staging-release", jobs, "KONFLUX_IMAGES"),
            ("trigger-konflux-chart-staging-release", jobs, "KONFLUX_CHARTS"),
            ("release-prod-chart", promote, "CHART_COMPONENTS"),
        ):
            script = next(step["run"] for step in source[job]["steps"] if step["name"].startswith("Assemble"))
            self.assertIn(f'EXPECTED = set(json.loads(os.environ["{variable}"]))', script)
            self.assertIn(variable, source[job]["env"])

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


FAKE_CURL = r"""#!/usr/bin/env python3
import json
import sys
from pathlib import Path

arguments = sys.argv[1:]
output = arguments[arguments.index("-o") + 1]
url = next(argument for argument in arguments if argument.startswith("http"))
state = json.loads(Path("refs.json").read_text())
if "-X" in arguments:
    body = json.loads(arguments[arguments.index("-d") + 1])
    assert "Authorization: Bearer app-token" in arguments
    if body["ref"] in state:
        code = 422
    else:
        state[body["ref"]] = body["sha"]
        code = 201
    Path("refs.json").write_text(json.dumps(state))
    Path(output).write_text("{}")
else:
    ref = "refs/" + url.split("/git/ref/", 1)[1]
    code = 200 if ref in state else 404
    Path(output).write_text(json.dumps({"object": {"sha": state.get(ref)}} if code == 200 else {}))
print(code, end="")
"""


class StagedVersionRecord(TemporaryRepos):
    """The steps that record which versions reached staging, against a fake GitHub API."""

    JOBS = {
        "trigger-konflux-staging-release": ("Record staged image versions", "konflux-image-versions"),
        "trigger-konflux-chart-staging-release": ("Record staged chart versions", "konflux-chart-versions"),
    }

    def record(self, job: str, versions: dict[str, str], refs: dict[str, str]) -> tuple[subprocess.CompletedProcess[str], dict[str, str]]:
        name, output = self.JOBS[job]
        steps = WORKFLOW["jobs"][job]["steps"]
        step = next(step for step in steps if step["name"] == name)
        token = steps[steps.index(step) - 1]
        self.assertEqual(token["if"], "steps.release.outcome == 'success'")
        self.assertEqual(step["if"], "steps.release.outcome == 'success' && steps.staged-token.outcome == 'success'")
        self.assertEqual(step["env"]["STAGED_VERSIONS"], f"${{{{ needs.prepare.outputs.{output} }}}}")
        self.assertTrue(token["continue-on-error"] and step["continue-on-error"])
        work = self.directory()
        (work / "curl").write_text(FAKE_CURL)
        (work / "curl").chmod(0o755)
        (work / "refs.json").write_text(json.dumps(refs))
        result = subprocess.run(
            ["bash", "-c", step["run"]],
            cwd=work,
            env={
                **ENV,
                "PATH": f"{work}:{os.environ['PATH']}",
                "GITHUB_API_URL": "https://api.example",
                "GITHUB_REPOSITORY": "osac-project/osac",
                "GH_APP_TOKEN": "app-token",
                "VERSION": "0.5.2",
                "STAGED_VERSIONS": json.dumps(versions),
            },
            capture_output=True,
            text=True,
            check=False,
        )
        return result, json.loads((work / "refs.json").read_text())

    def test_records_each_released_version_at_the_release_commit(self) -> None:
        for job in self.JOBS:
            with self.subTest(job=job):
                versions = {"osac-chart": "0.5.2", "osac-ui": "0.5.1"}
                result, state = self.record(job, versions, {"refs/tags/osac/v0.5.2": "aaa"})
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(state["refs/tags/konflux-staged/osac-chart-v0.5.2"], "aaa")
                self.assertEqual(state["refs/tags/konflux-staged/osac-ui-v0.5.1"], "aaa")

    def test_existing_record_or_missing_release_tag_does_not_fail_the_step(self) -> None:
        for job in self.JOBS:
            with self.subTest(job=job):
                refs = {"refs/tags/osac/v0.5.2": "aaa", "refs/tags/konflux-staged/osac-chart-v0.5.2": "old"}
                result, state = self.record(job, {"osac-chart": "0.5.2"}, refs)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("already recorded", result.stdout)
                self.assertEqual(state["refs/tags/konflux-staged/osac-chart-v0.5.2"], "old")
                result, state = self.record(job, {"osac-chart": "0.5.2"}, {})
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("::warning::Cannot read tag osac/v0.5.2", result.stdout)
                self.assertEqual(state, {})

    def test_recorded_names_are_what_prepare_requires_for_a_pin(self) -> None:
        # End to end: what one release records is exactly what lets the next one pin it.
        repo = self.repo()
        first, outputs = repo.release("0.5.0-rc.1", all_at("0.5.0-rc.1"))
        self.assertEqual(first.returncode, 0, first.stderr)
        repo.tag_all("0.5.0-rc.1", staged=False)
        recorded: dict[str, str] = {}
        for job, (_, output) in self.JOBS.items():
            _, state = self.record(job, json.loads(outputs[output]), {"refs/tags/osac/v0.5.2": "aaa"})
            recorded |= state
        repo.tag(*(ref.removeprefix("refs/tags/") for ref in recorded if "konflux-staged/" in ref))
        result, _ = repo.release("0.5.0-rc.2", "osac-ui=0.5.0-rc.2")
        self.assertEqual(result.returncode, 0, result.stderr)


class ReleaseSerialization(unittest.TestCase):
    def test_releases_of_a_branch_never_overlap(self) -> None:
        # Guards and chart pipelines read the line's tags; overlapping releases would read each other's.
        release = yaml.safe_load((ROOT / ".github/workflows/osac-release.yaml").read_text())
        self.assertEqual(release["concurrency"], {"group": "osac-release-${{ github.ref }}", "cancel-in-progress": False})


class ProdPromotionInputs(unittest.TestCase):
    def test_image_snapshot_must_be_a_name_or_an_explicit_none(self) -> None:
        promote = yaml.safe_load((ROOT / ".github/workflows/osac-promote-to-prod.yaml").read_text())
        self.assertTrue(promote[True]["workflow_dispatch"]["inputs"]["image_snapshot"]["required"])
        script = next(step["run"] for step in promote["jobs"]["resolve-and-verify"]["steps"] if step.get("id") == "validate")
        cases = {"none": (0, "true"), "osac-release-v0-5-1-attempt1": (0, "false"), "": (1, None), "None": (1, None)}
        for value, (code, omitted) in cases.items():
            with self.subTest(image_snapshot=value), tempfile.TemporaryDirectory() as work:
                output = Path(work) / "out"
                output.write_text("")
                result = subprocess.run(
                    ["bash", "-c", script],
                    env={
                        **ENV,
                        "GITHUB_OUTPUT": str(output),
                        "RELEASE_VERSION": "0.5.1",
                        "IMAGE_SNAPSHOT_INPUT": value,
                        "CHART_SNAPSHOT_INPUT": "osac-charts-release-v0-5-1-attempt1",
                    },
                    capture_output=True,
                    text=True,
                    check=False,
                )
                self.assertEqual(result.returncode, code, result.stderr)
                if omitted is not None:
                    outputs = dict(line.split("=", 1) for line in output.read_text().splitlines())
                    self.assertEqual(outputs["image-omitted"], omitted)
                    self.assertEqual(outputs["image-snapshot"], "" if value == "none" else value)


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
