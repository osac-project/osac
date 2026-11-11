"""Command-line entry point for the OSAC conformance suite."""

from __future__ import annotations

import argparse
import contextlib
import os
import sys
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path
from urllib.parse import urlparse

import yaml

from tests.conformance.area import AreaDeclaration, load_areas
from tests.conformance.config import Config, load_config
from tests.conformance.diagnostics import DiagnosticsCollector
from tests.conformance.errors import CommandLineError, ConfigError
from tests.conformance.interrupt import Interrupted, InterruptGate
from tests.conformance.lab import LabClient, LabFacts, read_lab
from tests.conformance.model import (
    Classification,
    ComponentResult,
    Layer,
    Plan,
    ProfileResult,
    Result,
    RunContext,
    TestIdentity,
    TestOutcome,
    Verdict,
)
from tests.conformance.output import OutputSink, new_run_id, write_plan
from tests.conformance.redact import Redactor
from tests.conformance.report import write_junit, write_report
from tests.conformance.selection import compute_plan, enrich_identities, plan_from_names
from tests.conformance.testtree import PytestTestTree, TestTree
from tests.conformance.verdict import evaluate, exit_code

_DEFAULT_AREAS = Path(__file__).with_name("areas")
_ALLOWED_RESULTS = {"conformance-plan.yaml"}


@dataclass
class Runtime:
    config: Config
    areas: dict[str, AreaDeclaration]
    tree: TestTree
    lab: LabClient
    results: Path
    redactor: Redactor
    output: OutputSink
    suite_image_digest: str = ""
    podman_socket: Path | None = None


class _Parser(argparse.ArgumentParser):
    def error(self, message: str) -> None:
        raise CommandLineError(message)


def _parser() -> argparse.ArgumentParser:
    parent = _Parser(add_help=False)
    parent.add_argument("--config", default="/config/conformance.yaml")
    parent.add_argument("--results", default="/results")
    parent.add_argument("--areas", default=str(_DEFAULT_AREAS))
    parent.add_argument("--tree", default="tests/e2e")
    parent.add_argument("--podman-socket", default="/run/podman/podman.sock")

    parser = _Parser(prog="osac-conformance", add_help=True)
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("list", parents=[parent], add_help=True)
    run = sub.add_parser("run", parents=[parent], add_help=True)
    run.add_argument("--plan")
    run.add_argument("--keep", action="store_true")
    return parser


def _check_results(path: Path) -> None:
    if path.exists():
        extra = {entry.name for entry in path.iterdir()} - _ALLOWED_RESULTS
        if extra:
            raise ConfigError([f"{path} is not empty: {', '.join(sorted(extra))}"])
    else:
        path.mkdir(parents=True)


def _redactor(config: Config) -> Redactor:
    values: list[str] = []
    for token_file in (config.lab_token_file, config.lab_aap.token_file if config.lab_aap else None):
        if token_file is not None:
            with contextlib.suppress(OSError):
                values.append(token_file.read_text().strip())
    redactor = Redactor(secrets=values)
    host = urlparse(config.lab_api_url).hostname
    if host:
        redactor.add_host(host)
    return redactor


def _build_runtime(args: argparse.Namespace) -> Runtime:
    areas = load_areas(Path(args.areas))
    config = load_config(Path(args.config), area_names=areas)
    results = Path(args.results)
    _check_results(results)
    redactor = _redactor(config)
    return Runtime(
        config=config,
        areas=areas,
        tree=PytestTestTree(root=Path(args.tree)),
        lab=KubectlLabClientAdapter(),
        results=results,
        redactor=redactor,
        output=OutputSink(results, redactor),
        suite_image_digest=os.environ.get("OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST", ""),
        podman_socket=Path(args.podman_socket),
    )


class KubectlLabClientAdapter:
    """Default lab client; imported lazily so unit tests need no kubectl."""

    def capabilities(self, *, url: str, token: str, ca_file: Path | None):  # type: ignore[no-untyped-def]
        from tests.conformance.lab import KubectlLabClient

        return KubectlLabClient().capabilities(url=url, token=token, ca_file=ca_file)

    def registrations(self, *, kubeconfig: Path):  # type: ignore[no-untyped-def]
        from tests.conformance.lab import KubectlLabClient

        return KubectlLabClient().registrations(kubeconfig=kubeconfig)

    def installed_release(self, *, kubeconfig: Path) -> str:
        from tests.conformance.lab import KubectlLabClient

        return KubectlLabClient().installed_release(kubeconfig=kubeconfig)


def _plan_names(plan_path: Path) -> list[str]:
    try:
        data = yaml.safe_load(plan_path.read_text())
    except OSError as exc:
        raise ConfigError([f"{plan_path} cannot be read: {exc}"]) from None
    except yaml.YAMLError as exc:
        raise ConfigError([f"{plan_path} is not valid YAML: {exc}"]) from None
    if not isinstance(data, dict) or not isinstance(data.get("tests"), list):
        raise ConfigError([f"{plan_path} must hold a tests list"])
    names: list[str] = []
    for item in data["tests"]:
        if isinstance(item, str):
            names.append(item)
        elif isinstance(item, dict) and isinstance(item.get("id"), str):
            names.append(item["id"])
        else:
            raise ConfigError([f"{plan_path} has a test entry without an id"])
    return names


def _derive_profiles(
    plan: Plan, outcomes: Sequence[TestOutcome], areas: dict[str, AreaDeclaration]
) -> tuple[ProfileResult, ...]:
    by_id = {outcome.identity.id: outcome for outcome in outcomes}
    results: list[ProfileResult] = []
    for area_name, area in sorted(areas.items()):
        for profile_name in sorted(area.profiles):
            selected = [test for test in plan.tests if test.layer is Layer.PROFILE and test.profile == profile_name]
            if not selected:
                results.append(ProfileResult(profile_name, area_name, False, Result.SKIP))
                continue
            blocking = [by_id.get(test.id) for test in selected if test.classification is Classification.BLOCKING]
            if not blocking:
                results.append(ProfileResult(profile_name, area_name, True, Result.SKIP))
                continue
            if all(outcome is not None and outcome.result is Result.PASS for outcome in blocking):
                results.append(ProfileResult(profile_name, area_name, True, Result.PASS))
            else:
                results.append(ProfileResult(profile_name, area_name, True, Result.FAIL))
    return tuple(results)


def _derive_components(
    facts: LabFacts, plan: Plan, outcomes: Sequence[TestOutcome], profiles: Sequence[ProfileResult]
) -> tuple[ComponentResult, ...]:
    by_id = {outcome.identity.id: outcome for outcome in outcomes}
    results: list[ComponentResult] = []
    for component in facts.components:
        tests = [test for test in plan.tests if test.area == component.area and test.layer is Layer.COMPONENT]
        component_tests_pass = bool(tests) and all(
            by_id.get(test.id) is not None and by_id[test.id].result is Result.PASS for test in tests
        )
        area_profiles_pass = all(
            profile.result is Result.PASS for profile in profiles if profile.area == component.area and profile.required
        )
        verdict = Verdict.CONFORMANT if component_tests_pass and area_profiles_pass else Verdict.IN_PROGRESS
        results.append(ComponentResult(component.area, component.fields, verdict))
    return tuple(results)


def _run_list(runtime: Runtime) -> int:
    raw = runtime.tree.collect()
    tests: tuple[TestIdentity, ...] = enrich_identities(raw, runtime.areas)
    plan = compute_plan(tests, runtime.config.selectors)
    path = write_plan(plan, runtime.results)
    runtime.output.line(path.read_text().rstrip())
    return 0


def _run_plan(runtime: Runtime, *, plan_path: Path | None, keep: bool) -> int:
    raw = runtime.tree.collect()
    tests = enrich_identities(raw, runtime.areas)
    if plan_path is None:
        plan = compute_plan(tests, runtime.config.selectors)
    else:
        plan = plan_from_names(tests, _plan_names(plan_path), runtime.config.selectors)
    if not plan.tests:
        raise ConfigError(["the execution plan holds no tests"])
    if (
        runtime.podman_socket is not None
        and any(test.layer is Layer.COMPONENT for test in plan.tests)
        and not runtime.podman_socket.exists()
    ):
        raise ConfigError([f"podman socket {runtime.podman_socket} is missing"])

    facts = read_lab(runtime.config, runtime.lab)
    run_id = new_run_id()
    context = RunContext(
        run_id=run_id,
        results_dir=runtime.results,
        config=runtime.config,
        redactor=runtime.redactor,
        output=runtime.output,
    )
    runtime.output.line(f"run {run_id}: {len(plan.tests)} tests")
    gate = InterruptGate()
    gate.install()
    interrupted = False
    outcomes: tuple[TestOutcome, ...] = ()
    try:
        outcomes = runtime.tree.run(plan, context, keep=keep)
    except Interrupted:
        interrupted = True
    except KeyboardInterrupt:
        return 130
    finally:
        gate.restore()

    profiles = _derive_profiles(plan, outcomes, runtime.areas)
    components = _derive_components(facts, plan, outcomes, profiles)
    report = evaluate(
        plan,
        outcomes,
        profiles,
        components,
        (),
        osac_release=runtime.config.osac_release,
        suite_image_digest=runtime.suite_image_digest,
        enabled_services=facts.enabled_services,
    )
    write_report(report, runtime.results, runtime.redactor)
    write_junit(report, runtime.results, runtime.redactor)
    DiagnosticsCollector().collect(context, context.tenant)
    runtime.output.line(f"result: {report.result.value}, exit {exit_code(report, focused=not plan.complete)}")
    if interrupted:
        return 130
    return exit_code(report, focused=not plan.complete)


def main(argv: Sequence[str] | None = None, *, runtime: Runtime | None = None) -> int:
    parser = _parser()
    try:
        args = parser.parse_args(argv)
    except CommandLineError as exc:
        print(f"osac-conformance: {exc}", file=sys.stderr)
        return 2

    try:
        active = runtime or _build_runtime(args)
        if args.command == "list":
            return _run_list(active)
        return _run_plan(active, plan_path=Path(args.plan) if args.plan else None, keep=args.keep)
    except ConfigError as exc:
        for check in exc.checks:
            print(f"osac-conformance: {check}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
