"""Run the AWX bootstrap render check without API access."""

import json
import os
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[6]
PLAYBOOK = "/repo/tests/integration/caas/charts/bootstrap/tests/render.yml"


def main() -> None:
    """Render production AWX inputs in the local EE and let Ansible verify them."""
    image = os.environ.get("EE_IMAGE", "localhost/osac-aap:it")
    runtime = os.environ.get("CONTAINER_TOOL", "podman")
    # Read the checkout as its owner while preserving its SELinux labels.
    runtime_args = ["--user", f"{os.getuid()}:{os.getgid()}"]
    if Path(runtime).name == "podman":
        runtime_args.extend(["--userns=keep-id", "--timeout=80"])

    result = subprocess.run(
        [
            runtime,
            "run",
            "--rm",
            "--pull=never",
            "--network=none",
            *runtime_args,
            "--security-opt",
            "label=disable",
            "--volume",
            f"{ROOT}:/repo:ro",
            "--workdir",
            "/tmp",
            "--env",
            "HOME=/tmp",
            "--env",
            "PYTHONDONTWRITEBYTECODE=1",
            "--env",
            "ANSIBLE_NOCOLOR=1",
            "--env",
            "NAMESPACE=osac",
            "--env",
            "AAP_PASSWORD=offline-placeholder",
            "--env",
            f"AAP_EE_IMAGE={image}",
            "--env",
            "AAP_EE_IMAGE_PULL=never",
            "--env",
            "AAP_PROJECT_GIT_BRANCH=" + "a" * 40,
            "--env",
            "CAAS_NODEPORT_ADDRESS=192.0.2.10",
            "--entrypoint",
            "ansible-playbook",
            image,
            PLAYBOOK,
            "--tags",
            "bootstrap-render",
            "--extra-vars",
            json.dumps({"project_directory": "/repo", "aap_configuration_dispatcher_roles": []}),
        ],
        capture_output=True,
        text=True,
        timeout=90,
        check=False,
    )
    if result.returncode:
        raise SystemExit(result.stdout + result.stderr)
    print("AWX bootstrap render check passed.")


if __name__ == "__main__":
    main()
