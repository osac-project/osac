#!/usr/bin/env python3
"""Networking checks of the installer integration leg (it-osac-installer).

Runs after `make install-osac` against the live Kind cluster and verifies what
the networking installation must leave behind (unified networking design,
network manager contract §4.2, DECISIONS 3, 8, 16, 18):

  1. the NetworkClass step Job succeeded and the private API holds exactly one
     NetworkClass whose fabric_manager, k8s_manager, spec.defaults,
     spec.east_west_capabilities and spec.address_plan match the step input the
     chart rendered (and the plan derived from the enabled services), the
     fulfillment controller mirrored it to a NetworkClass CR, and with provider
     networking disabled status.security_group_icmp stays unset;
  2. the ValidatingAdmissionPolicy and its binding exist and reject a direct
     networking CR create by a non-OSAC user that RBAC allows (kubectl --as),
     while the same user may still create an ordinary object;
  3. the operator and fulfillment Deployments carry
     OSAC_PROVISIONING_ENABLED / --networking-provisioning-enabled as rendered
     from the profile's global.networking.provisioningEnabled;
  4. a one-shot copy of the fulfillment gRPC server pod with the provisioning
     flag inverted refuses to start with the installation settings message,
     and neither the stored settings nor the running Deployment change.

Every check prints PASS/FAIL with the object and value it looked at. The script
exits non-zero when any check fails. It needs kubectl and helm on PATH and a
KUBECONFIG with cluster-admin (impersonation) rights.
"""

from __future__ import annotations

import argparse
import base64
import json
import subprocess
import sys
import time
import uuid

STEP_JOB = "create-network-class"
VAP_MESSAGE = "OSAC manages this resource; change it through the fulfillment API"
READER_MARKER = "IT-NETWORK-CLASSES "
PROVISIONING_FLAG = "--networking-provisioning-enabled"
PROVISIONING_CHANGED_MESSAGE = "global.networking.provisioningEnabled cannot change after installation"
GRPC_SERVER_DEPLOYMENT = "fulfillment-grpc-server"
REFUSAL_POD_PREFIX = "it-settings-refusal"
REFUSAL_TIMEOUT_SECONDS = 180
PLAN_SHARES = ("overlay", "fabric", "lb_vip")
CR_PLAN_SHARES = {"overlay": "overlay", "fabric": "fabric", "lb_vip": "lbVip"}

# Reads the private API's NetworkClasses from inside the cluster, with the
# NetworkClass step Job's own credentials, CA bundle and URL.
READER = r"""
import json, os, ssl, urllib.request
req = urllib.request.Request(os.environ["FULFILLMENT_URL"].rstrip("/") + "/api/private/v1/network_classes")
req.add_header("Authorization", "Bearer " + os.environ["AUTH_TOKEN"])
ctx = ssl.create_default_context(cafile=os.environ.get("FULFILLMENT_CA_FILE") or None)
with urllib.request.urlopen(req, timeout=30, context=ctx) as resp:
    print("IT-NETWORK-CLASSES " + json.dumps(json.loads(resp.read().decode() or "{}")))
"""


class Checks:
    def __init__(self) -> None:
        self.failures: list[str] = []
        self.passed = 0

    def section(self, title: str) -> None:
        print(f"\n=== {title} ===", flush=True)

    def info(self, message: str) -> None:
        print(f"  ... {message}", flush=True)

    def check(self, name: str, ok: bool, detail: str = "") -> bool:
        status = "PASS" if ok else "FAIL"
        print(f"  [{status}] {name}" + (f": {detail}" if detail else ""), flush=True)
        if ok:
            self.passed += 1
        else:
            self.failures.append(f"{name}: {detail}")
        return ok


def run(args: list[str], stdin: str | None = None, check: bool = True) -> subprocess.CompletedProcess:
    proc = subprocess.run(args, input=stdin, capture_output=True, text=True, timeout=600, check=False)
    if check and proc.returncode != 0:
        raise RuntimeError(
            f"{' '.join(args)} failed (rc={proc.returncode}): {proc.stderr.strip() or proc.stdout.strip()}"
        )
    return proc


def kubectl_json(*args: str) -> dict:
    return json.loads(run(["kubectl", *args, "-o", "json"]).stdout)


def manifests_to_json(text: str) -> list[dict]:
    """Convert YAML manifests to JSON objects without a YAML library."""
    out = run(["kubectl", "create", "--dry-run=client", "-o", "json", "-f", "-"], stdin=text).stdout
    decoder = json.JSONDecoder()
    docs = []
    offset = 0
    while offset < len(out):
        while offset < len(out) and out[offset].isspace():
            offset += 1
        if offset == len(out):
            break
        doc, offset = decoder.raw_decode(out, offset)
        docs.extend(doc.get("items", [doc]) if doc.get("kind") == "List" else [doc])
    return docs


def short(value: object, limit: int = 600) -> str:
    text = json.dumps(value, sort_keys=True) if not isinstance(value, str) else value
    return text if len(text) <= limit else text[:limit] + "..."


def normalize_plan(plan: dict | None) -> dict:
    plan = plan or {}
    return {share: float(plan.get(share) or 0) for share in PLAN_SHARES}


def normalize_rules(rules: list | None) -> list:
    return [
        {
            "protocol": r.get("protocol", ""),
            "ipv4_cidr": r.get("ipv4_cidr", ""),
            "port_from": r.get("port_from"),
            "port_to": r.get("port_to"),
        }
        for r in rules or []
    ]


def normalize_defaults(defaults: dict | None) -> dict:
    defaults = defaults or {}
    return {
        "virtual_network_ipv4_cidr": defaults.get("virtual_network_ipv4_cidr", ""),
        "subnet_ipv4_cidr": defaults.get("subnet_ipv4_cidr", ""),
        "ingress_rules": normalize_rules(defaults.get("ingress_rules")),
        "egress_rules": normalize_rules(defaults.get("egress_rules")),
    }


def derived_plan(services: dict, k8s_manager: str) -> dict:
    """The default plan of the osac chart (osac.networking.addressPlan)."""
    if k8s_manager:
        if services.get("caas"):
            plan = (0.5, 0.25, 0.25)
        elif services.get("bmaas"):
            plan = (0.5, 0.5, 0.0)
        else:
            plan = (0.75, 0.25, 0.0)
    elif services.get("caas"):
        plan = (0.0, 0.75, 0.25)
    else:
        plan = (0.0, 1.0, 0.0)
    return dict(zip(PLAN_SHARES, plan, strict=True))


# ---------------------------------------------------------------------------


def step_job_input(c: Checks, ns: str, release: str) -> tuple[dict, dict]:
    """Return (the rendered NetworkClass step Job, its parsed step input)."""
    hooks = manifests_to_json(run(["helm", "get", "hooks", release, "-n", ns]).stdout)
    jobs = [h for h in hooks if h.get("kind") == "Job" and h["metadata"]["name"] == STEP_JOB]
    if not c.check(f"release {release} renders the {STEP_JOB} hook Job", len(jobs) == 1, f"found {len(jobs)}"):
        return {}, {}
    job = jobs[0]
    env = {e["name"]: e.get("value") for e in job["spec"]["template"]["spec"]["containers"][0].get("env", [])}
    inp = json.loads(env.get("NETWORK_CLASS_STEP_INPUT") or "{}")
    c.info(f"rendered step input: {short(inp, 2000)}")
    return job, inp


def read_network_classes(c: Checks, ns: str, job: dict) -> list | None:
    """Run a read-only copy of the step Job and return the private API's NetworkClasses."""
    name = f"it-network-class-read-{uuid.uuid4().hex[:6]}"
    reader = json.loads(json.dumps(job))
    reader["metadata"] = {"name": name, "namespace": ns, "labels": {"app.kubernetes.io/name": "it-networking-checks"}}
    spec = reader["spec"]
    spec["backoffLimit"] = 2
    spec["activeDeadlineSeconds"] = 300
    spec["ttlSecondsAfterFinished"] = 600
    pod = spec["template"]["spec"]
    pod["restartPolicy"] = "Never"
    container = pod["containers"][0]
    script = container["command"][-1]
    if "exec python3 /scripts/network-class-step.py" not in script:
        c.check("the step Job runs network-class-step.py", False, short(script))
        return None
    encoded = base64.b64encode(READER.encode()).decode()
    container["command"][-1] = script.replace(
        "exec python3 /scripts/network-class-step.py",
        f"exec python3 -c \"import base64; exec(base64.b64decode('{encoded}'))\"",
    )
    container["name"] = "read-network-classes"
    run(["kubectl", "apply", "-n", ns, "-f", "-"], stdin=json.dumps(reader))
    c.info(f"started Job {ns}/{name} (read-only copy of {STEP_JOB})")
    try:
        done = run(
            ["kubectl", "wait", "-n", ns, f"job/{name}", "--for=condition=complete", "--timeout=300s"], check=False
        )
        logs = run(["kubectl", "logs", "-n", ns, f"job/{name}", "--all-containers"], check=False).stdout
        c.info(f"Job {name} logs: {short(logs, 3000)}")
        if not c.check(f"read-only Job {name} completed", done.returncode == 0, done.stderr.strip()):
            return None
        lines = [line for line in logs.splitlines() if line.startswith(READER_MARKER)]
        if not c.check(f"Job {name} printed the NetworkClass list", len(lines) == 1, f"{len(lines)} marker lines"):
            return None
        return json.loads(lines[0][len(READER_MARKER) :]).get("items") or []
    finally:
        run(["kubectl", "delete", "job", name, "-n", ns, "--ignore-not-found", "--wait=false"], check=False)


def check_network_class(c: Checks, ns: str, release: str, values: dict) -> None:
    c.section("NetworkClass step and the private API NetworkClass")
    proc = run(["kubectl", "get", "job", STEP_JOB, "-n", ns, "-o", "json"], check=False)
    if proc.returncode != 0:
        # hook-delete-policy hook-succeeded: Helm deletes the Job once it
        # succeeded, and `helm upgrade --install --wait` fails on a failed hook.
        c.check(
            f"Job {ns}/{STEP_JOB} succeeded (deleted by Helm on success)",
            "NotFound" in proc.stderr,
            proc.stderr.strip(),
        )
    else:
        status = json.loads(proc.stdout).get("status", {})
        c.info(f"Job {ns}/{STEP_JOB} status: {short(status)}")
        c.check(f"Job {ns}/{STEP_JOB} succeeded", int(status.get("succeeded") or 0) >= 1, short(status))

    job, inp = step_job_input(c, ns, release)
    if not inp:
        return
    networking = values.get("global", {}).get("networking", {})
    services = {
        k: bool(values.get("global", {}).get("services", {}).get(k, {}).get("enabled"))
        for k in ("vmaas", "bmaas", "caas")
    }
    c.check(
        "step input fabric_manager is global.networking.fabricManager",
        inp.get("fabric_manager") == networking.get("fabricManager"),
        f"input={inp.get('fabric_manager')!r} values={networking.get('fabricManager')!r}",
    )
    c.check(
        "step input k8s_manager is global.networking.k8sManager",
        (inp.get("k8s_manager") or "") == (networking.get("k8sManager") or ""),
        f"input={inp.get('k8s_manager')!r} values={networking.get('k8sManager')!r}",
    )
    c.check(
        "step input provisioning_enabled is global.networking.provisioningEnabled",
        inp.get("provisioning_enabled") == networking.get("provisioningEnabled"),
        f"input={inp.get('provisioning_enabled')!r} values={networking.get('provisioningEnabled')!r}",
    )
    plan_values = (networking.get("networkClass") or {}).get("addressPlan")
    expected_plan = (
        normalize_plan(
            {"overlay": plan_values["overlay"], "fabric": plan_values["fabric"], "lb_vip": plan_values["lbVip"]}
        )
        if plan_values
        else derived_plan(services, networking.get("k8sManager") or "")
    )
    c.check(
        "step input address_plan is the profile's plan (explicit or derived from the services)",
        normalize_plan(inp.get("address_plan")) == expected_plan,
        f"input={inp.get('address_plan')} expected={expected_plan} services={services}",
    )

    items = read_network_classes(c, ns, job)
    if items is None:
        return
    c.info(f"private API NetworkClasses: {short(items, 3000)}")
    if not c.check("the private API holds exactly one NetworkClass", len(items) == 1, f"found {len(items)}"):
        return
    nc = items[0]
    spec = nc.get("spec") or {}
    nc_id = nc.get("id")
    c.check(
        f"NetworkClass {nc_id} fabric_manager",
        nc.get("fabric_manager") == inp["fabric_manager"],
        f"{nc.get('fabric_manager')!r}, expected {inp['fabric_manager']!r}",
    )
    c.check(
        f"NetworkClass {nc_id} k8s_manager",
        (nc.get("k8s_manager") or "") == (inp.get("k8s_manager") or ""),
        f"{nc.get('k8s_manager')!r}, expected {inp.get('k8s_manager')!r}",
    )
    c.check(
        f"NetworkClass {nc_id} spec.defaults",
        normalize_defaults(spec.get("defaults")) == normalize_defaults(inp["defaults"]),
        f"{short(spec.get('defaults'))}, expected {short(inp['defaults'])}",
    )
    c.check(
        f"NetworkClass {nc_id} spec.address_plan",
        normalize_plan(spec.get("address_plan")) == expected_plan,
        f"{spec.get('address_plan')}, expected {expected_plan}",
    )
    stored_ew = bool((spec.get("east_west_capabilities") or {}).get("supports_east_west_ethernet"))
    wanted_ew = bool((inp.get("east_west_capabilities") or {}).get("supports_east_west_ethernet"))
    c.check(
        f"NetworkClass {nc_id} spec.east_west_capabilities",
        stored_ew == wanted_ew,
        f"supports_east_west_ethernet={stored_ew}, expected {wanted_ew}",
    )
    if not inp.get("provisioning_enabled"):
        c.check(
            f"NetworkClass {nc_id} status.security_group_icmp",
            "security_group_icmp" not in (nc.get("status") or {}),
            f"{short(nc.get('status'))}, expected unset with provider networking disabled",
        )

    # The fulfillment controller mirrors the NetworkClass to a CR in the OSAC
    # namespace (DECISIONS 3, 4: address plan in sixteenths).
    expected_cr_plan = {CR_PLAN_SHARES[s]: round(expected_plan[s] * 16) for s in PLAN_SHARES}
    deadline = time.monotonic() + 300
    crs: list = []
    while True:
        proc = run(["kubectl", "get", "networkclasses.osac.openshift.io", "-n", ns, "-o", "json"], check=False)
        crs = json.loads(proc.stdout).get("items", []) if proc.returncode == 0 else []
        ready = [
            cr
            for cr in crs
            if any(
                cond.get("type") == "Ready" and cond.get("status") == "True"
                for cond in (cr.get("status") or {}).get("conditions") or []
            )
        ]
        if (len(crs) == 1 and ready) or time.monotonic() > deadline:
            break
        time.sleep(5)
    summary = [{"name": cr["metadata"]["name"], "spec": cr.get("spec"), "status": cr.get("status")} for cr in crs]
    c.info(f"NetworkClass CRs in {ns}: {short(summary, 3000)}")
    if not c.check(f"exactly one NetworkClass CR in {ns} (within 300s)", len(crs) == 1, f"found {len(crs)}"):
        return
    cr = crs[0]
    cr_name = cr["metadata"]["name"]
    cr_spec = cr.get("spec") or {}
    c.check(
        f"NetworkClass CR {cr_name} spec.fabricManager",
        cr_spec.get("fabricManager") == inp["fabric_manager"],
        f"{cr_spec.get('fabricManager')!r}",
    )
    c.check(
        f"NetworkClass CR {cr_name} spec.k8sManager",
        (cr_spec.get("k8sManager") or "") == (inp.get("k8s_manager") or ""),
        f"{cr_spec.get('k8sManager')!r}",
    )
    c.check(
        f"NetworkClass CR {cr_name} spec.addressPlan in sixteenths",
        {k: int(v) for k, v in (cr_spec.get("addressPlan") or {}).items()} == expected_cr_plan,
        f"{cr_spec.get('addressPlan')}, expected {expected_cr_plan}",
    )
    c.check(
        f"NetworkClass CR {cr_name} spec.defaults CIDRs",
        (cr_spec.get("defaults") or {}).get("virtualNetworkIpv4Cidr") == inp["defaults"]["virtual_network_ipv4_cidr"]
        and (cr_spec.get("defaults") or {}).get("subnetIpv4Cidr") == inp["defaults"]["subnet_ipv4_cidr"],
        f"{short(cr_spec.get('defaults'))}",
    )
    conditions = (cr.get("status") or {}).get("conditions") or []
    ready = [cond for cond in conditions if cond.get("type") == "Ready"]
    c.check(
        f"NetworkClass CR {cr_name} is Ready",
        bool(ready) and ready[0].get("status") == "True",
        f"conditions={short(conditions)}",
    )


def check_admission_policy(c: Checks, ns: str) -> None:
    c.section("ValidatingAdmissionPolicy for OSAC-managed resources")
    name = f"{ns}-osac-managed-resources"
    proc = run(["kubectl", "get", "validatingadmissionpolicy", name, "-o", "json"], check=False)
    if not c.check(f"ValidatingAdmissionPolicy {name} exists", proc.returncode == 0, proc.stderr.strip()):
        return
    policy = json.loads(proc.stdout)
    spec = policy.get("spec", {})
    rules = spec.get("matchConstraints", {}).get("resourceRules", [])
    c.info(f"policy {name}: failurePolicy={spec.get('failurePolicy')} rules={short(rules, 1500)}")
    c.check(f"policy {name} failurePolicy is Fail", spec.get("failurePolicy") == "Fail", str(spec.get("failurePolicy")))
    networking_rule = [r for r in rules if "CREATE" in r.get("operations", [])]
    resources = networking_rule[0].get("resources", []) if networking_rule else []
    for resource in (
        "networkclasses",
        "virtualnetworks",
        "subnets",
        "securitygroups",
        "networkattachments",
        "externalippools",
        "externalips",
        "externalipattachments",
        "natgateways",
        "fabricdomains",
    ):
        c.check(
            f"policy {name} matches CREATE of {resource} and {resource}/status",
            resource in resources and f"{resource}/status" in resources,
            f"resources={resources}",
        )
    proc = run(["kubectl", "get", "validatingadmissionpolicybinding", name, "-o", "json"], check=False)
    if not c.check(f"ValidatingAdmissionPolicyBinding {name} exists", proc.returncode == 0, proc.stderr.strip()):
        return
    binding = json.loads(proc.stdout).get("spec", {})
    c.info(f"binding {name}: {short(binding)}")
    c.check(f"binding {name} uses policy {name}", binding.get("policyName") == name, str(binding.get("policyName")))
    c.check(
        f"binding {name} denies", binding.get("validationActions") == ["Deny"], str(binding.get("validationActions"))
    )
    selector = binding.get("matchResources", {}).get("namespaceSelector", {}).get("matchLabels", {})
    c.check(f"binding {name} selects namespace {ns}", selector == {"kubernetes.io/metadata.name": ns}, str(selector))

    user = f"it-networking-checks-{uuid.uuid4().hex[:6]}"
    role = f"{user}-writer"
    rbac = [
        {
            "apiVersion": "rbac.authorization.k8s.io/v1",
            "kind": "Role",
            "metadata": {"name": role, "namespace": ns},
            "rules": [
                {"apiGroups": ["osac.openshift.io"], "resources": ["virtualnetworks"], "verbs": ["create"]},
                {"apiGroups": [""], "resources": ["configmaps"], "verbs": ["create"]},
            ],
        },
        {
            "apiVersion": "rbac.authorization.k8s.io/v1",
            "kind": "RoleBinding",
            "metadata": {"name": role, "namespace": ns},
            "roleRef": {"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": role},
            "subjects": [{"apiGroup": "rbac.authorization.k8s.io", "kind": "User", "name": user}],
        },
    ]
    run(["kubectl", "apply", "-f", "-"], stdin=json.dumps({"apiVersion": "v1", "kind": "List", "items": rbac}))
    c.info(f"granted user {user} create on virtualnetworks and configmaps in {ns} (Role/RoleBinding {role})")
    try:
        deadline = time.monotonic() + 60
        while True:
            can = run(
                ["kubectl", "auth", "can-i", "create", "virtualnetworks.osac.openshift.io", "-n", ns, "--as", user],
                check=False,
            )
            if can.stdout.strip() == "yes" or time.monotonic() > deadline:
                break
            time.sleep(2)
        c.check(
            f"RBAC lets {user} create VirtualNetworks in {ns}",
            can.stdout.strip() == "yes",
            can.stdout.strip() or can.stderr.strip(),
        )
        vn = {
            "apiVersion": "osac.openshift.io/v1alpha1",
            "kind": "VirtualNetwork",
            "metadata": {"name": f"{user}-vn", "namespace": ns},
            "spec": {"ipv4Cidr": "10.201.0.0/16"},
        }
        denied = run(
            ["kubectl", "create", "--dry-run=server", "--as", user, "-n", ns, "-f", "-"],
            stdin=json.dumps(vn),
            check=False,
        )
        c.info(
            f"kubectl create VirtualNetwork --as {user}: rc={denied.returncode} "
            f"stdout={denied.stdout.strip()!r} stderr={denied.stderr.strip()!r}"
        )
        c.check(
            f"policy {name} rejects a direct VirtualNetwork create by {user}",
            denied.returncode != 0 and VAP_MESSAGE in denied.stderr and name in denied.stderr,
            denied.stderr.strip() or denied.stdout.strip(),
        )
        cm = {"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": f"{user}-cm", "namespace": ns}}
        allowed = run(
            ["kubectl", "create", "--dry-run=server", "--as", user, "-n", ns, "-f", "-"],
            stdin=json.dumps(cm),
            check=False,
        )
        c.check(
            f"{user} may still create a ConfigMap (the rejection is the policy's, not RBAC's)",
            allowed.returncode == 0,
            allowed.stderr.strip() or allowed.stdout.strip(),
        )
        admin = run(["kubectl", "create", "--dry-run=server", "-n", ns, "-f", "-"], stdin=json.dumps(vn), check=False)
        c.info(f"kubectl create VirtualNetwork as the cluster admin: rc={admin.returncode} {admin.stderr.strip()!r}")
        c.check(
            f"policy {name} also rejects the cluster admin (only OSAC controllers write networking CRs)",
            admin.returncode != 0 and VAP_MESSAGE in admin.stderr,
            admin.stderr.strip() or admin.stdout.strip(),
        )
    finally:
        for obj in reversed(rbac):
            run(
                ["kubectl", "delete", obj["kind"].lower(), obj["metadata"]["name"], "-n", ns, "--ignore-not-found"],
                check=False,
            )


def _container_flag(container: dict) -> list[str]:
    found = []
    for env in container.get("env") or []:
        if env.get("name") == "OSAC_PROVISIONING_ENABLED":
            found.append(f"env={env.get('value')}")
    for arg in (container.get("args") or []) + (container.get("command") or []):
        if isinstance(arg, str) and arg.startswith("--networking-provisioning-enabled="):
            found.append("arg=" + arg.split("=", 1)[1])
    return found


def check_provisioning_flags(c: Checks, ns: str, release: str, values: dict) -> None:
    c.section("Provider networking flag on the controllers")
    expected = values.get("global", {}).get("networking", {}).get("provisioningEnabled")
    c.info(f"global.networking.provisioningEnabled (helm get values --all) = {expected!r}")
    if not c.check("global.networking.provisioningEnabled is a boolean", isinstance(expected, bool), repr(expected)):
        return
    want = "true" if expected else "false"
    rendered = [
        m
        for m in manifests_to_json(run(["helm", "get", "manifest", release, "-n", ns]).stdout)
        if m.get("kind") == "Deployment"
    ]
    carriers = {}
    for dep in rendered:
        flags = [f for ctr in dep["spec"]["template"]["spec"]["containers"] for f in _container_flag(ctr)]
        if flags:
            carriers[dep["metadata"]["name"]] = flags
    c.info(f"rendered Deployments with the flag: {carriers}")
    names = list(carriers)
    for role, needle in (
        ("OSAC operator", "operator"),
        ("fulfillment gRPC server", "fulfillment-grpc-server"),
        ("fulfillment controller", "fulfillment-controller"),
    ):
        if role == "OSAC operator":
            match = [n for n in names if "operator" in n and "bare-metal" not in n and "bmf" not in n]
        else:
            match = [n for n in names if needle in n]
        c.check(f"the {role} Deployment is rendered with the flag", len(match) >= 1, f"candidates={names}")
    for name, flags in carriers.items():
        c.check(
            f"rendered Deployment {name} sets the flag to {want}",
            all(f.endswith("=" + want) for f in flags),
            f"{flags}",
        )
        proc = run(["kubectl", "get", "deployment", name, "-n", ns, "-o", "json"], check=False)
        if not c.check(f"Deployment {ns}/{name} exists", proc.returncode == 0, proc.stderr.strip()):
            continue
        live = json.loads(proc.stdout)
        live_flags = [f for ctr in live["spec"]["template"]["spec"]["containers"] for f in _container_flag(ctr)]
        c.check(
            f"live Deployment {name} sets the flag to {want}",
            bool(live_flags) and all(f.endswith("=" + want) for f in live_flags),
            f"{live_flags}",
        )
        status = live.get("status", {})
        c.check(
            f"live Deployment {name} is available",
            int(status.get("availableReplicas") or 0) >= 1 or int(live["spec"].get("replicas", 1)) == 0,
            f"replicas={live['spec'].get('replicas')} available={status.get('availableReplicas')}",
        )


def invert_provisioning_args(args: list[str]) -> list[str]:
    """Return args with every --networking-provisioning-enabled value inverted."""
    out, found = [], False
    for arg in args:
        if arg.startswith(PROVISIONING_FLAG + "="):
            value = arg.split("=", 1)[1].strip().lower()
            if value not in ("true", "false"):
                raise ValueError(f"unexpected value in {arg}")
            arg = f"{PROVISIONING_FLAG}={'false' if value == 'true' else 'true'}"
            found = True
        out.append(arg)
    if not found:
        raise ValueError(f"{PROVISIONING_FLAG}=<bool> not found in {args}")
    return out


def refusal_pod_manifest(deployment: dict, name: str) -> dict:
    """Build a run-to-completion Pod from the Deployment's pod spec (image, env, volumes, command/args)
    with the provisioning flag inverted. The pod does not carry the Deployment's labels, so no
    Service selects it, and it has no probes, so nothing restarts it."""
    spec = json.loads(json.dumps(deployment["spec"]["template"]["spec"]))
    spec["restartPolicy"] = "Never"
    spec.pop("terminationGracePeriodSeconds", None)
    containers = [ctr for ctr in spec["containers"] if any(
        isinstance(a, str) and a.startswith(PROVISIONING_FLAG + "=")
        for field in ("command", "args") for a in ctr.get(field) or []
    )]
    if len(containers) != 1:
        raise ValueError(f"expected one container with {PROVISIONING_FLAG}, found {len(containers)}")
    ctr = containers[0]
    for field in ("command", "args"):
        if any(isinstance(a, str) and a.startswith(PROVISIONING_FLAG + "=") for a in ctr.get(field) or []):
            ctr[field] = invert_provisioning_args(ctr[field])
    for probe in ("livenessProbe", "readinessProbe", "startupProbe"):
        ctr.pop(probe, None)
    spec["containers"] = [ctr]
    return {
        "apiVersion": "v1",
        "kind": "Pod",
        "metadata": {
            "name": name,
            "namespace": deployment["metadata"]["namespace"],
            "labels": {"app.kubernetes.io/name": REFUSAL_POD_PREFIX},
        },
        "spec": spec,
    }


def _deployment_state(ns: str) -> dict:
    dep = json.loads(run(["kubectl", "get", "deployment", GRPC_SERVER_DEPLOYMENT, "-n", ns, "-o", "json"]).stdout)
    containers = dep["spec"]["template"]["spec"]["containers"]
    return {
        "generation": dep["metadata"]["generation"],
        "command": [a for ctr in containers for a in ctr.get("command") or []],
        "args": [a for ctr in containers for a in ctr.get("args") or []],
    }


def _run_refusal_pod(c: Checks, ns: str, dep: dict, attempt: int) -> None:
    name = f"{REFUSAL_POD_PREFIX}-{attempt}-{uuid.uuid4().hex[:6]}"
    pod = refusal_pod_manifest(dep, name)
    ctr = pod["spec"]["containers"][0]
    flag = [a for a in (ctr.get("command") or []) + (ctr.get("args") or []) if a.startswith(PROVISIONING_FLAG)]
    run(["kubectl", "apply", "-f", "-"], stdin=json.dumps(pod))
    c.info(f"started one-shot pod {ns}/{name} with {flag}")
    try:
        deadline = time.monotonic() + REFUSAL_TIMEOUT_SECONDS
        phase = ""
        while time.monotonic() < deadline:
            out = run(["kubectl", "get", "pod", name, "-n", ns, "-o", "jsonpath={.status.phase}"], check=False)
            phase = out.stdout.strip()
            if phase in ("Succeeded", "Failed"):
                break
            time.sleep(3)
        logs = run(["kubectl", "logs", name, "-n", ns], check=False)
        text = logs.stdout + logs.stderr
        code = run(
            ["kubectl", "get", "pod", name, "-n", ns, "-o",
             "jsonpath={.status.containerStatuses[0].state.terminated.exitCode}"],
            check=False,
        ).stdout.strip()
        c.info(f"pod {name}: phase={phase!r} exitCode={code!r}; log tail: {short(text.strip()[-600:], 600)}")
        c.check(
            f"attempt {attempt}: pod with the provisioning flag inverted exits non-zero",
            phase == "Failed" and code not in ("", "0"),
            f"phase={phase!r} exitCode={code!r}",
        )
        c.check(
            f"attempt {attempt}: pod log carries the installation settings refusal",
            PROVISIONING_CHANGED_MESSAGE in text,
            short(text.strip()[-600:], 600),
        )
    finally:
        run(["kubectl", "delete", "pod", name, "-n", ns, "--ignore-not-found", "--wait=false"], check=False)


def check_installation_settings_refusal(c: Checks, ns: str) -> None:
    c.section("Installation settings startup refusal")
    dep = json.loads(run(["kubectl", "get", "deployment", GRPC_SERVER_DEPLOYMENT, "-n", ns, "-o", "json"]).stdout)
    before = _deployment_state(ns)
    c.info(f"Deployment {GRPC_SERVER_DEPLOYMENT} before: {before}")
    # A second refusal proves the first one did not overwrite the stored settings.
    for attempt in (1, 2):
        _run_refusal_pod(c, ns, dep, attempt)
    after = _deployment_state(ns)
    c.check(
        f"Deployment {GRPC_SERVER_DEPLOYMENT} generation, command and args are unchanged",
        before == after,
        f"before={before} after={after}",
    )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--namespace", default="osac")
    parser.add_argument("--release", default="osac")
    args = parser.parse_args()

    c = Checks()
    values = json.loads(
        run(["helm", "get", "values", args.release, "-n", args.namespace, "--all", "-o", "json"]).stdout
    )
    c.info(f"release {args.release}: global.networking={short(values.get('global', {}).get('networking'), 2000)}")
    steps = (
        lambda: check_network_class(c, args.namespace, args.release, values),
        lambda: check_admission_policy(c, args.namespace),
        lambda: check_provisioning_flags(c, args.namespace, args.release, values),
        lambda: check_installation_settings_refusal(c, args.namespace),
    )
    for step in steps:
        try:
            step()
        except (RuntimeError, ValueError, KeyError, subprocess.TimeoutExpired) as err:
            c.check("check ran without an error", False, f"{type(err).__name__}: {err}")

    print(f"\n=== {c.passed} passed, {len(c.failures)} failed ===")
    for failure in c.failures:
        print(f"  FAIL {failure}")
    return 1 if c.failures else 0


if __name__ == "__main__":
    sys.exit(main())
