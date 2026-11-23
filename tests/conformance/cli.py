"""Command-line entry point for the OSAC conformance suite."""

from __future__ import annotations

import argparse
import contextlib
import sys
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from urllib.parse import urlparse

import yaml

from tests.conformance import yamlio
from tests.conformance.catalog import Catalog, ProfileDefinition, expand_profiles, load_catalog
from tests.conformance.config import Config, load_config
from tests.conformance.diagnostics import DiagnosticsCollector
from tests.conformance.errors import CommandLineError, ConfigError
from tests.conformance.image import suite_image_digest
from tests.conformance.interrupt import Interrupted, InterruptGate
from tests.conformance.model import Classification, Plan, ProfileResult, Result, RunContext, TestIdentity, TestOutcome
from tests.conformance.output import OutputSink, new_run_id, write_plan
from tests.conformance.redact import Redactor
from tests.conformance.report import write_junit, write_report
from tests.conformance.selection import assign_profiles, compute_plan, plan_from_names
from tests.conformance.testtree import PytestTestTree, TestTree
from tests.conformance.verdict import evaluate, exit_code

_DEFAULT_CATALOG = Path(__file__).with_name("catalog.yaml")
_ALLOWED_RESULTS = {"conformance-plan.yaml"}


@dataclass
class Runtime:
    config: Config
    catalog: Catalog
    tree: TestTree
    results: Path
    redactor: Redactor
    output: OutputSink
    suite_image_digest: str = ""


class _Parser(argparse.ArgumentParser):
    def error(self, message: str) -> None:
        raise CommandLineError(message)


def _parser() -> argparse.ArgumentParser:
    parent = _Parser(add_help=False)
    parent.add_argument("--config", default="/config/conformance.yaml")
    parent.add_argument("--results", default="/results")
    parent.add_argument("--catalog", default=str(_DEFAULT_CATALOG))
    parent.add_argument("--tree", default="tests")
    # Omit the flag to discover the Podman or Docker unix socket.
    parent.add_argument("--runtime-socket", default=None)

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


def _hostname(value: object) -> str:
    """The host of a configured URL, or "" when it does not parse.

    This runs on the configuration-error path as well, where a malformed URL
    must not replace the diagnostic that reported it.
    """
    if not isinstance(value, str):
        return ""
    try:
        return urlparse(value).hostname or ""
    except ValueError:
        return ""


def _redactor_from_document(document: Mapping[str, object]) -> Redactor:
    """Build the one redaction boundary from a raw conformance.yaml document."""
    redactor = Redactor()
    target = document.get("target")
    if not isinstance(target, Mapping):
        return redactor
    token_files: list[str] = []
    token_file = target.get("tokenFile")
    if isinstance(token_file, str):
        token_files.append(token_file)
    aap = target.get("aap")
    if isinstance(aap, Mapping):
        aap_token_file = aap.get("tokenFile")
        if isinstance(aap_token_file, str):
            token_files.append(aap_token_file)
        aap_host = _hostname(aap.get("url"))
        if aap_host:
            redactor.add_host(aap_host)
    for value in token_files:
        with contextlib.suppress(OSError):
            redactor.add_secret(Path(value).read_text().strip())
    hub: object = None
    kubeconfig = target.get("kubeconfig")
    if isinstance(kubeconfig, str):
        with contextlib.suppress(OSError, yaml.YAMLError):
            hub = yaml.safe_load(Path(kubeconfig).read_text())
    if isinstance(hub, Mapping):
        users = hub.get("users", [])
        if isinstance(users, list):
            for user in users:
                credentials = user.get("user", {}) if isinstance(user, Mapping) else {}
                if isinstance(credentials, Mapping):
                    for key, value in credentials.items():
                        if key in {"token", "password", "client-key-data"} and isinstance(value, str):
                            redactor.add_secret(value)
        clusters = hub.get("clusters", [])
        if isinstance(clusters, list):
            for cluster in clusters:
                settings = cluster.get("cluster") if isinstance(cluster, Mapping) else None
                if not isinstance(settings, Mapping):
                    continue
                server_host = _hostname(settings.get("server"))
                if server_host:
                    redactor.add_host(server_host)
    api_host = _hostname(target.get("apiURL"))
    if api_host:
        redactor.add_host(api_host)
    return redactor


def redactor_for_config(config: Config) -> Redactor:
    target: dict[str, object] = {
        "kubeconfig": str(config.target_kubeconfig),
        "apiURL": config.target_api_url,
        "tokenFile": str(config.target_token_file),
    }
    if config.target_aap is not None:
        target["aap"] = {"url": config.target_aap.url, "tokenFile": str(config.target_aap.token_file)}
    return _redactor_from_document({"target": target})


def redactor_for_config_path(path: Path) -> Redactor:
    """Best-effort redactor for configuration errors raised before a Config exists."""
    try:
        document = yaml.safe_load(path.read_text())
    except (OSError, yaml.YAMLError):
        return Redactor()
    if not isinstance(document, Mapping):
        return Redactor()
    return _redactor_from_document(document)


def _build_runtime(args: argparse.Namespace) -> Runtime:
    catalog = load_catalog(Path(args.catalog))
    # Resolve against the invocation directory: the pytest child runs from the
    # tree root and reads this path from OSAC_CONFORMANCE_CONFIG.
    config = load_config(Path(args.config).resolve())
    expand_profiles(config.profiles, catalog)
    results = Path(args.results)
    _check_results(results)
    redactor = redactor_for_config(config)
    return Runtime(
        config=config,
        catalog=catalog,
        # Resolve once: pytest runs in a different working directory.
        tree=PytestTestTree(root=Path(args.tree).resolve()),
        results=results,
        redactor=redactor,
        output=OutputSink(results, redactor),
        suite_image_digest=suite_image_digest(Path(args.runtime_socket) if args.runtime_socket is not None else None),
    )


def _plan_names(plan_path: Path) -> list[str]:
    try:
        text = plan_path.read_text()
    except OSError as exc:
        raise ConfigError([f"{plan_path} cannot be read: {exc}"]) from None
    data = yamlio.loads(text, str(plan_path))
    if not isinstance(data, dict) or not isinstance(data.get("tests"), list):
        raise ConfigError([f"{plan_path} must hold a tests list"])
    names: list[str] = []
    for item in data["tests"]:
        if isinstance(item, str):
            names.append(item)
        elif isinstance(item, dict):
            # ``key`` is the opaque replay handle written next to the redacted
            # ``id``; plans written before that field existed still replay by id.
            identifier = item.get("key") or item.get("id")
            if not isinstance(identifier, str):
                raise ConfigError([f"{plan_path} has a test entry without an id"])
            names.append(identifier)
        else:
            raise ConfigError([f"{plan_path} has a test entry without an id"])
    return names


def _derive_profiles(plan: Plan, outcomes: Sequence[TestOutcome], catalog: Catalog) -> tuple[ProfileResult, ...]:
    by_id = {outcome.identity.id: outcome for outcome in outcomes}
    results: list[ProfileResult] = []
    # Report the profiles this run selected, not every profile the image ships.
    for profile_id in plan.profiles:
        profile: ProfileDefinition = catalog.profiles[profile_id]
        selected = [test for test in plan.tests if profile_id in test.profiles]
        if not selected:
            results.append(ProfileResult(profile_id, profile.selectors, False, Result.SKIP))
            continue
        blocking = [by_id.get(test.id) for test in selected if test.classification is Classification.BLOCKING]
        if not blocking:
            results.append(ProfileResult(profile_id, profile.selectors, True, Result.SKIP))
            continue
        if all(outcome is not None and outcome.result is Result.PASS for outcome in blocking):
            results.append(ProfileResult(profile_id, profile.selectors, True, Result.PASS))
        else:
            results.append(ProfileResult(profile_id, profile.selectors, True, Result.FAIL))
    return tuple(results)


def _run_list(runtime: Runtime) -> int:
    selected_profiles, selectors = expand_profiles(runtime.config.profiles, runtime.catalog)
    raw = runtime.tree.collect()
    tests: tuple[TestIdentity, ...] = assign_profiles(raw, runtime.catalog, selected_profiles)
    plan = compute_plan(tests, selectors, selected_profiles)
    path = write_plan(plan, runtime.results, runtime.redactor)
    runtime.output.echo(path.read_text().rstrip())
    return 0


def _run_plan(runtime: Runtime, *, plan_path: Path | None, keep: bool) -> int:
    selected_profiles, selectors = expand_profiles(runtime.config.profiles, runtime.catalog)
    raw = runtime.tree.collect()
    tests = assign_profiles(raw, runtime.catalog, selected_profiles)
    if plan_path is None:
        plan = compute_plan(tests, selectors, selected_profiles)
    else:
        plan = plan_from_names(tests, _plan_names(plan_path), selectors, selected_profiles)
    if not plan.tests:
        raise ConfigError(["the execution plan holds no tests"])

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
        outcomes = runtime.tree.run(plan, context, keep=keep, interrupt=gate)
    except Interrupted as exc:
        outcomes = exc.outcomes
        interrupted = True
    except KeyboardInterrupt:
        return 130
    finally:
        gate.restore()
    interrupted = interrupted or gate.first

    profiles = _derive_profiles(plan, outcomes, runtime.catalog)
    report = evaluate(
        plan,
        outcomes,
        profiles,
        (),
        osac_release=runtime.config.osac_release,
        suite_image_digest=runtime.suite_image_digest,
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

    active: Runtime | None = None
    try:
        active = runtime or _build_runtime(args)
        if args.command == "list":
            return _run_list(active)
        return _run_plan(active, plan_path=Path(args.plan) if args.plan else None, keep=args.keep)
    except ConfigError as exc:
        redactor = active.redactor if active is not None else redactor_for_config_path(Path(args.config))
        for check in exc.checks:
            print(f"osac-conformance: {redactor.text(check)}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
