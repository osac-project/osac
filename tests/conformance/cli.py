"""Command-line entry point for the OSAC conformance suite."""

from __future__ import annotations

import argparse
import contextlib
import os
import sys
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from urllib.parse import urlparse

import yaml

from tests.conformance.config import Config, load_config
from tests.conformance.diagnostics import DiagnosticsCollector
from tests.conformance.domain import DomainDeclaration, load_domains
from tests.conformance.errors import CommandLineError, ConfigError
from tests.conformance.interrupt import Interrupted, InterruptGate
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
from tests.conformance.profiles import ProfileDefinition, expand_profiles, load_profiles
from tests.conformance.redact import Redactor
from tests.conformance.report import write_junit, write_report
from tests.conformance.selection import compute_plan, enrich_identities, plan_from_names
from tests.conformance.target import TargetClient, TargetFacts, read_target
from tests.conformance.testtree import PytestTestTree, TestTree
from tests.conformance.verdict import evaluate, exit_code

_DEFAULT_DOMAINS = Path(__file__).with_name("domains")
_DEFAULT_PROFILES = Path(__file__).with_name("profiles.yaml")
_ALLOWED_RESULTS = {"conformance-plan.yaml"}


@dataclass
class Runtime:
    config: Config
    domains: dict[str, DomainDeclaration]
    profile_catalog: dict[str, ProfileDefinition]
    tree: TestTree
    target: TargetClient
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
    parent.add_argument("--domains", default=str(_DEFAULT_DOMAINS))
    parent.add_argument("--profile-catalog", default=str(_DEFAULT_PROFILES))
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
        aap_url = aap.get("url")
        if isinstance(aap_url, str) and urlparse(aap_url).hostname:
            redactor.add_host(urlparse(aap_url).hostname or "")
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
                server = cluster.get("cluster", {}).get("server") if isinstance(cluster, Mapping) else None
                if isinstance(server, str) and urlparse(server).hostname:
                    redactor.add_host(urlparse(server).hostname or "")
    api_url = target.get("apiURL")
    if isinstance(api_url, str) and urlparse(api_url).hostname:
        redactor.add_host(urlparse(api_url).hostname or "")
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
    domains = load_domains(Path(args.domains))
    catalog = load_profiles(Path(args.profile_catalog))
    config = load_config(Path(args.config), domain_names=domains)
    expand_profiles(config.profiles, catalog)
    results = Path(args.results)
    _check_results(results)
    redactor = redactor_for_config(config)
    return Runtime(
        config=config,
        domains=domains,
        profile_catalog=catalog,
        tree=PytestTestTree(root=Path(args.tree)),
        target=KubectlTargetClientAdapter(),
        results=results,
        redactor=redactor,
        output=OutputSink(results, redactor),
        suite_image_digest=os.environ.get("OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST", ""),
        podman_socket=Path(args.podman_socket),
    )


class KubectlTargetClientAdapter:
    """Default target client; imported lazily so unit tests need no kubectl."""

    def capabilities(self, *, url: str, token: str, ca_file: Path | None):  # type: ignore[no-untyped-def]
        from tests.conformance.target import KubectlTargetClient

        return KubectlTargetClient().capabilities(url=url, token=token, ca_file=ca_file)

    def registrations(self, *, kubeconfig: Path, namespace: str):  # type: ignore[no-untyped-def]
        from tests.conformance.target import KubectlTargetClient

        return KubectlTargetClient().registrations(kubeconfig=kubeconfig, namespace=namespace)

    def installed_release(self, *, kubeconfig: Path, namespace: str) -> str:
        from tests.conformance.target import KubectlTargetClient

        return KubectlTargetClient().installed_release(kubeconfig=kubeconfig, namespace=namespace)


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
    plan: Plan, outcomes: Sequence[TestOutcome], catalog: dict[str, ProfileDefinition]
) -> tuple[ProfileResult, ...]:
    by_id = {outcome.identity.id: outcome for outcome in outcomes}
    results: list[ProfileResult] = []
    for name in sorted(catalog):
        profile = catalog[name]
        selected = [test for test in plan.tests if test.layer is Layer.PROFILE and name in test.profiles]
        required = name in plan.profiles and bool(selected)
        if not required:
            results.append(ProfileResult(name, profile.selectors, False, Result.SKIP))
            continue
        blocking = [by_id.get(test.id) for test in selected if test.classification is Classification.BLOCKING]
        if not blocking:
            results.append(ProfileResult(name, profile.selectors, True, Result.SKIP))
            continue
        if all(outcome is not None and outcome.result is Result.PASS for outcome in blocking):
            results.append(ProfileResult(name, profile.selectors, True, Result.PASS))
        else:
            results.append(ProfileResult(name, profile.selectors, True, Result.FAIL))
    return tuple(results)


def _derive_components(
    facts: TargetFacts,
    plan: Plan,
    outcomes: Sequence[TestOutcome],
    profiles: Sequence[ProfileResult],
    domains: dict[str, DomainDeclaration],
) -> tuple[ComponentResult, ...]:
    by_id = {outcome.identity.id: outcome for outcome in outcomes}
    results: list[ComponentResult] = []
    for component in facts.components:
        tests = [test for test in plan.tests if test.domain == component.domain and test.layer is Layer.COMPONENT]
        component_tests_pass = bool(tests) and all(
            by_id.get(test.id) is not None and by_id[test.id].result is Result.PASS for test in tests
        )
        domain = domains.get(component.domain)
        domain_selectors = set(domain.selectors) if domain is not None else set()
        relevant_profiles = [
            profile for profile in profiles if profile.required and domain_selectors.intersection(profile.selectors)
        ]
        domain_profiles_pass = all(profile.result is Result.PASS for profile in relevant_profiles)
        verdict = Verdict.CONFORMANT if component_tests_pass and domain_profiles_pass else Verdict.IN_PROGRESS
        results.append(ComponentResult(component.domain, component.fields, verdict))
    return tuple(results)


def _run_list(runtime: Runtime) -> int:
    selected_profiles, selectors = expand_profiles(runtime.config.profiles, runtime.profile_catalog)
    raw = runtime.tree.collect()
    tests: tuple[TestIdentity, ...] = enrich_identities(
        raw, runtime.domains, runtime.profile_catalog, selected_profiles
    )
    plan = compute_plan(tests, selectors, selected_profiles)
    path = write_plan(plan, runtime.results)
    runtime.output.line(path.read_text().rstrip())
    return 0


def _run_plan(runtime: Runtime, *, plan_path: Path | None, keep: bool) -> int:
    selected_profiles, selectors = expand_profiles(runtime.config.profiles, runtime.profile_catalog)
    raw = runtime.tree.collect()
    tests = enrich_identities(raw, runtime.domains, runtime.profile_catalog, selected_profiles)
    if plan_path is None:
        plan = compute_plan(tests, selectors, selected_profiles)
    else:
        plan = plan_from_names(tests, _plan_names(plan_path), selectors, selected_profiles)
    if not plan.tests:
        raise ConfigError(["the execution plan holds no tests"])
    if (
        any(test.layer is Layer.COMPONENT for test in plan.tests)
        and runtime.podman_socket is not None
        and not runtime.podman_socket.exists()
    ):
        raise ConfigError([f"podman socket {runtime.podman_socket} is missing"])

    facts = read_target(runtime.config, runtime.target, runtime.domains)
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

    profiles = _derive_profiles(plan, outcomes, runtime.profile_catalog)
    components = _derive_components(facts, plan, outcomes, profiles, runtime.domains)
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
