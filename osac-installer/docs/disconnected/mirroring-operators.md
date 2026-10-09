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

## Prerequisites (TDD: define checks first)

Before you begin, confirm each item:

| Check | Command / evidence |
| ----- | ------------------ |
| OpenShift cluster is in a disconnected or restricted-network state | `oc get proxy/cluster -o jsonpath='{.spec}'` |
| `oc-mirror` v2 binary is available on the bastion host | `oc-mirror version --short` shows `>= 4.16` |
| Mirror registry is reachable from the bastion and from every cluster node | `curl -sSf https://<registry>:8443/v2/ -k` |
| Red Hat pull secret is configured for `registry.redhat.io` | `jq '.auths["registry.redhat.io"]' ~/.docker/config.json` |
| Mirror registry credentials are in the pull secret | `jq '.auths["<registry>:8443"]' ~/.docker/config.json` |
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

# Signature ConfigMap
oc apply --server-side -f oc-mirror-workspace/working-dir/cluster-resources/signature-configmap.yaml
```

### Step 3 — Record the CatalogSource name

The generated `CatalogSource` name is used when installing OSAC with Helm:

```bash
CS_NAME=$(oc get catalogsource -n openshift-marketplace \
  -o jsonpath='{.items[?(@.metadata.name!="redhat-operators")].metadata.name}')
echo "Use --set catalogSource=${CS_NAME}"
```

### Step 4 — Install operators via Helm (osac-deps)

Pass the mirrored catalog name to the `osac-deps` chart:

```bash
helm upgrade --install osac-deps osac-installer/charts/osac-deps \
  --namespace osac-deps --create-namespace \
  --set catalogSource=${CS_NAME} \
  --set catalogSourceNamespace=openshift-marketplace \
  --set cliImage=<mirror-registry>:8443/openshift/origin-cli:4.20
```

## Verification (BDD Scenarios)

```gherkin
Feature: OLM operator mirroring for disconnected OSAC

  Background:
    Given a disconnected OpenShift 4.22 cluster
    And a mirror registry populated by oc-mirror v2
    And the CatalogSource "cs-redhat-operator-index-v4-22" exists in "openshift-marketplace"

  Scenario: All seven operator packages are available in the mirrored catalog
    When I query the mirrored CatalogSource
    Then the catalog contains the package "openshift-cert-manager-operator"
    And the catalog contains the package "ansible-automation-platform-operator"
    And the catalog contains the package "amq-streams"
    And the catalog contains the package "kubevirt-hyperconverged"
    And the catalog contains the package "lvms-operator"
    And the catalog contains the package "metallb-operator"
    And the catalog contains the package "multicluster-engine"

  Scenario: CatalogSource pod reaches READY state
    When I check the CatalogSource status
    Then the CatalogSource "cs-redhat-operator-index-v4-22" has connectionState "READY"

  Scenario: Operator subscriptions resolve from the mirrored catalog
    Given the osac-deps Helm chart is installed with catalogSource override
    When I list Subscriptions across operator namespaces
    Then each Subscription has status "AtLatestKnown"
    And each Subscription references the mirrored CatalogSource

  Scenario: Operator CSVs reach Succeeded phase
    When I list ClusterServiceVersions in operator namespaces
    Then the CSV for "openshift-cert-manager-operator" has phase "Succeeded"
    And the CSV for "ansible-automation-platform-operator" has phase "Succeeded"
    And the CSV for "amq-streams" has phase "Succeeded"
    And the CSV for "kubevirt-hyperconverged" has phase "Succeeded"
    And the CSV for "lvms-operator" has phase "Succeeded"
    And the CSV for "metallb-operator" has phase "Succeeded"
    And the CSV for "multicluster-engine" has phase "Succeeded"

  Scenario: CRDs required by Phase 1b are registered
    When I query the API server for CRDs
    Then CRD "certificates.cert-manager.io" exists
    And CRD "clusterissuers.cert-manager.io" exists
    And CRD "kafkas.kafka.strimzi.io" exists
    And CRD "hyperconvergeds.hco.kubevirt.io" exists
    And CRD "lvmclusters.lvm.topolvm.io" exists
    And CRD "ipaddresspools.metallb.io" exists
    And CRD "multiclusterengines.multicluster.openshift.io" exists
```

## Validation Commands

```bash
# 1. Verify CatalogSource is READY
oc get catalogsource -n openshift-marketplace -o wide

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

# 5. Verify no Subscription references the default catalog
oc get subscriptions -A -o json | \
  jq -r '.items[] | select(.spec.source=="redhat-operators") | .metadata.name' | \
  xargs -I{} echo "WARNING: {} still references redhat-operators"
```

## Troubleshooting

| Symptom | Likely cause | Resolution |
| ------- | ------------ | ---------- |
| CatalogSource stays in `CONNECTING` state | Catalog image not mirrored or IDMS not applied | Verify `oc get idms` lists the catalog index; re-run `oc-mirror` |
| Subscription stuck on `UpgradePending` | Channel mismatch between ISC and Subscription | Ensure `defaultChannel` in ISC matches the Subscription channel |
| `ImagePullBackOff` on operator pods | IDMS/ITMS not applied or registry unreachable from nodes | `oc debug node/<node> -- chroot /host crictl pull <image>` |
| `oc-mirror` fails with `manifest unknown` | Registry disk full or partial upload | Check registry storage; re-run `oc-mirror` (idempotent) |
| `maxVersion` mismatch for AAP | Version format includes build metadata | Use exact string `'2.6.0+0.1787258256'` with quotes in YAML |

## Known Gaps / Limitations

1. **oc-mirror v2 catalog filtering** — `oc-mirror` v2 currently mirrors
   the full catalog index even when individual packages are specified.
   The `catalog-filter` library prunes unused packages, but the initial
   download is still the complete index.

2. **OLM v1 transition** — OpenShift 4.22 ships both OLM v0
   (`CatalogSource`) and OLM v1 (`ClusterCatalog`).  The `oc-mirror`
   output includes both resource types.  Apply both for forward
   compatibility.

3. **AAP version pinning** — The AAP operator `maxVersion` contains a
   build-metadata suffix (`+0.1787258256`).  This is intentional and
   documented in
   [cve-2026-75884-security-exception.md](../cve-2026-75884-security-exception.md).
