#!/usr/bin/env bash
# dev-full: configure AWX as the AAP provisioning backend on Kind.
#
# AWX is installed as a Helm chart dependency (awx-operator). This script
# configures the OAuth token, inventory, project, job templates and Kubernetes
# credential that osac-operator needs.
#
# Requires KUBECONFIG to point at the kind cluster.
#
# Usage: configure-awx.sh [osac-namespace]

set -euo pipefail

NS="${1:-${NS:-osac}}"

log()  { echo "[+] $*"; }
warn() { echo "[!] $*" >&2; }

configure_awx() {
  log "Configuring AWX for OSAC..."

  local admin_pass
  admin_pass=$(kubectl -n "${NS}" get secret awx-admin-password -o jsonpath='{.data.password}' | base64 -d)

  # Connect directly to AWX service (this script runs in-cluster as a Job pod)
  local api="http://awx-service.${NS}.svc.cluster.local/api/v2"

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

  # Disable collection/role sync and set ANSIBLE_JINJA2_NATIVE=true for osac-aap playbooks.
  curl -s -X PATCH "${api}/settings/jobs/" -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" \
    -d '{"AWX_COLLECTIONS_ENABLED": false, "AWX_ROLES_ENABLED": false, "AWX_TASK_ENV": {"ANSIBLE_JINJA2_NATIVE": "true"}}' >/dev/null

  # Project from the osac mono-repo.
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

  # Job templates (compute + networking).
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

  # Kubernetes credential for job templates.
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

  # Attach credential to all job templates.
  local templates jt_id
  templates=$(curl -s -H "Authorization: Bearer ${awx_token}" "${api}/job_templates/" | \
    python3 -c "import json,sys; print(' '.join(str(t['id']) for t in json.load(sys.stdin)['results']))")
  for jt_id in ${templates}; do
    curl -s -X POST "${api}/job_templates/${jt_id}/credentials/" -H "Authorization: Bearer ${awx_token}" \
      -H "Content-Type: application/json" -d "{\"id\": ${cred_id}}" >/dev/null
  done
  log "Credential ${cred_id} attached to all templates"

  # Store AWX token as secret for osac-operator.
  kubectl -n "${NS}" create secret generic awx-token \
    --from-literal=token="${awx_token}" \
    --dry-run=client -o yaml | kubectl apply -f -

  # Restart operator to pick up awx-token secret.
  if kubectl -n "${NS}" get deployment osac-operator >/dev/null 2>&1; then
    kubectl -n "${NS}" rollout restart deployment osac-operator
    kubectl -n "${NS}" rollout status deployment osac-operator --timeout=3m || true
    log "osac-operator restarted to pick up awx-token"
  else
    warn "osac-operator deployment not found in ${NS}; skipped restart"
  fi

  log "AWX configured for OSAC"
}

configure_awx
