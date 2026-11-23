#!/usr/bin/env python3
"""Run a per-test Netris fake inside an isolated Kind cluster.

The ``run`` command starts the existing fake behind a ClusterIP Service,
forwards its admin API to loopback for the profile command, and removes the
fixture resources after that command exits.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import secrets
import socket
import subprocess
import sys
import tempfile
import time
import uuid
from pathlib import Path
from typing import IO, Any
from urllib.error import URLError
from urllib.request import ProxyHandler, Request, build_opener

PORT = 8080
FIXTURE_DIR = Path(__file__).resolve().parent
FAKE_NETRIS = FIXTURE_DIR / "fake_netris.py"
RESOURCE_TYPES = ("configmap", "service", "deployment")


def _validate_dns_label(value: str, field: str) -> None:
    if len(value) > 63 or not re.fullmatch(r"[a-z0-9](?:[-a-z0-9]*[a-z0-9])?", value):
        raise ValueError(f"{field} must be a DNS label of at most 63 characters")


def _metadata(name: str, namespace: str, labels: dict[str, str]) -> dict[str, Any]:
    return {"name": name, "namespace": namespace, "labels": labels}


def build_resources(
    name: str,
    namespace: str,
    image: str,
    pull_policy: str = "IfNotPresent",
) -> list[dict[str, Any]]:
    """Build the namespaced resources for one isolated fake Netris server."""
    _validate_dns_label(name, "name")
    _validate_dns_label(namespace, "namespace")
    if not image.strip():
        raise ValueError("image must not be empty")

    labels = {
        "app.kubernetes.io/name": "netris-fake",
        "app.kubernetes.io/instance": name,
        "app.kubernetes.io/managed-by": "osac-aap-test-fixture",
    }
    metadata = _metadata(name, namespace, labels)
    fake_source = FAKE_NETRIS.read_text(encoding="utf-8")

    config_map = {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": metadata,
        "data": {"fake_netris.py": fake_source},
    }
    service = {
        "apiVersion": "v1",
        "kind": "Service",
        "metadata": metadata,
        "spec": {
            "type": "ClusterIP",
            "selector": labels,
            "ports": [{"name": "http", "port": PORT, "protocol": "TCP", "targetPort": "http"}],
        },
    }
    deployment = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": metadata,
        "spec": {
            "replicas": 1,
            "selector": {"matchLabels": labels},
            "template": {
                "metadata": {"labels": labels},
                "spec": {
                    "automountServiceAccountToken": False,
                    "securityContext": {
                        "runAsNonRoot": True,
                        "runAsUser": 1000,
                        "runAsGroup": 1000,
                        "fsGroup": 1000,
                        "seccompProfile": {"type": "RuntimeDefault"},
                    },
                    "containers": [
                        {
                            "name": "netris-fake",
                            "image": image,
                            "imagePullPolicy": pull_policy,
                            "command": ["python3", "/fixture/fake_netris.py"],
                            "args": [
                                "--host",
                                "0.0.0.0",
                                "--port",
                                str(PORT),
                                "--port-file",
                                "/state/port",
                                "--state-file",
                                "/state/state.json",
                            ],
                            "env": [{"name": "PYTHONDONTWRITEBYTECODE", "value": "1"}],
                            "ports": [{"name": "http", "containerPort": PORT, "protocol": "TCP"}],
                            "readinessProbe": {
                                "httpGet": {"path": "/__admin/state", "port": "http"},
                                "periodSeconds": 2,
                                "timeoutSeconds": 1,
                                "failureThreshold": 30,
                            },
                            "resources": {
                                "requests": {"cpu": "10m", "memory": "32Mi"},
                                "limits": {"cpu": "250m", "memory": "128Mi"},
                            },
                            "securityContext": {
                                "allowPrivilegeEscalation": False,
                                "capabilities": {"drop": ["ALL"]},
                                "readOnlyRootFilesystem": True,
                            },
                            "volumeMounts": [
                                {"name": "fixture", "mountPath": "/fixture", "readOnly": True},
                                {"name": "state", "mountPath": "/state"},
                            ],
                        }
                    ],
                    "volumes": [
                        {
                            "name": "fixture",
                            "configMap": {
                                "name": name,
                                "defaultMode": 0o444,
                                "items": [{"key": "fake_netris.py", "path": "fake_netris.py"}],
                            },
                        },
                        {"name": "state", "emptyDir": {}},
                    ],
                },
            },
        },
    }
    return [config_map, service, deployment]


class NetrisKindFixture:
    """Manage one fake Netris instance in a Kubernetes namespace."""

    def __init__(
        self,
        name: str,
        namespace: str,
        image: str | None,
        kubectl: str = "kubectl",
        kubeconfig: str | None = None,
        readiness_timeout: int = 120,
        pull_policy: str = "IfNotPresent",
    ) -> None:
        _validate_dns_label(name, "name")
        _validate_dns_label(namespace, "namespace")
        if image is not None and not image.strip():
            raise ValueError("image must not be empty")
        if readiness_timeout < 1:
            raise ValueError("readiness timeout must be positive")
        self.name = name
        self.namespace = namespace
        self.image = image
        self.kubectl = kubectl
        self.kubeconfig = kubeconfig
        self.readiness_timeout = readiness_timeout
        self.pull_policy = pull_policy
        self._created_resources: list[str] | None = None
        self._port_forward: subprocess.Popen[str] | None = None
        self._port_forward_log: IO[str] | None = None
        self.admin_url: str | None = None

    @property
    def service_url(self) -> str:
        return f"http://{self.name}.{self.namespace}.svc.cluster.local:{PORT}"

    def _kubectl_command(self, *args: str) -> list[str]:
        command = [self.kubectl]
        if self.kubeconfig:
            command.extend(["--kubeconfig", self.kubeconfig])
        return [*command, "--namespace", self.namespace, *args]

    def _run_kubectl(
        self,
        *args: str,
        input_text: str | None = None,
        timeout: int | None = None,
    ) -> subprocess.CompletedProcess[str]:
        command = self._kubectl_command(*args)
        result = subprocess.run(
            command,
            input=input_text,
            text=True,
            capture_output=True,
            check=False,
            timeout=timeout,
        )
        if result.returncode:
            message = result.stderr.strip() or result.stdout.strip() or "command failed"
            raise RuntimeError(f"kubectl {' '.join(args[:2])} failed: {message}")
        return result

    def _delete(self, resources: list[str]) -> None:
        if not resources:
            return
        selector = (
            "app.kubernetes.io/managed-by=osac-aap-test-fixture,"
            f"app.kubernetes.io/instance={self.name}"
        )
        result = subprocess.run(
            self._kubectl_command(
                "delete",
                *resources,
                "--selector",
                selector,
                "--ignore-not-found=true",
                "--wait=true",
            ),
            text=True,
            capture_output=True,
            check=False,
            timeout=self.readiness_timeout,
        )
        if result.returncode:
            message = result.stderr.strip() or result.stdout.strip() or "command failed"
            raise RuntimeError(f"kubectl delete failed: {message}")

    def start(self) -> str:
        if self._created_resources is not None:
            raise RuntimeError("this fake Netris fixture has already been started")
        if not self.image:
            raise ValueError("image is required to start the fake Netris fixture")

        self._created_resources = []
        try:
            for resource in build_resources(
                self.name,
                self.namespace,
                self.image,
                self.pull_policy,
            ):
                kind = resource["kind"].lower()
                self._run_kubectl("create", "-f", "-", input_text=json.dumps(resource))
                self._created_resources.append(f"{kind}/{self.name}")
            self._run_kubectl(
                "rollout",
                "status",
                f"deployment/{self.name}",
                f"--timeout={self.readiness_timeout}s",
                timeout=self.readiness_timeout + 5,
            )
            return self.service_url
        except Exception as error:
            try:
                self._delete(self._created_resources)
                self._created_resources.clear()
            except Exception as cleanup_error:
                raise RuntimeError(
                    f"{error}; fixture cleanup also failed: {cleanup_error}"
                ) from error
            raise

    def start_admin_forward(self) -> str:
        if self._created_resources is None or not self._created_resources:
            raise RuntimeError("start the fake Netris fixture before forwarding its admin API")
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            local_port = listener.getsockname()[1]

        self.admin_url = f"http://127.0.0.1:{local_port}"
        self._port_forward_log = tempfile.TemporaryFile(mode="w+t")
        command = self._kubectl_command(
            "port-forward",
            f"service/{self.name}",
            f"{local_port}:{PORT}",
            "--address=127.0.0.1",
        )
        try:
            self._port_forward = subprocess.Popen(
                command,
                stdout=self._port_forward_log,
                stderr=self._port_forward_log,
                text=True,
            )
            self._wait_for_admin()
        except Exception:
            self.stop_port_forward()
            raise
        return self.admin_url

    def _wait_for_admin(self) -> None:
        assert self.admin_url
        deadline = time.monotonic() + self.readiness_timeout
        opener = build_opener(ProxyHandler({}))
        while time.monotonic() < deadline:
            if self._port_forward and self._port_forward.poll() is not None:
                raise RuntimeError(f"kubectl port-forward exited: {self._port_forward_output()}")
            try:
                request = Request(f"{self.admin_url}/__admin/state")
                with opener.open(request, timeout=1) as response:
                    if response.status == 200:
                        return
            except (OSError, URLError):
                time.sleep(0.2)
        raise RuntimeError(
            f"Netris fake admin API did not become ready at {self.admin_url}: "
            f"{self._port_forward_output()}"
        )

    def _port_forward_output(self) -> str:
        if not self._port_forward_log:
            return ""
        self._port_forward_log.flush()
        self._port_forward_log.seek(0)
        return self._port_forward_log.read().strip()

    def stop_port_forward(self) -> None:
        if self._port_forward:
            if self._port_forward.poll() is None:
                self._port_forward.terminate()
                try:
                    self._port_forward.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    self._port_forward.kill()
                    self._port_forward.wait(timeout=5)
            self._port_forward = None
        if self._port_forward_log:
            self._port_forward_log.close()
            self._port_forward_log = None

    def stop(self) -> None:
        if self._created_resources is None:
            resources = [f"{kind}/{self.name}" for kind in RESOURCE_TYPES]
        else:
            resources = list(self._created_resources)
        self._delete(resources)
        if self._created_resources is not None:
            self._created_resources.clear()

    def consumer_environment(self) -> dict[str, str]:
        if not self.admin_url:
            raise RuntimeError("the fake Netris admin port-forward is not ready")
        environment = os.environ.copy()
        environment.update(
            {
                "NETRIS_CONTROLLER_URL": self.service_url,
                "NETRIS_USERNAME": "osac",
                "NETRIS_PASSWORD": secrets.token_urlsafe(32),
                "NETRIS_FAKE_ADMIN_URL": self.admin_url,
                "NETRIS_FAKE_NAME": self.name,
                "NETRIS_FAKE_NAMESPACE": self.namespace,
            }
        )
        return environment

    def __enter__(self) -> NetrisKindFixture:
        try:
            self.start()
            self.start_admin_forward()
        except BaseException:
            self.stop_port_forward()
            try:
                self.stop()
            except Exception as cleanup_error:
                print(f"Netris fake cleanup failed: {cleanup_error}", file=sys.stderr)
            raise
        return self

    def __exit__(self, exc_type: Any, exc: Any, traceback: Any) -> bool:
        errors = []
        try:
            self.stop_port_forward()
        except Exception as error:
            errors.append(error)
        try:
            self.stop()
        except Exception as error:
            errors.append(error)
        if errors:
            message = "; ".join(str(error) for error in errors)
            if exc is None:
                raise RuntimeError(f"Netris fake cleanup failed: {message}")
            print(f"Netris fake cleanup failed: {message}", file=sys.stderr)
        return False


def run_profile_command(fixture: NetrisKindFixture, command: list[str]) -> int:
    """Run one profile command with fixture settings and unconditional cleanup."""
    with fixture:
        print(f"Netris fake available at {fixture.service_url}", file=sys.stderr)
        result = subprocess.run(command, env=fixture.consumer_environment(), check=False)
        return result.returncode


def _add_common_arguments(
    parser: argparse.ArgumentParser,
    *,
    include_image: bool,
    require_name: bool = False,
) -> None:
    parser.add_argument("--namespace", required=True, help="existing isolated test namespace")
    parser.add_argument("--name", required=require_name, help="fixture resource name")
    if include_image:
        parser.add_argument(
            "--image",
            required=True,
            help="AAP execution-environment image available to Kind",
        )
        parser.add_argument(
            "--pull-policy",
            default="IfNotPresent",
            choices=["Always", "IfNotPresent", "Never"],
        )
    parser.add_argument("--kubectl", default="kubectl", help="kubectl executable")
    parser.add_argument("--kubeconfig", help="optional kubeconfig for the isolated Kind cluster")
    parser.add_argument(
        "--readiness-timeout",
        type=int,
        default=120,
        help="startup timeout in seconds",
    )


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="action", required=True)
    start = subparsers.add_parser("start", help="create the fake endpoint")
    _add_common_arguments(start, include_image=True, require_name=True)

    stop = subparsers.add_parser("stop", help="delete one named fake endpoint")
    _add_common_arguments(stop, include_image=False, require_name=True)

    run = subparsers.add_parser("run", help="run a command with a temporary fixture")
    _add_common_arguments(run, include_image=True)
    run.add_argument("command", nargs=argparse.REMAINDER, help="profile command after --")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    name = args.name or f"netris-fake-{uuid.uuid4().hex[:10]}"
    if args.action == "run" and args.command and args.command[0] == "--":
        args.command = args.command[1:]
    if args.action == "run" and not args.command:
        print("run requires a command after --", file=sys.stderr)
        return 2

    try:
        fixture = NetrisKindFixture(
            name=name,
            namespace=args.namespace,
            image=getattr(args, "image", None),
            kubectl=args.kubectl,
            kubeconfig=args.kubeconfig,
            readiness_timeout=args.readiness_timeout,
            pull_policy=getattr(args, "pull_policy", "IfNotPresent"),
        )
        if args.action == "start":
            url = fixture.start()
            print(json.dumps({"name": name, "namespace": args.namespace, "url": url}))
            return 0
        if args.action == "stop":
            fixture.stop()
            return 0
        return run_profile_command(fixture, args.command)
    except (OSError, RuntimeError, ValueError, subprocess.SubprocessError) as error:
        print(f"Netris fake fixture error: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
