#!/bin/bash
#
# Model tenant VM demo (polished) — OSAC-3921 model-tenant-vm-runbook
#
# Records the full runbook flow: prerequisites -> private admin login ->
# DiskImage -> Keycloak model-user + fulfillment User -> template ->
# catalog item -> model-user login -> ComputeInstance -> ExternalIP
# attachment. The final SSH connect is attempted with a timeout and shown
# failing: known OVN UDN north/south platform gap (deviations item 17).
#
# Usage:
#   ./record-demo.sh --dry-run     # preflight only (no changes)
#   ./record-demo.sh --rehearsal   # full run_demo, no asciinema recording
#   ./record-demo.sh --cleanup     # delete demo resources (keep pool/subnet)
#   ./record-demo.sh               # record with asciinema
#
set -uo pipefail

WORKTREE="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OSAC="${OSAC:-$WORKTREE/fulfillment-service/osac}"
KCFG="${KCFG:-$WORKTREE/.local/doc2-evidence/osac-demo.kubeconfig}"
CA="${CA:-$WORKTREE/.local/doc5-evidence/osac-ca-bundle.pem}"
export KUBECONFIG="$KCFG"

OSAC_API="${OSAC_API:-https://fulfillment-api-osac.apps.osac-demo.pool.se-lab.eng.rdu2.dc.redhat.com}"
OSAC_INTERNAL_API="${OSAC_INTERNAL_API:-https://fulfillment-internal-api-osac.apps.osac-demo.pool.se-lab.eng.rdu2.dc.redhat.com}"
KC_URL="${KC_URL:-https://keycloak-keycloak.apps.osac-demo.pool.se-lab.eng.rdu2.dc.redhat.com}"
KC_REALM="${KC_REALM:-osac}"

INSTANCE_TYPE="${INSTANCE_TYPE:-standard-2-8}"
STORAGE_TIER="${STORAGE_TIER:-local}"
SUBNET_ID="${SUBNET_ID:-01a19c6a-84f6-732e-a335-32570cdb4aac}"
POOL="${POOL:-br-sno-pool}"
MIRROR_REF="${MIRROR_REF:-registry-vm-svc.aap-portal-lab.svc.cluster.local:8443/containerdisks/fedora:latest}"
KEY="${KEY:-$HOME/.ssh/model-user-key}"
VM="${VM:-model-vm}"
EIP="${EIP:-model-vm-ip}"
ATT="${ATT:-model-vm-att}"
SG_CIDR="${SG_CIDR:-192.168.100.0/24}"

CAST_FILE="${CAST_FILE:-model-tenant-vm-demo.cast}"
TITLE="Model tenant VM — OSAC runbook walkthrough"

BOLD='\033[1m'; DIM='\033[2m'; CYAN='\033[1;36m'; GREEN='\033[1;32m'
YELLOW='\033[1;33m'; BLUE='\033[1;34m'; RED='\033[1;31m'; RESET='\033[0m'
TMP="$(mktemp -d)"

# ── Helpers ────────────────────────────────────────────────────────────────
type_cmd() {
  local cmd="$1"
  echo ""
  echo -ne "${GREEN}\$ ${RESET}"
  while IFS= read -r -N 1 c; do
    echo -n "$c"; sleep 0.015
  done <<< "$cmd"
  echo ""; sleep 0.3
}

run() { # run a typed command (fail fast: a failed command aborts the demo)
  type_cmd "$1"; shift
  "$@" || fail "command failed (exit $?): $*"
}

header() {
  echo ""
  echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  echo -e "${CYAN}  $*${RESET}"
  echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  sleep 0.8
}

info() { echo -e "  ${BLUE}ℹ${RESET} $*"; }
ok()   { echo -e "  ${GREEN}✓${RESET} $*"; }
fail() { echo -e "  ${RED}✗ $*${RESET}" >&2; exit 1; }

have() { # have TYPE NAME -> 0 if object exists
  # NB: `osac get` exits 0 and prints "[]" for a missing object, so the
  # exit code alone cannot detect existence.
  local out
  out=$("$OSAC" get "$1" "$2" -o yaml 2>/dev/null)
  [[ -n "$out" && "$out" != "[]" && "$out" != "null" ]]
}

model_user_id() { # id of the non-deleting User named model-user ("" if none)
  # NB: a same-named User may be stuck in DELETING (its finalizer is only
  # removed when the Keycloak account it references still exists), which
  # makes name-based get/delete ambiguous — always resolve by id.
  "$OSAC" get users -o yaml 2>/dev/null \
    | yq -er '.[] | select(.metadata.name == "model-user" and .metadata.deletion_timestamp == null) | .id' 2>/dev/null || true
}

manifest() { # manifest FILE "title" — display osac create -f - <<EOF then run
  local f=$1
  echo ""
  echo -e "${GREEN}\$ ${RESET}./fulfillment-service/osac create -f - <<'EOF'"
  while IFS= read -r line; do echo "$line"; sleep 0.04; done < "$f"
  echo "EOF"
  sleep 0.3
  ( cd "$WORKTREE" && ./fulfillment-service/osac create -f - < "$f" ) \
    || fail "manifest create failed: $f"
}

wait_state() { # wait_state TYPE NAME WANT [TIMEOUT] [YQ-EXPR]
  local type=$1 name=$2 want=$3 timeout=${4:-300} expr=${5:-'.status.state'}
  local elapsed=0 state
  while true; do
    state=$("$OSAC" get "$type" "$name" -o yaml 2>/dev/null | yq -er "$expr // \"PENDING\"" 2>/dev/null || echo UNKNOWN)
    [[ "$state" == *"$want"* ]] && { ok "$type/$name: ${GREEN}${state}${RESET}"; return 0; }
    [[ "$state" == *FAILED* || "$state" == *ERROR* ]] && fail "$type/$name state: $state"
    (( elapsed >= timeout )) && fail "timed out waiting for $type/$name (last: $state)"
    echo -ne "  ${YELLOW}⏳${RESET} $type/$name ${DIM}${state} (${elapsed}s)${RESET}      \r"
    sleep 5; elapsed=$((elapsed + 5))
  done
}

# ── Stage 0: prerequisites ─────────────────────────────────────────────────
stage0() {
  header "§0. Prerequisites — tenant inventory and missing resources"
  run "./fulfillment-service/osac get storagetiers" "$OSAC" get storagetiers
  run "./fulfillment-service/osac get instancetypes" "$OSAC" get instancetypes
  run "./fulfillment-service/osac get subnets" "$OSAC" get subnets
  run "./fulfillment-service/osac get secrets" "$OSAC" get secrets

  if have instancetype "$INSTANCE_TYPE"; then
    ok "instance type $INSTANCE_TYPE exists"
  else
    run "./fulfillment-service/osac create instancetype --name $INSTANCE_TYPE --vcpus 2 --memory-gib 8 --description 'Standard 2 vCPU / 8 GiB'" \
      "$OSAC" create instancetype --name "$INSTANCE_TYPE" --vcpus 2 --memory-gib 8 --description 'Standard 2 vCPU / 8 GiB'
  fi

  if [[ ! -f "$KEY" ]]; then
    run "ssh-keygen -t ed25519 -f \"$KEY\" -N ''" ssh-keygen -t ed25519 -f "$KEY" -N ''
  else
    ok "SSH key $KEY exists"
  fi
  if have secret model-user-ssh-key; then
    ok "SSH key Secret model-user-ssh-key exists"
  else
    run "./fulfillment-service/osac create secret --name model-user-ssh-key --type ssh-public-key --from-file=public_key=\"$KEY.pub\"" \
      "$OSAC" create secret --name model-user-ssh-key --type ssh-public-key --from-file=public_key="$KEY.pub"
  fi

  if have securitygroup allow-ssh; then
    ok "security group allow-ssh exists"
  else
    run "./fulfillment-service/osac create securitygroup --name allow-ssh --virtual-network default --ingress protocol=tcp,port-from=22,port-to=22,ipv4-cidr=$SG_CIDR --egress protocol=all,ipv4-cidr=0.0.0.0/0" \
      "$OSAC" create securitygroup --name allow-ssh --virtual-network default --ingress protocol=tcp,port-from=22,port-to=22,ipv4-cidr="$SG_CIDR" --egress protocol=all,ipv4-cidr=0.0.0.0/0
    wait_state securitygroup allow-ssh READY 300
  fi

  info "Provider-administrator ExternalIPPool (created in the runbook prerequisites):"
  run "./fulfillment-service/osac tenant shared" "$OSAC" tenant shared
  run "./fulfillment-service/osac get externalippool $POOL" "$OSAC" get externalippool "$POOL"
  run "./fulfillment-service/osac tenant model" "$OSAC" tenant model
}

# ── Stage 1: admin login ───────────────────────────────────────────────────
stage1() {
  header "§1. Log in and select the tenant (private admin API)"
  run "./fulfillment-service/osac login --insecure --private --address \"\$OSAC_INTERNAL_API\" --token-script \"oc create token -n osac admin\"" \
    "$OSAC" login --insecure --private --address "$OSAC_INTERNAL_API" \
      --token-script "KUBECONFIG=$KCFG oc create token -n osac admin"
  run "./fulfillment-service/osac tenant model" "$OSAC" tenant model
  run "./fulfillment-service/osac whoami" "$OSAC" whoami
}

# ── Stage 2: DiskImage ─────────────────────────────────────────────────────
stage2() {
  header "§2. Mirror-backed DiskImage"
  info "catalog objects live in the reserved 'shared' tenant:"
  run "./fulfillment-service/osac tenant shared" "$OSAC" tenant shared
  if have diskimage fedora-latest; then
    ok "diskimage fedora-latest exists"
  else
    # NB: `osac create -f -` overwrites metadata.tenant with the selected
    # tenant, so shared-catalog objects require `tenant shared` first.
    cat > "$TMP/disk.yaml" <<EOF
'@type': type.googleapis.com/osac.private.v1.DiskImage
metadata:
  name: fedora-latest
  tenant: shared
spec:
  source_type: SOURCE_TYPE_REGISTRY
  source_ref: $MIRROR_REF
  guest_os_family: GUEST_OS_FAMILY_LINUX
  architecture:
    - ARCHITECTURE_AMD64
EOF
    manifest "$TMP/disk.yaml"
  fi
  wait_state diskimage fedora-latest AVAILABLE 300 .spec.lifecycle
  run "./fulfillment-service/osac get diskimage fedora-latest" "$OSAC" get diskimage fedora-latest
  run "./fulfillment-service/osac tenant model" "$OSAC" tenant model
}

# ── Stage 3: Keycloak user + fulfillment User ──────────────────────────────
stage3() {
  header "§3. Tenant user — Keycloak account and fulfillment User"
  info "Keycloak admin credentials (kept in /dev/shm, never displayed):"
  run "oc get secret -n keycloak keycloak-admin-credentials -o jsonpath='{.data.admin-username}' | base64 -d > /dev/shm/kc-admin-user" \
    bash -c "oc get secret -n keycloak keycloak-admin-credentials -o jsonpath='{.data.admin-username}' | base64 -d > /dev/shm/kc-admin-user"
  run "oc get secret -n keycloak keycloak-admin-credentials -o jsonpath='{.data.admin-password}' | base64 -d > /dev/shm/kc-admin-pw" \
    bash -c "oc get secret -n keycloak keycloak-admin-credentials -o jsonpath='{.data.admin-password}' | base64 -d > /dev/shm/kc-admin-pw"
  chmod 600 /dev/shm/kc-admin-user /dev/shm/kc-admin-pw

  type_cmd "curl --cacert $CA -s -X POST \"$KC_URL/realms/master/protocol/openid-connect/token\" -d grant_type=password -d client_id=admin-cli --data-urlencode username@/dev/shm/kc-admin-user --data-urlencode password@/dev/shm/kc-admin-pw"
  KC_TOKEN=$(curl --cacert "$CA" -s -X POST "$KC_URL/realms/master/protocol/openid-connect/token" \
    -d grant_type=password -d client_id=admin-cli \
    --data-urlencode username@/dev/shm/kc-admin-user --data-urlencode password@/dev/shm/kc-admin-pw \
    | jq -r .access_token) || fail "Keycloak admin token request failed"
  [[ -n "$KC_TOKEN" && "$KC_TOKEN" != null ]] || fail "no Keycloak admin token"
  ok "Keycloak admin token acquired (not displayed)"
  rm -f /dev/shm/kc-admin-user /dev/shm/kc-admin-pw

  if curl --cacert "$CA" -s -H "Authorization: Bearer $KC_TOKEN" \
       "$KC_URL/admin/realms/$KC_REALM/users?username=model-user" | jq -e 'length > 0' >/dev/null; then
    ok "Keycloak user model-user already exists"
  else
    run "printf '%s' \"\$(openssl rand -base64 18)\" > /dev/shm/model-user-pw && chmod 600 /dev/shm/model-user-pw" \
      bash -c 'printf "%s" "$(openssl rand -base64 18)" > /dev/shm/model-user-pw && chmod 600 /dev/shm/model-user-pw'
    type_cmd "curl --cacert $CA -s -o /dev/null -w '%{http_code}' -X POST \"$KC_URL/admin/realms/$KC_REALM/users\" -H \"Authorization: Bearer \$KC_TOKEN\" -H 'Content-Type: application/json' -d '{\"username\":\"model-user\",\"email\":\"model-user@example.invalid\",\"enabled\":true,\"emailVerified\":true,\"firstName\":\"Model\",\"lastName\":\"User\"}'"
    local code
    # NB: firstName/lastName are REQUIRED — the realm's "Verify Profile"
    # default action blocks token grants for users without them ("Account is
    # not fully set up").
    code=$(curl --cacert "$CA" -s -o /dev/null -w '%{http_code}' -X POST "$KC_URL/admin/realms/$KC_REALM/users" \
      -H "Authorization: Bearer $KC_TOKEN" -H 'Content-Type: application/json' \
      -d '{"username":"model-user","email":"model-user@example.invalid","enabled":true,"emailVerified":true,"firstName":"Model","lastName":"User"}')
    [[ "$code" == 201 || "$code" == 409 ]] || fail "Keycloak user create returned $code"
    ok "Keycloak user created (HTTP $code)"
  fi

  local kc_uid
  kc_uid=$(curl --cacert "$CA" -s -H "Authorization: Bearer $KC_TOKEN" \
    "$KC_URL/admin/realms/$KC_REALM/users?username=model-user" | jq -r '.[0].id')
  [[ -n "$kc_uid" && "$kc_uid" != null ]] || fail "Keycloak user id not found"

  # `osac tenant model` requires membership in the Keycloak organization
  # named after the tenant (org `model`) — same as provision-tenant.sh.
  local org_id
  org_id=$(curl --cacert "$CA" -s -H "Authorization: Bearer $KC_TOKEN" \
    "$KC_URL/admin/realms/$KC_REALM/organizations" \
    | jq -er '.[] | select(.name == "model") | .id' 2>/dev/null)
  [[ -n "$org_id" && "$org_id" != null ]] || fail "Keycloak org 'model' not found"
  type_cmd "curl --cacert $CA -s -o /dev/null -w '%{http_code}' -X POST \"$KC_URL/admin/realms/$KC_REALM/organizations/<org-id>/members\" -H \"Authorization: Bearer \$KC_TOKEN\" -H 'Content-Type: application/json' -d '\"<kc-user-id>\"'"
  code=$(curl --cacert "$CA" -s -o /dev/null -w '%{http_code}' -X POST \
    "$KC_URL/admin/realms/$KC_REALM/organizations/$org_id/members" \
    -H "Authorization: Bearer $KC_TOKEN" -H 'Content-Type: application/json' \
    -d "\"$kc_uid\"")
  # 409 = already a member.
  [[ "$code" == 201 || "$code" == 409 ]] || fail "org membership add returned $code"
  ok "model-user is a member of Keycloak org 'model'"

  if [[ ! -f /dev/shm/model-user-pw ]]; then
    info "Setting a fresh permanent password (existing account):"
    run "printf '%s' \"\$(openssl rand -base64 18)\" > /dev/shm/model-user-pw && chmod 600 /dev/shm/model-user-pw" \
      bash -c 'printf "%s" "$(openssl rand -base64 18)" > /dev/shm/model-user-pw && chmod 600 /dev/shm/model-user-pw'
  fi
  run "jq -n --rawfile pw /dev/shm/model-user-pw '{type:\"password\", value:\$pw, temporary:false}' > /dev/shm/kc-reset.json" \
    bash -c 'jq -n --rawfile pw /dev/shm/model-user-pw "{type:\"password\", value:\$pw, temporary:false}" > /dev/shm/kc-reset.json'
  type_cmd "curl --cacert $CA -s -o /dev/null -w '%{http_code}' -X PUT \"$KC_URL/admin/realms/$KC_REALM/users/<id>/reset-password\" -H \"Authorization: Bearer \$KC_TOKEN\" -H 'Content-Type: application/json' -d @/dev/shm/kc-reset.json"
  code=$(curl --cacert "$CA" -s -o /dev/null -w '%{http_code}' -X PUT \
    "$KC_URL/admin/realms/$KC_REALM/users/$kc_uid/reset-password" \
    -H "Authorization: Bearer $KC_TOKEN" -H 'Content-Type: application/json' -d @/dev/shm/kc-reset.json)
  [[ "$code" == 204 ]] || fail "Keycloak password set returned $code"
  ok "permanent password set (kept in /dev/shm/model-user-pw, not displayed)"
  rm -f /dev/shm/kc-reset.json

  # Fulfillment User. Two reconciler behaviors shape this:
  # - it only looks up the Keycloak account while status.keycloak_user_id
  #   is EMPTY: a stale value (Keycloak user deleted/recreated) never
  #   relinks — clear the field and it re-links to the current account;
  # - deleting the User also deletes its Keycloak account, and hangs in
  #   DELETING forever when that account is already gone. So never delete
  #   the User here; repair the link instead.
  # Resolve by id (not name): a same-named object may still be deleting,
  # which makes name-based get/delete ambiguous.
  local user_id
  user_id=$(model_user_id)
  if [[ -n "$user_id" ]]; then
    local kid
    kid=$("$OSAC" get user "$user_id" -o yaml 2>/dev/null \
      | yq -er '.status.keycloak_user_id // ""' 2>/dev/null || true)
    if [[ -n "$kid" && "$kid" == "$kc_uid" ]]; then
      ok "fulfillment User model-user is linked to the current Keycloak account"
    else
      info "stale Keycloak link — clearing it so the reconciler re-links:"
      run "EDITOR=./demos_and_workflows/model-tenant-vm-demo/clear-kid-editor.sh ./fulfillment-service/osac edit user $user_id" \
        env EDITOR="$WORKTREE/demos_and_workflows/model-tenant-vm-demo/clear-kid-editor.sh" \
        "$OSAC" edit user "$user_id"
    fi
  else
    cat > "$TMP/user.yaml" <<'EOF'
'@type': type.googleapis.com/osac.private.v1.User
metadata:
  name: model-user
  tenant: model
spec:
  username: model-user
  email: model-user@example.invalid
  enabled: true
EOF
    manifest "$TMP/user.yaml"
  fi
  info "waiting for the User reconciler to link the Keycloak account:"
  local elapsed=0 kid
  while true; do
    kid=$("$OSAC" get users -o yaml 2>/dev/null \
      | yq -er '.[] | select(.metadata.name == "model-user" and .metadata.deletion_timestamp == null) | .status.keycloak_user_id // ""' 2>/dev/null || true)
    [[ -n "$kid" && "$kid" == "$kc_uid" ]] && break
    (( elapsed >= 120 )) && fail "keycloak_user_id not linked after 120s (want $kc_uid, have '${kid:-none}')"
    echo -ne "  ${YELLOW}⏳${RESET} linking (${elapsed}s)      \r"; sleep 5; elapsed=$((elapsed + 5))
  done
  ok "status.keycloak_user_id linked to the current Keycloak account"
  user_id=$(model_user_id)
  run "./fulfillment-service/osac get user $user_id -o yaml" "$OSAC" get user "$user_id" -o yaml
}

# ── Stage 4: template ──────────────────────────────────────────────────────
stage4() {
  header "§4. ComputeInstanceTemplate"
  # NB (OSAC 0.0.62): the template id is the Ansible role path the AAP
  # provisioning playbook includes (`include_role: name={{ spec.templateID }}`),
  # and the chart's publish-templates hook already seeds it
  # (osac.templates.ocp_virt_vm / ocp-virt-vm). A template with any other id
  # can never provision, and a second template with the same id cannot be
  # created (id is the primary key) — so REUSE the seeded template instead
  # of creating one.
  run "./fulfillment-service/osac tenant shared" "$OSAC" tenant shared
  run "./fulfillment-service/osac get computeinstancetemplate ocp-virt-vm" \
    "$OSAC" get computeinstancetemplate ocp-virt-vm
  info "template id = AAP role path (osac.templates.ocp_virt_vm); chart-seeded, not created here"
  run "./fulfillment-service/osac tenant model" "$OSAC" tenant model
}

# ── Stage 5: catalog item ──────────────────────────────────────────────────
stage5() {
  header "§5. ComputeInstanceCatalogItem"
  run "./fulfillment-service/osac tenant shared" "$OSAC" tenant shared
  if have computeinstancecatalogitem model-fedora-vm; then
    ok "catalog item exists"
  else
    cat > "$TMP/cat.yaml" <<EOF
'@type': type.googleapis.com/osac.private.v1.ComputeInstanceCatalogItem
metadata:
  name: model-fedora-vm
  tenant: shared
title: Model Fedora VM
description: |
  Fedora VM offering for the model tenant.
template:
  id: osac.templates.ocp_virt_vm
published: true
fields:
  disk_image:
    locked:
      name: fedora-latest
  instance_type:
    editable:
      default_value:
        name: ${INSTANCE_TYPE}
  boot_disk:
    size_gib:
      editable:
        default_value: 20
    storage_tier:
      editable:
        default_value:
          name: ${STORAGE_TIER}
  run_strategy:
    editable:
      default_value: COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS
EOF
    manifest "$TMP/cat.yaml"
  fi
  run "./fulfillment-service/osac get computeinstancecatalogitem model-fedora-vm" \
    "$OSAC" get computeinstancecatalogitem model-fedora-vm
  run "./fulfillment-service/osac tenant model" "$OSAC" tenant model
}

# ── Stage 6: tenant user login ─────────────────────────────────────────────
stage6() {
  header "§6. Log in as the regular tenant user"
  run "./fulfillment-service/osac logout" "$OSAC" logout
  run "./fulfillment-service/osac login --insecure --address \"\$OSAC_API\" --flow password --user model-user --password-file /dev/shm/model-user-pw" \
    "$OSAC" login --insecure --address "$OSAC_API" --flow password --user model-user --password-file /dev/shm/model-user-pw
  run "./fulfillment-service/osac tenant model" "$OSAC" tenant model
  run "./fulfillment-service/osac whoami" "$OSAC" whoami
  info "the tenant user sees the published catalog:"
  run "./fulfillment-service/osac get computeinstancecatalogitems" "$OSAC" get computeinstancecatalogitems
  rm -f /dev/shm/model-user-pw
  ok "password file removed"
}

# ── Stage 7: ComputeInstance ───────────────────────────────────────────────
stage7() {
  header "§7. Create the ComputeInstance"
  # NB (OSAC 0.0.62): name-based references in a tenant user's create fail
  # server-side reference validation — the storage tier 'local' lives in
  # the shared tenant and the security-group name lookup fails the same
  # way. Reference the security group by its ID and let the catalog item
  # default the storage tier (its editable default_value fills it).
  run "./fulfillment-service/osac get securitygroups" "$OSAC" get securitygroups
  SG_ID=$("$OSAC" get securitygroups -o yaml 2>/dev/null \
    | yq -er '.[] | select(.metadata.name == "allow-ssh") | .id' 2>/dev/null)
  [[ -n "$SG_ID" ]] || fail "security group allow-ssh not found"
  if have computeinstance "$VM"; then
    ok "computeinstance $VM exists"
  else
    run "./fulfillment-service/osac create computeinstance --name $VM --catalog-item model-fedora-vm --instance-type $INSTANCE_TYPE --boot-disk-size 20 --network-attachment \"subnet=${SUBNET_ID},security-groups=${SG_ID}\" --ssh-key name=model-user-ssh-key --run-strategy Always" \
      "$OSAC" create computeinstance --name "$VM" --catalog-item model-fedora-vm \
        --instance-type "$INSTANCE_TYPE" --boot-disk-size 20 \
        --network-attachment "subnet=${SUBNET_ID},security-groups=${SG_ID}" \
        --ssh-key name=model-user-ssh-key --run-strategy Always
  fi
  info "VMs take several minutes to provision:"
  wait_state computeinstance "$VM" RUNNING 900
}

# ── Stage 8: ExternalIP + SSH ──────────────────────────────────────────────
stage8() {
  header "§8. Connect with SSH through an ExternalIP"
  info "the tenant subnet 10.200.0.0/20 is a cluster-internal overlay;"
  info "the site tunnel already routes 192.168.100.0/24 where the pool lives"
  if have externalip "$EIP"; then
    ok "externalip $EIP exists"
  else
    run "./fulfillment-service/osac create externalip --name $EIP --pool $POOL" \
      "$OSAC" create externalip --name "$EIP" --pool "$POOL"
  fi
  wait_state externalip "$EIP" ALLOCATED 300
  EIP_ADDR=$("$OSAC" get externalip "$EIP" -o yaml | yq -er '.status.address')
  ok "allocated address: ${GREEN}${EIP_ADDR}${RESET}"

  if have externalipattachment "$ATT"; then
    ok "attachment $ATT exists"
  else
    run "./fulfillment-service/osac create externalipattachment --externalip $EIP --compute-instance $VM --name $ATT" \
      "$OSAC" create externalipattachment --externalip "$EIP" --compute-instance "$VM" --name "$ATT"
  fi
  info "waiting for the LoadBalancer service to bind (SSH, tcp/22):"
  sleep 15
  run "oc get svc -A | grep osac-eip-externalip" \
    bash -c "oc get svc -A | grep osac-eip-externalip"

  # NB: bounded on purpose — the SSH connect cannot succeed on this lab
  # (OVN programs the ExternalIP VIP only on the tenant UDN's load
  # balancer; no host-side routing steers north/south traffic into the
  # UDN). The failed attempt is part of the recording: it documents the
  # platform gap instead of hiding it. See deviations item 17.
  info "attempting SSH (bounded to 15 s — see the note below):"
  type_cmd "timeout 15 ssh -i \"$KEY\" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null fedora@$EIP_ADDR 'hostname; cat /etc/fedora-release'"
  if timeout 15 ssh -i "$KEY" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
      "fedora@$EIP_ADDR" 'hostname; cat /etc/fedora-release'; then
    ok "SSH session succeeded"
  else
    echo -e "  ${YELLOW}⚠${RESET} SSH timed out — known platform gap, not a runbook error:"
    info "OVN programs the ExternalIP VIP only on the tenant UDN's load"
    info "balancer; nothing on the host steers north/south traffic into the"
    info "UDN, so the LoadBalancer service is unreachable from outside the"
    info "cluster (and east-west from the cluster network). Needs an upstream"
    info "OVN multi-network services fix — evidence in the demo deviations,"
    info "item 17. Interactive access meanwhile:"
    info "  ./fulfillment-service/osac console serial computeinstance $VM"
  fi
}

# ── Stage 9: final verification ────────────────────────────────────────────
stage9() {
  header "§9. Verify the VM"
  run "./fulfillment-service/osac get computeinstances" "$OSAC" get computeinstances
  run "./fulfillment-service/osac get computeinstance $VM -o yaml" "$OSAC" get computeinstance "$VM" -o yaml
  run "./fulfillment-service/osac get externalip $EIP" "$OSAC" get externalip "$EIP"
  header "Done — model tenant VM running, ExternalIP attached"
  info "SSH through the ExternalIP is tracked as a platform gap (see §8)"
  info "resources are kept: the recording doubles as the working lab state"
}

run_demo() {
  # Ambient admin session so §0 inventory works before §1 shows the login.
  "$OSAC" login --insecure --private --address "$OSAC_INTERNAL_API" \
    --token-script "KUBECONFIG=$KCFG oc create token -n osac admin" >/dev/null 2>&1
  "$OSAC" tenant model >/dev/null 2>&1
  stage0; stage1; stage2; stage3; stage4; stage5; stage6; stage7; stage8; stage9
}

cleanup() {
  echo "Deleting demo resources (pool, subnet and tenant stay):"
  # Re-login as admin: after §6 the saved CLI session is the tenant user.
  "$OSAC" login --insecure --private --address "$OSAC_INTERNAL_API" \
    --token-script "KUBECONFIG=$KCFG oc create token -n osac admin" >/dev/null 2>&1
  # Delete tenant objects FIRST: the AAP delete playbook must release the
  # compute instance's template reference before the shared template can
  # be deleted ("in use" otherwise). Deletion is asynchronous — the VM
  # only disappears once the playbook removes the CR finalizer.
  "$OSAC" tenant model >/dev/null 2>&1
  for spec in "externalipattachment $ATT" "externalip $EIP" "computeinstance $VM" \
              "securitygroup allow-ssh" "secret model-user-ssh-key"; do
    # NB: `osac delete` exits non-zero when the object's async deletion
    # outlives the command's internal wait — the deletion still completes,
    # so a "skip" here can still end in a deleted object (verified in the
    # take-11 cleanup).
    if err=$("$OSAC" delete $spec 2>&1); then
      echo "  deleted $spec"
    else
      echo "  skip $spec: $err"
    fi
  done
  local elapsed=0
  while have computeinstance "$VM"; do
    (( elapsed >= 300 )) && { echo "  ⚠ $VM still deleting after 300s"; break; }
    echo "  waiting for $VM deletion to finish (${elapsed}s)"; sleep 10; elapsed=$((elapsed + 10))
  done
  "$OSAC" tenant shared >/dev/null 2>&1
  # NB: the ocp-virt-vm template is chart-seeded (like the pool) and stays.
  for spec in "computeinstancecatalogitem model-fedora-vm" \
              "diskimage fedora-latest" "instancetype $INSTANCE_TYPE"; do
    "$OSAC" delete $spec >/dev/null 2>&1 && echo "  deleted $spec (shared)" || echo "  skip $spec"
  done
  local user_id
  user_id=$(model_user_id)
  if [[ -n "$user_id" ]]; then
    # NB: deleting the User also deletes its Keycloak account (the KC
    # delete below then finds nothing — expected).
    "$OSAC" delete user "$user_id" >/dev/null 2>&1 && echo "  deleted user model-user ($user_id)"
  fi
  KC_TOKEN=$(curl --cacert "$CA" -s -X POST "$KC_URL/realms/master/protocol/openid-connect/token" \
    -d grant_type=password -d client_id=admin-cli \
    --data-urlencode username@<(oc get secret -n keycloak keycloak-admin-credentials -o jsonpath='{.data.admin-username}' | base64 -d) \
    --data-urlencode password@<(oc get secret -n keycloak keycloak-admin-credentials -o jsonpath='{.data.admin-password}' | base64 -d) \
    | jq -r .access_token)
  local uid; uid=$(curl --cacert "$CA" -s -H "Authorization: Bearer $KC_TOKEN" \
    "$KC_URL/admin/realms/$KC_REALM/users?username=model-user" | jq -r '.[0].id')
  [[ -n "$uid" && "$uid" != null ]] && curl --cacert "$CA" -s -o /dev/null \
    -X DELETE -H "Authorization: Bearer $KC_TOKEN" "$KC_URL/admin/realms/$KC_REALM/users/$uid" \
    && echo "  deleted Keycloak user model-user"
  rm -f /dev/shm/model-user-pw /dev/shm/kc-reset.json /dev/shm/kc-admin-user /dev/shm/kc-admin-pw
  echo "Cleanup done."
}

preflight() {
  echo "Dry-run preflight (no cluster changes):"
  local fail=0
  for t in oc jq yq curl asciinema openssl ssh; do
    command -v "$t" >/dev/null && ok "$t present" || { echo -e "  ${RED}✗ $t missing${RESET}"; fail=1; }
  done
  [[ -x "$OSAC" ]] && ok "osac CLI: $OSAC" || { echo -e "  ${RED}✗ osac CLI missing${RESET}"; fail=1; }
  [[ -r "$KCFG" ]] && ok "kubeconfig readable" || { echo -e "  ${RED}✗ kubeconfig missing${RESET}"; fail=1; }
  [[ -r "$CA" ]] && ok "CA bundle readable" || { echo -e "  ${RED}✗ CA bundle missing${RESET}"; fail=1; }
  pgrep -f "sshuttle.*192.168.100.0/24" >/dev/null && ok "sshuttle tunnel for 192.168.100.0/24 running" \
    || { echo -e "  ${YELLOW}⚠ sshuttle for 192.168.100.0/24 not detected${RESET}"; }
  curl --cacert "$CA" -s --max-time 10 -o /dev/null "$OSAC_API/healthz" && ok "fulfillment API reachable" \
    || { echo -e "  ${RED}✗ API unreachable${RESET}"; fail=1; }
  oc whoami >/dev/null 2>&1 && ok "cluster: $(oc whoami)" || { echo -e "  ${RED}✗ cluster unreachable${RESET}"; fail=1; }
  (( fail )) && exit 1
  echo "Preflight passed."
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  case "${1:-}" in
    --dry-run)   preflight ;;
    --rehearsal) run_demo ;;
    --cleanup)   cleanup ;;
    *) asciinema rec --title "$TITLE" -c "bash -c 'source $0 && run_demo'" "$CAST_FILE" ;;
  esac
fi
