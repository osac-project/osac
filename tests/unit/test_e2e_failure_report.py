"""Failure reporting for E2E: sanitizer, runner exceptions, poll_until, classification."""

from __future__ import annotations

import json
import subprocess

import pytest

from tests.e2e.core import runner
from tests.e2e.core.failures import Classified, E2EFailure, Kind, classify
from tests.e2e.core.runner import CommandFailed, CommandTimedOut, StuckState, poll_until, run
from tests.e2e.core.sanitize import sanitize_args, sanitize_obj, sanitize_text
from tests.e2e.core.wait_kinds import WaitResourceKind

JWT = ".".join(["eyJhbGciOiJSUzI1NiJ9", "eyJzdWIiOiJ0ZXN0In0", "c2lnbmF0dXJl"])
PAYLOAD = '{"object": {"metadata": {"name": "payload-marker"}}}'


def grpcurl_argv() -> list[str]:
    return [
        "grpcurl",
        "-insecure",
        "-H",
        f"Authorization: Bearer {JWT}",
        "-d",
        PAYLOAD,
        "fulfillment.example:443",
        "osac.public.v1.ComputeInstances/Create",
    ]


class TestSanitizeText:
    @pytest.mark.parametrize(
        "text",
        [
            f"Authorization: Bearer {JWT}",
            "Authorization: Basic dXNlcjpwYXNz",
            f"token {JWT} leaked",
            "password=hunter2 next",
            "client_secret=abc123",
            "run --token s3cr3t-value now",
            '{"password": "hunter2"}',
            '{"token": "abc"}',
            '{"access_token": "abc"}',
        ],
    )
    def test_secret_shapes_are_redacted(self, text: str) -> None:
        cleaned = sanitize_text(text)
        assert "<redacted>" in cleaned
        for secret in (JWT, "dXNlcjpwYXNz", "hunter2", "abc123", "s3cr3t-value", '"abc"'):
            assert secret not in cleaned

    def test_plain_text_is_unchanged(self) -> None:
        assert sanitize_text("Code: FailedPrecondition\nMessage: pool is full") == (
            "Code: FailedPrecondition\nMessage: pool is full"
        )


class TestSanitizeArgs:
    def test_grpcurl_argv_drops_token_and_payload(self) -> None:
        cleaned = sanitize_args(grpcurl_argv())
        assert cleaned[0] == "grpcurl"
        assert "fulfillment.example:443" in cleaned
        assert "osac.public.v1.ComputeInstances/Create" in cleaned
        joined = " ".join(cleaned)
        assert JWT not in joined
        assert "payload-marker" not in joined

    @pytest.mark.parametrize("flag", ["--header", "--data", "--token", "--token-script", "--password"])
    def test_value_after_flag_is_redacted(self, flag: str) -> None:
        assert sanitize_args(["prog", flag, "secret-value", "tail"]) == ["prog", flag, "<redacted>", "tail"]

    def test_other_args_are_scrubbed_by_text_rules(self) -> None:
        assert "hunter2" not in " ".join(sanitize_args(["prog", "password=hunter2"]))


class TestSanitizeObj:
    def test_drops_secret_keys_recursively_and_scrubs_strings(self) -> None:
        value = {
            "id": "1",
            "spec": {"userData": "x", "user_data": "x", "cloudInitUserData": "x", "sshKey": "k", "ssh_key": "k"},
            "template_parameters": {"a": 1},
            "items": [{"password": "p", "client_secret": "s", "name": f"Bearer {JWT}"}],
        }
        cleaned = sanitize_obj(value)
        text = json.dumps(cleaned)
        assert cleaned["id"] == "1"
        assert "template_parameters" not in cleaned
        for gone in ("userData", "user_data", "cloudInitUserData", "sshKey", "ssh_key", "password", "client_secret"):
            assert gone not in text
        assert JWT not in text


class TestCommandFailed:
    def test_run_raises_command_failed_with_stderr_intact(self) -> None:
        with pytest.raises(CommandFailed) as raised:
            run("sh", "-c", "echo boom >&2; exit 3")
        exc = raised.value
        assert isinstance(exc, subprocess.CalledProcessError)
        assert exc.returncode == 3
        assert exc.stderr.strip() == "boom"
        assert "boom" in str(exc)
        assert "exited 3" in str(exc)

    def test_token_and_payload_never_reach_str_or_repr(self) -> None:
        exc = CommandFailed(7, grpcurl_argv(), output="", stderr=f"denied Bearer {JWT}")
        for text in (str(exc), repr(exc), " ".join(exc.cmd)):
            assert JWT not in text
            assert "payload-marker" not in text
        assert exc.returncode == 7
        assert JWT in exc.stderr  # callers keep the raw capture

    def test_str_keeps_only_the_last_2000_characters(self) -> None:
        exc = CommandFailed(1, ["x"], output="", stderr="a" * 5000 + "END")
        assert str(exc).endswith("END")
        assert len(str(exc)) < 2100

    def test_timeout_is_sanitized_and_still_a_timeout_expired(self) -> None:
        exc = CommandTimedOut(grpcurl_argv(), 5, output=None, stderr=None)
        assert isinstance(exc, subprocess.TimeoutExpired)
        for text in (str(exc), repr(exc)):
            assert JWT not in text
            assert "payload-marker" not in text

    def test_run_timeout_raises_command_timed_out(self) -> None:
        with pytest.raises(CommandTimedOut) as raised:
            run("sleep", "5", timeout=1)
        assert isinstance(raised.value, subprocess.TimeoutExpired)
        assert raised.value.__cause__ is None


class TestStuckState:
    def test_is_a_timeout_error_with_attributes(self) -> None:
        with pytest.raises(TimeoutError) as raised:
            poll_until(
                fn=lambda: "pending",
                until=lambda _: False,
                retries=1,
                delay=0,
                description="thing ready",
                resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
                resource_id="thing-1",
            )
        exc = raised.value
        assert isinstance(exc, StuckState)
        assert exc.description == "thing ready"
        assert exc.last_value == "'pending'"
        assert str(exc) == "thing ready not reached after 0s; last value: 'pending'"

    def test_container_content_is_never_printed(self) -> None:
        with pytest.raises(StuckState) as raised:
            poll_until(
                fn=lambda: {"spec": {"marker": "container-secret"}, "b": 1},
                until=lambda _: False,
                retries=1,
                delay=0,
                description="object ready",
                resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
                resource_id="thing-1",
            )
        assert "container-secret" not in str(raised.value)
        assert "<dict with 2 keys>" in str(raised.value)

    def test_list_summary(self) -> None:
        with pytest.raises(StuckState) as raised:
            poll_until(
                fn=lambda: [1, 2, 3],
                until=lambda _: False,
                retries=1,
                delay=0,
                description="d",
                resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
                resource_id="thing-1",
            )
        assert "<list of 3>" in str(raised.value)

    def test_last_failed_call_is_named_without_the_token(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.setattr(runner.time, "sleep", lambda _delay: None)

        def fail() -> str:
            raise CommandFailed(1, grpcurl_argv(), output="", stderr="Code: Unavailable")

        with pytest.raises(StuckState) as raised:
            poll_until(
                fn=fail,
                until=bool,
                retries=2,
                delay=0,
                description="api up",
                retry_on_error=True,
                resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
                resource_id="thing-1",
            )
        text = str(raised.value)
        assert "last call failed: CommandFailed" in text
        assert "Unavailable" in text
        assert JWT not in text
        assert "payload-marker" not in text
        assert raised.value.__cause__ is None
        assert raised.value.__suppress_context__

    def test_plain_called_process_error_names_only_its_type(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.setattr(runner.time, "sleep", lambda _delay: None)

        def fail() -> str:
            raise subprocess.CalledProcessError(1, "secret-command", output="secret-response")

        with pytest.raises(StuckState) as raised:
            poll_until(
                fn=fail,
                until=bool,
                retries=2,
                delay=0,
                description="d",
                retry_on_error=True,
                resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
                resource_id="thing-1",
            )
        assert "last call failed: CalledProcessError" in str(raised.value)
        assert "secret-command" not in str(raised.value)
        assert "secret-response" not in str(raised.value)

    def test_scalar_summary_is_sanitized_and_capped(self) -> None:
        with pytest.raises(StuckState) as raised:
            poll_until(
                fn=lambda: f"Bearer {JWT} " + "x" * 500,
                until=lambda _: False,
                retries=1,
                delay=0,
                description="d",
                resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
                resource_id="thing-1",
            )
        assert JWT not in str(raised.value)
        assert len(raised.value.last_value) <= 120


class TestClassify:
    def test_e2e_failure_uses_its_kind_and_ids(self) -> None:
        result = classify(E2EFailure(Kind.TRAFFIC_BLOCKED, "no route", ids={"sg_id": "sg-1"}))
        assert result == Classified(Kind.TRAFFIC_BLOCKED, "no route", {"sg_id": "sg-1"})

    def test_grpcurl_failure_is_api_refused_with_code_and_message(self) -> None:
        stderr = "ERROR:\n  Code: FailedPrecondition\n  Message: pool is not ready\n"
        result = classify(CommandFailed(64, grpcurl_argv(), output="", stderr=stderr))
        assert result.kind is Kind.API_REFUSED
        assert "FailedPrecondition" in result.summary
        assert "pool is not ready" in result.summary
        assert JWT not in result.summary

    def test_osac_cli_failure_is_api_refused(self) -> None:
        assert classify(CommandFailed(1, ["osac", "create", "x"], output="", stderr="nope")).kind is Kind.API_REFUSED

    def test_other_command_is_unclassified(self) -> None:
        assert classify(CommandFailed(1, ["kubectl", "get", "x"], output="", stderr="nope")).kind is Kind.UNCLASSIFIED

    def test_stuck_state(self) -> None:
        assert classify(StuckState("x ready", "'a'", 10)).kind is Kind.STUCK_STATE

    def test_assertion_and_unclassified(self) -> None:
        assert classify(AssertionError("plain")).kind is Kind.ASSERTION
        other = classify(ValueError("odd"))
        assert other.kind is Kind.UNCLASSIFIED
        assert "ValueError" in other.summary

    def test_pytest_fail_inside_except_stuck_state_is_stuck(self) -> None:
        try:
            try:
                raise StuckState("thing ready", "'a'", 10)
            except TimeoutError as inner:
                pytest.fail(f"thing timed out. hint\n{inner}")
        except BaseException as outer:
            result = classify(outer)
        assert result.kind is Kind.STUCK_STATE
        assert "hint" in result.summary

    def test_summary_is_sanitized_and_capped(self) -> None:
        result = classify(AssertionError(f"Bearer {JWT} " + "y" * 3000))
        assert JWT not in result.summary
        assert len(result.summary) <= 1000

    def test_cause_chain_is_bounded(self) -> None:
        exc: BaseException = ValueError("root")
        for _ in range(10):
            new = RuntimeError("wrap")
            new.__cause__ = exc
            exc = new
        assert classify(exc).kind is Kind.UNCLASSIFIED


class TestFixRound1:
    def test_bytes_stderr_of_a_timed_out_command_classifies(self) -> None:
        exc = CommandTimedOut(["grpcurl", "x"], 5, output=None, stderr=b"Code: Unavailable\nMessage: down\n")
        result = classify(exc)
        assert result.kind is Kind.UNCLASSIFIED
        assert "Unavailable" in result.summary

    @pytest.mark.parametrize(
        "key", ["templateParameters", "template_parameters", "TemplateParameters", "userData", "UserData", "SSHKey"]
    )
    def test_every_form_of_a_dropped_key_is_dropped(self, key: str) -> None:
        assert sanitize_obj({"spec": {key: "x", "keep": "y"}}) == {"spec": {"keep": "y"}}

    def test_run_hides_its_frame_from_tracebacks(self) -> None:
        assert "__tracebackhide__" in runner.run.__code__.co_varnames
