"""Synthetic endpoint privacy contracts through real command and reporting consumers."""

from __future__ import annotations

import json
import logging
import subprocess
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from unittest.mock import Mock

import pytest

from tests.e2e.core import failure_report as reporting
from tests.e2e.core import runner
from tests.e2e.core import sanitize as diagnostics
from tests.e2e.core.failure_report import FailureReporter
from tests.e2e.core.failure_snapshot import write_json
from tests.e2e.core.failures import Kind, classify
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.sanitize import sanitize_args, sanitize_obj, sanitize_text
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.unit.test_e2e_failure_plugin import CONFTEST, run_pytest

HOST = "private-api.synthetic.example.invalid"
ADDRESS = f"{HOST}:7443"
METHOD = "osac.private.v1.ExternalIPPools/Create"
FIXTURE_TOKEN = "synthetic-only-token"
FIXTURE_PASSWORD = "fixture-password-value"
DETAIL = f"Code: PermissionDenied\nMessage: denied at {ADDRESS}; host {HOST}."


@pytest.mark.parametrize("scheme", ["HTTP", "HTTPS", "GRPC", "GRPCS", "hTtP", "hTtPs", "gRpC", "gRpCs"])
@pytest.mark.parametrize("as_bytes", [False, True])
def test_mixed_case_url_targets_stay_private_in_failure_consumers(
    scheme: str, as_bytes: bool, tmp_path: Path, caplog: pytest.LogCaptureFixture
) -> None:
    """Learn fresh URL targets without changing caller captures or useful RPC evidence."""
    target = f"{scheme}://{ADDRESS}/fixture/path?request=fixture-id"
    argv = ["osac", "--endpoint=" + target, METHOD]
    cleaned = sanitize_args(argv)
    assert cleaned[1] == f"--endpoint={scheme}://<redacted>/fixture/path?request=fixture-id"
    detail = f"{DETAIL}; password={FIXTURE_PASSWORD}"
    raw = detail.encode() if as_bytes else detail
    error = runner.CommandFailed(7, argv, output=raw, stderr=raw)
    result = classify(error)
    assert result.kind is Kind.API_REFUSED
    assert "PermissionDenied" in result.summary
    reporter = FailureReporter(Mock())
    reporter.snapshotter = Mock()
    reporter.snapshotter.take.return_value = None
    reporter._path = tmp_path / "results-main.jsonl"
    with caplog.at_level(logging.ERROR):
        reporter.capture_fixture_failure(error, "fixture-url", 0)
    for rendered in (
        str(error),
        repr(error),
        str(error.cmd),
        str(error.args),
        result.summary,
        caplog.text,
        reporter._path.read_text(),
    ):
        assert HOST not in rendered and FIXTURE_PASSWORD not in rendered
        assert METHOD in rendered
    assert argv[1] == "--endpoint=" + target
    assert error.output == raw and error.stderr == raw and error.returncode == 7


@pytest.mark.parametrize(
    ("host", "credential"),
    [
        ("password", "password={value}"),
        ("passwd", "passwd={value}"),
        ("client_secret", "client_secret={value}"),
        ("token", "--token {value}"),
        ("token", "--token={value}"),
        ("token", '{{"token": "{value}"}}'),
        ("secret", '{{"secret": "{value}"}}'),
        ("password", '{{"password": "{value}"}}'),
        ("token", '{{"access_token": "{value}"}}'),
        ("client_secret", '{{"client_secret": "{value}"}}'),
    ],
)
@pytest.mark.parametrize("as_bytes", [False, True])
def test_endpoint_key_collisions_keep_credentials_private_through_failure_consumers(
    tmp_path: Path, caplog: pytest.LogCaptureFixture, host: str, credential: str, as_bytes: bool
) -> None:
    """Configured single-label hosts must not defeat credential scrubbing in real failures."""
    GRPCClient(address=f"{host}:443")
    fixture_credential = "fixture-credential-value"
    secret_text = credential.format(value=fixture_credential)
    detail = f"Code: PermissionDenied\nMessage: {secret_text}; target {host}:443"
    raw = detail.encode() if as_bytes else detail
    error = runner.CommandFailed(7, ["grpcurl", secret_text, f"{host}:443", METHOD], output=raw, stderr=raw)
    assert error.output == raw and error.stderr == raw and error.returncode == 7
    classified = classify(error)
    assert classified.kind == Kind.API_REFUSED and "PermissionDenied" in classified.summary
    reporter = FailureReporter(Mock())
    reporter.snapshotter = Mock()
    reporter.snapshotter.take.return_value = None
    reporter._path = tmp_path / "results-main.jsonl"
    with caplog.at_level(logging.ERROR):
        reporter.capture_fixture_failure(error, "fixture-create", 0)
    reporter._append({"nested": [{b"detail": raw, "exception": error}]})
    for rendered in (
        str(error),
        repr(error),
        str(error.cmd),
        str(error.args),
        classified.summary,
        caplog.text,
        reporter._path.read_text(),
    ):
        assert fixture_credential not in rendered
        assert f"{host}:443" not in rendered
        assert METHOD in rendered
    assert diagnostics.sanitize_text(host) == diagnostics.REDACTED
    assert error.output == raw and error.stderr == raw


@pytest.mark.parametrize(
    "value", ["8080:80", "10:30", "image:5", "image.example:5", "--publish=8080:80", "--image=image:5"]
)
def test_non_endpoint_arguments_do_not_poison_later_failure_diagnostics(
    value: str, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Port mappings, times and image tags stay intact in commands and later saved reports."""
    assert sanitize_args(["synthetic", value]) == ["synthetic", value]
    detail = "Code: PermissionDenied\nMessage: image:5 at 10:30 maps 8080:80; ID 8080"
    monkeypatch.setattr(
        runner.subprocess,
        "run",
        Mock(side_effect=subprocess.CalledProcessError(7, ["synthetic", value, METHOD], stderr=detail)),
    )
    with pytest.raises(runner.CommandFailed) as caught:
        runner.run("synthetic", value, METHOD)
    error = caught.value
    assert error.stderr == detail and detail in str(error)
    assert detail in classify(error).summary
    reporter = FailureReporter(Mock())
    reporter._path = tmp_path / "results-main.jsonl"
    reporter._append({"detail": detail, "error": error})
    assert detail.replace("\n", "\\n") in reporter._path.read_text()


@pytest.mark.parametrize(
    "address", ["https://url.synthetic.example.invalid/api", "grpc://api:443", "198.51.100.7:443", "[2001:db8::1]:443"]
)
def test_unambiguous_transport_arguments_learn_hosts_for_later_diagnostics(address: str) -> None:
    """Explicit URLs and IP authorities still protect command and subsequent error renderings."""
    host = address.split("://", 1)[-1].split("/", 1)[0].rsplit(":", 1)[0].strip("[]")
    cleaned = sanitize_args(["synthetic", address])
    assert host not in str(cleaned)
    error = runner.CommandFailed(7, ["synthetic", METHOD], stderr=f"Code: PermissionDenied\nMessage: {host}")
    assert host not in str(error) and METHOD in str(error)


@pytest.mark.parametrize("program", ["grpcurl", "osac"])
@pytest.mark.parametrize("as_bytes", [False, True])
def test_endpoint_named_after_api_program_preserves_failure_classification(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, program: str, as_bytes: bool
) -> None:
    """A redacted executable spelling must not change a real command failure's API classification."""
    address = f"{program}:7443"
    GRPCClient(address=address)
    detail = f"Code: PermissionDenied\nMessage: denied at {address}"
    raw = detail.encode() if as_bytes else detail
    monkeypatch.setattr(
        runner.subprocess,
        "run",
        Mock(side_effect=subprocess.CalledProcessError(7, [program, address, METHOD], output=raw, stderr=raw)),
    )
    with pytest.raises(runner.CommandFailed) as caught:
        runner.run(program, address, METHOD)
    error = caught.value
    assert error.cmd[0] == diagnostics.REDACTED
    assert error.stdout == raw and error.stderr == raw
    classified = classify(error)
    assert classified.kind == Kind.API_REFUSED
    reporter = FailureReporter(Mock())
    reporter.snapshotter = Mock()
    reporter.snapshotter.take.return_value = None
    reporter._path = tmp_path / "results-main.jsonl"
    reporter.capture_fixture_failure(error, "fixture-api-program", 0)
    saved = json.loads(reporter._path.read_text())
    assert saved["kind"] == "api-refused" and saved["outcome"] == "failed"
    assert METHOD in saved["summary"] and "PermissionDenied" in saved["summary"]
    for rendered in (str(error), repr(error), str(error.cmd), str(error.args), reporter._path.read_text()):
        assert address not in rendered and METHOD in rendered
    assert error.stdout == raw and error.stderr == raw


def test_absolute_dns_endpoint_redacts_resolver_normalized_hostname() -> None:
    """Hide both an absolute DNS target and its spelling in resolver error output."""
    GRPCClient(address=f"{HOST}.:7443")
    error = runner.CommandFailed(7, ["grpcurl", f"{HOST}.:7443", METHOD], stderr=DETAIL)
    assert HOST not in str(error) and HOST not in repr(error)
    assert METHOD in str(error) and "PermissionDenied" in str(error)


def test_nested_byte_secret_keys_preserve_payload_protection(tmp_path: Path) -> None:
    """Byte metadata must not bypass existing dropped credential and payload keys."""
    GRPCClient(address=ADDRESS)
    metadata = {"nested": {b"token": FIXTURE_TOKEN, b"user_data": "synthetic-payload", b"detail": DETAIL.encode()}}
    write_json(tmp_path, "bytes.json", metadata)
    rendered = (tmp_path / "bytes.json").read_text()
    assert HOST not in rendered and FIXTURE_TOKEN not in rendered and "synthetic-payload" not in rendered
    assert "PermissionDenied" in rendered and metadata["nested"][b"token"] == FIXTURE_TOKEN


def test_concurrent_clients_retain_all_endpoint_registrations() -> None:
    """Concurrent client creation must retain earlier endpoints for later failure rendering."""
    addresses = [f"worker-{index}.synthetic.example.invalid:7443" for index in range(128)]

    def register_and_render(address: str) -> str:
        """Exercise concurrent writes and reads through actual client initialization."""
        GRPCClient(address=address)
        return sanitize_text(address)

    with ThreadPoolExecutor(max_workers=8) as pool:
        assert all("synthetic.example.invalid" not in value for value in pool.map(register_and_render, addresses))
    for address in addresses:
        host = address.split(":", 1)[0]
        error = runner.CommandFailed(7, ["synthetic"], stderr=f"Code: PermissionDenied\nMessage: {address}")
        assert host not in str(error) and "PermissionDenied" in str(error)


@pytest.mark.parametrize("address", ["", ".", "[broken"])
def test_unusable_endpoints_do_not_mask_ordinary_diagnostics(address: str) -> None:
    """Malformed or empty endpoint learning must not create an empty redaction pattern."""
    GRPCClient(address=address)
    assert sanitize_text(f"Code: PermissionDenied {METHOD}") == f"Code: PermissionDenied {METHOD}"


@pytest.mark.parametrize("address", ["API.synthetic.example.invalid:7443", "[2001:db8::1]:7443", "api:7443"])
def test_endpoint_boundaries_preserve_rpc_and_unrelated_resource_identifiers(address: str) -> None:
    """Hide configured authority spellings without treating suffixes or RPC names as endpoints."""
    GRPCClient(address=address)
    host = address.rsplit(":", 1)[0].strip("[]").lower()
    cleaned = sanitize_text(f"{address} {host} {METHOD} FailedPrecondition")
    assert host not in cleaned.lower() and METHOD in cleaned and "FailedPrecondition" in cleaned
    if ":" not in host:
        unrelated = f"other.{host} {host}-resource osac.private.v1"
        assert sanitize_text(unrelated) == unrelated


@pytest.mark.parametrize("as_bytes", [False, True])
@pytest.mark.parametrize("stream", ["stdout", "stderr"])
def test_command_failure_redacts_endpoints_without_changing_raw_captures(
    monkeypatch: pytest.MonkeyPatch, as_bytes: bool, stream: str
) -> None:
    """Keep failure status and raw captures while scrubbing every outward exception form."""
    client = GRPCClient(address=ADDRESS, token=FIXTURE_TOKEN)
    value = DETAIL.encode() if as_bytes else DETAIL
    output = value if stream == "stdout" else None
    stderr = value if stream == "stderr" else None

    def fail(*args: object, **kwargs: object) -> None:
        """Fail the real runner's subprocess boundary with a synthetic provider response."""
        assert ADDRESS in args[0] and METHOD in args[0]
        assert any(FIXTURE_TOKEN in arg for arg in args[0])
        raise subprocess.CalledProcessError(7, args[0], output=output, stderr=stderr)

    monkeypatch.setattr(runner.subprocess, "run", fail)
    with pytest.raises(runner.CommandFailed) as caught:
        client.create_external_ip_pool(name="synthetic-pool", cidrs=["198.51.100.0/30"])
    error = caught.value
    assert error.output == output and error.stderr == stderr and error.returncode == 7
    for rendered in (str(error), repr(error), str(error.args), str(error.cmd)):
        assert HOST not in rendered and ADDRESS not in rendered and FIXTURE_TOKEN not in rendered
        assert METHOD in rendered
    classified = classify(error)
    assert classified.kind == Kind.API_REFUSED
    assert HOST not in classified.summary and "PermissionDenied" in classified.summary


@pytest.mark.parametrize(
    "address", [ADDRESS, "override.synthetic.example.invalid", "https://url.synthetic.example.invalid/api"]
)
def test_configured_overrides_scrub_bare_hosts_without_masking_service_names(address: str) -> None:
    """Learn the actual configured target, not a blanket pattern for dotted identifiers."""
    GRPCClient(address=address)
    host = address.removeprefix("https://").split("/", 1)[0].split(":", 1)[0]
    cleaned = sanitize_text(f"target={address}, hostname={host}. {METHOD} Code: FailedPrecondition")
    assert host not in cleaned
    assert METHOD in cleaned and "FailedPrecondition" in cleaned
    assert sanitize_text(f"other.{host} {host}-resource osac.private.v1") == (
        f"other.{host} {host}-resource osac.private.v1"
    )


def test_direct_command_addresses_and_existing_secret_rules_remain_safe() -> None:
    """Recognize command endpoint syntax while retaining payload and credential protection."""
    address = "command.synthetic.example.invalid:443"
    args = sanitize_args(
        ["grpcurl", "-H", f"Authorization: Bearer {FIXTURE_TOKEN}", "-d", '{"name":"payload-marker"}', address, METHOD]
    )
    assert "command.synthetic.example.invalid" not in str(args)
    assert FIXTURE_TOKEN not in str(args) and "payload-marker" not in str(args)
    assert args[0] == "grpcurl" and args[-1] == METHOD
    assert sanitize_text("pool resource.example status READY") == "pool resource.example status READY"


@pytest.mark.parametrize(
    "address", ["early.synthetic.example.invalid:443", "bare.synthetic.example.invalid", "api:443", "api"]
)
def test_command_endpoint_is_hidden_even_before_its_target_argument(address: str) -> None:
    """Protect earlier authority arguments and bare grpcurl targets before any client registration."""
    host = address.split(":", 1)[0]
    args = sanitize_args(["grpcurl", "-authority", host, address, METHOD])
    assert host not in str(args) and args[-1] == METHOD


def test_success_and_unchecked_command_returns_remain_raw(monkeypatch: pytest.MonkeyPatch) -> None:
    """Endpoint privacy changes outward diagnostics, not caller-owned process results."""
    GRPCClient(address=ADDRESS)
    completed = subprocess.CompletedProcess(["synthetic"], 0, stdout=DETAIL, stderr=DETAIL)
    monkeypatch.setattr(runner.subprocess, "run", Mock(return_value=completed))
    assert runner.run("synthetic") == DETAIL
    assert runner.run_unchecked("synthetic") == (DETAIL, 0)
    assert runner.run_unchecked("synthetic", include_stderr=True) == (DETAIL + "\n" + DETAIL, 0)
    completed.returncode = 7
    assert runner.run_unchecked("synthetic") == (DETAIL + "\n" + DETAIL, 7)


@pytest.mark.parametrize("unchecked", [False, True])
def test_real_runner_timeout_keeps_raw_streams_and_sanitized_exception(
    monkeypatch: pytest.MonkeyPatch, unchecked: bool
) -> None:
    """Both command wrappers preserve TimeoutExpired semantics without leaking configured targets."""
    GRPCClient(address=ADDRESS)
    error = subprocess.TimeoutExpired(["grpcurl", ADDRESS, METHOD], 3, output=DETAIL.encode(), stderr=DETAIL)
    monkeypatch.setattr(runner.subprocess, "run", Mock(side_effect=error))
    command = runner.run_unchecked if unchecked else runner.run
    with pytest.raises(runner.CommandTimedOut) as caught:
        command("grpcurl", ADDRESS, METHOD)
    assert caught.value.output == DETAIL.encode() and caught.value.stderr == DETAIL
    assert caught.value.timeout == 3 and isinstance(caught.value, subprocess.TimeoutExpired)
    assert HOST not in str(caught.value) and HOST not in repr(caught.value)
    assert METHOD in str(caught.value)


def test_timeout_and_direct_progress_logger_do_not_expose_configured_hosts(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture
) -> None:
    """Preserve timeout/raw-output semantics and scrub actual runner progress messages."""
    GRPCClient(address=ADDRESS)
    error = runner.CommandTimedOut(["grpcurl", ADDRESS, METHOD], 4, output=DETAIL.encode(), stderr=DETAIL)
    assert error.output == DETAIL.encode() and error.stderr == DETAIL
    for rendered in (str(error), repr(error), str(error.cmd), str(error.args)):
        assert HOST not in rendered and METHOD in rendered
    with caplog.at_level(logging.INFO, logger=runner.logger.name), pytest.raises(runner.StuckState):
        runner.poll_until(
            fn=lambda: False,
            until=bool,
            retries=1,
            delay=0,
            description=f"{HOST} {METHOD}",
            resource_kind=WaitResourceKind.EXTERNAL_IP_POOL,
            resource_id="synthetic-pool",
        )
    assert HOST not in caplog.text and METHOD in caplog.text


def test_nested_report_metadata_and_snapshot_writers_scrub_hosts_and_bytes(tmp_path: Path) -> None:
    """Sanitize serialized keys, byte captures and exception leaves without mutating input."""
    GRPCClient(address=ADDRESS)
    metadata = {"nested": [{HOST: DETAIL.encode(), "error": RuntimeError(DETAIL), "password": FIXTURE_TOKEN}]}
    reporter = FailureReporter(Mock())
    reporter.output_dir = tmp_path
    reporter._path = tmp_path / "results-main.jsonl"
    reporter._append(metadata)
    rendered = reporter._path.read_text()
    assert HOST not in rendered and FIXTURE_TOKEN not in rendered and "PermissionDenied" in rendered
    assert metadata["nested"][0][HOST] == DETAIL.encode()
    assert HOST not in json.dumps(sanitize_obj(metadata), default=str)
    write_json(tmp_path, "snapshot.json", metadata)
    snapshot = (tmp_path / "snapshot.json").read_text()
    assert HOST not in snapshot and FIXTURE_TOKEN not in snapshot and "PermissionDenied" in snapshot


def test_reporter_failure_names_and_ids_are_sanitized(tmp_path: Path, caplog: pytest.LogCaptureFixture) -> None:
    """Keep both first and repeated failure log lines safe when test labels contain endpoints."""
    GRPCClient(address=ADDRESS)
    reporter = FailureReporter(Mock())
    reporter.snapshotter = Mock()
    error = runner.CommandFailed(7, ["grpcurl", ADDRESS, METHOD], stderr=DETAIL)
    with caplog.at_level(logging.ERROR):
        for phase in ("setup", "call"):
            reporter._on_failure(None, error, {"test": HOST, "phase": phase}, 0)
    assert HOST not in caplog.text and "api-refused" in caplog.text
    assert HOST not in reporter.snapshotter.take.call_args.args[0]


@pytest.mark.parametrize("site", ["classification", "snapshot", "hook", "capture"])
def test_reporter_internal_error_logs_scrub_tracebacks(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture, site: str
) -> None:
    """Reporting errors retain useful traceback details without exposing configured hosts or tokens."""
    GRPCClient(address=ADDRESS)
    reporter = FailureReporter(Mock())
    reporter.output_dir = tmp_path
    reporter._path = tmp_path / "results-main.jsonl"
    reporter.snapshotter = Mock()
    reporter.snapshotter.take.return_value = None
    internal_error = RuntimeError(f"{DETAIL} Authorization: Bearer {FIXTURE_TOKEN}")
    error = runner.CommandFailed(7, ["grpcurl", ADDRESS, METHOD], stderr=DETAIL)
    if site == "classification":
        monkeypatch.setattr(reporting, "classify", Mock(side_effect=internal_error))
    elif site == "snapshot":
        reporter.snapshotter.take.side_effect = internal_error
    elif site == "hook":
        monkeypatch.setattr(reporter, "_append", Mock(side_effect=internal_error))
    config = Mock()
    config.pluginmanager.get_plugin.side_effect = internal_error
    with caplog.at_level(logging.WARNING):
        if site == "capture":
            reporting.capture_fixture_failure(config, error, "test_synthetic")
        else:
            reporter.capture_fixture_failure(error, "test_synthetic", 0)
    assert HOST not in caplog.text and FIXTURE_TOKEN not in caplog.text
    assert "internal error" in caplog.text and "RuntimeError" in caplog.text and "PermissionDenied" in caplog.text


@pytest.mark.parametrize("traceback", ["short", "long"])
@pytest.mark.parametrize("endpoint_host", [HOST, "password", "grpcurl"])
def test_actual_disabled_pool_private_create_setup_failure_is_reported_safely(
    tmp_path: Path, traceback: str, endpoint_host: str
) -> None:
    """Drive the real private pool fixture, runner and plugin through a failed pytest setup."""
    conftest = (
        CONFTEST
        + '''
import subprocess
import pytest
from tests.e2e.core import runner
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.references.test_networking_references import disabled_reference_mode, disabled_reference_pool

@pytest.fixture
def private_grpc(monkeypatch):
    """Use the real private client while faking only its subprocess boundary."""
    fixture_password = "fixture-password-value"
    def fail(*args, **kwargs):
        """Keep synthetic raw data for callers and force the private create to fail."""
        if args[0][-1].endswith("InstallationSettings/Get"):
            assert "-emit-defaults" in args[0][:-2]
            return subprocess.CompletedProcess(args[0], 0, stdout='{"networkingProvisioningEnabled": false}', stderr="")
        assert args[0][-1].endswith("ExternalIPPools/Create")
        detail = (
            "Code: PermissionDenied\\nMessage: denied at private-api.synthetic.example.invalid:7443 "
            f"password={fixture_password}"
        )
        raise subprocess.CalledProcessError(7, args[0], stderr=detail)
    monkeypatch.setattr(runner.subprocess, "run", fail)
    fixture_token = "synthetic-only-token"
    return GRPCClient(address="private-api.synthetic.example.invalid:7443", token=fixture_token)

@pytest.fixture
def k8s_hub_client():
    """No Kubernetes access is possible before the private create fails."""
    return None

@pytest.fixture
def ref_test_run_id():
    """Provide a deterministic synthetic resource suffix."""
    return "synthetic"
'''.replace(HOST, endpoint_host)
    )
    result = run_pytest(
        tmp_path,
        '''
        def test_pool_setup(disabled_reference_pool):
            """The failed setup must prevent the test body from running."""
            raise AssertionError("BODY-MUST-NOT-RUN")
    ''',
        conftest=conftest,
        args=(f"--tb={traceback}",),
    )
    assert result.returncode == 1
    assert "BODY-MUST-NOT-RUN" not in result.stdout
    assert endpoint_host not in result.stdout and FIXTURE_TOKEN not in result.stdout
    assert FIXTURE_PASSWORD not in result.stdout
    assert METHOD in result.stdout and "PermissionDenied" in result.stdout
    log_text = (tmp_path / "e2e.log").read_text()
    assert endpoint_host not in log_text and FIXTURE_TOKEN not in log_text
    assert FIXTURE_PASSWORD not in log_text
    failed = result.tests()["test_pool_setup"]
    assert (failed["phase"], failed["outcome"], failed["kind"]) == ("setup", "failed", "api-refused")
    for path in result.out.rglob("*"):
        if path.is_file():
            assert endpoint_host not in path.read_text() and FIXTURE_TOKEN not in path.read_text()
            assert FIXTURE_PASSWORD not in path.read_text()
