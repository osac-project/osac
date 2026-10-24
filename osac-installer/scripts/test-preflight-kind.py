#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["jsonschema==4.23.0", "pyyaml==6.0.2"]
# ///
"""Install preflight against a real API server: fault injection, install,
upgrade and hook-log checks with the Helm on PATH.

The osac chart cannot fully install on a bare Kind cluster, so the test builds
a preflight-only copy of the real chart: the real engine, catalog and report
templates and the real hook annotations, without the workloads. The
prerequisites it checks are stand-ins created here (stub CRDs, a Service with
an EndpointSlice, Secrets), so the checks run their real `lookup` calls
against real API objects.

It changes cluster-scoped state (CRDs, the default StorageClass annotation,
node cordon) and restores it afterwards. It therefore refuses to run on any
context that is not a Kind cluster.

Run: uv run --script scripts/test-preflight-kind.py [--context kind-<name>]
"""

from __future__ import annotations

import argparse
import json
import pathlib
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import uuid

import jsonschema
import yaml

INSTALLER = pathlib.Path(__file__).resolve().parents[1]
CHART = INSTALLER / "charts/osac"
SCHEMA = json.loads((CHART / "files/preflight/report.schema.json").read_text(encoding="utf-8"))
VALIDATOR = jsonschema.Draft202012Validator(SCHEMA, format_checker=jsonschema.FormatChecker())
JSON_LINE = re.compile(r"(?m)^OSAC-PREFLIGHT-JSON v1: (\{.*\})$")
RELEASE = "osac"
MARKER = "OSAC-HOOK-LOG-MARKER"
FAST_FAIL_SECONDS = 60


class Cluster:
    """The Kind cluster under test and the helpers that talk to it."""

    def __init__(self, context: str) -> None:
        self.context = context
        self.ns = f"osac-pf-{uuid.uuid4().hex[:6]}"
        self.work = pathlib.Path(tempfile.mkdtemp(prefix="preflight-kind-"))
        self.chart = self.work / "osac"
        self.restore: list[tuple[str, ...]] = []
        self.extra_ns: list[str] = []  # namespaces created by cases that need their own release

    def run(self, *cmd: str, check: bool = True, stdin: str | None = None) -> subprocess.CompletedProcess[str]:
        proc = subprocess.run(cmd, capture_output=True, text=True, input=stdin)
        if check and proc.returncode != 0:
            raise AssertionError(f"{' '.join(cmd)} failed ({proc.returncode}):\n{proc.stdout}\n{proc.stderr}")
        return proc

    def kubectl(self, *args: str, check: bool = True, stdin: str | None = None) -> subprocess.CompletedProcess[str]:
        return self.run("kubectl", "--context", self.context, *args, check=check, stdin=stdin)

    def apply(self, manifest: str) -> None:
        self.kubectl("apply", "-f", "-", stdin=manifest)

    def helm(self, *args: str, check: bool = False) -> subprocess.CompletedProcess[str]:
        return self.run("helm", "--kube-context", self.context, *args, check=check)

    # -- the chart under test ------------------------------------------------

    def build_chart(self) -> None:
        """A copy of the real chart with only the preflight templates."""
        shutil.copytree(CHART, self.chart, ignore=shutil.ignore_patterns("charts", "tests", "ci", "Chart.lock"))
        chart_yaml = yaml.safe_load((self.chart / "Chart.yaml").read_text(encoding="utf-8"))
        chart_yaml.pop("dependencies", None)
        (self.chart / "Chart.yaml").write_text(yaml.safe_dump(chart_yaml), encoding="utf-8")
        keep = {"_helpers.tpl", "_networking.tpl", "validate-values.yaml", "NOTES.txt", "preflight"}
        for entry in (self.chart / "templates").iterdir():
            if entry.name not in keep:
                shutil.rmtree(entry) if entry.is_dir() else entry.unlink()
        values = yaml.safe_load((CHART / "ci/default-values.yaml").read_text(encoding="utf-8"))
        # The operator subchart supplies this default in the full chart.
        values["operator"] = {"networkManagers": {"enabled": True}}
        (self.work / "values.yaml").write_text(yaml.safe_dump(values), encoding="utf-8")

    def install(
        self, *extra: str, action: str = "install", ns: str | None = None, chart: pathlib.Path | str | None = None
    ) -> subprocess.CompletedProcess[str]:
        args = [action, RELEASE, str(chart or self.chart), "-n", ns or self.ns, "-f", str(self.work / "values.yaml"), "--timeout", "2m"]
        if action == "upgrade":
            args.append("--install")
        return self.helm(*args, *extra)

    def fresh_namespace(self) -> str:
        """A namespace with the client-side prerequisites, for a case that needs its own release."""
        ns = f"{self.ns}-{len(self.extra_ns) + 1}"
        self.extra_ns.append(ns)
        self.apply(f"apiVersion: v1\nkind: Namespace\nmetadata: {{name: {ns}}}\n")
        create_namespace_prerequisites(self, ns)
        return ns

    # -- cluster state -----------------------------------------------------

    def releases(self, ns: str | None = None) -> list[dict]:
        # `helm list --all` is Helm 3 only; naming every state works on Helm 3 and 4.
        states = ("--deployed", "--failed", "--pending", "--superseded", "--uninstalled")
        out = self.helm("list", *states, "-n", ns or self.ns, "-o", "json", check=True).stdout
        return json.loads(out or "[]")

    def report_configmap(self, ns: str | None = None) -> dict | None:
        proc = self.kubectl("get", "configmap", f"{RELEASE}-preflight-report", "-n", ns or self.ns, "-o", "json", check=False)
        if proc.returncode != 0:
            return None
        return json.loads(json.loads(proc.stdout)["data"]["render.json"])


def result(report: dict, check_id: str) -> dict:
    found = [r for r in report["results"] if r["id"] == check_id]
    assert len(found) == 1, f"{check_id}: {len(found)} results"
    return found[0]


def failure_report(proc: subprocess.CompletedProcess[str]) -> dict:
    """The report in the stderr line of a failed render; validated against the schema."""
    assert proc.returncode != 0, "expected the render to fail\n" + proc.stdout
    lines = JSON_LINE.findall(proc.stderr)
    assert len(lines) == 1, f"expected one OSAC-PREFLIGHT-JSON line, found {len(lines)}:\n{proc.stderr[:2000]}"
    report = json.loads(lines[0])
    VALIDATOR.validate(report)
    return report


def blocking_ids(report: dict) -> set[str]:
    return {r["id"] for r in report["results"] if r["blocking"]}


# ----------------------------------------------------------------- fixtures

CRD = """
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {{name: {plural}.{group}}}
spec:
  group: {group}
  scope: {scope}
  names: {{kind: {kind}, plural: {plural}, singular: {singular}}}
  versions:
  - name: {version}
    served: true
    storage: true
    subresources: {{status: {{}}}}
    schema:
      openAPIV3Schema: {{type: object, x-kubernetes-preserve-unknown-fields: true}}
"""
STUB_CRDS = [
    dict(group="cert-manager.io", version="v1", kind="Certificate", plural="certificates", singular="certificate", scope="Namespaced"),
    dict(group="cert-manager.io", version="v1", kind="ClusterIssuer", plural="clusterissuers", singular="clusterissuer", scope="Cluster"),
    dict(group="kafka.strimzi.io", version="v1", kind="Kafka", plural="kafkas", singular="kafka", scope="Namespaced"),
    dict(group="kubevirt.io", version="v1", kind="KubeVirt", plural="kubevirts", singular="kubevirt", scope="Namespaced"),
    dict(group="multicluster.openshift.io", version="v1", kind="MultiClusterEngine", plural="multiclusterengines", singular="multiclusterengine", scope="Cluster"),
]
CERT_MANAGER_CRD = "certificates.cert-manager.io"


def create_prerequisites(c: Cluster) -> None:
    """Stand-ins for what a client prepares before `helm install`."""
    for crd in STUB_CRDS:
        c.apply(CRD.format(**crd))
    for name in [f"{crd['plural']}.{crd['group']}" for crd in STUB_CRDS]:
        c.kubectl("wait", "--for=condition=Established", f"crd/{name}", "--timeout=60s")
    ready = '{"status":{"conditions":[{"type":"Ready","status":"True"}]}}'
    c.apply("apiVersion: cert-manager.io/v1\nkind: ClusterIssuer\nmetadata: {name: default-ca}\nspec: {}\n")
    c.kubectl("patch", "clusterissuer", "default-ca", "--subresource=status", "--type=merge", "-p", ready)
    c.apply("apiVersion: v1\nkind: Namespace\nmetadata: {name: osac-kafka}\n")
    c.apply("apiVersion: kafka.strimzi.io/v1\nkind: Kafka\nmetadata: {name: osac-kafka, namespace: osac-kafka}\nspec: {}\n")
    c.kubectl("patch", "kafka", "osac-kafka", "-n", "osac-kafka", "--subresource=status", "--type=merge", "-p", ready)
    c.apply("apiVersion: v1\nkind: Namespace\nmetadata: {name: osac-infra}\n")
    c.apply(
        "apiVersion: v1\nkind: Service\nmetadata: {name: postgres, namespace: osac-infra}\n"
        "spec: {ports: [{port: 5432}]}\n---\n"
        "apiVersion: discovery.k8s.io/v1\nkind: EndpointSlice\n"
        "metadata:\n  name: postgres-stub\n  namespace: osac-infra\n  labels: {kubernetes.io/service-name: postgres}\n"
        "addressType: IPv4\nports: [{port: 5432}]\nendpoints: [{addresses: ['10.244.0.99'], conditions: {ready: true}}]\n"
    )
    c.apply(f"apiVersion: v1\nkind: Namespace\nmetadata: {{name: {c.ns}}}\n")
    create_namespace_prerequisites(c)


def create_namespace_prerequisites(c: Cluster, ns: str | None = None) -> None:
    ns = ns or c.ns
    c.apply(
        f"apiVersion: v1\nkind: ConfigMap\nmetadata: {{name: ca-bundle, namespace: {ns}}}\ndata: {{bundle.pem: stub}}\n---\n"
        f"apiVersion: v1\nkind: Secret\nmetadata: {{name: fulfillment-controller-credentials, namespace: {ns}}}\n"
        "stringData: {client-id: id, client-secret: secret}\n---\n"
        f"apiVersion: v1\nkind: Secret\nmetadata: {{name: vault-credentials, namespace: {ns}}}\n"
        "stringData: {client-secret: secret}\n"
    )


def cleanup(c: Cluster) -> None:
    for args in reversed(c.restore):
        c.kubectl(*args, check=False)
    c.helm("uninstall", RELEASE, "-n", c.ns, "--no-hooks")
    # Wait, so that a run right after this one does not meet a terminating namespace.
    c.kubectl("delete", "namespace", c.ns, *c.extra_ns, "osac-kafka", "osac-infra", "--ignore-not-found", "--timeout=180s", check=False)
    for crd in STUB_CRDS:
        c.kubectl("delete", "crd", f"{crd['plural']}.{crd['group']}", "--ignore-not-found", "--timeout=120s", check=False)
    shutil.rmtree(c.work, ignore_errors=True)


# ----------------------------------------------------------- fault injection


def default_storageclass(c: Cluster) -> str:
    out = c.kubectl("get", "storageclass", "-o", "json").stdout
    for item in json.loads(out)["items"]:
        if item["metadata"].get("annotations", {}).get("storageclass.kubernetes.io/is-default-class") == "true":
            return item["metadata"]["name"]
    raise AssertionError("the Kind cluster has no default StorageClass")


def set_default_storageclass(c: Cluster, name: str, value: str | None) -> None:
    ann = "storageclass.kubernetes.io/is-default-class"
    c.kubectl("annotate", "storageclass", name, f"{ann}={value}" if value else f"{ann}-", "--overwrite")


# -------------------------------------------------------------------- cases


def case_healthy_install_publishes_report(c: Cluster) -> None:
    proc = c.install("--wait")
    assert proc.returncode == 0, proc.stderr
    report = c.report_configmap()
    assert report is not None, "the report ConfigMap is not kept after the install"
    VALIDATOR.validate(report)
    assert report["summary"]["blocking"] == 0 and report["summary"]["fail"] == 0, report["summary"]
    assert report["release"] == {"name": RELEASE, "namespace": c.ns, "revision": 1, "action": "install"}, report["release"]
    assert report["kubeVersion"], "an online render must report the cluster version"
    assert result(report, "ocp-version")["skipReason"] == "not OpenShift"
    assert result(report, "cert-issuer-ready")["status"] == "pass"
    assert result(report, "db-init-host-endpoints")["status"] == "pass"
    assert result(report, "kafka-access")["status"] == "pass"
    assert [r["status"] for r in c.releases()] == ["deployed"]
    # The hook ConfigMap is a hook resource: uninstall leaves it behind (documented).
    labels = c.kubectl("get", "configmap", f"{RELEASE}-preflight-report", "-n", c.ns, "-o", "jsonpath={.metadata.labels}").stdout
    assert "osac.openshift.io/preflight-report" in labels


def case_upgrade_with_unchanged_prerequisites(c: Cluster) -> None:
    proc = c.install("--wait", action="upgrade")
    assert proc.returncode == 0, proc.stderr
    report = c.report_configmap()
    assert report is not None
    assert report["release"]["revision"] == 2 and report["release"]["action"] == "upgrade", report["release"]
    assert report["summary"]["blocking"] == 0, report["summary"]


def case_faulted_upgrade_keeps_previous_revision(c: Cluster) -> None:
    before = c.report_configmap()
    c.kubectl("delete", "secret", "fulfillment-controller-credentials", "-n", c.ns)
    try:
        proc = c.install(action="upgrade")
        failed = failure_report(proc)
    finally:
        create_namespace_prerequisites(c)
    assert "controller-credentials" in blocking_ids(failed), blocking_ids(failed)
    assert failed["release"]["revision"] == 3 and failed["release"]["action"] == "upgrade", failed["release"]
    deployed = c.releases()
    assert [(r["revision"], r["status"]) for r in deployed] == [("2", "deployed")], deployed
    # The ConfigMap reflects the last render that passed; the failed attempt is in stderr only.
    assert c.report_configmap() == before, "a failed render must leave the last passing report untouched"


def case_upgrade_downgrades_cluster_state_to_warnings(c: Cluster) -> None:
    sc = default_storageclass(c)
    set_default_storageclass(c, sc, None)
    c.restore.append(("annotate", "storageclass", sc, "storageclass.kubernetes.io/is-default-class=true", "--overwrite"))
    try:
        proc = c.install("--wait", action="upgrade")
        assert proc.returncode == 0, "a missing default StorageClass must not break a working release\n" + proc.stderr
        report = c.report_configmap()
        assert report is not None
        assert result(report, "default-storageclass")["status"] == "warn"
        assert "Preflight warnings (the install went ahead)" in proc.stdout, proc.stdout
    finally:
        set_default_storageclass(c, sc, "true")
        c.restore.pop()


def case_fresh_install_lists_every_fault_once(c: Cluster) -> None:
    """Four independent faults on a fresh namespace: one run, all four, fast, nothing created."""
    c.helm("uninstall", RELEASE, "-n", c.ns, "--no-hooks")
    c.kubectl("delete", "configmap", f"{RELEASE}-preflight-report", "-n", c.ns, "--ignore-not-found")
    sc = default_storageclass(c)
    node = c.kubectl("get", "nodes", "-o", "jsonpath={.items[0].metadata.name}").stdout
    set_default_storageclass(c, sc, None)
    c.kubectl("cordon", node)
    c.kubectl("delete", "secret", "fulfillment-controller-credentials", "-n", c.ns)
    c.kubectl("delete", "crd", CERT_MANAGER_CRD)
    try:
        started = time.monotonic()
        proc = c.install("--wait")
        elapsed = time.monotonic() - started
        report = failure_report(proc)
    finally:
        c.kubectl("uncordon", node)
        set_default_storageclass(c, sc, "true")
        create_namespace_prerequisites(c)
        c.apply(CRD.format(**STUB_CRDS[0]))
        c.kubectl("wait", "--for=condition=Established", f"crd/{CERT_MANAGER_CRD}", "--timeout=60s")
    assert elapsed < FAST_FAIL_SECONDS, f"the preflight took {elapsed:.0f}s"
    assert blocking_ids(report) == {"default-storageclass", "nodes-schedulable", "controller-credentials", "cert-manager-crds"}, blocking_ids(report)
    issuer = result(report, "cert-issuer-ready")
    assert issuer["status"] == "unknown" and issuer["dependsOn"] == ["cert-manager-crds"], issuer
    assert "Nothing was changed" in proc.stderr
    for fix in ("oc patch storageclass", "Uncordon a node", "Install the cert-manager Operator", "Create the controller credential Secret"):
        assert fix in proc.stderr, f"missing fix text: {fix}"
    assert c.releases() == [], "a failed render must not leave a release behind"
    assert c.report_configmap() is None, "a failed render must not create the report"


def hook_report(stdout: str) -> dict:
    """The report in the hooks[] of `helm install --dry-run=server -o json`."""
    hooks = [h for h in json.loads(stdout)["hooks"] if h["name"].endswith("-preflight-report")]
    assert len(hooks) == 1, f"expected one report hook, found {len(hooks)}"
    report = json.loads(yaml.safe_load(hooks[0]["manifest"])["data"]["render.json"])
    VALIDATOR.validate(report)
    return report


def case_report_mode_recipe(c: Cluster) -> None:
    """The read-only report for a tool: nothing installed, same answer on Helm 3 and 4."""
    c.kubectl("delete", "secret", "vault-credentials", "-n", c.ns)
    try:
        values = ["-f", str(c.work / "values.yaml"), "--set", "preflight.mode=report"]
        proc = c.helm("install", RELEASE, str(c.chart), "-n", c.ns, *values, "--dry-run=server", "-o", "json")
        assert proc.returncode == 0, proc.stderr
        report = hook_report(proc.stdout)
        assert report["mode"] == "report"
        # Exactly the fault that was injected: no check may fail for a prepared cluster.
        assert blocking_ids(report) == {"vault-config"}, blocking_ids(report)
        major = c.run("helm", "version", "--template", "{{.Version}}").stdout.lstrip("v").split(".")[0]
        if major == "3":
            # `helm template --dry-run=server` enables lookup but leaves .Capabilities at their
            # defaults; the engine must give the same answer there (an Established CRD counts).
            t = c.helm("template", RELEASE, str(c.chart), "-n", c.ns, *values, "--dry-run=server", "-s", "templates/preflight/report.yaml")
            assert t.returncode == 0, t.stderr
            doc = yaml.safe_load(t.stdout.split("---", 1)[1])
            assert blocking_ids(json.loads(doc["data"]["render.json"])) == {"vault-config"}
    finally:
        create_namespace_prerequisites(c)


def case_missing_namespace_is_one_root_cause(c: Cluster) -> None:
    ghost = f"{c.ns}-missing"
    proc = c.helm(
        "install", RELEASE, str(c.chart), "-n", ghost, "--create-namespace", "-f", str(c.work / "values.yaml"), "--timeout", "2m"
    )
    report = failure_report(proc)
    assert blocking_ids(report) == {"install-namespace-active"}, blocking_ids(report)
    for dep in ("ca-bundle-present", "controller-credentials", "vault-config"):
        r = result(report, dep)
        assert r["status"] == "unknown" and r["dependsOn"] == ["install-namespace-active"], (dep, r)
    assert c.kubectl("get", "namespace", ghost, check=False).returncode != 0, "a failed render must not create the namespace"


def case_database_url_is_redacted(c: Cluster) -> None:
    password = f"pw-{uuid.uuid4().hex}"  # generated per run: no credential-shaped literal in the repo
    c.apply(
        f"apiVersion: v1\nkind: Secret\nmetadata: {{name: my-db, namespace: {c.ns}}}\n"
        f"stringData: {{url: 'postgres://svc:{password}@db.example.com:5432/osac?sslmode=require'}}\n"
    )
    conn = ["--set", "service.database.connection[0].secret.name=my-db",
            "--set", "service.database.connection[0].secret.items[0].key=url",
            "--set", "service.database.connection[0].secret.items[0].param=url"]
    ok = c.install("--wait", *conn, action="upgrade")
    assert ok.returncode == 0, ok.stderr
    report = c.report_configmap()
    assert report is not None
    assert result(report, "db-secrets")["observed"]["secrets"][0]["url"] == {
        "scheme": "postgres", "host": "db.example.com", "port": "5432", "database": "osac"
    }
    c.kubectl("delete", "secret", "fulfillment-controller-credentials", "-n", c.ns)
    try:
        failed = c.install(*conn, action="upgrade")
        failure_report(failed)
    finally:
        create_namespace_prerequisites(c)
    everything = "\n".join(
        [ok.stdout, ok.stderr, failed.stdout, failed.stderr, json.dumps(c.report_configmap())]
    )
    assert password not in everything, "the database password reached the output or the report"


def case_failed_hook_prints_its_log(c: Cluster) -> None:
    """A failing hook Job prints its pod log; proven with the real db-init Job's annotations."""
    rendered = subprocess.run(
        ["helm", "template", RELEASE, str(CHART), "-n", c.ns, "-f", str(CHART / "ci/default-values.yaml"),
         "-s", "templates/hooks/db-init.yaml"],
        capture_output=True, text=True, check=True,
    ).stdout
    job = next(d for d in yaml.safe_load_all(rendered) if d and d["kind"] == "Job")
    assert job["metadata"]["annotations"]["helm.sh/hook-output-log-policy"] == "hook-failed"

    def hook_chart(with_policy: bool) -> pathlib.Path:
        j = json.loads(json.dumps(job))
        spec = j["spec"]["template"]["spec"]
        spec.pop("initContainers", None)
        spec.pop("volumes", None)
        spec.pop("serviceAccountName", None)
        spec.pop("securityContext", None)
        spec["containers"] = [{
            "name": "fail", "image": "quay.io/quay/busybox:latest",
            "command": ["sh", "-c", f"echo {MARKER}; exit 1"],
        }]
        j["spec"]["backoffLimit"] = 0
        j["metadata"]["namespace"] = c.ns
        if not with_policy:
            del j["metadata"]["annotations"]["helm.sh/hook-output-log-policy"]
        d = c.work / f"hook-{'with' if with_policy else 'without'}"
        (d / "templates").mkdir(parents=True)
        (d / "Chart.yaml").write_text("apiVersion: v2\nname: hookprobe\nversion: 0.0.1\n", encoding="utf-8")
        (d / "templates/job.yaml").write_text(yaml.safe_dump(j), encoding="utf-8")
        return d

    for with_policy in (False, True):
        proc = c.helm("install", f"hookprobe-{int(with_policy)}", str(hook_chart(with_policy)), "-n", c.ns, "--wait", "--timeout", "3m")
        assert proc.returncode != 0
        printed = MARKER in proc.stdout + proc.stderr
        assert printed == with_policy, f"hook log printed={printed} with policy={with_policy}\n{proc.stderr}"
        c.helm("uninstall", f"hookprobe-{int(with_policy)}", "-n", c.ns, "--no-hooks")


def limited_kubeconfig(c: Cluster, ns: str) -> pathlib.Path:
    """A kubeconfig whose user manages ConfigMaps, Secrets and ServiceAccounts in one namespace and nothing else."""
    name = "limited-installer"
    c.apply(
        f"apiVersion: v1\nkind: ServiceAccount\nmetadata: {{name: {name}, namespace: {ns}}}\n---\n"
        f"apiVersion: rbac.authorization.k8s.io/v1\nkind: Role\nmetadata: {{name: {name}, namespace: {ns}}}\n"
        "rules: [{apiGroups: [''], resources: [configmaps, secrets, serviceaccounts], verbs: ['*']}]\n---\n"
        f"apiVersion: rbac.authorization.k8s.io/v1\nkind: RoleBinding\nmetadata: {{name: {name}, namespace: {ns}}}\n"
        f"roleRef: {{apiGroup: rbac.authorization.k8s.io, kind: Role, name: {name}}}\n"
        f"subjects: [{{kind: ServiceAccount, name: {name}, namespace: {ns}}}]\n"
    )
    token = c.kubectl("create", "token", name, "-n", ns, "--duration=30m").stdout.strip()
    admin = json.loads(c.kubectl("config", "view", "--raw", "--minify", "-o", "json").stdout)
    cluster = admin["clusters"][0]["cluster"]
    path = c.work / "limited.kubeconfig"
    path.write_text(yaml.safe_dump({
        "apiVersion": "v1", "kind": "Config", "current-context": "limited",
        "clusters": [{"name": "kind", "cluster": cluster}],
        "users": [{"name": "limited", "user": {"token": token}}],
        "contexts": [{"name": "limited", "context": {"cluster": "kind", "user": "limited", "namespace": ns}}],
    }), encoding="utf-8")
    return path


def case_limited_permission_installer(c: Cluster) -> None:
    """An installer that cannot read the cluster: validation off must still install; on, it says why it stops."""
    ns = c.fresh_namespace()
    kubeconfig = limited_kubeconfig(c, ns)

    def as_limited(*extra: str) -> subprocess.CompletedProcess[str]:
        return c.run(
            "helm", "--kubeconfig", str(kubeconfig), "install", RELEASE, str(c.chart), "-n", ns,
            "-f", str(c.work / "values.yaml"), "--timeout", "2m", *extra, check=False,
        )

    # The off switch means no cluster reads at all (not even the reachability probe).
    off = as_limited("--set", "validation.enabled=false")
    assert off.returncode == 0, off.stderr
    report = c.report_configmap(ns)
    assert report is not None and {r["status"] for r in report["results"]} == {"skipped"}, report
    c.helm("uninstall", RELEASE, "-n", ns, check=True)
    c.kubectl("delete", "configmap", "-n", ns, "-l", "osac.openshift.io/preflight-report=true")

    # With validation on the checks read nodes, CRDs and StorageClasses. Helm turns a forbidden
    # read into a render error that names the resource and the verb; nothing is installed.
    on = as_limited()
    assert on.returncode != 0, on.stdout
    assert "forbidden" in on.stderr and "cannot get resource" in on.stderr, on.stderr
    assert "OSAC-PREFLIGHT-JSON" not in on.stderr, "a forbidden read is Helm's own error, not a preflight result"
    assert c.releases(ns) == [], "a failed render must not leave a release behind"


def old_release_chart(c: Cluster) -> pathlib.Path:
    """A stand-in for a chart published before the preflight: its own pre-install hook and no preflight values."""
    d = c.work / "old-osac"
    (d / "templates").mkdir(parents=True)
    (d / "Chart.yaml").write_text("apiVersion: v2\nname: osac\nversion: 0.0.17\n", encoding="utf-8")
    # The old release carries the chart's full defaults, as a real one does, minus the preflight values:
    # `--reuse-values` then hands the new chart a values set without them.
    defaults = yaml.safe_load((CHART / "values.yaml").read_text(encoding="utf-8"))
    defaults.pop("preflight")
    (d / "values.yaml").write_text(yaml.safe_dump(defaults), encoding="utf-8")
    (d / "templates/marker.yaml").write_text(
        "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}-marker\ndata: {version: old}\n", encoding="utf-8"
    )
    (d / "templates/validate-hook.yaml").write_text(
        "apiVersion: v1\nkind: ServiceAccount\nmetadata:\n  name: {{ .Release.Name }}-validate\n  annotations:\n"
        '    "helm.sh/hook": pre-install,pre-upgrade\n    "helm.sh/hook-delete-policy": before-hook-creation,hook-succeeded\n',
        encoding="utf-8",
    )
    return d


def case_upgrade_from_a_release_made_before_the_preflight(c: Cluster) -> None:
    """The first upgrade to a chart with the preflight, keeping the old release's values (--reuse-values)."""
    ns = c.fresh_namespace()
    old = c.helm("install", RELEASE, str(old_release_chart(c)), "-n", ns, "-f", str(c.work / "values.yaml"), "--wait")
    assert old.returncode == 0, old.stderr
    assert [(r["revision"], r["status"]) for r in c.releases(ns)] == [("1", "deployed")]

    def upgrade() -> subprocess.CompletedProcess[str]:
        return c.helm("upgrade", RELEASE, str(c.chart), "-n", ns, "--reuse-values", "--timeout", "2m", "--wait")

    c.kubectl("delete", "secret", "fulfillment-controller-credentials", "-n", ns)
    blocked = upgrade()
    report = failure_report(blocked)
    # The old release counts as deployed, so this is an upgrade, and the old release is untouched.
    assert report["release"]["action"] == "upgrade" and report["release"]["revision"] == 2, report["release"]
    assert blocking_ids(report) == {"controller-credentials"}, blocking_ids(report)
    assert [(r["revision"], r["status"]) for r in c.releases(ns)] == [("1", "deployed")]

    create_namespace_prerequisites(c, ns)
    ok = upgrade()
    assert ok.returncode == 0, ok.stderr
    history = json.loads(c.helm("history", RELEASE, "-n", ns, "-o", "json", check=True).stdout)
    assert [(h["revision"], h["status"]) for h in history] == [(1, "superseded"), (2, "deployed")], history
    assert c.report_configmap(ns)["release"] == {"name": RELEASE, "namespace": ns, "revision": 2, "action": "upgrade"}
    # Nothing of the old release is left: its hook ServiceAccount was deleted on success, and its
    # ConfigMap is not part of the new chart.
    for gone in (f"configmap/{RELEASE}-marker", f"serviceaccount/{RELEASE}-validate"):
        assert c.kubectl("get", gone, "-n", ns, check=False).returncode != 0, f"{gone} was left behind"


PACKAGED_FILES = (
    "osac/files/preflight/catalog.yaml",
    "osac/files/preflight/report.schema.json",
    "osac/templates/preflight/_engine.tpl",
    "osac/templates/preflight/report.yaml",
)


def case_install_from_the_chart_archive(c: Cluster) -> None:
    """Clients get the chart as a package, not a directory: the gate must be inside the archive and work from it."""
    out = c.work / "package"
    out.mkdir()
    c.run("helm", "package", str(c.chart), "-d", str(out))
    archive = next(out.glob("*.tgz"))
    with tarfile.open(archive) as tar:
        names = set(tar.getnames())
    missing = [f for f in PACKAGED_FILES if f not in names]
    assert not missing, f"missing from the chart archive: {missing}"

    ns = c.fresh_namespace()
    c.kubectl("delete", "secret", "fulfillment-controller-credentials", "-n", ns)
    blocked = c.install(ns=ns, chart=archive)
    assert blocking_ids(failure_report(blocked)) == {"controller-credentials"}
    create_namespace_prerequisites(c, ns)
    ok = c.install("--wait", ns=ns, chart=archive)
    assert ok.returncode == 0, ok.stderr
    report = c.report_configmap(ns)
    assert report is not None and report["summary"]["blocking"] == 0, report


def case_report_survives_uninstall_until_removed(c: Cluster) -> None:
    """The report is a hook resource. Uninstall leaves it (the guide says so); the guide's command removes it."""
    ns = c.fresh_namespace()
    assert c.install("--wait", ns=ns).returncode == 0
    first = c.report_configmap(ns)
    assert first is not None
    c.helm("uninstall", RELEASE, "-n", ns, check=True)
    assert c.report_configmap(ns) == first, "the report is expected to outlive the release"
    # Reinstalling next to the leftover must work and replace it.
    again = c.install("--wait", ns=ns)
    assert again.returncode == 0, again.stderr
    second = c.report_configmap(ns)
    assert second is not None and second["release"]["revision"] == 1
    c.helm("uninstall", RELEASE, "-n", ns, check=True)
    # The command from the uninstall section of the customer guide.
    c.kubectl("delete", "configmap", "-n", ns, "-l", "osac.openshift.io/preflight-report=true")
    assert c.report_configmap(ns) is None


def case_helm_flags_do_not_change_the_gate(c: Cluster) -> None:
    """The gate runs at render time, so --no-hooks, --atomic and rollback must not bypass or break it."""
    ns = c.fresh_namespace()
    c.kubectl("delete", "secret", "fulfillment-controller-credentials", "-n", ns)
    blocked = c.install("--no-hooks", ns=ns)
    assert blocking_ids(failure_report(blocked)) == {"controller-credentials"}, "--no-hooks must not skip the gate"
    create_namespace_prerequisites(c, ns)

    ok = c.install("--no-hooks", "--wait", ns=ns)
    assert ok.returncode == 0, ok.stderr
    assert c.report_configmap(ns) is None, "--no-hooks skips the report hook, so no report is written"

    c.kubectl("delete", "secret", "fulfillment-controller-credentials", "-n", ns)
    atomic = c.install("--atomic", action="upgrade", ns=ns)
    assert blocking_ids(failure_report(atomic)) == {"controller-credentials"}
    assert [(r["revision"], r["status"]) for r in c.releases(ns)] == [("1", "deployed")], "--atomic must not add a rollback revision"

    create_namespace_prerequisites(c, ns)
    assert c.install("--wait", action="upgrade", ns=ns).returncode == 0
    c.kubectl("delete", "secret", "fulfillment-controller-credentials", "-n", ns)
    try:
        rollback = c.helm("rollback", RELEASE, "1", "-n", ns, "--wait", "--timeout", "2m")
        assert rollback.returncode == 0, "a rollback does not render, so the gate does not run\n" + rollback.stderr
        assert [(r["revision"], r["status"]) for r in c.releases(ns)][0] == ("3", "deployed")
    finally:
        create_namespace_prerequisites(c, ns)


CASES = [
    case_healthy_install_publishes_report,
    case_upgrade_with_unchanged_prerequisites,
    case_faulted_upgrade_keeps_previous_revision,
    case_upgrade_downgrades_cluster_state_to_warnings,
    case_database_url_is_redacted,
    case_missing_namespace_is_one_root_cause,
    case_report_mode_recipe,
    case_fresh_install_lists_every_fault_once,
    case_failed_hook_prints_its_log,
    case_limited_permission_installer,
    case_upgrade_from_a_release_made_before_the_preflight,
    case_install_from_the_chart_archive,
    case_report_survives_uninstall_until_removed,
    case_helm_flags_do_not_change_the_gate,
]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--context", help="kubeconfig context of a Kind cluster (default: the current context)")
    parser.add_argument("--only", action="append", default=[], help="run only the cases whose name contains this text (repeatable)")
    args = parser.parse_args()
    context = args.context or subprocess.run(
        ["kubectl", "config", "current-context"], capture_output=True, text=True, check=True
    ).stdout.strip()
    if not context.startswith("kind-"):
        print(f"refusing to run on context {context!r}: this test changes cluster-scoped state and needs a Kind cluster")
        return 2
    c = Cluster(context)
    helm_version = c.run("helm", "version", "--short").stdout.strip()
    print(f"preflight kind test: context={context} namespace={c.ns} helm={helm_version}")
    selected = [k for k in CASES if not args.only or any(o in k.__name__ for o in args.only)]
    failures = 0
    try:
        c.build_chart()
        create_prerequisites(c)
        for case in selected:
            started = time.monotonic()
            try:
                case(c)
                print(f"PASS {case.__name__} ({time.monotonic() - started:.0f}s)")
            except Exception as exc:  # noqa: BLE001 - report every case, then fail
                failures += 1
                print(f"FAIL {case.__name__}: {exc}")
    finally:
        cleanup(c)
    print(f"{len(selected) - failures}/{len(selected)} cases passed")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
