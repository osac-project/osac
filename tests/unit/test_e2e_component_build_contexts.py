"""Every E2E component declaration must give its Containerfile a usable build context.

The E2E suites hand `osac-test-infra`'s reusable workflow a `components` list and it runs
`podman build -f <repo>/<containerfile> <repo>/<context>` for each entry, so a COPY source that
falls outside the declared context cannot be read. That is a configuration error no YAML linter
sees: it only surfaces as `copier: stat: "<path>": no such file or directory` an hour into a
scheduled run. These checks pin every declaration against the Containerfile it names.
"""

from __future__ import annotations

import json
import re
from pathlib import Path
from typing import NamedTuple

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[2]
WORKFLOWS = ROOT / ".github/workflows"
RESOLVER = ROOT / ".github/actions/resolve-e2e-components/action.yaml"
_RESOLVER_SOURCE = f"{RESOLVER.parent.name}/{RESOLVER.name}"

# A whole-line Containerfile comment, and an instruction continued onto the next line. Comments
# are dropped before continuations are joined, which is the order the builder itself uses: a
# comment line inside a continuation is removed rather than ending the instruction.
_COMMENT = re.compile(r"^[ \t]*#.*\n?", re.MULTILINE)
_CONTINUED = re.compile(r"\\\s*\n")
_COPY = re.compile(r"^\s*COPY\s+(?P<arguments>\S.*)$", re.IGNORECASE)
# The resolver builds its list inside a jq program. Both of its arrays are flat (no nested
# brackets) and only the two `--arg` names appear unquoted, so this stays parseable as JSON.
_JQ_ARRAY = re.compile(r"\[[^][]*\"containerfile\"[^][]*\]")


class Declaration(NamedTuple):
    """One component entry, named by where it is declared."""

    source: str
    containerfile: str
    context: str

    def __str__(self) -> str:
        return f"{self.source}:{self.containerfile}(context={self.context})"


def _context_of(entry: dict, containerfile: str) -> str:
    # Matches the reusable workflow: an omitted context defaults to the Containerfile's
    # own parent directory, so only an explicit context can widen or narrow it.
    context = entry.get("context")
    if context:
        return str(context)
    parent = Path(containerfile).parent
    return "." if parent == Path() else str(parent)


def _entries(source: str, raw_entries: list[dict]) -> list[Declaration]:
    declarations: list[Declaration] = []
    for entry in raw_entries:
        # `ansible-builder` entries name an execution-environment.yaml and are built from a
        # generated context, so they have no Containerfile of their own to check here.
        if entry.get("buildType", "containerfile") != "containerfile":
            continue
        # The same default the reusable workflow applies to a missing `containerfile`.
        containerfile = str(entry.get("containerfile", "Containerfile"))
        declarations.append(Declaration(source, containerfile, _context_of(entry, containerfile)))
    return declarations


def _workflow_declarations() -> list[Declaration]:
    declarations: list[Declaration] = []
    for path in sorted(WORKFLOWS.glob("e2e-*-full-install.yml")):
        workflow = yaml.safe_load(path.read_text())
        for job in workflow["jobs"].values():
            raw = job.get("with", {}).get("components")
            if raw:
                declarations += _entries(path.name, json.loads(raw))
    return declarations


def _resolver_declarations() -> list[Declaration]:
    action = yaml.safe_load(RESOLVER.read_text())
    script = "\n".join(step["run"] for step in action["runs"]["steps"] if "run" in step)
    declarations: list[Declaration] = []
    for array in _JQ_ARRAY.findall(script):
        literal = array.replace("$repo", '"repo"').replace("$ref", '"ref"')
        declarations += _entries(_RESOLVER_SOURCE, json.loads(literal))
    return declarations


def _declarations() -> list[Declaration]:
    return _workflow_declarations() + _resolver_declarations()


def _copy_sources(containerfile: Path) -> list[str]:
    """Return the build-context paths a Containerfile copies, ignoring earlier build stages."""
    sources: list[str] = []
    text = _CONTINUED.sub(" ", _COMMENT.sub("", containerfile.read_text()))
    for line in text.splitlines():
        match = _COPY.match(line)
        if not match:
            continue
        arguments = match.group("arguments").split()
        assert not arguments[0].startswith("["), f"{containerfile}: COPY exec form is not parsed here"
        if any(argument.startswith("--from=") for argument in arguments):
            continue
        operands = [argument for argument in arguments if not argument.startswith("--")]
        # Everything but the trailing destination is read from the build context.
        sources += operands[:-1]
    return sources


DECLARATIONS = _declarations()


@pytest.mark.parametrize("declaration", DECLARATIONS, ids=str)
def test_copy_sources_are_inside_the_declared_context(declaration: Declaration) -> None:
    containerfile = ROOT / declaration.containerfile
    assert containerfile.is_file(), f"{declaration} names a Containerfile that does not exist"

    context = ROOT / declaration.context
    sources = _copy_sources(containerfile)
    assert sources, f"{declaration} names a Containerfile with no COPY from the build context"

    unreachable = [source for source in sources if not (context / source).exists()]
    assert not unreachable, (
        f"{declaration} cannot reach {unreachable}. Set the context to the directory those paths are relative to."
    )


def test_every_declaration_site_is_covered() -> None:
    # Guards the parsing above: a declaration site that stops being read would otherwise turn
    # this file into a silent no-op instead of a failure. Only callers that actually declare
    # components are required to contribute, so adding a caller without them is not a failure.
    callers = {
        path.name
        for path in WORKFLOWS.glob("e2e-*-full-install.yml")
        for job in yaml.safe_load(path.read_text())["jobs"].values()
        if job.get("with", {}).get("components")
    }
    assert callers, "No e2e-*-full-install.yml caller declares components"
    assert {declaration.source for declaration in DECLARATIONS} == callers | {_RESOLVER_SOURCE}


def test_echo_adapter_builds_from_the_repository_root() -> None:
    # The echo adapter's Containerfile copies go.work and every workspace sibling's go.mod from
    # the repository root, matching its Makefile target and GHCR publish workflow. Narrowing its
    # context to osac-metering/ broke the scheduled E2E at `COPY --chown=1001:0 go.work ./`.
    adapter = [item for item in DECLARATIONS if item.containerfile.endswith("Containerfile.echo-adapter")]
    assert adapter, "Expected at least one echo-adapter component declaration"
    assert {item.context for item in adapter} == {"."}
