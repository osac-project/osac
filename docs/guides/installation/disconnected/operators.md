# Prepare OSAC Operators and Shared Infrastructure

> [!IMPORTANT]
> This is Doc 2b for OSAC **0.0.24**. Complete the [mirroring handoff](README.md) and [installation prerequisites](prereqs.md) first. This document prepares Phase 1; [installation.md](installation.md) owns the OSAC Helm release and Phase 2 acceptance.

Use this procedure for Core and optional VMaaS. BMaaS and CaaS provisioning are not covered. The internal dependency and infrastructure charts are reference implementations, not customer releases: do not install or render them as a prerequisite step.

The production path consumes customer-managed PostgreSQL, a prepared Keycloak realm, and external Vault/OpenBao. Kafka may be external or in-cluster. Product operators and optional storage/network operands are installed below. Disposable identity/database/secret-store fixtures are not production substitutes.

> [!WARNING]
> Installing an operator, approving an InstallPlan, publishing a Git ref, changing an issuer, consuming storage devices, or creating a credential is stateful work. Agree one mutation owner and a maintenance window. Do not run two copies of this procedure against the same resources. Never use force-conflict flags to take resources from another owner.

## 1. Reconfirm Entry Gates

### Establish the administration session

Use Bash with `oc`, Helm, `jq`, Mike Farah `yq` v4, Git, OpenSSL, `curl`, `timeout`, `sha256sum`, and Python 3. The bastion procedure also uses `dnf`, systemd, `flock`, `runuser`, `useradd`, and the site's firewall administration facility. Install packages from approved repositories before the disconnected change window.

Run in the directory containing the values and evidence produced by `prereqs.md`:

```bash
set -euo pipefail
umask 077
export OSAC_VERSION=0.0.24
export NS=osac
export AAP_OPERATOR_NAMESPACE=ansible-aap
export CATALOG_SOURCE=redhat-operators
export CATALOG_NAMESPACE=openshift-marketplace
export OSAC_CHART_DIR="$PWD/osac"
export VALUES_FILE="$PWD/values-osac.yaml"
export PREREQS_RECORD="$PWD/prereqs-completion.txt"
export SOURCE_ROOT='<absolute-path-to-approved-osac-source-checkout>'
export KAFKA_PROVIDER=external       # external or in-cluster
export ENABLE_VMAAS=false           # true or false
export WITH_LVMS=false              # true or false
export WITH_METALLB=false           # true or false
export STORAGE_CLASS='<approved-dynamic-storage-class>'
export RUN_ID="$(date -u +%Y%m%dt%H%M%Sz)-$(openssl rand -hex 3)"
export EVIDENCE="$PWD/operators-evidence-$RUN_ID"
mkdir -p "$EVIDENCE" "$EVIDENCE/manifests" "$EVIDENCE/jobs"
export EVIDENCE

case "$KAFKA_PROVIDER" in external|in-cluster) ;; *) exit 1 ;; esac
for flag in ENABLE_VMAAS WITH_LVMS WITH_METALLB; do
  case "${!flag}" in true|false) ;; *) echo "Invalid $flag" >&2; exit 1 ;; esac
done
if test "$ENABLE_VMAAS" = false; then
  test "$WITH_LVMS" = false && test "$WITH_METALLB" = false
fi
oc whoami
oc auth can-i create subscriptions.operators.coreos.com -n "$AAP_OPERATOR_NAMESPACE"
```

Replace angle-bracket inputs with site values. Do not load evidence text with `source` or `eval`; it is data, not a shell program. Set the choices above to match the prerequisite selection, and stop if they differ.

### Verify release and upstream handoff

```bash
test -s "$PREREQS_RECORD"
test -s "$VALUES_FILE"
test -f "$OSAC_CHART_DIR/Chart.yaml"
yq -e '.version == "0.0.24"' "$OSAC_CHART_DIR/Chart.yaml" >/dev/null
sha256sum "$PREREQS_RECORD" "$VALUES_FILE" "$OSAC_CHART_DIR/Chart.yaml" \
  > "$EVIDENCE/entry.sha256"

# The shipped default is quay.io/openshift/origin-cli:4.20.0.
helm show values "$OSAC_CHART_DIR" | yq -r '.cliImage' > "$EVIDENCE/shipped-cli-image.txt"
export CLI_IMAGE="$(yq -r '.cliImage // "quay.io/openshift/origin-cli:4.20.0"' "$VALUES_FILE")"
test -n "$CLI_IMAGE" && test "$CLI_IMAGE" != null

expected_values_sha="$(sed -n 's/^values_sha256=//p' "$PREREQS_RECORD")"
test -n "$expected_values_sha"
test "$expected_values_sha" = "$(sha256sum "$VALUES_FILE" | awk '{print $1}')"

oc get operatorhub cluster -o json | jq -e '.spec.disableAllDefaultSources == true'
oc get catalogsource "$CATALOG_SOURCE" -n "$CATALOG_NAMESPACE" -o json \
  | jq -e '.status.connectionState.lastObservedState == "READY"'
oc get imagedigestmirrorset -o json | jq -e '.items | length > 0'
oc get imagetagmirrorset -o json | jq -e '.items | length > 0'
```

Check the archived Doc 1 ISC/workspace, five generated artifacts, ClusterCatalog/signature evidence, affected MCP generations, mirror read-only credential, and representative pulls against `prereqs.md`. Both IDMS and ITMS are required. No legacy mirror-policy migration is performed here.

The 0.0.24 chart ships `cliImage: quay.io/openshift/origin-cli:4.20.0`. Keep any customer override mirror-resident and validated in `prereqs.md`; changing the image name here is not mirroring. The actual AAP probe image below is the approved Git-capable EE, not an assumption that every CLI image contains Git or Python.

An existing prerequisite record that names another chart release or inventories images from that release is not an acceptable 0.0.24 handoff. Return to `prereqs.md` and Doc 1 for release-specific reconciliation; do not merely edit the version field in an evidence file.

### Freeze ownership and capture safe evidence

Create a local lock and coordinate with every other administration host. A local lock does not serialize remote administrators.

```bash
exec 9>"$PWD/.osac-phase1.lock"
flock -n 9 || { echo 'Another local Phase 1 session is active' >&2; exit 1; }
printf 'run=%s\nrelease=%s\noperator=%s\nstarted=%s\n' \
  "$RUN_ID" "$OSAC_VERSION" "$(oc whoami)" "$(date -u +%FT%TZ)" \
  > "$EVIDENCE/session.txt"
printf 'kind\tnamespace\tname\tuid\tresourceVersion\townership\n' \
  > "$EVIDENCE/resources.tsv"
printf 'stage\toutcome\tevidence\n' > "$EVIDENCE/stages.tsv"
```

Record existing objects as `adopted` only after their owner approves the exact contract. For a new object, record `guide-created`; never add Helm ownership annotations to an adopted object. Existing OLM installations must be inspected rather than replaced. Do not save Secret bodies, decoded credentials, tokens, or full shell traces in evidence.

For non-sensitive manifests created below, use this apply function. A dry-run failure or field conflict stops the stage:

```bash
apply_manifest() {
  local f="$1" tmp
  tmp="$(mktemp -d)"
  yq ea 'select(.kind == "Namespace")' "$f" > "$tmp/namespaces.yaml"
  yq ea 'select(.kind != null and .kind != "Namespace")' "$f" > "$tmp/resources.yaml"
  # A dry-run Namespace is not persisted: create/verify the namespace first,
  # then server-validate resources that actually depend on it.
  if test -s "$tmp/namespaces.yaml"; then
    oc apply --server-side --dry-run=server \
      --field-manager=osac-disconnected-phase1 -f "$tmp/namespaces.yaml" >/dev/null
    oc apply --server-side --field-manager=osac-disconnected-phase1 -f "$tmp/namespaces.yaml"
  fi
  if test -s "$tmp/resources.yaml"; then
    oc apply --server-side --dry-run=server \
      --field-manager=osac-disconnected-phase1 -f "$tmp/resources.yaml" >/dev/null
    oc apply --server-side --field-manager=osac-disconnected-phase1 -f "$tmp/resources.yaml"
  fi
  # Save identities only. Ownership is reviewed before completion, never inferred.
  oc get -f "$f" -o json | jq -r '
    (.items // [.])[] |
    [.kind, (.metadata.namespace // "-"), .metadata.name,
     .metadata.uid, .metadata.resourceVersion, "unreviewed"] | @tsv' \
    >> "$EVIDENCE/resources.tsv"
  rm -rf "$tmp"
}
record_resource() {
  local kind="$1" namespace="$2" name="$3" ownership="$4"
  if test "$namespace" = -; then
    oc get "$kind" "$name" -o json
  else
    oc get "$kind" "$name" -n "$namespace" -o json
  fi | jq -r --arg owner "$ownership" '
    [.kind, (.metadata.namespace // "-"), .metadata.name,
     .metadata.uid, .metadata.resourceVersion, $owner] | @tsv' \
    >> "$EVIDENCE/resources.tsv"
}
wait_condition() {
  timeout 660s oc --request-timeout=30s wait "$@" --timeout=10m
}
wait_bundle() {
  local end=$((SECONDS+600))
  until oc --request-timeout=30s get bundle ca-bundle -o json | jq -e '
    .metadata.generation as $g |
    any(.status.conditions[]?; .type == "Synced" and .status == "True" and
        .observedGeneration == $g)' >/dev/null; do
    (( SECONDS < end )) || { echo 'Bundle has not synced its current generation' >&2; return 1; }
    sleep 5
  done
}
```

Success is a matching chart/values handoff, current disconnected checks, agreed ownership, and an evidence directory. Missing images/packages return to Doc 1. DNS, mirror trust, pull-secret, MCP, or input contract failures return to `prereqs.md`. Do not repair these by installing operators.

## 2. Phase 1a: Install Supported Operators

### Select the product profile

| Product | Package | Namespace | Channel | Scope | Approval |
| --- | --- | --- | --- | --- | --- |
| cert-manager | `openshift-cert-manager-operator` | `cert-manager-operator` | `stable-v1` | AllNamespaces | Automatic |
| AAP | `ansible-automation-platform-operator` | `ansible-aap` | `stable-2.6-cluster-scoped` | AllNamespaces | Manual, pinned |
| In-cluster Kafka only | `amq-streams` | `osac-kafka` | `stable` | OwnNamespace | Manual |
| VMaaS | `kubevirt-hyperconverged` | `openshift-cnv` | `stable` | OwnNamespace | Automatic |
| With LVMS only | `lvms-operator` | `openshift-storage` | Cluster minor channel | OwnNamespace | Automatic |
| With MetalLB only | `metallb-operator` | `metallb-system` | `stable` | OwnNamespace | Automatic |

Core with external Kafka needs two OLM installations, not three. Mirroring a package does not require installing its operator. Do not install MCE or unused VMaaS storage/network operators.

Follow the [OpenShift operator installation procedure](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/operators/user-tasks) for product support requirements. These explicit manifests add the disconnected source and OSAC compatibility constraints.

### Generate explicit Namespace, OperatorGroup and Subscription manifests

The following helper accepts each product's distinct scope and approval mode; it is not a single scope copied across all products. On an existing namespace, inspect `oc get operatorgroup,subscription -n <namespace>` first. Stop on an incompatible OperatorGroup or a competing Subscription. Do not apply a second group.

```bash
write_operator() {
  local namespace="$1" group="$2" sub="$3" package="$4"
  local channel="$5" scope="$6" approval="$7" csv="${8:-}"
  local f="$EVIDENCE/manifests/operator-$sub.yaml"
  cat > "$f" <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: $namespace
---
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: $group
  namespace: $namespace
EOF
  if test "$scope" = all; then
    printf 'spec: {}\n' >> "$f"
  else
    printf 'spec:\n  targetNamespaces:\n    - %s\n' "$namespace" >> "$f"
  fi
  cat >> "$f" <<EOF
---
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: $sub
  namespace: $namespace
spec:
  channel: $channel
  name: $package
  source: redhat-operators
  sourceNamespace: openshift-marketplace
  installPlanApproval: $approval
EOF
  if test -n "$csv"; then printf '  startingCSV: %s\n' "$csv" >> "$f"; fi
}

export AAP_CSV=aap-operator.v2.6.0-0.1787258256
write_operator cert-manager-operator cert-manager-operator \
  openshift-cert-manager-operator openshift-cert-manager-operator stable-v1 all Automatic
write_operator ansible-aap dev-ansible-automation-platform-operator \
  dev-ansible-automation-platform ansible-automation-platform-operator \
  stable-2.6-cluster-scoped all Manual "$AAP_CSV"

if test "$KAFKA_PROVIDER" = in-cluster; then
  write_operator osac-kafka amq-streams amq-streams amq-streams stable own Manual
fi
if test "$ENABLE_VMAAS" = true; then
  write_operator openshift-cnv kubevirt-hyperconverged-group \
    kubevirt-hyperconverged kubevirt-hyperconverged stable own Automatic
fi
if test "$WITH_LVMS" = true; then
  OCP_MINOR="$(oc get clusterversion version -o jsonpath='{.status.desired.version}' | cut -d. -f1,2)"
  write_operator openshift-storage lvms-operator lvms-operator lvms-operator \
    "stable-$OCP_MINOR" own Automatic
fi
if test "$WITH_METALLB" = true; then
  write_operator metallb-system metallb-operator metallb-operator metallb-operator \
    stable own Automatic
fi
```

Verify package identity, source and channel before each apply. For AAP, also verify that the pinned CSV is visible in the mirrored channel:

```bash
oc get packagemanifest ansible-automation-platform-operator -n openshift-marketplace -o json \
  | jq -e --arg csv "$AAP_CSV" '
      .status.catalogSource == "redhat-operators" and
      .status.catalogSourceNamespace == "openshift-marketplace" and
      any(.status.channels[]; .name == "stable-2.6-cluster-scoped" and
          .currentCSV == $csv)'
```

A channel head different from the expected pin is a stop, not permission to approve another bundle. Return to the Doc 1 catalog compatibility gate. The pin and Manual mode are present in the inspected 0.0.24 source; `startingCSV` does not permanently block future upgrades by itself.

### Install cert-manager first

```bash
apply_manifest "$EVIDENCE/manifests/operator-openshift-cert-manager-operator.yaml"
```

Poll the Subscription's installed CSV, not an all-namespace grep:

```bash
wait_csv() {
  local namespace="$1" sub="$2" expected="${3:-}" csv='' end=$((SECONDS+900))
  while (( SECONDS < end )); do
    csv="$(oc get subscription "$sub" -n "$namespace" -o jsonpath='{.status.installedCSV}')"
    if test -n "$csv"; then
      if test -n "$expected" && test "$csv" != "$expected"; then
        echo "Unexpected installed CSV: $csv" >&2; return 1
      fi
      if oc get csv "$csv" -n "$namespace" -o json \
        | jq -e '.status.phase == "Succeeded"' >/dev/null; then
        printf '%s\t%s\t%s\n' "$namespace" "$sub" "$csv" >> "$EVIDENCE/csvs.tsv"
        return 0
      fi
    fi
    sleep 10
  done
  oc get subscription,installplan,csv -n "$namespace"
  return 1
}
wait_csv cert-manager-operator openshift-cert-manager-operator
wait_condition crd/certmanagers.operator.openshift.io --for=condition=Established
```

Create the cert-manager initialization CR only after its CRD exists:

```bash
cat > "$EVIDENCE/manifests/certmanager-cluster.yaml" <<'EOF'
apiVersion: operator.openshift.io/v1alpha1
kind: CertManager
metadata:
  name: cluster
spec:
  managementState: Managed
EOF
apply_manifest "$EVIDENCE/manifests/certmanager-cluster.yaml"
for deployment in cert-manager cert-manager-webhook cert-manager-cainjector; do
  timeout 660s oc rollout status deployment/"$deployment" -n cert-manager --timeout=10m
done
wait_condition crd/certificates.cert-manager.io --for=condition=Established
oc get endpoints cert-manager-webhook -n cert-manager -o json \
  | jq -e 'any(.subsets[]?; (.addresses // [] | length) > 0)'
```

Verify the scope has no target namespaces:

```bash
oc get operatorgroup cert-manager-operator -n cert-manager-operator -o json \
  | jq -e '(.spec.targetNamespaces // [] | length) == 0'
```

Success is the installed CSV `Succeeded`, API available, all three deployments rolled out, and webhook endpoints populated. A missing package is a Doc 1 failure; an unresolved Subscription, missing API, unhealthy controller or webhook remains here. Do not create certificates yet.

### AAP and AMQ Streams: exact Manual InstallPlan approval

1. Apply the intended Manual Subscription.
2. Follow that Subscription's `status.installPlanRef`, recording Subscription and InstallPlan UIDs.
3. Review the complete CSV list and bundle set. AAP must contain the exact pinned CSV.
4. Re-read immediately before approval and use API-enforced UID/resourceVersion tests in the patch.
5. Wait for the intended installed CSV and recheck the Subscription source/channel/Manual mode.

Apply AAP:

```bash
apply_manifest "$EVIDENCE/manifests/operator-dev-ansible-automation-platform.yaml"
```

Use this snapshot function for each Manual Subscription. It intentionally does not approve a plan:

```bash
snapshot_plan() {
  local namespace="$1" sub="$2" expected="${3:-}" end=$((SECONDS+600)) plan=''
  local dir="$EVIDENCE/plan-$namespace-$sub"
  mkdir -p "$dir"
  while (( SECONDS < end )); do
    oc get subscription "$sub" -n "$namespace" -o json > "$dir/subscription.json"
    plan="$(jq -r '.status.installPlanRef.name // empty' "$dir/subscription.json")"
    if test -n "$plan"; then break; fi
    sleep 5
  done
  test -n "$plan"
  oc get installplan "$plan" -n "$namespace" -o json > "$dir/installplan.json"
  jq -e --slurpfile sub "$dir/subscription.json" '
    .metadata.uid == $sub[0].status.installPlanRef.uid and
    .status.phase == "RequiresApproval" and .spec.approved == false and
    (.status.plan | length > 0)' "$dir/installplan.json"
  jq -e '.spec.installPlanApproval == "Manual" and
         .spec.source == "redhat-operators" and
         .spec.sourceNamespace == "openshift-marketplace"' "$dir/subscription.json"
  if test -n "$expected"; then
    jq -e --arg csv "$expected" '.spec.startingCSV == $csv' "$dir/subscription.json"
    jq -e --arg csv "$expected" '.spec.clusterServiceVersionNames | index($csv) != null' "$dir/installplan.json"
  fi
  jq '{csvs:.spec.clusterServiceVersionNames,bundles:.status.bundleLookups,plan:.status.plan}' \
    "$dir/installplan.json" > "$dir/review.json"
  cat "$dir/review.json"
  sha256sum "$dir/review.json" > "$dir/review.sha256"
  printf 'namespace=%s\nsubscription=%s\nplan=%s\n' "$namespace" "$sub" "$plan"
}
snapshot_plan ansible-aap dev-ansible-automation-platform "$AAP_CSV"
```

Review `review.json`, including every dependency, registry image, and CSV, against the approved mirrored catalog. Record acceptance out of band. Do not auto-generate an approval based only on one matching CSV.

After review, approve from the same frozen snapshot:

```bash
approve_plan() {
  local namespace="$1" sub="$2" dir="$EVIDENCE/plan-$1-$2" plan
  plan="$(jq -r '.metadata.name' "$dir/installplan.json")"
  oc get subscription "$sub" -n "$namespace" -o json > "$dir/subscription-now.json"
  jq -e --slurpfile old "$dir/subscription.json" '
    .metadata.uid == $old[0].metadata.uid and
    .spec == $old[0].spec and
    .status.installPlanRef == $old[0].status.installPlanRef' \
    "$dir/subscription-now.json"
  oc get installplan "$plan" -n "$namespace" -o json > "$dir/installplan-now.json"
  jq -e --slurpfile old "$dir/installplan.json" '
    .metadata.uid == $old[0].metadata.uid and
    .spec == $old[0].spec and
    .status.bundleLookups == $old[0].status.bundleLookups and
    .status.plan == $old[0].status.plan' "$dir/installplan-now.json"
  jq '[
    {op:"test",path:"/metadata/uid",value:.metadata.uid},
    {op:"test",path:"/metadata/resourceVersion",value:.metadata.resourceVersion},
    {op:"test",path:"/spec/clusterServiceVersionNames",value:.spec.clusterServiceVersionNames},
    {op:"test",path:"/spec/approved",value:false},
    {op:"replace",path:"/spec/approved",value:true}
  ]' "$dir/installplan-now.json" > "$dir/approval.json"
  oc patch installplan "$plan" -n "$namespace" --type=json \
    --patch-file="$dir/approval.json"
  oc get installplan "$plan" -n "$namespace" -o json > "$dir/approved.json"
}
approve_plan ansible-aap dev-ansible-automation-platform
wait_csv ansible-aap dev-ansible-automation-platform "$AAP_CSV"
wait_condition crd/ansibleautomationplatforms.aap.ansible.com --for=condition=Established
```

A rejected test means the reviewed state changed. Stop, take a new snapshot and repeat the full review. Do not drop tests or switch to merge patch. If an adopted installation is already approved and `Succeeded`, record its evidence and do not call `approve_plan` again. Kubernetes cannot atomically patch a Subscription and its InstallPlan together; the maintenance window and immutable catalog handoff are still necessary.

For in-cluster Kafka:

```bash
if test "$KAFKA_PROVIDER" = in-cluster; then
  apply_manifest "$EVIDENCE/manifests/operator-amq-streams.yaml"
  snapshot_plan osac-kafka amq-streams
fi
```

After separate review of that plan, run:

```bash
if test "$KAFKA_PROVIDER" = in-cluster; then
  approve_plan osac-kafka amq-streams
  wait_csv osac-kafka amq-streams
  wait_condition crd/kafkas.kafka.strimzi.io --for=condition=Established
  oc get crd kafkas.kafka.strimzi.io -o json \
    | jq -e 'any(.spec.versions[]; .name == "v1" and .served == true)'
fi
```

Do not assign an unverified fixed AMQ CSV from another OSAC release. Record the reviewed resolved CSV. Missing `kafka.strimzi.io/v1` blocks the in-cluster broker path; do not silently substitute an older API. External Kafka skips both AMQ installation blocks and uses the [canonical Kafka guide](../kafka-configuration.md).

### Install selected VMaaS operators

```bash
if test "$ENABLE_VMAAS" = true; then
  apply_manifest "$EVIDENCE/manifests/operator-kubevirt-hyperconverged.yaml"
  wait_csv openshift-cnv kubevirt-hyperconverged
  wait_condition crd/hyperconvergeds.hco.kubevirt.io --for=condition=Established
fi
if test "$WITH_LVMS" = true; then
  apply_manifest "$EVIDENCE/manifests/operator-lvms-operator.yaml"
  wait_csv openshift-storage lvms-operator
  wait_condition crd/lvmclusters.lvm.topolvm.io --for=condition=Established
fi
if test "$WITH_METALLB" = true; then
  apply_manifest "$EVIDENCE/manifests/operator-metallb-operator.yaml"
  wait_csv metallb-system metallb-operator
  for crd in metallbs.metallb.io ipaddresspools.metallb.io l2advertisements.metallb.io; do
    wait_condition crd/"$crd" --for=condition=Established
  done
fi
```

Verify each OwnNamespace OperatorGroup has exactly its installation namespace in `spec.targetNamespaces`. Namespace-specific CSV checks are authoritative; copied CSVs can be disabled. A successful operator installation does not prove the operand is ready. Continue to Section 3 only when all selected CSV/API gates pass.

## 3. Phase 1b: Configure Shared Infrastructure

### Apply the twelve-stage dependency order

Do not apply a large directory and assume write order equals readiness. Run these stages serially; Sections 3 and 4 supply the concrete procedures.

1. Confirm release/profile, ownership and evidence from Section 1.
2. Create the selected namespaces, recording whether each is new or adopted.
3. Prepare only the ServiceAccounts and RBAC needed by the selected controllers or verification Jobs.
4. Prepare non-sensitive configuration and protected credential inputs; verify key names without printing values.
5. Prepare selected storage in Section 4 before any workload that consumes its PVCs.
6. Install or adopt trust-manager, after cert-manager's CRDs and webhook are ready.
7. Establish the approved CA/issuer chain; wait for Certificate Secrets.
8. Verify Services, endpoints and webhook CA injection before creating dependent custom resources.
9. Prepare/adopt the Keycloak and database services; verify their production contracts and routes.
10. Configure selected network operands and establish the disconnected AAP Git source below.
11. Complete the applicable seven bootstrap responsibilities in the order recorded below, preserving evidence.
12. Run Section 5 and publish the Phase 1 completion record. Do not create the OSAC release here.

For every stage, record `passed`, `skipped` with a concrete provider reason, or `failed`. `Skipped` is not acceptable for an active profile dependency.

The development reference inventory contains approximately 88 objects, including bundled database and secret-store fixtures. That is not a resource-count acceptance test for this production path. An external service, adopted controller or disabled profile removes objects from the guide-owned inventory. Conversely, the persistent bastion Git service is part of the operational inventory even though it is not a Kubernetes object.

### Create namespaces and prepare source material

Use an approved source checkout or transferred archive of tag `osac/v0.0.24` for the static trust-manager manifest. The installed OSAC chart is still the artifact prepared by `prereqs.md`; source code is not a replacement Helm artifact.

```bash
test -f "$SOURCE_ROOT/osac-installer/prerequisites/trust-manager.yaml"
git -C "$SOURCE_ROOT" rev-parse 'osac/v0.0.24^{commit}' > "$EVIDENCE/source-commit.txt"
test "$(git -C "$SOURCE_ROOT" rev-parse HEAD)" = "$(cat "$EVIDENCE/source-commit.txt")"
for namespace in "$NS" cert-manager; do
  oc create namespace "$namespace" --dry-run=client -o yaml \
    | oc apply --server-side --field-manager=osac-disconnected-phase1 -f -
done
```

For an adopted Keycloak or database, do not create a competing instance or change its namespace. Record the external service's ownership and evidence instead. The `ansible-aap` namespace is the operator namespace; it is not proof of where an AAP instance runs.

### Install or adopt trust-manager v0.20.0

If a compatible trust-manager is already installed, inspect its image, trust namespace, Bundle CRD, serving certificate, endpoints and current owner. Record adoption and skip installation; do not overwrite its RBAC or cluster-scoped webhook.

For a new installation, copy the release's standalone static manifest into the evidence workspace. This does not install an internal chart. Separate APIs, foundation resources, certificate resources, and the webhook to make readiness explicit:

```bash
cp "$SOURCE_ROOT/osac-installer/prerequisites/trust-manager.yaml" \
  "$EVIDENCE/manifests/trust-manager-source.yaml"
sha256sum "$EVIDENCE/manifests/trust-manager-source.yaml" \
  > "$EVIDENCE/trust-manager-source.sha256"

yq ea 'select(.kind != null) | del(.metadata.labels."app.kubernetes.io/managed-by")' \
  "$EVIDENCE/manifests/trust-manager-source.yaml" \
  > "$EVIDENCE/manifests/trust-manager.yaml"
yq ea 'select(.kind == "CustomResourceDefinition")' \
  "$EVIDENCE/manifests/trust-manager.yaml" > "$EVIDENCE/manifests/trust-crd.yaml"
yq ea 'select(.kind != null and .kind != "CustomResourceDefinition" and
              .kind != "Certificate" and .kind != "Issuer" and
              .kind != "ValidatingWebhookConfiguration")' \
  "$EVIDENCE/manifests/trust-manager.yaml" > "$EVIDENCE/manifests/trust-foundation.yaml"
yq ea 'select(.kind == "Issuer" or .kind == "Certificate")' \
  "$EVIDENCE/manifests/trust-manager.yaml" > "$EVIDENCE/manifests/trust-certificate.yaml"
yq ea 'select(.kind == "ValidatingWebhookConfiguration")' \
  "$EVIDENCE/manifests/trust-manager.yaml" > "$EVIDENCE/manifests/trust-webhook.yaml"
```

The static manifest uses controller `quay.io/jetstack/trust-manager:v0.20.0` and package `quay.io/jetstack/trust-pkg-debian-bookworm:20230311-deb12u1.1`. Verify both exact references are covered by the Doc 1 handoff. An upstream name covered by IDMS/ITMS is not public-network access; an uncovered image blocks this step.

```bash
apply_manifest "$EVIDENCE/manifests/trust-crd.yaml"
wait_condition crd/bundles.trust.cert-manager.io --for=condition=Established
apply_manifest "$EVIDENCE/manifests/trust-foundation.yaml"
apply_manifest "$EVIDENCE/manifests/trust-certificate.yaml"
wait_condition certificate/trust-manager -n cert-manager --for=condition=Ready
oc get secret trust-manager-tls -n cert-manager -o json \
  | jq -e '.data | has("tls.crt") and has("tls.key")'
timeout 660s oc rollout status deployment/trust-manager -n cert-manager --timeout=10m
oc get endpoints trust-manager -n cert-manager -o json \
  | jq -e 'any(.subsets[]?; (.addresses // [] | length) > 0)'
apply_manifest "$EVIDENCE/manifests/trust-webhook.yaml"
```

Wait for webhook CA injection before creating a Bundle:

```bash
end=$((SECONDS+300))
until oc get validatingwebhookconfiguration trust-manager -o json \
  | jq -e 'all(.webhooks[]; (.clientConfig.caBundle // "" | length) > 0)' >/dev/null; do
  (( SECONDS < end )) || { echo 'Trust webhook CA injection failed' >&2; exit 1; }
  sleep 5
done
```

A missing endpoint, certificate or injected CA remains a Phase 1 trust-manager failure. Do not disable webhook validation or retry Bundle writes indefinitely.

### Establish the approved issuer and trust bundle

Production may adopt an approved enterprise ClusterIssuer. If the site explicitly approves an OSAC-local CA, the following is the release's local-CA shape. Do not replace an existing `default-ca` issuer/Secret with a newly generated root.

```bash
cat > "$EVIDENCE/manifests/osac-ca-issuer.yaml" <<'EOF'
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: selfsigned-issuer
  namespace: cert-manager
spec:
  selfSigned: {}
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: default-ca-cert
  namespace: cert-manager
spec:
  isCA: true
  commonName: default-ca
  secretName: default-ca
  privateKey:
    algorithm: RSA
    size: 4096
  issuerRef:
    name: selfsigned-issuer
    kind: Issuer
---
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: default-ca
spec:
  ca:
    secretName: default-ca
EOF
```

For the approved new local CA only:

```bash
apply_manifest "$EVIDENCE/manifests/osac-ca-issuer.yaml"
wait_condition certificate/default-ca-cert -n cert-manager --for=condition=Ready
wait_condition clusterissuer/default-ca --for=condition=Ready
```

For an enterprise issuer, use its approved manifest and name instead, then run the equivalent `Ready` check. The issuer name used by the Phase 2 values must match this choice. Supply the **public** CA chain including database, identity, Kafka, Vault/OpenBao and AAP trust anchors to the trust-manager trust namespace. Do not copy issuer private keys.

```bash
export CA_BUNDLE_FILE='<absolute-path-to-approved-combined-public-ca-bundle.pem>'
test -s "$CA_BUNDLE_FILE"
openssl crl2pkcs7 -nocrl -certfile "$CA_BUNDLE_FILE" \
  | openssl pkcs7 -print_certs -noout >/dev/null
oc create configmap osac-source-ca -n cert-manager \
  --from-file=bundle.pem="$CA_BUNDLE_FILE" --dry-run=client -o yaml \
  > "$EVIDENCE/manifests/osac-source-ca.yaml"
apply_manifest "$EVIDENCE/manifests/osac-source-ca.yaml"
oc label namespace "$NS" osac.openshift.io/phase1-trust=true --overwrite
```

For a dedicated namespace already approved for this trust distribution, create the Bundle:

```bash
cat > "$EVIDENCE/manifests/osac-ca-bundle.yaml" <<'EOF'
apiVersion: trust.cert-manager.io/v1alpha1
kind: Bundle
metadata:
  name: ca-bundle
spec:
  sources:
    - configMap:
        name: osac-source-ca
        key: bundle.pem
  target:
    configMap:
      key: bundle.pem
    namespaceSelector:
      matchLabels:
        osac.openshift.io/phase1-trust: "true"
EOF
apply_manifest "$EVIDENCE/manifests/osac-ca-bundle.yaml"
wait_bundle
oc get configmap ca-bundle -n "$NS" -o json \
  | jq -e '.data["bundle.pem"] | contains("BEGIN CERTIFICATE")'
```

Add only approved namespaces whose workloads need this chain. If `Bundle/ca-bundle` already belongs to another installation, do not narrow its selector or replace its sources; have the owner extend it and record adoption. Do not make two Bundles compete for the same target ConfigMap. Certificate rotation later must update the governed source chain and repeat the downstream trust checks.

### Prepare the AAP Git source on the bastion

AAP must fetch the full OSAC monorepo, including `osac-aap/` playbook paths. Mirroring an execution-environment image does not deliver that Git content. An existing approved internal Git server can meet the same endpoint/commit contract; otherwise use the controlled bastion procedure below.

> [!WARNING]
> The `git://` service below is anonymous and has no transport authentication or encryption. The site must approve this source-delivery path, restrict ingress to the actual cluster egress range, and verify transferred content through a trusted checksum/provenance channel. If that is not allowed, adopt the site's approved Git service; do not expose the daemon publicly or open the port to all sources.

#### Extract the immutable source commit from the actual chart

Run on the administration host against the chart downloaded in `prereqs.md`, before changing its defaults:

```bash
helm show values "$OSAC_CHART_DIR" > "$EVIDENCE/released-chart-values.yaml"
export AAP_COMMIT="$(yq -r '.aap.configAsCode.projectGitBranch' "$EVIDENCE/released-chart-values.yaml")"
printf '%s\n' "$AAP_COMMIT" | grep -Eq '^[0-9a-f]{40}$'
export AAP_RELEASE_REF="refs/heads/osac-aap-${OSAC_VERSION}-${AAP_COMMIT:0:12}"
export AAP_GIT_URL='git://<bastion-internal-ip>:9418/osac.git'
export AAP_EE_IMAGE='<mirror-resident-git-capable-osac-aap-execution-environment-image>'
sha256sum "$OSAC_CHART_DIR/Chart.yaml" "$EVIDENCE/released-chart-values.yaml" \
  > "$EVIDENCE/aap-chart-input.sha256"
```

Do not infer `AAP_COMMIT` from an image tag or copy a different release’s commit. The source-tag defaults are not a substitute for the actual release chart. Keep `projectGitBranch` set to the full commit; the release-specific branch makes that commit fetchable and auditable.

#### Create and transfer the full bundle

On the approved connected source workstation, synchronize the public monorepo using the site's approved process, then run:

```bash
export CONNECTED_OSAC_REPO='<absolute-path-to-approved-monorepo-clone>'
git -C "$CONNECTED_OSAC_REPO" cat-file -e "${AAP_COMMIT}^{commit}"
test "$(git -C "$CONNECTED_OSAC_REPO" cat-file -t "${AAP_COMMIT}:osac-aap")" = tree
# Ensure the pinned commit is included even if no ordinary branch currently names it.
git -C "$CONNECTED_OSAC_REPO" update-ref "$AAP_RELEASE_REF" "$AAP_COMMIT"
git -C "$CONNECTED_OSAC_REPO" bundle create "$PWD/osac-${OSAC_VERSION}.bundle" --all
sha256sum "osac-${OSAC_VERSION}.bundle" > "osac-${OSAC_VERSION}.bundle.sha256"
```

Use a clean, reviewed source clone; `--all` includes every local ref, not just one release. Transfer the bundle and checksum over the approved channel to the bastion. Verify the expected checksum through the independent trusted channel; a checksum downloaded beside an untrusted bundle is not authentication. Transfer the chart-input evidence and the exact `AAP_COMMIT`/`AAP_RELEASE_REF` values too.

#### Install packages and stage the bare repository

Run the following as the bastion administrator. Set these variables again in that session:

```bash
export OSAC_VERSION=0.0.24
export AAP_COMMIT='<full-chart-derived-40-character-commit>'
export AAP_RELEASE_REF="refs/heads/osac-aap-${OSAC_VERSION}-${AAP_COMMIT:0:12}"
export BASTION_IP='<bastion-internal-ip>'
export CLUSTER_EGRESS_CIDR='<approved-ipv4-cluster-egress-cidr>'
export FIREWALL_ZONE='<active-site-firewalld-zone>'
export BUNDLE_FILE='<absolute-path-to-verified-osac-0.0.24.bundle>'
export EXPECTED_BUNDLE_SHA256='<64-character-sha256-from-independent-approved-channel>'
printf '%s\n' "$AAP_COMMIT" | grep -Eq '^[0-9a-f]{40}$'
printf '%s\n' "$EXPECTED_BUNDLE_SHA256" | grep -Eq '^[0-9a-f]{64}$'
test "$(sha256sum "$BUNDLE_FILE" | awk '{print $1}')" = "$EXPECTED_BUNDLE_SHA256"
sudo dnf install -y git git-daemon
test -x /usr/bin/git
if ! id osacgit >/dev/null 2>&1; then
  sudo useradd --system --home-dir /srv/git --shell /sbin/nologin osacgit
fi
sudo install -d -o osacgit -g osacgit -m 0750 /srv/git
```

The package transaction must use the site's approved repositories. If package retrieval fails, stop; do not change repository trust or use an arbitrary installer.

Serialize repository publication, including upgrades. Readers may remain active because release refs are immutable, but no two writers may replace the bare repository or the same ref:

```bash
sudo flock /run/lock/osacgit-publish.lock bash -s -- \
  "$BUNDLE_FILE" "$AAP_COMMIT" "$AAP_RELEASE_REF" <<'HOST'
set -euo pipefail
bundle="$1" commit="$2" ref="$3"
if ! test -d /srv/git/osac.git; then
  # Clone to a private sibling; do not export a partially cloned repository.
  stage="$(mktemp -d /srv/git/.staging-XXXXXX)"
  trap 'rm -rf "$stage"' EXIT
  git clone --bare "$bundle" "$stage/osac.git"
  git -C "$stage/osac.git" bundle verify "$bundle"
  git -C "$stage/osac.git" fsck --full
  git -C "$stage/osac.git" cat-file -e "${commit}^{commit}"
  test "$(git -C "$stage/osac.git" cat-file -t "${commit}:osac-aap")" = tree
  chown -R osacgit:osacgit "$stage/osac.git"
  mv "$stage/osac.git" /srv/git/osac.git
else
  # Do not fetch into an existing server blindly. Verify this release already exists.
  runuser -u osacgit -- git -C /srv/git/osac.git cat-file -e "${commit}^{commit}"
  test "$(runuser -u osacgit -- git -C /srv/git/osac.git cat-file -t "${commit}:osac-aap")" = tree
fi
current="$(runuser -u osacgit -- git -C /srv/git/osac.git rev-parse --verify "$ref" 2>/dev/null || :)"
if test -z "$current"; then
  runuser -u osacgit -- git -C /srv/git/osac.git update-ref "$ref" "$commit" \
    0000000000000000000000000000000000000000
else
  test "$current" = "$commit"
fi
# Optional first-install compatibility alias: never repoint it during an upgrade.
alias_ref=refs/heads/osac-aap-release
alias_sha="$(runuser -u osacgit -- git -C /srv/git/osac.git rev-parse --verify "$alias_ref" 2>/dev/null || :)"
if test -z "$alias_sha"; then
  runuser -u osacgit -- git -C /srv/git/osac.git update-ref "$alias_ref" "$commit" \
    0000000000000000000000000000000000000000
fi
runuser -u osacgit -- touch /srv/git/osac.git/git-daemon-export-ok
HOST
```

If an existing server lacks the requested commit, stop and plan a separately reviewed additive fetch under the same lock. Do not delete the repository, move an immutable release ref, or repoint the compatibility alias to pass a test. Preserve refs used by existing OSAC installations.

#### Configure the read-only service and firewall

Check that TCP 9418 is not already owned by another service before creating the unit. The following unit exports only repositories with `git-daemon-export-ok`; it does not use `--export-all`:

```bash
sudo tee /etc/systemd/system/osac-git-daemon.service >/dev/null <<EOF
[Unit]
Description=Read-only OSAC source delivery
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=osacgit
Group=osacgit
ExecStart=/usr/bin/git daemon --reuseaddr --verbose --base-path=/srv/git --listen=${BASTION_IP} --port=9418 --disable=receive-pack --forbid-override=receive-pack /srv/git
Restart=on-failure
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadOnlyPaths=/srv/git

[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable --now osac-git-daemon.service
sudo systemctl is-active --quiet osac-git-daemon.service
sudo ss -lntp '( sport = :9418 )'
```

The listener must bind the internal bastion address, not all public interfaces. Keep SELinux enforcing; inspect service/audit logs if policy denies access and use a site-reviewed policy correction instead of disabling enforcement.

For a firewalld-managed IPv4 bastion, add the approved source range only. Other firewall managers and IPv6 use the site's equivalent reviewed rule:

```bash
rule="rule family=\"ipv4\" source address=\"${CLUSTER_EGRESS_CIDR}\" port port=\"9418\" protocol=\"tcp\" accept"
sudo firewall-cmd --zone="$FIREWALL_ZONE" --add-rich-rule="$rule"
sudo firewall-cmd --permanent --zone="$FIREWALL_ZONE" --add-rich-rule="$rule"
sudo firewall-cmd --zone="$FIREWALL_ZONE" --query-rich-rule="$rule"
```

Do not add a global `--add-port=9418/tcp` rule. Review existing zone/services/rich rules to ensure another rule has not already opened this port broadly. Record service configuration checksum, repository refs, allowed egress range and firewall evidence without host credentials.

#### Probe from the disconnected cluster

Return to the administration session. The mirrored EE image must contain Git and be in the approved mirror inventory; do not use a public probe image.

```bash
export GIT_PROBE="aap-git-probe-$(date -u +%s)-$(openssl rand -hex 3)"
oc run "$GIT_PROBE" -n "$NS" --restart=Never --image="$AAP_EE_IMAGE" \
  --overrides='{"spec":{"activeDeadlineSeconds":120,"automountServiceAccountToken":false}}' \
  --env="AAP_GIT_URL=$AAP_GIT_URL" --env="AAP_RELEASE_REF=$AAP_RELEASE_REF" \
  --command -- /bin/sh -ec 'git ls-remote --exit-code "$AAP_GIT_URL" "$AAP_RELEASE_REF"'

probe_rc=0
timeout 150s oc wait pod/"$GIT_PROBE" -n "$NS" \
  --for=jsonpath='{.status.phase}'=Succeeded --timeout=140s || probe_rc=$?
oc logs "$GIT_PROBE" -n "$NS" > "$EVIDENCE/git-probe-output.txt" 2> "$EVIDENCE/git-probe-log-error.txt" || probe_rc=1
oc get pod "$GIT_PROBE" -n "$NS" -o json \
  | jq '{metadata:{name:.metadata.name,namespace:.metadata.namespace,uid:.metadata.uid},
         spec:{containers:[.spec.containers[]|{name,image}]},status}' \
  > "$EVIDENCE/git-probe-status.json"
test "$probe_rc" -eq 0
advertised="$(awk -v ref="$AAP_RELEASE_REF" '$2==ref {print $1}' "$EVIDENCE/git-probe-output.txt")"
test "$advertised" = "$AAP_COMMIT"
printf 'url=%s\nref=%s\nexpected_sha=%s\nadvertised_sha=%s\nprobe=%s/%s\nverified_at=%s\n' \
  "$AAP_GIT_URL" "$AAP_RELEASE_REF" "$AAP_COMMIT" "$advertised" "$NS" "$GIT_PROBE" "$(date -u +%FT%TZ)" \
  > "$EVIDENCE/aap-git-handoff.txt"
oc delete pod "$GIT_PROBE" -n "$NS" --wait=true
```

A timeout routes to DNS, firewall or actual cluster-egress ownership. A different SHA routes to source publication. A missing `osac-aap/` tree routes to bundle/source preparation. A failed probe is retained until logs and events are captured; do not clean it up automatically on failure.

Set the actual chart values without changing the source pin:

```bash
export AAP_GIT_URL AAP_COMMIT
yq -i '.aap.configAsCode.projectGitUri = strenv(AAP_GIT_URL) |
       .aap.configAsCode.projectGitBranch = strenv(AAP_COMMIT)' "$VALUES_FILE"
test "$(yq -r '.aap.configAsCode.projectGitBranch' "$VALUES_FILE")" = "$AAP_COMMIT"
helm lint "$OSAC_CHART_DIR" --values "$VALUES_FILE"
helm template osac "$OSAC_CHART_DIR" -n "$NS" --values "$VALUES_FILE" \
  > "$EVIDENCE/osac-after-git.yaml"
sha256sum "$VALUES_FILE" "$EVIDENCE/osac-after-git.yaml" > "$EVIDENCE/phase1-values.sha256"
```

This intentional values change supersedes the earlier values checksum. Retain both old and new checksums with the change reason; repeat the prerequisite render/image/Secret-reference review before completion. The cluster does not fetch GitHub. Actual AAP project synchronization and its synchronized SHA are Phase 2 acceptance, not a claim made by this generic pod probe.

### Account for the seven bootstrap responsibilities

The reference implementation's seven familiar bootstrap Jobs are **conditional**, and their numeric weights are scheduling hints, not API dependencies. The inspected 0.0.24 ordering is:

| Order | Responsibility | This production procedure |
| --- | --- | --- |
| 10 | `configure-kafka` | In-cluster only: complete canonical broker procedure and evidence in Section 4; external Kafka records no broker installation |
| 10 | `configure-metallb` | With MetalLB only: apply and verify the approved operands in Section 4 |
| 12 | `apply-ca-bundle` | Complete the explicit certificate/webhook/Bundle gate above |
| 15 | `wait-keycloak` | Verify adopted production identity route, discovery and issuer in Section 4 |
| 16 | `keycloak-set-passwords` | Demo-user fixture only; skipped in production, never reset example users/passwords |
| 17 | `create-creds` | Create/adopt `fulfillment-controller-credentials` from the governed client contract in Section 4 |
| 19 | `create-ui-backend-creds` | Create/adopt `osac-ui-backend-credentials` from that same contract |

Run the explicit commands in this document instead of installing hook Jobs with broad cluster credentials. Record the responsibility's outcome and evidence. Do not call skipped fixture Jobs successful. This preserves dependency outcomes without running an internal chart or copying the Kafka/Vault configuration workflows.

For any site-owned Job used to implement a canonical prerequisite, use a unique execution name and retain its UID, input checksum, image, start/completion times, conditions, pod exit codes, and protected logs. Do not infer success from existence or a `Running` pod:

```bash
export STAGE_JOB_NAMESPACE='<namespace-of-applicable-site-job>'
export STAGE_JOB_NAME='<unique-name-of-applicable-site-job>'
job_rc=0
timeout 660s oc wait job/"$STAGE_JOB_NAME" -n "$STAGE_JOB_NAMESPACE" \
  --for=condition=Complete --timeout=10m || job_rc=$?
oc get job "$STAGE_JOB_NAME" -n "$STAGE_JOB_NAMESPACE" -o json \
  | jq '{metadata:{name:.metadata.name,namespace:.metadata.namespace,uid:.metadata.uid},status}' \
  > "$EVIDENCE/jobs/$STAGE_JOB_NAME.json"
oc logs job/"$STAGE_JOB_NAME" -n "$STAGE_JOB_NAMESPACE" \
  > "$EVIDENCE/jobs/$STAGE_JOB_NAME.log" 2>&1 || job_rc=1
test "$job_rc" -eq 0
```

Protect these logs: a third-party Job may log secrets even when this guide does not. A failed credential job must not be blindly rerun: determine whether it created the Secret or changed the service-side credential first. Resume from the first unverified dependency; do not replay all earlier writes.

Worked failure: if Bundle creation fails because the trust-manager webhook has no endpoints, stop before identity/credential consumers. Save Certificate status, webhook CA state, endpoints, events and controller logs. Correct the certificate or controller owner, repeat the endpoint and CA-injection checks, then reapply only the reviewed Bundle. Never delete the issuer CA or disable admission to make the request pass.

## 4. Phase 1b: Configure Supported Operands

### Identity: adopt a production Keycloak realm

Use the realm and client contracts prepared in `prereqs.md` and the [customer installation guide](../customer-install-guide.md). The issuer must identify the same realm for UI, Fulfillment Service, operator and Vault/OpenBao. This production procedure does not install the development Keycloak database, import demonstration tenant users, or run `keycloak-set-passwords` against production users.

The identity administrator provisions the governed `osac-controller`, identity-administration and `osac-ui-backend` clients and supplies their secrets through protected files. Retain the prepared realm export/configuration and issuer evidence with the service owner. Do not generate unrelated local credentials and assume Keycloak accepts them.

```bash
export OIDC_ISSUER='<https://internal-keycloak.example/realms/osac>'
export IDP_CA_FILE='<absolute-path-to-approved-identity-ca.pem>'
export CONTROLLER_CLIENT_ID=osac-controller
export CONTROLLER_CLIENT_SECRET_FILE='<protected-controller-client-secret-file>'
export UI_BACKEND_CLIENT_ID=osac-ui-backend
export UI_BACKEND_CLIENT_SECRET_FILE='<protected-ui-backend-client-secret-file>'

test -s "$IDP_CA_FILE"
test -s "$CONTROLLER_CLIENT_SECRET_FILE"
test -s "$UI_BACKEND_CLIENT_SECRET_FILE"
curl --fail --silent --show-error --cacert "$IDP_CA_FILE" \
  "$OIDC_ISSUER/.well-known/openid-configuration" > "$EVIDENCE/oidc-discovery.json"
jq -e --arg issuer "$OIDC_ISSUER" '
  .issuer == $issuer and (.token_endpoint|length>0) and (.jwks_uri|length>0)' \
  "$EVIDENCE/oidc-discovery.json"
jwks_uri="$(jq -r '.jwks_uri' "$EVIDENCE/oidc-discovery.json")"
curl --fail --silent --show-error --cacert "$IDP_CA_FILE" "$jwks_uri" \
  | jq -e '.keys | length > 0'
```

All endpoints must be internal/site-approved; discovery that redirects clients to a public identity service fails the disconnected contract. Retain approved connectivity evidence from an OSAC-namespace workload as well as from the bastion. Set `IDENTITY_CLUSTER_EVIDENCE` to that approved evidence file and verify it is nonempty before completion. If the service is not already prepared, remain in Phase 1 and have its owner complete the canonical identity contract. Do not deploy test fixtures to bypass the gate.

Check the controller's service-account client authentication without logging the token:

```bash
credential_test_dir="$(mktemp -d)"
chmod 0700 "$credential_test_dir"
token_endpoint="$(jq -r '.token_endpoint' "$EVIDENCE/oidc-discovery.json")"
curl --fail --silent --show-error --cacert "$IDP_CA_FILE" \
  --data-urlencode grant_type=client_credentials \
  --data-urlencode "client_id=$CONTROLLER_CLIENT_ID" \
  --data-urlencode "client_secret@$CONTROLLER_CLIENT_SECRET_FILE" \
  "$token_endpoint" > "$credential_test_dir/token.json"
jq -e '(.access_token // "" | length) > 0' "$credential_test_dir/token.json" >/dev/null
rm -rf "$credential_test_dir"
printf 'issuer=%s\ncontroller_client=%s\nauthenticated_at=%s\n' \
  "$OIDC_ISSUER" "$CONTROLLER_CLIENT_ID" "$(date -u +%FT%TZ)" \
  > "$EVIDENCE/controller-authentication.txt"
unset token_endpoint
```

A token alone does not prove OSAC authorization: the identity administrator must also verify the release's role mappings. Do not put tokens into evidence or pass them as inline shell arguments in later commands.

Create the two runtime credential Secrets from protected files. Existing Secrets are adoption/rotation decisions; never overwrite a newer credential because an older local file still exists:

```bash
create_client_secret() {
  local name="$1" client="$2" file="$3" remote_hash file_hash
  if oc get secret "$name" -n "$NS" >/dev/null 2>&1; then
    oc get secret "$name" -n "$NS" -o json | jq -e --arg client "$client" '
      (.data["client-id"] | @base64d) == $client and
      (.data["client-secret"] | length > 0)'
    remote_hash="$(oc get secret "$name" -n "$NS" -o json \
      | jq -j '.data["client-secret"] | @base64d' | sha256sum | awk '{print $1}')"
    file_hash="$(sha256sum "$file" | awk '{print $1}')"
    test "$remote_hash" = "$file_hash"
    echo "Existing $NS/$name matches approved input; not overwritten"
  else
    oc create secret generic "$name" -n "$NS" \
      --from-literal=client-id="$client" --from-file=client-secret="$file"
  fi
}
create_client_secret fulfillment-controller-credentials \
  "$CONTROLLER_CLIENT_ID" "$CONTROLLER_CLIENT_SECRET_FILE"
create_client_secret osac-ui-backend-credentials \
  "$UI_BACKEND_CLIENT_ID" "$UI_BACKEND_CLIENT_SECRET_FILE"
```

For a site-managed Keycloak deployment using the reference `keycloak-client-secrets` Secret, its keys remain under that deployment owner's control. Read and validate only the specific required keys; do not copy the entire source Secret into OSAC. There is no need to create a duplicate Keycloak-side Secret for an external identity service.

Map the runtime references in the released values schema (`service.auth.controllerCredentials`, `service.idp.credentials`, `operator.fulfillment.credentials`, and UI authentication settings) through the values workflow in `prereqs.md`. Confirm those references in the rendered manifest; do not replace the schema's arrays with guessed scalar fields.

### PostgreSQL connection and mTLS resources

The database administrator supplies a provisioned database/user, approved connection URL, CA chain, and client keypair. Use external PostgreSQL; no bundled database is installed here. All files below are protected service-owner inputs, not committed content.

```bash
export POSTGRES_URL_FILE='<protected-file-containing-postgresql-connection-url>'
export DB_CLIENT_CERT_FILE='<protected-db-client-certificate-file>'
export DB_CLIENT_KEY_FILE='<protected-db-client-key-file>'
export DB_CA_FILE='<public-db-ca-chain-file>'
for file in "$POSTGRES_URL_FILE" "$DB_CLIENT_CERT_FILE" "$DB_CLIENT_KEY_FILE" "$DB_CA_FILE"; do
  test -s "$file"
done
openssl x509 -in "$DB_CLIENT_CERT_FILE" -noout -checkend 3600
openssl verify -CAfile "$DB_CA_FILE" "$DB_CLIENT_CERT_FILE"
cert_public="$(openssl x509 -in "$DB_CLIENT_CERT_FILE" -pubkey -noout | openssl pkey -pubin -outform DER | sha256sum)"
key_public="$(openssl pkey -in "$DB_CLIENT_KEY_FILE" -pubout -outform DER | sha256sum)"
test "$cert_public" = "$key_public"
```

Create each Secret only if absent; for adoption, check required keys and obtain service-owner confirmation that the stored credential matches the current database:

```bash
if ! oc get secret osac-db-config -n "$NS" >/dev/null 2>&1; then
  oc create secret generic osac-db-config -n "$NS" --from-file=url="$POSTGRES_URL_FILE"
fi
if ! oc get secret osac-db-client-cert -n "$NS" >/dev/null 2>&1; then
  oc create secret generic osac-db-client-cert -n "$NS" --type=kubernetes.io/tls \
    --from-file=tls.crt="$DB_CLIENT_CERT_FILE" \
    --from-file=tls.key="$DB_CLIENT_KEY_FILE" --from-file=ca.crt="$DB_CA_FILE"
fi
oc get secret osac-db-config -n "$NS" -o json | jq -e '.data.url | length > 0'
oc get secret osac-db-client-cert -n "$NS" -o json \
  | jq -e '.data | has("tls.crt") and has("tls.key") and has("ca.crt")'
```

When metering is selected, create its separately governed `osac-db-metering-config` and `osac-db-metering-client-cert` resources using the same file-based procedure and that service's own credentials. Do not reuse the Fulfillment Service database identity automatically. Record the database owner's authenticated TLS/connectivity evidence and backup/recovery evidence from `prereqs.md`. A missing key or failed database connection blocks Phase 2; Secret existence alone is not a database test.

### Kafka: canonical configuration, disconnected orchestration

[Kafka Configuration](../kafka-configuration.md) is normative for broker configuration, users, ACLs, connection resources, values and verification. Do not copy those schemas or commands here.

For `KAFKA_PROVIDER=external`, no broker or AMQ operator is installed. Verify the canonical-guide handoff resources and current service-owner evidence.

For `KAFKA_PROVIDER=in-cluster`, Section 2 must pass first. The Kafka owner applies its reviewed, mirror-backed broker/node-pool resources following the canonical guide, with the selected persistent storage and internal listeners. Verify the resulting broker and connection contract here; do not run an unreviewed internal chart hook that auto-approves operator upgrades.

```bash
export KAFKA_CREDENTIAL_NAMESPACE="$NS"
export KAFKA_CREDENTIAL_SECRET='<canonical-guide-credential-secret-name>'
export KAFKA_CA_NAMESPACE="$NS"
export KAFKA_CA_CONFIGMAP='<canonical-guide-ca-configmap-name>'
export KAFKA_EVIDENCE='<path-to-current-canonical-kafka-verification-evidence>'
test -s "$KAFKA_EVIDENCE"
oc get secret "$KAFKA_CREDENTIAL_SECRET" -n "$KAFKA_CREDENTIAL_NAMESPACE" -o json \
  | jq -e '.data | type == "object" and length > 0'
oc get configmap "$KAFKA_CA_CONFIGMAP" -n "$KAFKA_CA_NAMESPACE" -o json \
  | jq -e '.data | type == "object" and length > 0'
if test "$KAFKA_PROVIDER" = in-cluster; then
  export KAFKA_CLUSTER='<approved-kafka-cluster-name>'
  wait_condition kafka/"$KAFKA_CLUSTER" -n osac-kafka --for=condition=Ready
  oc get kafka "$KAFKA_CLUSTER" -n osac-kafka -o json \
    | jq '{metadata:{name:.metadata.name,uid:.metadata.uid},status}' \
    > "$EVIDENCE/kafka-status.json"
fi
```

Compare the expected key names with the canonical guide without printing their values. The canonical evidence must include the required cluster-side connectivity checks, not just a reachable bootstrap address. Missing provider configuration returns to that guide and service owner. Missing mirrored operator/operand images returns to Doc 1; disconnected DNS/trust failures return to `prereqs.md`.

### Vault/OpenBao: canonical external service handoff

Production Vault/OpenBao is not installed, initialized, unsealed, backed up or upgraded by this procedure. Follow [Secrets Management Configuration](../secrets-management-configuration.md) for its normative namespace, authentication, policy, role, Secret, values and verification contracts. Bundled OpenBao remains evaluation-only.

```bash
export VAULT_CREDENTIAL_NAMESPACE="$NS"
export VAULT_CREDENTIAL_SECRET='<canonical-guide-credential-secret-name>'
export VAULT_CA_NAMESPACE="$NS"
export VAULT_CA_CONFIGMAP='<canonical-guide-ca-configmap-name>'
export VAULT_EVIDENCE='<path-to-current-canonical-secrets-verification-evidence>'
test -s "$VAULT_EVIDENCE"
oc get secret "$VAULT_CREDENTIAL_SECRET" -n "$VAULT_CREDENTIAL_NAMESPACE" -o json \
  | jq -e '.data | type == "object" and length > 0'
oc get configmap "$VAULT_CA_CONFIGMAP" -n "$VAULT_CA_NAMESPACE" -o json \
  | jq -e '.data | type == "object" and length > 0'
```

Record endpoint identity, referenced resource names and evidence timestamp. Missing role/policy/namespace or identity claims returns to the canonical guide. A disconnected DNS/TLS failure remains with the `prereqs.md` network/trust owner. No Vault command or policy is reproduced here.

### AAP: operator prerequisites now, chart-owned instance later

> [!IMPORTANT]
> The 0.0.24 OSAC chart's AAP subchart creates `AnsibleAutomationPlatform/osac-aap` in the OSAC release namespace when `aap.aap.instance.enabled` is true. Do **not** pre-create that chart-owned object in Phase 1. Its gateway/controller routes, integration workloads and actual project synchronization are verified after Helm in `installation.md`.

Before Phase 2, verify the operator, APIs, source handoff and license input. The instance namespace is `$NS`; the operator namespace is `ansible-aap`.

```bash
oc get csv "$AAP_CSV" -n "$AAP_OPERATOR_NAMESPACE" -o json \
  | jq -e '.status.phase == "Succeeded"'
wait_condition crd/ansibleautomationplatforms.aap.ansible.com --for=condition=Established
test -s "$EVIDENCE/aap-git-handoff.txt"
test "$(yq -r '.aap.configAsCode.projectGitUri' "$VALUES_FILE")" = "$AAP_GIT_URL"
test "$(yq -r '.aap.configAsCode.projectGitBranch' "$VALUES_FILE")" = "$AAP_COMMIT"
```

Check the actual child-chart contract for the manifest Secret. The default name is `config-as-code-manifest-ig`; if the values file overrides it, use that exact name. Supply the subscription manifest ZIP through a protected file:

```bash
export AAP_LICENSE_FILE='<absolute-path-to-approved-license.zip>'
export AAP_MANIFEST_SECRET="$(yq -r '.aap.configAsCode.manifestSecret // "config-as-code-manifest-ig"' "$VALUES_FILE")"
test -s "$AAP_LICENSE_FILE"
if ! oc get secret "$AAP_MANIFEST_SECRET" -n "$NS" >/dev/null 2>&1; then
  oc create secret generic "$AAP_MANIFEST_SECRET" -n "$NS" \
    --from-file=license.zip="$AAP_LICENSE_FILE"
fi
oc label secret "$AAP_MANIFEST_SECRET" -n "$NS" \
  osac.openshift.io/project=osac-aap --overwrite
oc get secret "$AAP_MANIFEST_SECRET" -n "$NS" -o json \
  | jq -e '(.data["license.zip"] // "" | length) > 0 and
           .metadata.labels["osac.openshift.io/project"] == "osac-aap"'
yq -i '.aap.configAsCode.manifest.create = false' "$VALUES_FILE"
```

Do not put ZIP bytes into values. For an existing AAP instance, stop before Helm if the chart would create a competing one. Disabling instance creation is not sufficient by itself: the bootstrap endpoint, credentials and adopted-instance contract must all be reviewed with the AAP owner. This document does not assume that a route in `ansible-aap` is the correct instance route, or select the first route in a namespace.

### VMaaS storage and networking

Run this subsection only for VMaaS. Product references are [LVM Storage](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html-single/storage/index#persistent-storage-using-lvms), [OpenShift Virtualization](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/virtualization/installing), and [MetalLB](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/networking_operators/metallb-operator).

#### Storage

For an existing provider, retain its successful prerequisite PVC/workload evidence. For LVMS, obtain the storage owner's approved device selection first. Do not copy the development reference that consumes all available disks.

```bash
export LVM_DEVICE_PATH='<approved-unused-block-device-path>'
if test "$WITH_LVMS" = true; then
  cat > "$EVIDENCE/manifests/lvmcluster.yaml" <<EOF
apiVersion: lvm.topolvm.io/v1alpha1
kind: LVMCluster
metadata:
  name: lvms-cluster
  namespace: openshift-storage
spec:
  storage:
    deviceClasses:
      - name: vg1
        default: true
        deviceSelector:
          paths:
            - $LVM_DEVICE_PATH
        thinPoolConfig:
          name: thin-pool-1
          sizePercent: 90
          overprovisionRatio: 10
EOF
  timeout 660s oc rollout status deployment/lvms-operator -n openshift-storage --timeout=10m
  apply_manifest "$EVIDENCE/manifests/lvmcluster.yaml"
  end=$((SECONDS+600))
  until oc get storageclass lvms-vg1 >/dev/null 2>&1; do
    (( SECONDS < end )) || { echo 'LVMS StorageClass not created' >&2; exit 1; }
    sleep 5
  done
  export STORAGE_CLASS=lvms-vg1
fi
```

The device selector is a site input: for heterogeneous nodes, have the owner supply a reviewed per-node selection rather than assuming one path is valid everywhere. Confirm the served CRD accepts the selection in the server dry-run. No command above changes the cluster's default StorageClass. Set the intended class explicitly in customer values.

Create a test consumer as well as a PVC, so `WaitForFirstConsumer` storage classes are not misdiagnosed as broken:

```bash
if test "$ENABLE_VMAAS" = true; then
  cat > "$EVIDENCE/manifests/storage-check.yaml" <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: phase1-storage-$RUN_ID
  namespace: $NS
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: $STORAGE_CLASS
  resources:
    requests:
      storage: 1Gi
---
apiVersion: v1
kind: Pod
metadata:
  name: phase1-storage-$RUN_ID
  namespace: $NS
spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  activeDeadlineSeconds: 300
  containers:
    - name: test
      image: $AAP_EE_IMAGE
      command: [/bin/sh, -ec]
      args: ['printf "storage-ok\\n" > /data/result; test -s /data/result']
      volumeMounts:
        - name: data
          mountPath: /data
  volumes:
    - name: data
      persistentVolumeClaim:
        claimName: phase1-storage-$RUN_ID
EOF
  apply_manifest "$EVIDENCE/manifests/storage-check.yaml"
  wait_condition pvc/"phase1-storage-$RUN_ID" -n "$NS" --for=jsonpath='{.status.phase}'=Bound
  wait_condition pod/"phase1-storage-$RUN_ID" -n "$NS" --for=jsonpath='{.status.phase}'=Succeeded
  oc logs "phase1-storage-$RUN_ID" -n "$NS" > "$EVIDENCE/storage-check.log"
fi
```

A Pending PVC with a waiting consumer is not success. Inspect PVC, pod scheduling and provisioner events; remain with the storage owner until both binding and the write test pass. Do not silently install a second storage provider.

#### MetalLB or existing LoadBalancer provider

If MetalLB is selected, the network owner supplies a non-overlapping range and confirms L2 is appropriate. For BGP, use the reviewed product-specific peer/advertisement design instead of the L2 example. Never derive an address pool from a node's IP.

```bash
export METALLB_ADDRESS_RANGE='<approved-first-address-last-address>'
if test "$WITH_METALLB" = true; then
  cat > "$EVIDENCE/manifests/metallb.yaml" <<EOF
apiVersion: metallb.io/v1beta1
kind: MetalLB
metadata:
  name: metallb
  namespace: metallb-system
---
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: osac-address-pool
  namespace: metallb-system
spec:
  addresses:
    - $METALLB_ADDRESS_RANGE
  autoAssign: true
  avoidBuggyIPs: true
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: osac-l2-advertisement
  namespace: metallb-system
spec:
  ipAddressPools: [osac-address-pool]
EOF
  timeout 660s oc rollout status deployment/metallb-operator-controller-manager \
    -n metallb-system --timeout=10m
  timeout 660s oc rollout status deployment/metallb-operator-webhook-server \
    -n metallb-system --timeout=10m
  apply_manifest "$EVIDENCE/manifests/metallb.yaml"
fi
```

Verify actual allocation and reachability with a disposable TCP service. The selected mirror-resident EE must include Python 3:

```bash
if test "$ENABLE_VMAAS" = true; then
  cat > "$EVIDENCE/manifests/loadbalancer-check.yaml" <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: phase1-lb-$RUN_ID
  namespace: $NS
spec:
  replicas: 1
  selector:
    matchLabels:
      phase1-lb-run: $RUN_ID
  template:
    metadata:
      labels:
        phase1-lb-run: $RUN_ID
    spec:
      automountServiceAccountToken: false
      containers:
        - name: server
          image: $AAP_EE_IMAGE
          command: [python3, -m, http.server, "8080", --directory, /tmp]
          ports:
            - containerPort: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: phase1-lb-$RUN_ID
  namespace: $NS
spec:
  type: LoadBalancer
  selector:
    phase1-lb-run: $RUN_ID
  ports:
    - port: 8080
      targetPort: 8080
EOF
  apply_manifest "$EVIDENCE/manifests/loadbalancer-check.yaml"
  timeout 660s oc rollout status deployment/"phase1-lb-$RUN_ID" -n "$NS" --timeout=10m
  wait_condition service/"phase1-lb-$RUN_ID" -n "$NS" --for=jsonpath='{.status.loadBalancer.ingress[0]}'
  address="$(oc get service "phase1-lb-$RUN_ID" -n "$NS" -o json \
    | jq -r '.status.loadBalancer.ingress[0] | .ip // .hostname')"
  test -n "$address" && test "$address" != null
  curl --fail --connect-timeout 10 --max-time 20 "http://$address:8080/" >/dev/null
fi
```

Repeat the client request from the intended consumer network, not just the bastion. An address without successful client traffic is not a passed LoadBalancer gate. For an existing provider, the same test applies without creating MetalLB objects.

#### HyperConverged and CDI

```bash
if test "$ENABLE_VMAAS" = true; then
  cat > "$EVIDENCE/manifests/hyperconverged.yaml" <<'EOF'
apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: openshift-cnv
spec:
  enableCommonBootImageImport: false
EOF
  apply_manifest "$EVIDENCE/manifests/hyperconverged.yaml"
  wait_condition hyperconverged/kubevirt-hyperconverged -n openshift-cnv --for=condition=Available
  oc get hyperconverged kubevirt-hyperconverged -n openshift-cnv -o json | jq -e '
    any(.status.conditions[]; .type == "Available" and .status == "True") and
    any(.status.conditions[]; .type == "Degraded" and .status == "False") and
    any(.status.conditions[]; .type == "Progressing" and .status == "False")'
fi
```

Do not enable common boot-image imports that contact upstream sources. Use the selected DiskImage/containerdisk sources and CDI trust/access path from [VMaaS mirroring](mirroring-vmaas.md). That guide owns `NeverContactSource`, mirror-host/port and fresh import/no-external-contact evidence; do not reproduce or weaken that contract here.

Record a fresh DataVolume/VMI test from that procedure, with image source, importer pod UID, events/logs and final states. Required success is `DataVolume=Succeeded` and `VMI=Running`. A node image pull alone does not prove CDI's separate importer path. Preserve evidence and investigate the owning image/trust/provider boundary if the test fails.

## 5. Readiness Verification

### Re-run selected operator and infrastructure gates

```bash
while IFS=$'\t' read -r namespace sub csv; do
  oc get csv "$csv" -n "$namespace" -o json | jq -e '.status.phase == "Succeeded"'
  oc get subscription "$sub" -n "$namespace" -o json | jq -e '
    .spec.source == "redhat-operators" and
    .spec.sourceNamespace == "openshift-marketplace"'
done < "$EVIDENCE/csvs.tsv"
oc get subscription dev-ansible-automation-platform -n ansible-aap -o json \
  | jq -e --arg pin "$AAP_CSV" '
      .spec.installPlanApproval == "Manual" and .spec.startingCSV == $pin and
      .status.installedCSV == $pin'
if test "$KAFKA_PROVIDER" = in-cluster; then
  oc get subscription amq-streams -n osac-kafka -o json \
    | jq -e '.spec.installPlanApproval == "Manual"'
fi
wait_bundle
oc get configmap ca-bundle -n "$NS" -o json \
  | jq -e '.data["bundle.pem"] | contains("BEGIN CERTIFICATE")'
for name in fulfillment-controller-credentials osac-ui-backend-credentials osac-db-config osac-db-client-cert "$AAP_MANIFEST_SECRET"; do
  oc get secret "$name" -n "$NS" -o json | jq -e '.data | length > 0'
done
```

Keep the original reviewed and approved InstallPlan UIDs/bundle evidence, including adopted-installation evidence. If OLM now references a different plan, do not approve it during the completion check.

For guide-created/adopted workload Deployments and StatefulSets, check exact desired and ready counts rather than pod listings. Run only in namespaces from the ownership inventory:

```bash
for namespace in cert-manager "$NS"; do
  oc get deployment,statefulset -n "$namespace" -o json | jq -e '
    all(.items[];
      (.spec.replicas // 1) as $desired |
      (.status.observedGeneration // 0) >= .metadata.generation and
      (.status.readyReplicas // 0) == $desired)'
done
```

An unrelated existing workload failure is not permission to ignore a failed selected resource; identify ownership explicitly instead of suppressing the check. The OSAC workloads and the chart-owned AAP instance are not expected before Phase 2. Do not install Helm merely to make Phase 1's workload count nonempty.

Verify the Git endpoint/ref again and compare with the same chart-derived commit. A generic probe is not the final AAP synchronized revision. Phase 2 must additionally retain its bootstrap Job, AAP project-update result and actual revision.

### Record each bootstrap responsibility

Create a seven-row responsibility ledger from the outcomes actually verified, not from expected success. Use `passed`, `skipped-external-provider`, `skipped-profile-disabled`, or `skipped-production-fixture`. Any failed or missing active responsibility blocks completion.

```text
responsibility              expected evidence
configure-kafka             canonical Kafka verification; external records no broker creation
configure-metallb           provider allocation and client reachability, or existing provider evidence
apply-ca-bundle             Bundle UID/status and target ca-bundle content identity
wait-keycloak              exact issuer/discovery/JWKS and governed-client evidence
keycloak-set-passwords      skipped-production-fixture
create-creds               governed controller credential keys/authentication evidence
create-ui-backend-creds     governed backend credential keys/identity-owner evidence
```

This ledger intentionally records manual equivalents rather than claiming that development fixture Jobs ran. Store any applicable site Job's actual UID/result/logs in `jobs/`; never fabricate Job UIDs for a direct command.

### Worked failure: unexpected Manual InstallPlan

If AMQ Streams resolves to an unexpected bundle, leave it unapproved. Compare the live Subscription and referenced plan with the saved source/channel, UID, CSV list and bundle list. A missing intended bundle in a healthy catalog returns to Doc 1. A wrong Subscription remains in this document. After correction, take a new snapshot and obtain review again. A JSON Patch test failure means the reviewed state changed; it is not a reason to remove the test. Continue only after the intended CSV is `Succeeded` and its required API is served.

### Final inventory and handoff

For every object created or adopted, add its identity using `record_resource`. Include namespaces, selected OperatorGroups/Subscriptions/CSVs, trust-manager objects, issuer/certificate/Bundle, credential Secret metadata, selected storage/network/virtualization operands and test resources. The Secret recorder extracts only identity metadata, never data.

Record the bastion service separately: listener, systemd checksum, allowed egress range, bundle checksum, full source commit, immutable advertised ref, probe identity and timestamp. Preserve source and actual release-chart identities separately.

The apply helper writes metadata-only inventory candidates. Add the credential Secrets and selected adopted objects, then consolidate repeated observations by UID. Review each row’s ownership column against the entry/change record; replace `unreviewed` with `guide-created`, `adopted` or `temporary`. Do not relabel existing objects as newly created simply because this procedure applied a manifest to them.

```bash
for name in fulfillment-controller-credentials osac-ui-backend-credentials osac-db-config osac-db-client-cert "$AAP_MANIFEST_SECRET"; do
  record_resource secret "$NS" "$name" unreviewed
done
oc get -n "$NS" configmap ca-bundle -o json | jq -r '
  [.kind,.metadata.namespace,.metadata.name,.metadata.uid,.metadata.resourceVersion,"unreviewed"] | @tsv' \
  >> "$EVIDENCE/resources.tsv"
cp "$EVIDENCE/resources.tsv" "$EVIDENCE/resources-observations.tsv"
python3 - "$EVIDENCE/resources.tsv" <<'PY'
import csv, sys
p = sys.argv[1]
with open(p, newline="") as f:
    rows = list(csv.reader(f, delimiter="\t"))
latest = {r[3]: r for r in rows[1:]}
with open(p, "w", newline="") as f:
    w = csv.writer(f, delimiter="\t", lineterminator="\n")
    w.writerow(rows[0])
    w.writerows(sorted(latest.values(), key=lambda r: (r[0], r[1], r[2])))
PY
# Review/edit only the final ownership column; retain the observation copy.
"${EDITOR:-vi}" "$EVIDENCE/resources.tsv"
awk -F '\t' 'NR>1 && $6!="guide-created" && $6!="adopted" && $6!="temporary" {bad=1}
  END {exit bad}' "$EVIDENCE/resources.tsv"
```

Before handing off, render the latest values and repeat the static/image-coverage review in `prereqs.md`:

```bash
helm lint "$OSAC_CHART_DIR" --values "$VALUES_FILE"
helm template osac "$OSAC_CHART_DIR" -n "$NS" --values "$VALUES_FILE" \
  > "$EVIDENCE/osac-ready-for-phase2.yaml"
sha256sum "$VALUES_FILE" "$EVIDENCE/osac-ready-for-phase2.yaml" \
  > "$EVIDENCE/final-render.sha256"
```

Do not require a global server-side dry-run of the entire OSAC render before Phase 2 installs its own CRDs. Server-side dry-runs above validate only the prerequisite resources whose APIs were already established.

Create the responsibility ledger from reviewed evidence before writing the completion record. For an adopted service, supply the owner’s current attestation file as the evidence path. File existence alone does not prove the attestation is true.

```bash
export PROVIDER_NETWORK_EVIDENCE='<path-to-successful-provider-allocation-and-client-test-evidence>'
export IDENTITY_OWNER_EVIDENCE='<path-to-identity-realm-and-client-role-verification-evidence>'
export IDENTITY_CLUSTER_EVIDENCE='<path-to-successful-osac-namespace-identity-connectivity-evidence>'
test -s "$IDENTITY_OWNER_EVIDENCE"
test -s "$IDENTITY_CLUSTER_EVIDENCE"
if test "$ENABLE_VMAAS" = true; then test -s "$PROVIDER_NETWORK_EVIDENCE"; fi
printf 'responsibility\toutcome\tevidence\n' > "$EVIDENCE/bootstrap-responsibilities.tsv"
printf 'configure-kafka\tpassed-%s-provider\t%s\n' "$KAFKA_PROVIDER" "$KAFKA_EVIDENCE" \
  >> "$EVIDENCE/bootstrap-responsibilities.tsv"
if test "$ENABLE_VMAAS" = true; then
  printf 'configure-metallb\tpassed-selected-provider\t%s\n' "$PROVIDER_NETWORK_EVIDENCE" \
    >> "$EVIDENCE/bootstrap-responsibilities.tsv"
else
  printf 'configure-metallb\tskipped-profile-disabled\tVMaaS-disabled\n' \
    >> "$EVIDENCE/bootstrap-responsibilities.tsv"
fi
printf 'apply-ca-bundle\tpassed\t%s/manifests/osac-ca-bundle.yaml\n' "$EVIDENCE" \
  >> "$EVIDENCE/bootstrap-responsibilities.tsv"
printf 'wait-keycloak\tpassed-adopted-service\t%s\n' "$IDENTITY_OWNER_EVIDENCE" \
  >> "$EVIDENCE/bootstrap-responsibilities.tsv"
printf 'keycloak-set-passwords\tskipped-production-fixture\tNo-demo-users-or-password-resets\n' \
  >> "$EVIDENCE/bootstrap-responsibilities.tsv"
printf 'create-creds\tpassed\t%s/controller-authentication.txt\n' "$EVIDENCE" \
  >> "$EVIDENCE/bootstrap-responsibilities.tsv"
printf 'create-ui-backend-creds\tpassed-owner-verified\t%s\n' "$IDENTITY_OWNER_EVIDENCE" \
  >> "$EVIDENCE/bootstrap-responsibilities.tsv"
test "$(wc -l < "$EVIDENCE/bootstrap-responsibilities.tsv")" -eq 8
```

Create the completion record only after every active gate has passed:

```bash
cat > "$EVIDENCE/operators-completion.txt" <<EOF
completed_at=$(date -u +%FT%TZ)
release=$OSAC_VERSION
operator=$(oc whoami)
run=$RUN_ID
namespace=$NS
catalog_source=openshift-marketplace/redhat-operators
aap_operator_namespace=$AAP_OPERATOR_NAMESPACE
aap_operator_csv=$AAP_CSV
aap_instance_owner=phase2-osac-chart
kafka_provider=$KAFKA_PROVIDER
vmaas=$ENABLE_VMAAS
with_lvms=$WITH_LVMS
with_metallb=$WITH_METALLB
storage_class=$STORAGE_CLASS
values_sha256=$(sha256sum "$VALUES_FILE" | awk '{print $1}')
chart_yaml_sha256=$(sha256sum "$OSAC_CHART_DIR/Chart.yaml" | awk '{print $1}')
prereqs_record_sha256=$(sha256sum "$PREREQS_RECORD" | awk '{print $1}')
aap_git_uri=$AAP_GIT_URL
aap_expected_commit=$AAP_COMMIT
aap_immutable_ref=$AAP_RELEASE_REF
aap_git_evidence=aap-git-handoff.txt
aap_actual_sync_verification_owner=installation.md
kafka_canonical_evidence=$KAFKA_EVIDENCE
vault_canonical_evidence=$VAULT_EVIDENCE
inventory=resources.tsv
csv_evidence=csvs.tsv
bootstrap_responsibilities=bootstrap-responsibilities.tsv
EOF

test "$(wc -l < "$EVIDENCE/resources.tsv")" -gt 1
test -s "$EVIDENCE/csvs.tsv"
test -s "$EVIDENCE/bootstrap-responsibilities.tsv"
sha256sum "$EVIDENCE/resources.tsv" "$EVIDENCE/csvs.tsv" \
  "$EVIDENCE/bootstrap-responsibilities.tsv" "$EVIDENCE/operators-completion.txt" \
  > "$EVIDENCE/handoff.sha256"
```

The ledger is an evidence attestation, not automated proof that an operator ran every procedure. The change owner reviews it and the ownership inventory before releasing the maintenance window. Never create an empty evidence file merely to satisfy a file-existence check.

Remove only successful disposable storage/LoadBalancer test objects after capturing evidence; do not delete shared namespaces or adopted resources:

```bash
if test "$ENABLE_VMAAS" = true; then
  oc delete -n "$NS" -f "$EVIDENCE/manifests/storage-check.yaml" \
    -f "$EVIDENCE/manifests/loadbalancer-check.yaml" --wait=true
fi
```

The persistent Git source must remain available for AAP project updates and later fulfillment. Uninstalling an OSAC release does not authorize deleting this server, its refs, external services, trust roots or persistent storage.

- [ ] The prerequisite evidence is release-correct and current.
- [ ] Every selected operator's exact namespace, scope, source, approval mode, CSV and required APIs passed.
- [ ] Every Manual approval retained its reviewed InstallPlan UID and bundle-set evidence.
- [ ] Trust-manager, certificate/webhook and `ca-bundle` gates passed without public CA downloads.
- [ ] Production identity/database/Kafka/Vault contracts and credential references passed.
- [ ] The bastion or adopted Git source serves the full monorepo and the exact chart commit; read-only exposure and ingress restriction are approved.
- [ ] Selected storage, LoadBalancer, HyperConverged and CDI workflows passed.
- [ ] All seven bootstrap responsibilities have a truthful outcome and evidence; production fixture skips are explicit.
- [ ] No chart-owned AAP instance or OSAC Helm release was pre-created here.
- [ ] Final values/render checksums, ownership inventory, logs and completion attestation are retained securely.

Continue to [Install OSAC](installation.md) only after this handoff is approved. A successful `apply`, a listening Git port, or a list of Running pods alone is not acceptance.
