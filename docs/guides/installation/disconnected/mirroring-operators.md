# Category 1A: Mirroring OLM Operators

## Aim

Mirror the three always-required core operator packages into a
disconnected mirror registry using `oc-mirror` v2. Add the VMaaS package
fragment only when `global.services.vmaas.enabled: true`, and add the
BMaaS package fragment only when `global.services.bmaas.enabled: true`.
All selected packages are merged into one catalog entry in the
authoritative ISC and mirrored with one workspace lineage.

After mirroring, a `CatalogSource` CR is applied so that operator `Subscription`
resources resolve against the local catalog instead of the
default `redhat-operators` source.

> **Relationship to the main guide**: this page is Category 1A of the
> [OSAC Disconnected Mirroring Guide](README.md). It defines the operator
> package tiers contributed to the single authoritative
> `ImageSetConfiguration`. It does not install operators. Operator
> installation belongs in [installation.md](installation.md).

## Operator Package Tiers

Compose the package set for the selected OSAC profile before the dry run.
All selected packages are appended to one catalog entry in the same ISC.

### Core tier - always required

1. `openshift-cert-manager-operator` - channel `stable-v1`
2. `ansible-automation-platform-operator` - channel
   `stable-2.6-cluster-scoped`
3. `amq-streams` - channel `stable`

### VMaaS tier - conditional

Include when `global.services.vmaas.enabled: true`:

1. `kubevirt-hyperconverged` - channel `stable`
2. `lvms-operator` - channel `stable-4.22`
3. `metallb-operator` - channel `stable`

### BMaaS tier - conditional

Include when `global.services.bmaas.enabled: true`:

1. `multicluster-engine` - channel `stable-2.17`

### Profile package counts

| Profile | Required packages | Count |
| ------- | ----------------- | ----- |
| Core | Three core packages | 3 |
| Core + VMaaS | Three core plus three VMaaS packages | 6 |
| Full-stack | Three core plus three VMaaS packages plus MCE | 7 |

Do not create separate ISCs or workspaces for VMaaS or BMaaS. The dry
run validates the complete selected package set atomically.

## Prerequisites

Before you begin, confirm each item:

| Check | Command / evidence |
| ----- | ------------------ |
| OpenShift cluster is in a disconnected or restricted-network state | `oc get proxy/cluster -o jsonpath='{.spec}'` |
| Latest available `oc-mirror` v2 binary on the bastion | `oc-mirror version` |
| Mirror registry certificate validates from the bastion | `curl -sS -o /dev/null -w '%{http_code}\n' https://<registry>:8443/v2/` (no `-k`; HTTP `200` or `401` proves TLS validation and reachability) |
| Mirror registry certificate validates from every cluster node | Run the per-node certificate validation below; every node must return HTTP `200` or `401` without `-k` |
| **Bastion authfile** contains source read + mirror write credentials | `jq -e '.auths \| has("registry.redhat.io")' $BASTION_AUTHFILE` |
| **Bastion authfile** includes mirror registry write credentials | `jq -e '.auths \| has("<registry>:8443")' $BASTION_AUTHFILE` |
| **Cluster pull secret** contains mirror read-only credentials | `oc get secret/pull-secret -n openshift-config -o jsonpath='{.data.\.dockerconfigjson}' \| base64 -d \| jq '.auths \| keys'` |
| At least 400 GiB free disk on the bastion | `df -h $(pwd)` |
| Cluster admin access | `oc auth can-i create catalogsource -n openshift-marketplace` |

Validate the mirror registry certificate and reachability from every
cluster node:

```bash
for node in $(oc get nodes -o name); do
  code="$(
    oc debug "$node" -- chroot /host \
      curl -sS -o /dev/null -w '%{http_code}' \
      'https://<registry>:8443/v2/'
  )" || {
    echo "ERROR: TLS validation or reachability failed on $node" >&2
    exit 1
  }

  case "$code" in
    200|401) printf '%s %s\n' "$node" "$code" ;;
    *)
      echo "ERROR: unexpected registry response on $node: HTTP $code" >&2
      exit 1
      ;;
  esac
done
```

### Credential Separation

Two distinct authfiles enforce a trust boundary between the
bastion and the cluster:

| Authfile | Contents | Used by |
| -------- | -------- | ------- |
| **Bastion authfile** | Source registry read + mirror registry write | `oc mirror` on the bastion |
| **Cluster pull secret** | Mirror registry read-only | Nodes (CRI-O / kubelet image pulls) |

> **Never upload the bastion authfile to the cluster.** The
> bastion authfile carries write credentials to the mirror
> registry and read credentials to upstream sources; neither
> belongs in the cluster pull secret.

## Resources Required

| Resource | Minimum |
| -------- | ------- |
| Disk (bastion, oc-mirror workspace) | 400 GiB |
| Network bandwidth (connected side) | Operators typically require 50-80 GiB depending on channel depth |
| Time estimate | 2-4 hours for initial mirror |

## ImageSetConfiguration Section

The following YAML is the `operators` portion of the unified
`ImageSetConfiguration`. It always includes the three core packages,
adds the three VMaaS packages when VMaaS is selected, and adds MCE when
BMaaS is selected. All selected packages use one catalog entry and one
workspace lineage.

### Operator Package Reference

The following operator packages are required (channels and version
constraints are specified in the ImageSetConfiguration below):

| # | Package | Purpose |
| - | ------- | ------- |
| 1 | `openshift-cert-manager-operator` | TLS certificate lifecycle |
| 2 | `ansible-automation-platform-operator` | AAP controller and hub |
| 3 | `amq-streams` | Kafka messaging |
| 4 | `kubevirt-hyperconverged` | VM workloads *(VMaaS only)* |
| 5 | `lvms-operator` | Local volume provisioning *(VMaaS only)* |
| 6 | `metallb-operator` | Bare-metal load balancing *(VMaaS only)* |
| 7 | `multicluster-engine` | Cluster lifecycle and BMaaS *(BMaaS only)* |

```yaml
apiVersion: mirror.openshift.io/v2alpha1
kind: ImageSetConfiguration
archiveSize: 8
mirror:
  operators:
    - catalog: registry.redhat.io/redhat/redhat-operator-index:v4.22
      targetCatalog: redhat-operators
      packages:
        # --- Core platform operators ---
        - name: openshift-cert-manager-operator
          channels:
            - name: stable-v1

        - name: ansible-automation-platform-operator
          defaultChannel: stable-2.6-cluster-scoped
          channels:
            - name: stable-2.6-cluster-scoped
              maxVersion: '2.6.0+0.1787258256'

        - name: amq-streams
          channels:
            - name: stable

        # --- VMaaS operators ---
        - name: kubevirt-hyperconverged
          channels:
            - name: stable

        - name: lvms-operator
          channels:
            - name: stable-4.22

        - name: metallb-operator
          channels:
            - name: stable

        # --- BMaaS only (include when global.services.bmaas.enabled) ---
        - name: multicluster-engine
          channels:
            - name: stable-2.17
```

> **Dry-run gate**: after constructing the ISC, run `oc mirror`
> with `--dry-run` (see
> [Canonical mirror execution](#step-1--dry-run-connected-bastion))
> and inspect the dry-run output for truncated AAP or package
> graphs before proceeding with the full mirror.

### Operator Summary Table

| # | Operator Package | Channel | Purpose |
| - | ---------------- | ------- | ------- |
| 1 | `openshift-cert-manager-operator` | `stable-v1` | TLS certificate lifecycle; provides `Certificate` and `ClusterIssuer` CRDs |
| 2 | `ansible-automation-platform-operator` | `stable-2.6-cluster-scoped` | AAP controller/hub; provides `AutomationController` CRDs |
| 3 | `amq-streams` | `stable` | Kafka messaging; provides `Kafka` CRD |
| 4 | `kubevirt-hyperconverged` | `stable` | VM workloads (VMaaS); provides `HyperConverged` CRD *(VMaaS only)* |
| 5 | `lvms-operator` | `stable-4.22` | Local volume provisioning; provides `LVMCluster` CRD *(VMaaS only)* |
| 6 | `metallb-operator` | `stable` | Bare-metal load balancing; provides `IPAddressPool` and `L2Advertisement` CRDs *(VMaaS only)* |
| 7 | `multicluster-engine` | `stable-2.17` | Cluster lifecycle and BMaaS agent services *(BMaaS only)* |

## Execution Steps

### Step 1 — Dry run (connected bastion)

```bash
# Dry run first — inspect output for truncated package graphs
oc mirror -c imageset-config.yaml \
  --workspace file://$(pwd)/oc-mirror-workspace \
  docker://<mirror-registry>:8443/ \
  --v2 --dry-run
```

### Step 1b — Full mirror (connected bastion)

```bash
# Then full mirror
oc mirror -c imageset-config.yaml \
  --workspace file://$(pwd)/oc-mirror-workspace \
  docker://<mirror-registry>:8443/ \
  --v2
```

> **Tip**: For development setups use
> `--dest-tls-verify=false --parallel-images 1 --parallel-layers 1`
> to avoid TLS and registry lock-up issues.

### Step 2 — Apply generated cluster resources (disconnected cluster)

The `oc mirror` run generates five cluster resource files.  Apply
them in the following order to avoid broken image resolution or
duplicate catalog sources.

#### 2a. Disable default catalog sources

```bash
oc patch operatorhub cluster --type merge \
  -p '{"spec":{"disableAllDefaultSources":true}}'
```

#### 2b. Verify the generated CatalogSource name

The ISC sets `targetCatalog: redhat-operators`. Verify the generated
CatalogSource name before its first apply:

```bash
grep '^  name:' \
  oc-mirror-workspace/working-dir/cluster-resources/cs-*.yaml
```

Expected: `name: redhat-operators`.

If the installed `oc mirror` version does not honor `targetCatalog`,
rename the generated resource to `redhat-operators` before its first
apply. Never apply both the generated name and `redhat-operators`.

#### 2c. Merge read-only pull credentials

Ensure the cluster pull secret contains mirror registry
**read-only** credentials so that nodes can pull mirrored
images (see [Credential Separation](#credential-separation)).

#### 2d. Apply all five generated artifacts

```bash
oc apply --server-side -f oc-mirror-workspace/working-dir/cluster-resources/idms-oc-mirror.yaml
oc apply --server-side -f oc-mirror-workspace/working-dir/cluster-resources/itms-oc-mirror.yaml
oc apply --server-side -f oc-mirror-workspace/working-dir/cluster-resources/cs-redhat-operator-index-v4-22.yaml
oc apply --server-side -f oc-mirror-workspace/working-dir/cluster-resources/cc-redhat-operator-index-v4-22.yaml
oc apply --server-side -f oc-mirror-workspace/working-dir/cluster-resources/signature-configmap.yaml
```

#### 2e. Wait for MCPs to converge

IDMS/ITMS changes cause CRI-O and kubelet restarts on every
node.  Wait for all MachineConfigPools to finish rolling:

```bash
oc wait mcp --all --for=condition=Updated --timeout=30m
```

## Handoff Verification (Doc 1 gate)

The following read-only checks gate the transition to Doc 2.
**Any failure returns ownership to the mirror operator; Doc 2
must not begin.**  Preserve the ISC and workspace metadata for
re-runs.

```bash
# 1. disableAllDefaultSources is set
oc get operatorhub cluster \
  -o jsonpath='{.spec.disableAllDefaultSources}{"\n"}'
# Expected: true

# 2. IDMS resources applied
oc get imagedigestmirrorset

# 3. ITMS resources applied
oc get imagetagmirrorset

# 4. CatalogSource redhat-operators exists and is READY
catalog_state="$(
  oc get catalogsource redhat-operators -n openshift-marketplace \
    -o jsonpath='{.status.connectionState.lastObservedState}'
)" || {
  echo "ERROR: CatalogSource redhat-operators was not found" >&2
  exit 1
}

if [ "$catalog_state" != "READY" ]; then
  echo "ERROR: CatalogSource redhat-operators is not READY: ${catalog_state:-<empty>}" >&2
  oc get catalogsource redhat-operators -n openshift-marketplace -o yaml >&2
  exit 1
fi
printf 'OK: CatalogSource redhat-operators state=%s\n' "$catalog_state"

# 5. Every generated ClusterCatalog exists and is serving
mapfile -t cluster_catalogs < <(oc get clustercatalog -o name)
if [ "${#cluster_catalogs[@]}" -eq 0 ]; then
  echo "ERROR: no ClusterCatalog resources found" >&2
  exit 1
fi
for catalog in "${cluster_catalogs[@]}"; do
  oc wait "$catalog" --for=condition=Serving --timeout=10m
done

# 6. At least one managed signature ConfigMap is present
signature_configmaps="$(
  oc get configmap -n openshift-config-managed \
    -l operator.openshift.io/managed=true \
    -o name 2>/dev/null
)"
if [ -z "$signature_configmaps" ]; then
  echo "ERROR: no managed signature ConfigMap found in openshift-config-managed" >&2
  exit 1
fi
printf '%s\n' "$signature_configmaps"

# 7. MCPs converged
oc wait mcp --all --for=condition=Updated --timeout=0

# 8. Package Manifests
# 8a. Core package manifests are always required
core_packages=(
  openshift-cert-manager-operator
  ansible-automation-platform-operator
  amq-streams
)

for pkg in "${core_packages[@]}"; do
  oc get packagemanifest "$pkg" -n openshift-marketplace -o name \
    2>/dev/null && echo "OK: core package $pkg" || {
      echo "MISSING: core package $pkg"
      exit 1
    }
done

# 8b. Require the selected profile flags explicitly; never default an
# unset flag to false because that can skip required handoff checks.
: "${ENABLE_VMAAS:?Set ENABLE_VMAAS=true or false from the selected profile}"
: "${ENABLE_BMAAS:?Set ENABLE_BMAAS=true or false from the selected profile}"
case "$ENABLE_VMAAS" in true|false) ;; *) echo "ERROR: ENABLE_VMAAS must be true or false" >&2; exit 1 ;; esac
case "$ENABLE_BMAAS" in true|false) ;; *) echo "ERROR: ENABLE_BMAAS must be true or false" >&2; exit 1 ;; esac
printf 'Selected profile flags: ENABLE_VMAAS=%s ENABLE_BMAAS=%s\n' \
  "$ENABLE_VMAAS" "$ENABLE_BMAAS"

# 8c. VMaaS package manifests are required only when VMaaS is selected
if [ "$ENABLE_VMAAS" = "true" ]; then
  vmaas_packages=(kubevirt-hyperconverged lvms-operator metallb-operator)
  for pkg in "${vmaas_packages[@]}"; do
    oc get packagemanifest "$pkg" -n openshift-marketplace -o name \
      2>/dev/null && echo "OK: VMaaS package $pkg" || {
        echo "MISSING: VMaaS package $pkg"
        exit 1
      }
  done
fi

# 8d. MCE is required only when BMaaS is selected
if [ "$ENABLE_BMAAS" = "true" ]; then
  oc get packagemanifest multicluster-engine \
    -n openshift-marketplace -o name 2>/dev/null \
    && echo "OK: BMaaS package multicluster-engine" || {
      echo "MISSING: BMaaS package multicluster-engine"
      exit 1
    }
fi

## Troubleshooting

| Symptom | Likely cause | Resolution |
| ------- | ------------ | ---------- |
| CatalogSource stays in `CONNECTING` state | Catalog image not mirrored or IDMS not applied | Verify `oc get idms` lists the catalog index; re-run `oc-mirror` |
| Subscription stuck on `UpgradePending` | Channel mismatch between ISC and Subscription | Ensure `defaultChannel` in ISC matches the Subscription channel |
| `ImagePullBackOff` on operator pods | IDMS/ITMS not applied or registry unreachable from nodes | `oc debug node/<node> -- chroot /host crictl pull <image>` |
| `oc-mirror` fails with `manifest unknown` | Registry disk full or partial upload | Check registry storage; re-run `oc-mirror` (idempotent) |
| `maxVersion` mismatch for AAP | Version format includes build metadata | Use exact string `'2.6.0+0.1787258256'` with quotes in YAML |

## CatalogSource naming requirement

The Subscription manifests for the three core packages, the three
VMaaS packages when VMaaS is selected, and MCE when BMaaS is selected
use `source: redhat-operators` and
`sourceNamespace: openshift-marketplace`. The mirrored CatalogSource
must therefore use that exact name and namespace.

| Operator package | Subscription `.spec.source` |
| ---------------- | ---------------------------- |
| `ansible-automation-platform-operator` | `redhat-operators` |
| `amq-streams` | `redhat-operators` |
| `openshift-cert-manager-operator` | `redhat-operators` |
| `kubevirt-hyperconverged` | `redhat-operators` |
| `lvms-operator` | `redhat-operators` |
| `metallb-operator` | `redhat-operators` |
| `multicluster-engine` | `redhat-operators` |

**Consequence for disconnected installs**: the mirrored
`CatalogSource` must be named `redhat-operators` in
`openshift-marketplace`, or the Subscriptions cannot resolve.
See [Step 2b](#2b-verify-the-generated-catalogsource-name) above.

## Known Gaps / Limitations

1. **oc-mirror v2 catalog filtering** — `oc-mirror` v2 currently
   mirrors the full catalog index even when individual packages
   are specified.  The `catalog-filter` library prunes unused
   packages, but the initial download is still the complete index.

2. **OLM Classic / OLM v1 transition** — OpenShift 4.22 ships
   both CatalogSource (OLM Classic) and ClusterCatalog (OLM v1).
   The `oc mirror` output includes both resource types.  Apply
   both for forward compatibility.  Never use "OLM v2" — the
   successor to OLM Classic is called OLM v1.

3. **AAP version pinning** — The AAP operator `maxVersion`
   contains a build-metadata suffix (`+0.1787258256`).  This is
   intentional and documented in
   [cve-2026-75884-security-exception.md](../cve-2026-75884-security-exception.md).

4. **Fixed Subscription source** — The documented Subscription
   manifests reference `source: redhat-operators` literally.  A
   mirrored CatalogSource with a different name requires editing
   every Subscription manifest to match.
