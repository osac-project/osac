#!/usr/bin/env python3
"""Sync config/rbac and config/manager manifests into charts/operator/templates for Helm.

Each source file is read as raw text, then targeted substitutions are applied to replace
kustomize-specific values (hardcoded names, namespaces, labels) with Helm template
expressions. The YAML structure itself passes through unchanged, so upstream additions
to the config files are automatically reflected in the Helm chart.
"""

from __future__ import annotations

import re
import sys
import textwrap
from pathlib import Path

RBAC_DIR = Path("config/rbac")
MANAGER_DIR = Path("config/manager")
DEFAULT_DIR = Path("config/default")
TEMPLATES_DIR = Path("charts/operator/templates")

CHART = "bare-metal-fulfillment-operator"
FULLNAME = f'{{{{ include "{CHART}.fullname" . }}}}'
SA_NAME = f'{{{{ include "{CHART}.serviceAccountName" . }}}}'
LABELS_4 = f'{{{{- include "{CHART}.labels" . | nindent 4 }}}}'
SEL_6 = f'{{{{- include "{CHART}.selectorLabels" . | nindent 6 }}}}'
SEL_8 = f'{{{{- include "{CHART}.selectorLabels" . | nindent 8 }}}}'
SEL_4 = f'{{{{- include "{CHART}.selectorLabels" . | nindent 4 }}}}'
NS = "{{ .Release.Namespace }}"

# Maps every kustomize resource name that appears in config/ to its Helm equivalent.
# Names are mapped 1-to-1: the kustomize name is preserved as a suffix after the
# Helm fullname. Add an entry here whenever controller-gen produces a new name.
NAME_MAP: dict[str, str] = {
    # roles
    "manager-role":                      f"{FULLNAME}-manager-role",
    "leader-election-role":              f"{FULLNAME}-leader-election-role",
    "metrics-auth-role":                 f"{FULLNAME}-metrics-auth-role",
    "metrics-reader":                    f"{FULLNAME}-metrics-reader",
    "baremetalpool-editor-role":         f"{FULLNAME}-baremetalpool-editor-role",
    "baremetalpool-viewer-role":         f"{FULLNAME}-baremetalpool-viewer-role",
    "baremetalpool-admin-role":          f"{FULLNAME}-baremetalpool-admin-role",
    # bindings
    "manager-rolebinding":               f"{FULLNAME}-manager-rolebinding",
    "leader-election-rolebinding":       f"{FULLNAME}-leader-election-rolebinding",
    "metrics-auth-rolebinding":          f"{FULLNAME}-metrics-auth-rolebinding",
    # service
    "controller-manager-metrics-service": f"{FULLNAME}-controller-manager-metrics-service",
}

# Maps kustomize secret/configmap names to Helm values expressions in the Deployment.
SECRET_TO_VALUES: dict[str, str] = {
    "osac-inventory-config": "{{ .Values.secrets.inventoryConfig }}",
    "osac-management-config": "{{ .Values.secrets.managementConfig }}",
    "osac-os-clouds": "{{ .Values.secrets.osClouds }}",
}
CONFIGMAP_TO_VALUES: dict[str, str] = {
    "osac-profiles": "{{ .Values.configMaps.profiles }}",
}

# Environment variables for values that are environment-specific.
# Only include variables that are expected to be changed at deploy time.
# All other configuration uses reasonable defaults in the Go code.
# AAP vars are conditional so the pod starts without AAP configured —
# prepare-aap.sh patches them in post-install via `oc set env`.
_ENV_BLOCK = """\
          {{- if .Values.env.aapUrl }}
          - name: OSAC_AAP_URL
            value: {{ .Values.env.aapUrl | quote }}
          {{- end }}
          {{- if .Values.env.aapTokenSecretName }}
          - name: OSAC_AAP_TOKEN
            valueFrom:
              secretKeyRef:
                name: {{ .Values.env.aapTokenSecretName | quote }}
                key: {{ .Values.env.aapTokenSecretKey | default "token" | quote }}
                optional: true
          {{- end }}
          {{- if .Values.env.aapInsecureSkipVerify }}
          - name: OSAC_AAP_INSECURE_SKIP_VERIFY
            value: {{ .Values.env.aapInsecureSkipVerify | quote }}
          {{- end }}
"""

# Matches a kustomize-generated labels block at exactly 2-space metadata indent.
# Anchored to line-start (MULTILINE) so it doesn't accidentally match pod-template
# labels at deeper indentation levels.
_KUSTOMIZE_LABELS_RE = re.compile(r"^  labels:\n(?:^    [^\n]+\n)+", re.MULTILINE)

# Label key prefixes that are injected by kustomize and should be replaced by the Helm
# labels helper. Any other labels (e.g. rbac.authorization.k8s.io/aggregate-to-*)
# are preserved verbatim after the Helm include.
_KUSTOMIZE_LABEL_PREFIXES = ("app.kubernetes.io/",)


def _is_kustomize_label(line: str) -> bool:
    return any(line.strip().startswith(p) for p in _KUSTOMIZE_LABEL_PREFIXES)


def replace_names(content: str) -> str:
    """Replace every known kustomize resource name with its Helm equivalent.

    Sorted longest-first so that 'manager-rolebinding' is replaced before its
    prefix 'manager-role', preventing partial-match corruption.
    """
    for kustomize_name, helm_name in sorted(NAME_MAP.items(), key=lambda x: -len(x[0])):
        content = content.replace(f"name: {kustomize_name}", f"name: {helm_name}")
    return content


def replace_labels(content: str) -> str:
    """Replace the kustomize labels block with the Helm labels helper.

    Non-kustomize labels (e.g. rbac.authorization.k8s.io/aggregate-to-*) are
    preserved after the Helm include so they survive repeated sync runs.

    If no labels block is present (e.g. metrics roles), inject one after the
    metadata name line so all resources get consistent Helm labels.
    """
    match = _KUSTOMIZE_LABELS_RE.search(content)
    if match:
        extra = "".join(
            line + "\n"
            for line in match.group().splitlines()[1:]  # skip "  labels:" header
            if line and not _is_kustomize_label(line)
        )
        helm_labels_block = f"  labels:\n    {LABELS_4}\n{extra}"
        return _KUSTOMIZE_LABELS_RE.sub(helm_labels_block, content)
    # Inject after the first `  name: ...` line inside metadata.
    helm_labels_block = f"  labels:\n    {LABELS_4}\n"
    return re.sub(
        r"(  name: [^\n]+\n)",
        r"\1" + helm_labels_block,
        content,
        count=1,
    )


def replace_namespace(content: str) -> str:
    """Replace the kustomize placeholder namespace with {{ .Release.Namespace }}."""
    return content.replace("namespace: system", f"namespace: {NS}")


def replace_sa_name(content: str) -> str:
    """Replace the hardcoded service-account name with the Helm SA helper."""
    # Match at any indentation level (subjects list uses 2-space indent).
    return re.sub(r"(\s+name:\s*)controller-manager\b", rf"\g<1>{SA_NAME}", content)


def inject_rolebinding_namespace(content: str) -> str:
    """Inject metadata.namespace for RoleBindings whose source file omits it.

    Kustomize injects namespace at deploy time via kustomization.yaml; Helm
    needs it declared explicitly in the template. The check is scoped to the
    metadata section (before roleRef) so the namespace present in subjects
    doesn't suppress the injection.
    """
    if not re.search(r"^kind: RoleBinding$", content, re.MULTILINE):
        return content
    metadata_section = re.split(r"^roleRef:", content, maxsplit=1, flags=re.MULTILINE)[0]
    if "  namespace:" in metadata_section:
        return content
    # count=1 targets only the first name line, which is always metadata.name.
    return re.sub(
        r"^(  name: [^\n]+\n)",
        rf"\1  namespace: {NS}\n",
        content,
        count=1,
        flags=re.MULTILINE,
    )


def common_transforms(content: str) -> str:
    """Apply all substitutions that are shared across every resource type."""
    content = replace_names(content)
    content = replace_labels(content)
    content = replace_namespace(content)
    content = replace_sa_name(content)
    content = inject_rolebinding_namespace(content)
    return content


def sync_rbac(src: Path, dst: Path) -> None:
    """Transform a single config/rbac file and write it to the templates directory."""
    content = common_transforms(src.read_text())
    dst.write_text(content)


def sync_serviceaccount(src: Path, dst: Path) -> None:
    """Transform the ServiceAccount and wrap it in the Helm create conditional."""
    content = common_transforms(src.read_text()).strip()
    dst.write_text(
        f"{{{{- if .Values.serviceAccount.create }}}}\n"
        f"{content}\n"
        f"{{{{- end }}}}\n"
    )


def sync_aggregate(sources: list[Path], dst: Path) -> None:
    """Combine multiple config/rbac files into a single multi-document template."""
    parts = [common_transforms(src.read_text()).strip() for src in sources]
    dst.write_text("---\n" + "\n---\n".join(parts) + "\n")


def sync_metrics_service(src: Path, dst: Path) -> None:
    """Transform config/default/metrics_service.yaml into a Helm Service template."""
    content = src.read_text()
    content = common_transforms(content)
    # Replace the hardcoded selector labels with the Helm selector helper.
    content = re.sub(
        r"^(  selector:\n)(?:^    [^\n]+\n)+",
        rf"\1    {SEL_4}\n",
        content,
        flags=re.MULTILINE,
    )
    dst.write_text(content)


def sync_deployment(src: Path, dst: Path) -> None:
    """Transform config/manager/manager.yaml into a Helm Deployment template.

    Applies common transforms plus deployment-specific substitutions:
    - Image reference → Values expressions
    - Secret/ConfigMap names → Values expressions
    - Pod/container labels → selector label helpers
    - Resource limits → toYaml helper
    - serviceAccountName → SA helper
    - Strips the leading Namespace document (kept in kustomize, not in Helm)
    """
    content = src.read_text()

    # Drop the Namespace document — Helm manages namespaces separately.
    # The Namespace may be the first document (no leading ---) or separated by ---.
    content = re.sub(
        r"(?:---\n)?apiVersion: v1\nkind: Namespace\n.*?(?=---\n|\Z)",
        "",
        content,
        flags=re.DOTALL,
    )
    content = content.lstrip("-\n")

    # Deployment metadata name — 1-to-1 with the kustomize name.
    content = content.replace("name: controller-manager", f"name: {FULLNAME}-controller-manager")
    content = replace_namespace(content)

    # Replace selector matchLabels and pod template labels with Helm selector helpers
    # BEFORE replace_labels, so the metadata labels block is the only one left for it.
    content = re.sub(
        r"(    matchLabels:\n)(?:      [^\n]+\n)+",
        rf"\1      {SEL_6}\n",
        content,
    )
    content = re.sub(
        r"(      labels:\n)(?:        [^\n]+\n)+",
        rf"\1        {SEL_8}\n",
        content,
    )

    # Now replace only the remaining metadata labels block.
    content = replace_labels(content)

    # Replica count → Values.
    content = re.sub(r"replicas:\s*\d+", "replicas: {{ .Values.replicaCount }}", content)

    # Image → Values.
    content = re.sub(
        r"image:\s*\S+",
        'image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"',
        content,
        count=1,
    )

    # imagePullPolicy → Values (may not exist in source; skip if absent).
    if "imagePullPolicy:" in content:
        content = re.sub(
            r"imagePullPolicy:\s*\S+",
            "imagePullPolicy: {{ .Values.image.pullPolicy }}",
            content,
            count=1,
        )

    # Resources block → toYaml helper.
    content = re.sub(
        r"        resources:\n(?:          [^\n]+\n)+",
        "        resources:\n          {{- toYaml .Values.resources | nindent 10 }}\n",
        content,
    )

    # Secret and ConfigMap volume source names → Values expressions.
    for kustomize_name, helm_expr in SECRET_TO_VALUES.items():
        content = content.replace(f"secretName: {kustomize_name}", f"secretName: {helm_expr}")
    for kustomize_name, helm_expr in CONFIGMAP_TO_VALUES.items():
        content = content.replace(f"name: {kustomize_name}", f"name: {helm_expr}")

    # serviceAccountName → SA helper (distinct from `name:` fields handled by replace_sa_name).
    content = re.sub(
        r"serviceAccountName:\s*\S+",
        f"serviceAccountName: {SA_NAME}",
        content,
    )

    # Inject configurable env vars before volumeMounts.
    anchor = "        volumeMounts:"
    if anchor not in content:
        raise RuntimeError(f"volumeMounts anchor string not found in deployment template. Expected: {anchor!r}")
    content = content.replace(
        anchor,
        _ENV_BLOCK + anchor,
        1,  # Limit to one replacement
    )

    # Add seccompProfile to pod securityContext if not already present.
    if "seccompProfile:" not in content:
        content = content.replace(
            "      securityContext:\n        runAsNonRoot: true\n",
            "      securityContext:\n        runAsNonRoot: true\n        seccompProfile:\n          type: RuntimeDefault\n",
        )

    # Add readOnlyRootFilesystem to container securityContext if not already present.
    if "readOnlyRootFilesystem:" not in content:
        content = content.replace(
            "          allowPrivilegeEscalation: false\n",
            "          allowPrivilegeEscalation: false\n          readOnlyRootFilesystem: true\n",
        )

    # Validate that security hardening settings are present in the final output.
    missing = []
    if "seccompProfile:" not in content:
        missing.append("seccompProfile")
    if "readOnlyRootFilesystem:" not in content:
        missing.append("readOnlyRootFilesystem")
    if missing:
        raise RuntimeError(
            f"Security hardening injection failed for deployment template. "
            f"Missing: {', '.join(missing)}. Check source file formatting."
        )

    dst.write_text(content)


# Maps each single-source output template to its config/rbac source file.
SINGLE_SOURCES: dict[str, Path] = {
    "clusterrole.yaml":               RBAC_DIR / "role.yaml",
    "clusterrolebinding.yaml":        RBAC_DIR / "role_binding.yaml",
    "leader-election-role.yaml":      RBAC_DIR / "leader_election_role.yaml",
    "leader-election-rolebinding.yaml": RBAC_DIR / "leader_election_role_binding.yaml",
}

# Maps each aggregate output template to its ordered list of source files.
AGGREGATE_SOURCES: dict[str, list[Path]] = {
    "aggregate-roles.yaml": [
        RBAC_DIR / "baremetalpool_editor_role.yaml",
        RBAC_DIR / "baremetalpool_viewer_role.yaml",
        RBAC_DIR / "baremetalpool_admin_role.yaml",
        RBAC_DIR / "metrics_auth_role.yaml",
        RBAC_DIR / "metrics_auth_role_binding.yaml",
        RBAC_DIR / "metrics_reader_role.yaml",
    ],
}


def main() -> int:
    print(f"Syncing operator manifests to {TEMPLATES_DIR}...")
    TEMPLATES_DIR.mkdir(parents=True, exist_ok=True)

    for dst_name, src in SINGLE_SOURCES.items():
        sync_rbac(src, TEMPLATES_DIR / dst_name)
        print(f"  {dst_name}")

    sync_serviceaccount(RBAC_DIR / "service_account.yaml", TEMPLATES_DIR / "serviceaccount.yaml")
    print("  serviceaccount.yaml")

    for dst_name, srcs in AGGREGATE_SOURCES.items():
        sync_aggregate(srcs, TEMPLATES_DIR / dst_name)
        print(f"  {dst_name}")

    sync_metrics_service(DEFAULT_DIR / "metrics_service.yaml", TEMPLATES_DIR / "metrics-service.yaml")
    print("  metrics-service.yaml")

    sync_deployment(MANAGER_DIR / "manager.yaml", TEMPLATES_DIR / "deployment.yaml")
    print("  deployment.yaml")

    print("Done.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
