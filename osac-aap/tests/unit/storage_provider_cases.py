"""Discover effective provider routes and execute their real class creators."""

from dataclasses import dataclass

from ansible.parsing.dataloader import DataLoader
from ansible.template import Templar
import yaml

from .storage_runner import FIXTURES, OSAC_AAP, run_tasks


ROLES = OSAC_AAP / "collections/ansible_collections/osac/templates/roles"
SERVICE = OSAC_AAP / "collections/ansible_collections/osac/service/roles"
CONTRACTS = yaml.safe_load((FIXTURES / "providers.yaml").read_text())
DEFAULTS = yaml.safe_load((SERVICE / "storage_provider/defaults/main.yaml").read_text())
DISPATCH = yaml.safe_load((SERVICE / "storage_provider/tasks/_dispatch_provider.yaml").read_text())
DISPATCH_NAME = next(task for task in DISPATCH if "block" in task)["block"][0]["ansible.builtin.include_role"]["name"]


@dataclass(frozen=True)
class ProviderCase:
    provider: str
    role: str
    mode: str = "default"
    protocols: tuple[str, ...] = ("block",)

    @property
    def id(self):
        return f"{self.provider}-{self.mode}"


def discover_cases(roles=ROLES, contracts=CONTRACTS):
    metadata = {}
    candidates = set(DEFAULTS["storage_provider_role_overrides"])
    for role in roles.iterdir():
        path = role / "meta/osac.yaml"
        if (role / "tasks/ensure_storage_class.yaml").exists():
            assert path.exists(), f"Storage class creator {role.name} has no provider metadata"
        if not path.exists():
            continue
        meta = yaml.safe_load(path.read_text())
        if meta.get("template_type") != "storage_provider":
            continue
        canonical = role.resolve().name
        assert canonical in contracts, f"Uncovered storage provider: {role.name} ({canonical}); add a fixture contract"
        protocols = meta.get("capabilities", {}).get("supported_protocols")
        assert protocols, f"Provider {role.name} must declare supported_protocols"
        assert set(protocols) <= {"block", "nfs"}, f"Uncovered protocols for {role.name}: {protocols}"
        metadata[canonical] = meta
        candidates.update({role.name.replace("_", "-"), role.name.removesuffix("_storage").replace("_", "-")})
    assert set(metadata) == set(contracts), "Stale or missing storage provider fixture contracts"
    cases = []
    for provider in sorted(candidates):
        templar = Templar(loader=DataLoader(), variables={**DEFAULTS, "_current_provider": provider})
        dispatched = templar.template(DISPATCH_NAME).strip().split(".")[-1]
        assert (roles / dispatched).exists(), f"Uncovered dispatch target: {provider} -> {dispatched}"
        canonical = (roles / dispatched).resolve().name
        assert canonical in contracts, f"Uncovered dispatch provider: {provider} -> {canonical}"
        protocols = tuple(metadata[canonical]["capabilities"]["supported_protocols"])
        if "nfs" in protocols:
            assert "nfs_parameters" in contracts[canonical], f"Missing NFS fixture contract: {canonical}"
        cases.extend(ProviderCase(provider, canonical, mode, protocols) for mode in contracts[canonical]["modes"])
    return cases


CASES = discover_cases()
CREATORS = list({(case.role, case.mode): case for case in CASES}.values())
BLOCK_CREATORS = [case for case in CREATORS if "block" in case.protocols]
LOCAL = ProviderCase("lvms", "csi_driver_install", "local")


def run_provider(directory, case, *, protocols=None, names=None, existing=(),
                 environment=None, overrides=None, action="ensure_storage_class", expected=None):
    protocols = case.protocols if protocols is None else protocols
    tiers = [dict(name=name, protocol=protocol, provider=case.provider, backend_id="fixture")
             for name, protocol in zip(names or protocols, protocols, strict=True)]
    connection = {"endpoint": "https://fixture.invalid", "username": "fixture", "password": "fixture-only"}
    objects = list(existing)
    if case.mode == "shared":
        connection = {"provider_config": {"profiles": [{
            "name": "fixture", "shared": {"tbc_name": "fixture", "tbc_namespace": "trident"},
        }]}}
        for tier in tiers:
            tier["provider_config"] = {"profile": "fixture", "pool_selector": {"tier": tier["name"]}}
        objects.append({"kind": "TridentBackendConfig", "metadata": {"name": "fixture"},
                        "spec": {"storageDriverName": "ontap-san", "sanType": "fcp", "backendName": "fixture"},
                        "status": {"phase": "Bound", "backendInfo": {"backendName": "fixture"}}})
    elif case.mode == "dedicated":
        connection["tenant_preparation"] = {"provider_config": {
            "username": "fixture", "password": "fixture-only", "svm": "fixture-svm",
        }}
    variables = {
        "storage_provider_tiers": tiers, "storage_provider_action": action,
        "storage_provider_backend_connections": {"fixture": connection},
        "vast_storage_config_namespace": "osac-system", "vast_storage_validate_certs": False,
        "netapp_storage_validate_certs": False, "_cluster_name": "fixture-cluster",
        **(overrides or {}),
    }
    if case.role == "osac_csi_storage":
        variables["_remote_kubeconfig"] = "/fixture/guest"
        variables["fulfillment_endpoint"] = "https://fulfillment.invalid"
        variables["fulfillment_issuer_url"] = "https://issuer.invalid"
    include = {"name": "Dispatch real provider", "ansible.builtin.include_role": {"name": "osac.service.storage_provider"}}
    if case == LOCAL:
        variables["csi_driver_install_tiers"] = tiers
        include["ansible.builtin.include_role"] = {"name": "osac.service.csi_driver_install", "tasks_from": "storage_classes"}
    tasks = [include]
    if case != LOCAL and action == "ensure_storage_class":
        tasks.append({"name": "Verify every requested tier is returned", "ansible.builtin.assert": {"that": [
            "storage_provider_storage_class_names | map(attribute='tier') | sort | list == storage_provider_tiers | map(attribute='name') | sort | list",
            "storage_provider_storage_class_names | map(attribute='name') | unique | length == storage_provider_tiers | length",
        ]}})
    if expected is not None:
        tasks.append({"name": "Verify complete output", "ansible.builtin.assert": {"that":
            "storage_provider_storage_class_names | map(attribute='name') | sort | list == expected_names | sort"},
            "vars": {"expected_names": expected}})
    return run_tasks(directory, tasks, variables, objects, environment)


def created_classes(calls):
    return [call["definition"] for call in calls
            if call["module"] == "k8s" and call["state"] == "present"
            and (call["definition"] or {}).get("kind") == "StorageClass"]


def assert_success(result):
    assert result.returncode == 0, result.stdout + result.stderr
