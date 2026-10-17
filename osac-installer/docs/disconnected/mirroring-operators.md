# Category 1A: Mirroring OLM Operators

## Aim

Mirror all seven OLM-managed operators required by OSAC into a disconnected
mirror registry using `oc-mirror` v2.  After mirroring, a `CatalogSource` CR
is applied so that operator `Subscription` resources resolve against the
local catalog instead of the default `redhat-operators` source.

> **Relationship to the main guide**: this document expands on the operators
> section of [disconnected-install.md](../disconnected-install.md).  The
> unified `ImageSetConfiguration` in that guide already includes every
> operator listed here; this page provides category-specific detail and
> verification steps.

## Prerequisites

Before you begin, confirm each item:

| Check | Command / evidence |
| ----- | ------------------ |
| OpenShift cluster is in a disconnected or restricted-network state | `oc get proxy/cluster -o jsonpath='{.spec}'` |
| Latest available `oc-mirror` v2 binary on the bastion | `oc-mirror version` |
| Mirror registry is reachable from the bastion and from every cluster node | `curl -sSf https://<registry>:8443/v2/ -k` |
| Red Hat pull secret is configured for `registry.redhat.io` | `jq -e '.auths \| has("registry.redhat.io")' ~/.docker/config.json` |
| Mirror registry credentials are in the pull secret | `jq -e '.auths \| has("<registry>:8443")' ~/.docker/config.json` |
| At least 400 GiB free disk on the bastion | `df -h $(pwd)` |
| Cluster admin access | `oc auth can-i create catalogsource -n openshift-marketplace` |

## Resources Required

| Resource | Minimum |
| -------- | ------- |
| Disk (bastion, oc-mirror workspace) | 400 GiB |
| Network bandwidth (connected side) | Operators typically require 50-80 GiB depending on channel depth |
| Time estimate | 2-4 hours for initial mirror |

## ImageSetConfiguration Section

The following YAML is the **operators** portion of the unified
`ImageSetConfiguration`.  It mirrors the catalog index and the seven
operator packages with their specified channels.

### Operator Package Reference

The following operator packages are required (channels and version
constraints are specified in the ImageSetConfiguration below):

| # | Package | Purpose |
| - | ------- | ------- |
| 1 | `openshift-cert-manager-operator` | TLS certificate lifecycle |
| 2 | `ansible-automation-platform-operator` | AAP controller and hub |
| 3 | `amq-streams` | Kafka messaging |
| 4 | `kubevirt-hyperconverged` | VM workloads (VMaaS) |
| 5 | `lvms-operator` | Local volume provisioning |
| 6 | `metallb-operator` | Bare-metal load balancing |
| 7 | `multicluster-engine` | Cluster lifecycle and BMaaS |

```yaml
apiVersion: mirror.openshift.io/v2alpha1
kind: ImageSetConfiguration
archiveSize: 8
mirror:
  operators:
    - catalog: registry.redhat.io/redhat/redhat-operator-index:v4.22
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

        - name: multicluster-engine
          channels:
            - name: stable-2.17
```

### Operator Summary Table

| # | Operator Package | Channel | Purpose |
| - | ---------------- | ------- | ------- |
| 1 | `openshift-cert-manager-operator` | `stable-v1` | TLS certificate lifecycle; provides `Certificate` and `ClusterIssuer` CRDs |
| 2 | `ansible-automation-platform-operator` | `stable-2.6-cluster-scoped` | AAP controller/hub; provides `AutomationController` CRDs |
| 3 | `amq-streams` | `stable` | Kafka messaging; provides `Kafka` CRD |
| 4 | `kubevirt-hyperconverged` | `stable` | VM workloads (VMaaS); provides `HyperConverged` CRD |
| 5 | `lvms-operator` | `stable-4.22` | Local volume provisioning; provides `LVMCluster` CRD |
| 6 | `metallb-operator` | `stable` | Bare-metal load balancing; provides `IPAddressPool` and `L2Advertisement` CRDs |
| 7 | `multicluster-engine` | `stable-2.17` | Cluster lifecycle and BMaaS agent services |

## Execution Steps

### Step 1 — Run oc-mirror (connected bastion)

```bash
oc-mirror --v2 --config=imageset-config.yaml --workspace \
  file://$(pwd)/oc-mirror-workspace docker://<mirror-registry>:8443/
```

> **Tip**: For development setups use
> `--dest-tls-verify=false --parallel-images 1 --parallel-layers 1`
> to avoid TLS and registry lock-up issues.

### Step 2 — Apply cluster resources (disconnected cluster)

The `oc-mirror` run generates several cluster resource files.  Apply them in
this order:

```bash
# CatalogSource
oc apply --server-side -f oc-mirror-workspace/working-dir/cluster-resources/cs-redhat-operator-index-v4-22.yaml

# ClusterCatalog (OLM v1)
oc apply --server-side -f oc-mirror-workspace/working-dir/cluster-resources/cc-redhat-operator-index-v4-22.yaml

# ImageDigestMirrorSet
oc apply --server-side -f oc-mirror-workspace/working-dir/cluster-resources/idms-oc-mirror.yaml

# ImageTagMirrorSet
oc apply --server-side -f oc-mirror-workspace/working-dir/cluster-resources/itms-oc-mirror.yaml
```

### Step 3 — Rename the CatalogSource to `redhat-operators`

The Subscription manifests used to install the OSAC operators (and
Red Hat's own documented Subscription examples) reference the
`redhat-operators` catalog source in `openshift-marketplace` (see
[CatalogSource naming requirement](#catalogsource-naming-requirement)).
The `CatalogSource` that OLM resolves against must therefore be named
`redhat-operators` in `openshift-marketplace`.

`oc-mirror` generates a CatalogSource whose name encodes the index
(e.g. `cs-redhat-operator-index-v4-22`).  Rename it before applying:

```bash
# Adjust the generated CatalogSource name to match what
# the Subscription templates expect.
sed -i 's/name: cs-redhat-operator-index-v4-22/name: redhat-operators/' \
  oc-mirror-workspace/working-dir/cluster-resources/cs-redhat-operator-index-v4-22.yaml

oc apply --server-side -f \
  oc-mirror-workspace/working-dir/cluster-resources/cs-redhat-operator-index-v4-22.yaml

oc get catalogsource redhat-operators -n openshift-marketplace
```

> **Why rename?** The documented Subscription manifests for all seven
> operators specify `source: redhat-operators` and
> `sourceNamespace: openshift-marketplace` — the same catalog name a
> connected OpenShift cluster uses by default.  Naming the mirrored
> CatalogSource `redhat-operators` lets those Subscriptions resolve
> without editing each manifest.  If the connected-environment default
> `redhat-operators` CatalogSource still exists, delete it first so
> the mirrored one takes its place.

### Step 4 — Install the operators

Install each operator following the official Red Hat documentation —
via OperatorHub in the web console, or by applying `Namespace`,
`OperatorGroup`, and `Subscription` manifests with `oc`.  For the
documented procedure, see
[Adding Operators to a cluster](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/operators/user-tasks).

Install them in the namespaces and channels below — these are the
namespaces the OSAC platform expects the operators in:

| Operator package | Namespace | Channel |
| ---------------- | --------- | ------- |
| `openshift-cert-manager-operator` | `cert-manager-operator` | `stable-v1` |
| `ansible-automation-platform-operator` | `ansible-aap` | `stable-2.6-cluster-scoped` |
| `amq-streams` | `osac-kafka` | `stable` |
| `kubevirt-hyperconverged` | `openshift-cnv` | `stable` |
| `lvms-operator` | `openshift-storage` | `stable-4.22` |
| `metallb-operator` | `metallb-system` | `stable` |
| `multicluster-engine` | `multicluster-engine` | `stable-2.17` |

Example manifests for the cert-manager operator (repeat the pattern
for each operator, adjusting name, namespace, and channel):

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: cert-manager-operator
---
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: cert-manager-operator
  namespace: cert-manager-operator
spec:
  targetNamespaces:
    - cert-manager-operator
---
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: openshift-cert-manager-operator
  namespace: cert-manager-operator
spec:
  channel: stable-v1
  name: openshift-cert-manager-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
  installPlanApproval: Automatic
```

> **AAP version pin**: subscribe the
> `ansible-automation-platform-operator` with
> `installPlanApproval: Manual` and
> `startingCSV: aap-operator.v2.6.0-0.1787258256` so it resolves to
> the version mirrored in Category 1A
> (`maxVersion: '2.6.0+0.1787258256'`), then approve the generated
> InstallPlan:
>
> ```bash
> oc patch installplan <installplan-name> -n ansible-aap \
>   --type merge -p '{"spec":{"approved":true}}'
> ```

Wait for every operator CSV to reach `Succeeded` — cert-manager
first, since the Phase 1b certificates and CA resources depend on it —
before continuing to [Category 1B](mirroring-core-components.md):

```bash
oc get csv -A --no-headers | awk '{print $1, $2, $NF}'
```

## Validation Commands

```bash
# 1. Verify CatalogSource is READY
oc get catalogsource redhat-operators -n openshift-marketplace \
  -o jsonpath='{.status.connectionState.lastObservedState}{"\n"}'
# Expected output: READY
# If not READY, check catalog pod logs in openshift-marketplace

# 2. Verify all 7 Subscriptions exist and are healthy
oc get subscriptions -A -o custom-columns=\
'NAME:.metadata.name,NAMESPACE:.metadata.namespace,STATE:.status.state,CATALOG:.spec.source'

# 3. Verify all 7 CSVs are Succeeded
oc get csv -A -o custom-columns=\
'NAME:.metadata.name,NAMESPACE:.metadata.namespace,PHASE:.status.phase' \
| grep -E 'cert-manager|ansible|amq|kubevirt|lvms|metallb|multicluster'

# 4. Confirm required CRDs are present
for crd in certificates.cert-manager.io \
           clusterissuers.cert-manager.io \
           kafkas.kafka.strimzi.io \
           hyperconvergeds.hco.kubevirt.io \
           lvmclusters.lvm.topolvm.io \
           ipaddresspools.metallb.io \
           multiclusterengines.multicluster.openshift.io; do
  oc get crd "$crd" -o name 2>/dev/null && echo "  OK: $crd" || echo "  MISSING: $crd"
done

# 5. Verify all Subscriptions reference "redhat-operators"
# The documented Subscription manifests specify
# source: redhat-operators.  The mirrored CatalogSource must
# use that name (see Step 3).  Any Subscription pointing
# elsewhere indicates a manual override or stale resource.
oc get subscriptions -A -o json | \
  jq -r '.items[] |
    select(.spec.source != "redhat-operators") |
    "\(.metadata.name) (\(.metadata.namespace)): \
source=\(.spec.source) — expected redhat-operators"'
```

## Troubleshooting

| Symptom | Likely cause | Resolution |
| ------- | ------------ | ---------- |
| CatalogSource stays in `CONNECTING` state | Catalog image not mirrored or IDMS not applied | Verify `oc get idms` lists the catalog index; re-run `oc-mirror` |
| Subscription stuck on `UpgradePending` | Channel mismatch between ISC and Subscription | Ensure `defaultChannel` in ISC matches the Subscription channel |
| `ImagePullBackOff` on operator pods | IDMS/ITMS not applied or registry unreachable from nodes | `oc debug node/<node> -- chroot /host crictl pull <image>` |
| `oc-mirror` fails with `manifest unknown` | Registry disk full or partial upload | Check registry storage; re-run `oc-mirror` (idempotent) |
| `maxVersion` mismatch for AAP | Version format includes build metadata | Use exact string `'2.6.0+0.1787258256'` with quotes in YAML |

## CatalogSource naming requirement

The documented Subscription manifests for all seven operators specify
`source: redhat-operators` and `sourceNamespace: openshift-marketplace`
— the catalog name a connected OpenShift cluster uses by default.

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
See [Step 3](#step-3--rename-the-catalogsource-to-redhat-operators)
above.

## Known Gaps / Limitations

1. **oc-mirror v2 catalog filtering** — `oc-mirror` v2 currently
   mirrors the full catalog index even when individual packages
   are specified.  The `catalog-filter` library prunes unused
   packages, but the initial download is still the complete index.

2. **OLM v1 transition** — OpenShift 4.22 ships both OLM v0
   (`CatalogSource`) and OLM v1 (`ClusterCatalog`).  The
   `oc-mirror` output includes both resource types.  Apply both
   for forward compatibility.

3. **AAP version pinning** — The AAP operator `maxVersion`
   contains a build-metadata suffix (`+0.1787258256`).  This is
   intentional and documented in
   [cve-2026-75884-security-exception.md](../cve-2026-75884-security-exception.md).

4. **Fixed Subscription source** — The documented Subscription
   manifests reference `source: redhat-operators` literally.  A
   mirrored CatalogSource with a different name requires editing
   every Subscription manifest to match.
