"""The jira-reference check: Prow's label counts, and so does the title of a cherry-pick of the cherry-pick robot, whose
"[release-X.Y]" prefix keeps Prow's jira plugin from labelling it. Its shell runs with a stand-in `gh`."""

import json
import os
import stat
import subprocess
from pathlib import Path

import pytest
import yaml

pytestmark = pytest.mark.unit
ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github" / "workflows" / "jira-reference.yml"
GH = '#!/bin/bash\nprintf \'%s\' "$GH_VIEW"\n'
BOT = "openshift-cherrypick-robot"


def step() -> dict:
    """The step of the workflow that checks the label."""
    steps = yaml.safe_load(WORKFLOW.read_text(encoding="utf-8"))["jobs"]["jira-reference"]["steps"]
    return next(s for s in steps if s["name"] == "Check the Jira reference label")


def run(tmp_path: Path, *, title: str, author: str, labels: tuple[str, ...] = ()) -> subprocess.CompletedProcess[str]:
    """Run the shell of the step for a pull request with this title, author and labels, `gh` being a stand-in."""
    fake = tmp_path / "gh"
    fake.write_text(GH, encoding="utf-8")
    fake.chmod(fake.stat().st_mode | stat.S_IXUSR)
    view = {"title": title, "author": {"login": author}, "labels": [{"name": n} for n in labels]}
    env = {
        **os.environ,
        "PATH": f"{tmp_path}:{os.environ['PATH']}",
        "GH_VIEW": json.dumps(view),
        "REPO": "o/r",
        "PR_NUMBER": "7",
        "CHERRYPICK_BOT": step()["env"]["CHERRYPICK_BOT"],
    }
    return subprocess.run(["bash", "-e", "-c", step()["run"]], env=env, capture_output=True, text=True, check=False)


def test_the_label_of_prow_is_enough_for_anybody(tmp_path: Path) -> None:
    """Prow's label passes for any author and any title."""
    assert run(tmp_path, title="Fix a thing", author="dev", labels=("jira/valid-reference",)).returncode == 0


@pytest.mark.parametrize(
    "title",
    [
        "[release-0.4] NO-ISSUE: wait for the issuer",
        "[release-0.4] OSAC-6264: add the upgrade test",
        "[release-1.12] NO-ISSUE: x",
        "[release-10.0] OSAC-1: x",
    ],
)
def test_a_cherry_pick_of_the_robot_keeps_the_reference_of_the_pull_request_it_copies(tmp_path: Path, title: str) -> None:
    """The robot's titles with a release branch and then a reference pass without the label."""
    done = run(tmp_path, title=title, author=BOT)
    assert done.returncode == 0 and "cherry-pick" in done.stdout


@pytest.mark.parametrize(
    ("title", "author"),
    [
        ("[release-0.4] fix something", BOT),  # no reference at all
        ("[release-0.4] NO-ISSUE wait", BOT),  # not the form Prow reads
        ("[main] NO-ISSUE: x", BOT),  # not a release branch
        ("[release-0] NO-ISSUE: x", BOT),  # a version needs a major and a minor part
        ("[release-0.] NO-ISSUE: x", BOT),
        ("[release-.4] NO-ISSUE: x", BOT),
        ("[release-0.4.1] NO-ISSUE: x", BOT),  # the robot names a release X.Y branch
        ("[release-0abc] NO-ISSUE: x", BOT),
        ("NO-ISSUE: x", BOT),  # no prefix and no label: Prow has not decided yet
        ("[release-0.4] NO-ISSUE: wait for the issuer", "someone-else"),  # only the robot's titles are trusted
        ("[release-0.4] NO-ISSUE: x", BOT + "-fork"),
    ],
)
def test_nothing_else_passes_without_the_label(tmp_path: Path, title: str, author: str) -> None:
    """Any other title or author, or a branch that is not a release X.Y, fails without the label."""
    done = run(tmp_path, title=title, author=author)
    assert done.returncode == 1 and "no valid Jira reference" in done.stdout
