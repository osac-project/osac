"""Exercise disabled-reference applicability through real pytest fixtures and RPC consumers."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from tests.e2e.core import grpc_client
from tests.unit.test_e2e_failure_plugin import CONFTEST, run_pytest


@pytest.mark.parametrize("emit_defaults", [False, True])
@pytest.mark.parametrize("data", [None, {}])
def test_grpc_defaults_are_explicit_opt_in(
    monkeypatch: pytest.MonkeyPatch, emit_defaults: bool, data: dict | None
) -> None:
    """Keep existing command arguments unchanged and place the opt-in before the target."""
    commands = []

    def capture(*args: str, **kwargs: int) -> str:
        """Capture the real client's command without executing a subprocess."""
        commands.append((args, kwargs))
        return '{"networkingProvisioningEnabled": false}'

    monkeypatch.setattr(grpc_client, "run", capture)
    fixture_token = "fixture-token"
    client = grpc_client.GRPCClient(address="fixture-mode.example.invalid:443", token=fixture_token)
    options = {"emit_defaults": True} if emit_defaults else {}
    service = f"{grpc_client.PRIVATE_API}.InstallationSettings/Get"
    assert client.call(service=service, data=data, timeout=17, **options) == {"networkingProvisioningEnabled": False}
    expected = ["grpcurl", "-insecure", "-H", f"Authorization: Bearer {fixture_token}"]
    if emit_defaults:
        expected.append("-emit-defaults")
    if data is not None:
        expected.extend(["-d", "{}"])
    expected.extend([client.address, service])
    assert commands == [(tuple(expected), {"timeout": 17})]


MODE_CONFTEST = r'''
import json
import subprocess
from pathlib import Path
from unittest.mock import Mock
import pytest
from tests.e2e.core import runner
from tests.e2e.core.grpc_client import GRPCClient, PRIVATE_API, PUBLIC_API
from tests.e2e.references import test_networking_references as suite
from tests.e2e.references.test_networking_references import disabled_reference_pool

disabled_reference_mode = suite.disabled_reference_mode

@pytest.fixture
def private_grpc(monkeypatch):
    """Keep real clients and fixtures; replace only subprocesses, readiness and cleanup waits."""
    fixture_password = "fixture-mode-password"
    fixture_token = "fixture-mode-token"
    def process(args, **kwargs):
        """Return synthetic RPC data and retain every real command for ordering assertions."""
        with Path("commands.jsonl").open("a") as stream:
            stream.write(json.dumps(args) + "\n")
        service = args[-1]
        if service == f"{PRIVATE_API}.InstallationSettings/Get":
            if MODE_RESPONSE in ("rpc-error", "db-error"):
                code = "PermissionDenied" if MODE_RESPONSE == "rpc-error" else "Internal"
                raise subprocess.CalledProcessError(7, args, stderr=(
                    f"Code: {code}\nMessage: denied at fixture-mode.example.invalid:443 "
                    f"password={fixture_password} Bearer {fixture_token}"
                ))
            response = (
                '{"networkingProvisioningEnabled": false}' if MODE_RESPONSE == "create-success" else MODE_RESPONSE
            )
            return subprocess.CompletedProcess(args, 0, stdout=response, stderr="")
        if service == f"{PRIVATE_API}.ExternalIPPools/Create":
            response = {"object": {"id": "fixture-pool-id"}}
        elif service == f"{PRIVATE_API}.ExternalIPPools/Get":
            response = {"object": {"status": {"state": "EXTERNAL_IP_POOL_STATE_READY", "available": 2}}}
        elif service == f"{PUBLIC_API}.ExternalIPs/Create":
            if MODE_RESPONSE == "create-success":
                return subprocess.CompletedProcess(
                    args, 0, stdout='{"object": {"id": "fixture-unexpected-id"}}', stderr=""
                )
            raise subprocess.CalledProcessError(1, args, stderr=(
                "ERROR:\nCode: FailedPrecondition\nMessage: provider networking is disabled\n"
            ))
        else:
            response = {"items": [], "total": 0}
        return subprocess.CompletedProcess(args, 0, stdout=json.dumps(response), stderr="")
    def cleanup(**kwargs):
        """Exercise real deletion callbacks without requiring Kubernetes."""
        kwargs["delete"]()
    monkeypatch.setattr(runner.subprocess, "run", process)
    monkeypatch.setattr(suite, "wait_for_external_ip_pool_cr", Mock(return_value="fixture-pool-cr"))
    monkeypatch.setattr(suite, "wait_for_external_ip_pool_ready", Mock())
    monkeypatch.setattr(suite, "wait_owned_gone", cleanup)
    return GRPCClient(address="fixture-mode.example.invalid:443", token=fixture_token)

@pytest.fixture
def grpc(private_grpc):
    """Use the same synthetic transport for public rejection and private observation."""
    return private_grpc

@pytest.fixture
def k8s_hub_client():
    """No cluster is contacted by this hermetic fixture."""
    return Mock()

@pytest.fixture
def ref_test_run_id():
    """Give the real reference test a deterministic resource name."""
    return "fixture-run"
'''


@pytest.mark.parametrize(
    "response, outcome",
    [
        ('{"networkingProvisioningEnabled": true}', "skipped"),
        ('{"networkingProvisioningEnabled": false}', "passed"),
        ("{}", "failed"),
        ('{"networkingProvisioningEnabled": null}', "failed"),
        ('{"networkingProvisioningEnabled": "false"}', "failed"),
        ('{"networkingProvisioningEnabled": 0}', "failed"),
        ('{"networkingProvisioningEnabled": 1}', "failed"),
        ("[]", "failed"),
        ("not-json", "failed"),
        ("rpc-error", "failed"),
        ("db-error", "failed"),
        ("create-success", "failed-call"),
        (
            json.dumps(
                {
                    "networkingProvisioningEnabled": (
                        "fixture-mode-password fixture-mode-token fixture-mode.example.invalid"
                    )
                }
            ),
            "failed",
        ),
        ("not-json fixture-mode-password fixture-mode-token fixture-mode.example.invalid", "failed"),
    ],
)
def test_mode_guard_orders_real_pool_fixture_before_both_selectors(tmp_path: Path, response: str, outcome: str) -> None:
    """Skip only enabled mode, fail unknown/read errors and retain the real disabled contract."""
    result = run_pytest(
        tmp_path,
        '''
        import pytest
        from tests.e2e.references.test_networking_references import TestNetworkingReferences as ReferenceCases

        @pytest.mark.parametrize("pool_selector", ["id", "name"])
        def test_reference(grpc, private_grpc, k8s_hub_client, disabled_reference_pool, ref_test_run_id, pool_selector):
            """Exercise the unchanged rejection/storage/accounting test through its real pool fixture."""
            ReferenceCases().test_disabled_external_ip_reference_stores_nothing(
                grpc, private_grpc, k8s_hub_client, disabled_reference_pool, ref_test_run_id, pool_selector
            )
        ''',
        conftest=CONFTEST + "\nMODE_RESPONSE = " + repr(response) + "\n" + MODE_CONFTEST,
        args=("--tb=long", "-rs"),
    )
    assert result.returncode == (1 if outcome.startswith("failed") else 0), result.stdout
    records = [record for record in result.records() if "test" in record]
    assert len(records) == 2
    assert {record["outcome"] for record in records} == {"failed" if outcome.startswith("failed") else outcome}
    commands = [json.loads(line) for line in (tmp_path / "project/commands.jsonl").read_text().splitlines()]
    settings = f"{grpc_client.PRIVATE_API}.InstallationSettings/Get"
    if outcome in ("failed", "skipped"):
        assert [command[-1] for command in commands] == [settings, settings]
        assert all(record["phase"] == "setup" for record in records)
    else:
        assert commands[0][-1] == settings
        assert sum(command[-1].endswith("ExternalIPPools/Create") for command in commands) == 2
        assert sum(command[-1].endswith("ExternalIPPools/Delete") for command in commands) == 2
        public_creates = [command for command in commands if command[-1].endswith("v1.ExternalIPs/Create")]
        assert [
            json.loads(command[command.index("-d") + 1])["object"]["spec"]["pool"] for command in public_creates
        ] == [{"id": "fixture-pool-id"}, {"name": "ref-disabled-pool-fixture-run"}]
        if outcome == "failed-call":
            assert all(record["phase"] == "call" for record in records)
            assert "DID NOT RAISE" in result.stdout
            deletions = [command for command in commands if command[-1].endswith("/Delete")]
            assert [command[-1] for command in deletions] == [
                f"{grpc_client.PUBLIC_API}.ExternalIPs/Delete",
                f"{grpc_client.PRIVATE_API}.ExternalIPPools/Delete",
            ] * 2
            assert all(
                json.loads(command[command.index("-d") + 1]) == {"id": "fixture-unexpected-id"}
                for command in deletions
                if command[-1] == f"{grpc_client.PUBLIC_API}.ExternalIPs/Delete"
            )
    assert all("-emit-defaults" in command[:-2] for command in commands if command[-1] == settings)
    if outcome == "skipped":
        assert "provider networking provisioning is enabled" in result.stdout
    if response in ("rpc-error", "db-error"):
        if response == "rpc-error":
            assert {record["kind"] for record in records} == {"api-refused"}
        code = "PermissionDenied" if response == "rpc-error" else "Internal"
        assert code in result.stdout and "InstallationSettings/Get" in result.stdout
    if outcome.startswith("failed"):
        outputs = [result.stdout, (tmp_path / "e2e.log").read_text()]
        outputs.extend(path.read_text() for path in result.out.rglob("*") if path.is_file())
        for output in outputs:
            assert "fixture-mode.example.invalid" not in output
            assert "fixture-mode-password" not in output and "fixture-mode-token" not in output
