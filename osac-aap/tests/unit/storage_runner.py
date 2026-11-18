"""Shared real-Ansible runner with normal, reusable provider boundary modules."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys


OSAC_AAP = Path(__file__).resolve().parents[2]
FIXTURES = Path(__file__).parent / "fixtures/storage"


def install_modules(directory):
    for collection, sources in {
        "kubernetes/core": {"k8s": "kubernetes", "k8s_info": "kubernetes", "helm": "helm"},
        "vastdata/vms": {"viewpolicies": "vms", "views": "vms", "tenants": "vms"},
    }.items():
        modules = directory / "ansible_collections" / collection / "plugins/modules"
        modules.mkdir(parents=True, exist_ok=True)
        for name, source in sources.items():
            shutil.copyfile(FIXTURES / f"{source}.py", modules / f"{name}.py")


def run_tasks(directory, tasks, variables=None, existing=(), environment=None):
    directory.mkdir(parents=True, exist_ok=True)
    install_modules(directory)
    capture = directory / "calls.jsonl"
    capture.unlink(missing_ok=True)
    store = directory / "objects.json"
    store.write_text(json.dumps(existing))
    playbook = directory / "playbook.json"
    playbook.write_text(json.dumps([{
        "name": "Exercise storage consumers", "hosts": "localhost", "gather_facts": False,
        "vars": {"ansible_connection": "local", "ansible_python_interpreter": sys.executable,
                 "tenant_name": "fstype-test", "tenant_namespace": "osac-tenants", **(variables or {})},
        "tasks": tasks,
    }]))
    settings = {
        **os.environ,
        "ANSIBLE_CONFIG": str(OSAC_AAP / "ansible.cfg"),
        "ANSIBLE_COLLECTIONS_PATH": f"{directory}:{OSAC_AAP / 'collections'}:{OSAC_AAP / 'vendor'}",
        "ANSIBLE_FORCE_COLOR": "false", "ANSIBLE_STDOUT_CALLBACK": "default",
        "OSAC_STORAGE_CAPTURE": str(capture), "OSAC_STORAGE_STORE": str(store),
        "OSAC_REMOTE_CLUSTER_KUBECONFIG": "", "NO_PROXY": "127.0.0.1,localhost",
        "no_proxy": "127.0.0.1,localhost",
    }
    settings.pop("OSAC_STORAGE_BLOCK_FSTYPE", None)
    settings.update(environment or {})
    result = subprocess.run(
        ["ansible-playbook", "-i", "localhost,", str(playbook)], cwd=OSAC_AAP,
        env=settings, capture_output=True, text=True, timeout=90, check=False,
    )
    calls = [json.loads(line) for line in capture.read_text().splitlines()] if capture.exists() else []
    return result, calls
