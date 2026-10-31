"""Reach a VM guest from a test-owned helper Pod in the VM's namespace.

VMs on a primary user-defined network have no management NIC and `virtctl ssh` is documented as
unsupported there. A Pod in the same namespace joins the same primary network, so it can open an
SSH session to the VM IP. Tests reach the Pod only through `kubectl exec` over the API server.

One Pod per VM namespace: a namespace belongs to exactly one VirtualNetwork, hence to one primary
network, so the namespace is also the right key. The Pod, its key Secret and its NetworkPolicy are
created on first use, reused, and removed by `remove_helper` before the VirtualNetwork is deleted.

The private key is mounted from a Secret and copied to a private file inside the Pod; it never
appears on a command line, in a log line or in an error message.
"""

from __future__ import annotations

import base64
import json
import logging
import os
import re
import subprocess
import threading
from collections import defaultdict
from pathlib import Path

from tests.e2e.core.k8s_client import K8sClient

log = logging.getLogger(__name__)

# The OSAC execution-environment image: project-built, already pulled by the cluster for every AAP job, with
# OpenSSH clients, sh, sleep and cp, and a passwd entry for uid 65534 (ssh refuses to run as a uid without one).
# Verified with docker as uid 65534: /usr/bin/ssh, sleep and cp exist, `nobody` is 65534, OpenSSH 9.9.
# Pinned by digest; export OSAC_E2E_SSH_HELPER_IMAGE to use the deployed tag or a mirror instead.
DEFAULT_IMAGE = "ghcr.io/osac-project/osac-aap@sha256:f537e7a2a72751c2a56902e072c86ea6418d1203286c2bec802c97e631e0ab6d"
IMAGE_ENV = "OSAC_E2E_SSH_HELPER_IMAGE"
POD_NAME = "osac-e2e-ssh-helper"
SECRET_NAME = "osac-e2e-ssh-key"
POLICY_NAME = "osac-e2e-ssh-helper"
_RUN_AS_USER = 65534
_READY_TIMEOUT = "600s"
_RC_RE = re.compile(r"(?m)^__OSAC_SSH_RC__=(\d+)$")
# Runs inside the Pod: keep ssh's own status separate from the status of `kubectl exec`.
_EXEC_WRAPPER = 'ssh "$@"; rc=$?; printf "\\n__OSAC_SSH_RC__=%s\\n" "$rc"'
_START_SCRIPT = "cp /keys/id /tmp/id && chmod 600 /tmp/id && exec sleep 86400"


class HelperPodError(RuntimeError):
    """The helper Pod could not be created or is not Ready: waiting or retrying cannot fix it."""


class HelperExecError(HelperPodError):
    """`kubectl exec` into the helper Pod returned no SSH status (API or kubelet blip, Pod gone). Transient."""


_READY: set[str] = set()
_VM_IPS: dict[tuple[str, str], str] = {}
_GROUPS: dict[str, set[str]] = {}
# VMs of one namespace can be waited on at the same time; one lock per namespace keeps them from applying twice.
_NAMESPACE_LOCKS: defaultdict[str, threading.Lock] = defaultdict(threading.Lock)
_LOCKS_GUARD = threading.Lock()


def reset_state() -> None:
    """Forget cached Pods and addresses (tests)."""
    _READY.clear()
    _VM_IPS.clear()
    _GROUPS.clear()
    _NAMESPACE_LOCKS.clear()


def ssh_user() -> str:
    return os.environ.get("OSAC_VM_SSH_USER", "fedora").strip() or "fedora"


def image() -> str:
    return os.environ.get(IMAGE_ENV, "").strip() or DEFAULT_IMAGE


def _kubeconfig() -> str:
    value = os.environ.get("OSAC_VM_KUBECONFIG", "").strip()
    if not value:
        raise RuntimeError("OSAC_VM_KUBECONFIG must identify the VM cluster kubeconfig")
    return value


def _base() -> list[str]:
    return K8sClient(namespace="default", kubeconfig=_kubeconfig()).base_args()


def _scrub(text: str, secrets: tuple[str, ...] = ()) -> str:
    for secret in secrets:
        if secret:
            text = text.replace(secret, "<redacted>")
    return " ".join(text.split())[:600]


def _kubectl(
    args: list[str], *, timeout: int, payload: str | None = None, secrets: tuple[str, ...] = ()
) -> subprocess.CompletedProcess[str]:
    try:
        return subprocess.run(
            [*_base(), *args], input=payload, capture_output=True, text=True, timeout=timeout, check=False
        )
    except subprocess.TimeoutExpired as exc:
        raise RuntimeError(f"kubectl {args[0]} timed out after {timeout}s") from exc
    except OSError as exc:
        raise RuntimeError(f"failed to start kubectl: {_scrub(str(exc), secrets)}") from exc


def manifests(*, namespace: str, key_b64: str, helper_image: str) -> list[dict[str, object]]:
    """The Secret, NetworkPolicy and Pod of one helper. The policy admits only this Pod's egress to tcp/22."""
    labels = {"osac-e2e/ssh-helper": "true"}
    return [
        {
            "apiVersion": "v1",
            "kind": "Secret",
            "metadata": {"name": SECRET_NAME, "namespace": namespace, "labels": labels},
            "type": "Opaque",
            "data": {"id": key_b64},
        },
        {
            "apiVersion": "networking.k8s.io/v1",
            "kind": "NetworkPolicy",
            "metadata": {"name": POLICY_NAME, "namespace": namespace, "labels": labels},
            "spec": {
                "podSelector": {"matchLabels": labels},
                "policyTypes": ["Egress"],
                # Any destination for now. Narrowing this to the VM namespace (`to: podSelector: {}`) waits for a
                # green run, because it adds a dependency on pod-IP address sets matching the UDN addresses.
                "egress": [{"ports": [{"protocol": "TCP", "port": 22}]}],
            },
        },
        {
            "apiVersion": "v1",
            "kind": "Pod",
            "metadata": {"name": POD_NAME, "namespace": namespace, "labels": labels},
            "spec": {
                "automountServiceAccountToken": False,
                "restartPolicy": "Always",
                # `exec sleep` is PID 1 and ignores SIGTERM, so do not wait out the default grace period.
                "terminationGracePeriodSeconds": 1,
                "securityContext": {
                    "runAsNonRoot": True,
                    "runAsUser": _RUN_AS_USER,
                    "seccompProfile": {"type": "RuntimeDefault"},
                },
                "containers": [
                    {
                        "name": "ssh",
                        "image": helper_image,
                        "command": ["sh", "-c", _START_SCRIPT],
                        "securityContext": {
                            "allowPrivilegeEscalation": False,
                            "readOnlyRootFilesystem": True,
                            "capabilities": {"drop": ["ALL"]},
                        },
                        # Ready only once the key copy is done, so the first SSH cannot race it.
                        "readinessProbe": {"exec": {"command": ["test", "-s", "/tmp/id"]}, "periodSeconds": 1},
                        "resources": {"requests": {"cpu": "10m", "memory": "16Mi"}},
                        "volumeMounts": [
                            {"name": "keys", "mountPath": "/keys", "readOnly": True},
                            {"name": "tmp", "mountPath": "/tmp"},
                        ],
                    }
                ],
                "volumes": [
                    {"name": "keys", "secret": {"secretName": SECRET_NAME, "defaultMode": 0o444}},
                    {"name": "tmp", "emptyDir": {"medium": "Memory", "sizeLimit": "4Mi"}},
                ],
            },
        },
    ]


def ensure_helper(namespace: str, identity_file: str) -> None:
    """Create the helper once per namespace and wait until it is Running and Ready; fail clearly otherwise.

    Safe from several threads: callers of one namespace queue on its lock and re-check `_READY` inside it.
    """
    if namespace in _READY:
        return
    with _LOCKS_GUARD:
        lock = _NAMESPACE_LOCKS[namespace]
    with lock:
        if namespace in _READY:
            return
        _create_helper(namespace, identity_file)


def _create_helper(namespace: str, identity_file: str) -> None:
    try:
        key_text = Path(identity_file).read_text()
    except OSError as exc:
        raise HelperPodError(f"cannot read the SSH identity file for the helper Pod: {type(exc).__name__}") from exc
    key_b64 = base64.b64encode(key_text.encode()).decode()
    secrets = (key_b64, key_text, key_text.strip())
    payload = json.dumps(
        {
            "apiVersion": "v1",
            "kind": "List",
            "items": manifests(namespace=namespace, key_b64=key_b64, helper_image=image()),
        }
    )
    applied = _kubectl(["apply", "-f", "-"], timeout=60, payload=payload, secrets=secrets)
    if applied.returncode != 0:
        raise HelperPodError(
            f"could not create the SSH helper Pod in namespace {namespace}: "
            f"{_scrub(applied.stderr + applied.stdout, secrets)}. If this is a security context constraint "
            "rejection: the Pod runs as uid 65534 and the suite creates it as system:admin, so the identity "
            "needs an SCC that allows that uid."
        )
    waited = _kubectl(
        ["wait", "--for=condition=Ready", f"pod/{POD_NAME}", "-n", namespace, f"--timeout={_READY_TIMEOUT}"],
        timeout=660,
    )
    if waited.returncode != 0:
        state = _kubectl(
            ["get", "pod", POD_NAME, "-n", namespace, "-o", "jsonpath={.status.phase} {.status.containerStatuses}"],
            timeout=30,
        )
        events = _kubectl(
            [
                "get",
                "events",
                "-n",
                namespace,
                "--field-selector",
                f"involvedObject.name={POD_NAME}",
                "-o",
                "jsonpath={.items[-3:].message}",
            ],
            timeout=30,
        )
        raise HelperPodError(
            f"SSH helper Pod {namespace}/{POD_NAME} is not Running and Ready (image {image()}): "
            f"{_scrub(waited.stderr + waited.stdout, secrets)} | {_scrub(state.stdout, secrets)} | "
            f"events: {_scrub(events.stdout, secrets)}. "
            f"If the image cannot be pulled, set {IMAGE_ENV} to an image with an ssh client that the "
            "cluster can pull."
        )
    _READY.add(namespace)


def remove_helper(namespace: str) -> None:
    """Delete the Pod, Secret and NetworkPolicy of a namespace; safe when they or the namespace are gone."""
    _READY.discard(namespace)
    errors: list[str] = []
    for kind, name in (("pod", POD_NAME), ("networkpolicy", POLICY_NAME), ("secret", SECRET_NAME)):
        result = _kubectl(
            ["delete", kind, name, "-n", namespace, "--ignore-not-found", "--wait=true", "--timeout=120s"], timeout=180
        )
        if result.returncode != 0:
            errors.append(f"{kind}: {_scrub(result.stderr + result.stdout)}")
    if errors:
        raise RuntimeError(f"could not remove the SSH helper in namespace {namespace}: " + "; ".join(errors))


def note_namespace(group: str, namespace: str) -> None:
    """Remember that VMs of `group` (a VirtualNetwork ID) live in `namespace`."""
    _GROUPS.setdefault(group, set()).add(namespace)


def remove_helpers_for(group: str) -> None:
    """Remove the helpers of every namespace recorded for `group`; keeps going and reports all failures."""
    errors: list[str] = []
    for namespace in sorted(_GROUPS.get(group, ())):
        try:
            remove_helper(namespace)
        except RuntimeError as exc:
            errors.append(str(exc))
    if errors:
        raise RuntimeError("; ".join(errors))
    _GROUPS.pop(group, None)


def vm_ip(namespace: str, vm_name: str) -> str:
    """The VMI address of a ComputeInstance; a VM without one yet is a transport failure."""
    cached = _VM_IPS.get((namespace, vm_name))
    if cached:
        return cached
    result = _kubectl(
        [
            "get",
            "virtualmachineinstance",
            "-n",
            namespace,
            "-l",
            f"osac.openshift.io/computeinstance={vm_name}",
            "-o",
            "jsonpath={.items[0].status.interfaces[0].ipAddress}",
        ],
        timeout=30,
    )
    address = result.stdout.strip() if result.returncode == 0 else ""
    if not address:
        raise RuntimeError(
            f"ComputeInstance {namespace}/{vm_name} has no VMI address yet, so it cannot be reached over SSH"
        )
    _VM_IPS[(namespace, vm_name)] = address
    return address


def ssh_args(*, address: str, command: str, timeout: int) -> list[str]:
    """Arguments of the `ssh` run inside the Pod. The key is a Pod-local path, never its contents."""
    return [
        "-F",
        "/dev/null",
        "-n",
        "-T",
        "-i",
        "/tmp/id",
        "-o",
        "IdentitiesOnly=yes",
        "-o",
        "StrictHostKeyChecking=no",
        "-o",
        "UserKnownHostsFile=/dev/null",
        "-o",
        "BatchMode=yes",
        "-o",
        f"ConnectTimeout={max(1, min(10, timeout))}",
        "-o",
        "LogLevel=ERROR",
        f"{ssh_user()}@{address}",
        command,
    ]


def exec_ssh(*, namespace: str, vm_name: str, identity_file: str, command: str, timeout: int) -> tuple[str, int]:
    """Run `command` on the guest. Returns (output, ssh status): 255 is an SSH transport failure.

    Raises HelperPodError (Pod not usable), HelperExecError (exec lost) or RuntimeError (timeout, no VM
    address), which is different from any guest or ssh exit status.
    """
    ensure_helper(namespace, identity_file)
    address = vm_ip(namespace, vm_name)
    args = [
        "exec",
        "-n",
        namespace,
        POD_NAME,
        "--",
        "sh",
        "-c",
        _EXEC_WRAPPER,
        "sh",
        *ssh_args(address=address, command=command, timeout=timeout),
    ]
    try:
        result = subprocess.run([*_base(), *args], capture_output=True, text=True, timeout=timeout, check=False)
    except subprocess.TimeoutExpired as exc:
        raise RuntimeError(f"SSH to {namespace}/{vm_name} through the helper Pod timed out after {timeout}s") from exc
    except OSError as exc:
        raise RuntimeError(f"failed to start kubectl exec for {namespace}/{vm_name}: {type(exc).__name__}") from exc

    matches = _RC_RE.findall(result.stdout)
    if len(matches) != 1:
        _READY.discard(namespace)
        raise HelperExecError(
            f"kubectl exec into helper Pod {namespace}/{POD_NAME} failed (rc={result.returncode}): "
            f"{_scrub(result.stderr + ' ' + result.stdout)}"
        )
    stdout = _RC_RE.sub("", result.stdout).strip()
    output = (stdout + "\n" + result.stderr.strip()).strip()
    return output, int(matches[0])
