#!/usr/bin/env bash
# dev-full: install and configure AWX as the AAP provisioning backend on Kind.
#
# AWX is the open-source upstream of Red Hat AAP. The osac-operator (configured
# by values/dev/kind-instance.yaml to talk to awx-service.awx.svc.cluster.local
# and read the 'awx-token' secret) launches AWX job templates that run the real
# osac-aap playbooks. This script installs AWX and configures the token, inventory,
# project, job templates and Kubernetes credential it needs.
#
# Requires KUBECONFIG to point at the kind cluster (set by the Makefile).
#
# Usage: install-awx.sh [osac-namespace]

set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null && pwd)"
MANIFESTS="${SCRIPT_DIR}/manifests"
NS="${1:-${NS:-osac}}"
AWX_PORT="${AWX_PORT:-8052}"

log()  { echo "[+] $*"; }
warn() { echo "[!] $*" >&2; }

install_awx() {
  log "Installing AWX operator..."
  helm repo add awx-operator https://ansible-community.github.io/awx-operator-helm/ 2>/dev/null || true
  helm repo update awx-operator >/dev/null 2>&1 || true
  helm upgrade --install awx-operator awx-operator/awx-operator \
    -n awx --create-namespace --wait --timeout 3m 2>&1 | tail -2

  log "Creating AWX instance..."
  kubectl apply -f "${MANIFESTS}/awx-instance.yaml"

  log "Waiting for AWX pods (this takes ~10 minutes)..."
  local i task_ready
  for i in $(seq 1 60); do
    task_ready=$(kubectl -n awx get pods -l app.kubernetes.io/name=awx-task --no-headers 2>/dev/null | grep -c "4/4" || true)
    [[ "$task_ready" -ge 1 ]] && break
    sleep 10
  done
  kubectl -n awx get pods 2>/dev/null | grep -v Completed || true
  log "AWX installed"
}

configure_awx() {
  log "Configuring AWX for OSAC..."

  # Route the AWX web UI through the shared Envoy Gateway HTTP listener.
  kubectl apply -f "${MANIFESTS}/httproute-awx.yaml"

  local admin_pass
  admin_pass=$(kubectl -n awx get secret awx-admin-password -o jsonpath='{.data.password}' | base64 -d)

  # Port-forward the AWX API.
  command -v lsof >/dev/null 2>&1 && { lsof -ti:"${AWX_PORT}" | xargs -r kill -9 2>/dev/null || true; }
  sleep 1
  kubectl -n awx port-forward svc/awx-service "${AWX_PORT}":80 >/dev/null 2>&1 &
  local pf_pid=$!
  trap 'kill "${pf_pid}" 2>/dev/null || true; wait "${pf_pid}" 2>/dev/null || true' RETURN
  sleep 3

  local api="http://localhost:${AWX_PORT}/api/v2"

  # OAuth token.
  local awx_token
  awx_token=$(curl -s -X POST "${api}/tokens/" -u "admin:${admin_pass}" \
    -H "Content-Type: application/json" -d '{"scope": "write"}' | \
    python3 -c "import json,sys; print(json.load(sys.stdin).get('token',''))")
  if [[ -z "$awx_token" ]]; then
    warn "Failed to create AWX token — AWX may not be ready yet"
    return 1
  fi
  log "AWX token created"

  # Inventory (create or reuse).
  local inv_id
  inv_id=$(curl -s -X POST "${api}/inventories/" -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" -d '{"name": "OSAC Dev", "organization": 1}' | \
    python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))" 2>/dev/null || true)
  if [[ -z "$inv_id" ]]; then
    inv_id=$(curl -s -H "Authorization: Bearer ${awx_token}" "${api}/inventories/?name=OSAC+Dev" | \
      python3 -c "import json,sys; d=json.load(sys.stdin); print(d['results'][0]['id'] if d.get('results') else '')")
  fi
  curl -s -X POST "${api}/inventories/${inv_id}/hosts/" -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" \
    -d '{"name": "localhost", "variables": "ansible_connection: local"}' >/dev/null 2>&1 || true

  # Disable collection/role sync — Red Hat proprietary collections (ansible.platform)
  # are not available in open-source AWX.
  #
  # AWX_TASK_ENV injects env vars into every job's execution environment. We set
  # ANSIBLE_JINJA2_NATIVE=true because the osac-aap ocp_virt_vm role is authored
  # for jinja2 native mode (e.g. `cores: "{{ vm_cpu_cores }}"` must render as an
  # int, or KubeVirt's virtualmachines-mutator webhook rejects it: "cpu.cores must
  # be of type integer"). osac-aap sets this in osac-aap/ansible.cfg, but AWX clones
  # the whole mono-repo and runs ansible-runner with cwd = repo root (no ansible.cfg
  # there), so that config is never loaded. Injecting the env var restores native
  # mode for all osac-aap job runs.
  curl -s -X PATCH "${api}/settings/jobs/" -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" \
    -d '{"AWX_COLLECTIONS_ENABLED": false, "AWX_ROLES_ENABLED": false, "AWX_TASK_ENV": {"ANSIBLE_JINJA2_NATIVE": "true"}}' >/dev/null

  # Project from the osac mono-repo. osac-aap playbooks live under osac-aap/, and
  # AWX's Project API always clones the whole repo, so playbook paths below are
  # prefixed with osac-aap/.
  local project_id
  project_id=$(curl -s -X POST "${api}/projects/" -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" -d '{
      "name": "osac-aap", "organization": 1, "scm_type": "git",
      "scm_url": "https://github.com/osac-project/osac.git",
      "scm_branch": "main", "scm_update_on_launch": false
    }' | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))" 2>/dev/null || true)
  if [[ -z "$project_id" ]]; then
    project_id=$(curl -s -H "Authorization: Bearer ${awx_token}" "${api}/projects/?name=osac-aap" | \
      python3 -c "import json,sys; d=json.load(sys.stdin); print(d['results'][0]['id'] if d.get('results') else '')")
    if [[ -n "$project_id" ]]; then
      # A project surviving a pre-mono-repo run may still point at the old repo.
      curl -s -X PATCH "${api}/projects/${project_id}/" -H "Authorization: Bearer ${awx_token}" \
        -H "Content-Type: application/json" \
        -d '{"scm_url": "https://github.com/osac-project/osac.git", "scm_branch": "main"}' >/dev/null
      curl -s -X POST "${api}/projects/${project_id}/update/" -H "Authorization: Bearer ${awx_token}" >/dev/null
    fi
  fi

  # Wait for project sync.
  local i proj_status="unknown"
  for i in $(seq 1 20); do
    proj_status=$(curl -s -H "Authorization: Bearer ${awx_token}" "${api}/projects/${project_id}/" | \
      python3 -c "import json,sys; print(json.load(sys.stdin).get('status','unknown'))" 2>/dev/null || echo unknown)
    [[ "$proj_status" == "successful" || "$proj_status" == "failed" ]] && break
    sleep 5
  done
  log "AWX project synced: ${proj_status}"

  # Compute-instance job templates (real playbooks).
  # Dev-full storage fallback: the compute-instance playbook resolves a
  # StorageClass by matching its requested tier (_requested_storage_tier,
  # default 'local') against this injected tenant_storage_classes list. On kind
  # the only StorageClass is 'standard' (rancher.io/local-path) and the
  # LVMS-backed 'local' StorageTier hook (register-local-storage.yaml) is
  # skipped, so nothing populates the tenant's status.storageClasses. We inject
  # the list here as a job-template extra_var (which outranks the playbook's
  # osac_job_vars-derived value) so provisioning works without a real storage
  # backend. The tier MUST be 'local' to match the playbook's requested tier —
  # a mismatched tier name fails the run ("tier not available").
  #
  # We deliberately do NOT inject tenant_target_namespace / compute_instance_target_namespace
  # here. As top-level extra_vars they would OUTRANK the ocp_virt_vm role's own
  # set_fact (create.yaml), which resolves the VM namespace from the CI's
  # osac.openshift.io/subnet-target-namespace annotation (falling back to the
  # tenant namespace). The osac-operator ComputeInstance controller looks for the
  # KubeVirt VM in exactly that subnet-target namespace, so forcing a fixed
  # namespace here makes the VM boot where the operator never looks — the CI stays
  # stuck at Provisioned=False/WaitingForVM forever. Let the role resolve it; the
  # subnet namespace itself is created by provision-tenant.sh (subnet provisioning
  # is a noop on kind, so nothing else creates it).
  local compute_extra_vars
  compute_extra_vars="tenant_storage_classes:
  - name: standard
    tier: local"
  local entry name playbook
  for entry in \
    "osac-create-compute-instance:osac-aap/playbook_osac_create_compute_instance.yml" \
    "osac-delete-compute-instance:osac-aap/playbook_osac_delete_compute_instance.yml"; do
    name="${entry%%:*}"; playbook="${entry##*:}"
    curl -s -X POST "${api}/job_templates/" -H "Authorization: Bearer ${awx_token}" \
      -H "Content-Type: application/json" -d "{
        \"name\": \"${name}\", \"organization\": 1, \"inventory\": ${inv_id},
        \"project\": ${project_id}, \"playbook\": \"${playbook}\",
        \"ask_variables_on_launch\": true,
        \"extra_vars\": $(echo "${compute_extra_vars}" | jq -Rs .)
      }" >/dev/null
    log "  template: ${name}"
  done

  # Networking job templates (real playbooks — effective no-ops on kind, no fabric).
  for entry in \
    "osac-create-virtual-network:osac-aap/playbook_osac_create_virtual_network.yml" \
    "osac-delete-virtual-network:osac-aap/playbook_osac_delete_virtual_network.yml" \
    "osac-create-subnet:osac-aap/playbook_osac_create_subnet.yml" \
    "osac-delete-subnet:osac-aap/playbook_osac_delete_subnet.yml" \
    "osac-create-security-group:osac-aap/playbook_osac_create_security_group.yml" \
    "osac-delete-security-group:osac-aap/playbook_osac_delete_security_group.yml"; do
    name="${entry%%:*}"; playbook="${entry##*:}"
    curl -s -X POST "${api}/job_templates/" -H "Authorization: Bearer ${awx_token}" \
      -H "Content-Type: application/json" -d "{
        \"name\": \"${name}\", \"organization\": 1, \"inventory\": ${inv_id},
        \"project\": ${project_id}, \"playbook\": \"${playbook}\",
        \"ask_variables_on_launch\": true
      }" >/dev/null
    log "  template: ${name}"
  done

  # Kubernetes credential so job templates can act on the cluster.
  kubectl -n "${NS}" create serviceaccount awx-runner 2>/dev/null || true
  kubectl create clusterrolebinding awx-runner-admin --clusterrole=cluster-admin \
    --serviceaccount="${NS}:awx-runner" 2>/dev/null || true

  local awx_runner_token cluster_ca cred_id
  awx_runner_token=$(kubectl -n "${NS}" create token awx-runner --duration=87600h)
  cluster_ca=$(kubectl config view --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d)
  cred_id=$(curl -s -X POST "${api}/credentials/" -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" -d "{
      \"name\": \"kind-cluster\", \"organization\": 1, \"credential_type\": 17,
      \"inputs\": {
        \"host\": \"https://kubernetes.default.svc.cluster.local:443\",
        \"bearer_token\": \"${awx_runner_token}\", \"verify_ssl\": true,
        \"ssl_ca_cert\": $(echo "${cluster_ca}" | jq -Rs .)
      }
    }" | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))")

  # Attach the credential to every job template.
  local templates jt_id
  templates=$(curl -s -H "Authorization: Bearer ${awx_token}" "${api}/job_templates/" | \
    python3 -c "import json,sys; print(' '.join(str(t['id']) for t in json.load(sys.stdin)['results']))")
  for jt_id in ${templates}; do
    curl -s -X POST "${api}/job_templates/${jt_id}/credentials/" -H "Authorization: Bearer ${awx_token}" \
      -H "Content-Type: application/json" -d "{\"id\": ${cred_id}}" >/dev/null
  done
  log "Credential ${cred_id} attached to all templates"

  # Store the AWX token as a secret for the operator (name matches kind-instance.yaml).
  kubectl -n "${NS}" create secret generic awx-token \
    --from-literal=token="${awx_token}" \
    --dry-run=client -o yaml | kubectl apply -f -

  # The operator reads awx-token at startup; on a clean install it came up before
  # this secret existed (AWX takes ~10 min to be ready), so restart it to pick up
  # the token. Without this the operator never talks to AWX and no jobs launch.
  if kubectl -n "${NS}" get deployment osac-operator >/dev/null 2>&1; then
    kubectl -n "${NS}" rollout restart deployment osac-operator
    kubectl -n "${NS}" rollout status deployment osac-operator --timeout=3m || true
    log "osac-operator restarted to pick up awx-token"
  else
    warn "osac-operator deployment not found in ${NS}; skipped restart (token secret created)"
  fi

  log "AWX configured for OSAC"
}

install_awx
configure_awx
