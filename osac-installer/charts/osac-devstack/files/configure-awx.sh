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
OSAC_EE_IMAGE="${OSAC_EE_IMAGE:-ghcr.io/osac-project/osac-aap:latest}"
OSAC_EE_PULL="${OSAC_EE_PULL:-missing}"

log()  { echo "[+] $*"; }
warn() { echo "[!] $*" >&2; }

json_string() {
  python3 -c 'import json, sys; print(json.dumps(sys.stdin.read()))'
}

execution_environment_json() {
  OSAC_EE_IMAGE="${OSAC_EE_IMAGE}" OSAC_EE_PULL="${OSAC_EE_PULL}" \
    python3 -c 'import json, os; print(json.dumps({"name": "osac-aap-ee", "organization": 1, "image": os.environ["OSAC_EE_IMAGE"], "pull": os.environ["OSAC_EE_PULL"]}))'
}

execution_environment_image_json() {
  OSAC_EE_IMAGE="${OSAC_EE_IMAGE}" OSAC_EE_PULL="${OSAC_EE_PULL}" \
    python3 -c 'import json, os; print(json.dumps({"image": os.environ["OSAC_EE_IMAGE"], "pull": os.environ["OSAC_EE_PULL"]}))'
}

ensure_inventory() {
  local inventory_name="$1" api="$2" awx_token="$3" inventory_id
  inventory_id=$(curl -s -X POST "${api}/inventories/" -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" -d "{\"name\": \"${inventory_name}\", \"organization\": 1}" | \
    python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))" 2>/dev/null || true)
  if [[ -z "${inventory_id}" ]]; then
    inventory_id=$(curl -s -H "Authorization: Bearer ${awx_token}" "${api}/inventories/?name=${inventory_name}" | \
      python3 -c "import json,sys; d=json.load(sys.stdin); print(d['results'][0]['id'] if d.get('results') else '')")
  fi
  [[ -n "${inventory_id}" ]] || return 1
  curl -s -X POST "${api}/inventories/${inventory_id}/hosts/" -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" -d '{"name": "localhost", "variables": "ansible_connection: local"}' >/dev/null 2>&1 || true
  printf '%s' "${inventory_id}"
}

controller_template_specs() {
  local controller_url="${OSAC_CONTROLLER_VARS_URL:-https://raw.githubusercontent.com/osac-project/osac/main/osac-aap/collections/ansible_collections/osac/config_as_code/roles/aap/vars/controller.yml}"
  curl -fsSL "${controller_url}" | python3 -c '
import re
import sys

text = sys.stdin.read()
section = text.split("controller_templates:", 1)[1].split("controller_job_template_surveys:", 1)[0]
entry_pattern = re.compile(r"(?ms)^\s+- name: \"\{\{ aap_prefix \}\}-(?P<name>[^\"]+)\"\s*\n(?P<body>.*?)(?=^\s+- name:|\Z)")
for entry in entry_pattern.finditer(section):
    body = entry.group("body")
    playbook = re.search(r"^\s+playbook:\s+\"([^\"]+)\"", body, re.MULTILINE)
    inventory = re.search(r"^\s+inventory:\s+\"\{\{ aap_prefix \}\}-([^\"]+)\"", body, re.MULTILINE)
    if not playbook or not inventory:
        continue
    workflow_status = re.search(r"^\s+workflow_status:\s+(\w+)", body, re.MULTILINE)
    print("|".join(("osac-" + entry.group("name"), playbook.group(1), "osac-" + inventory.group(1), workflow_status.group(1) if workflow_status else "")))
'
}

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

  # Disable project collection/role sync: OSAC's collections are installed in
  # its dedicated execution environment from osac-aap/collections/requirements.yml.
  # Set ANSIBLE_JINJA2_NATIVE=true for osac-aap playbooks.
  curl -s -X PATCH "${api}/settings/jobs/" -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" \
    -d '{"AWX_COLLECTIONS_ENABLED": false, "AWX_ROLES_ENABLED": false, "AWX_TASK_ENV": {"ANSIBLE_JINJA2_NATIVE": "true"}}' >/dev/null

  # Register the OSAC AAP execution environment. It is built from the same
  # collection requirements as AAP deployments, including vastdata.vms.
  local ee_id
  ee_id=$(curl -s -X POST "${api}/execution_environments/" -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" \
    -d "$(execution_environment_json)" | \
    python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))" 2>/dev/null || true)
  if [[ -z "${ee_id}" ]]; then
    ee_id=$(curl -s -H "Authorization: Bearer ${awx_token}" "${api}/execution_environments/?name=osac-aap-ee" | \
      python3 -c "import json,sys; d=json.load(sys.stdin); print(d['results'][0]['id'] if d.get('results') else '')")
  fi
  if [[ -z "${ee_id}" ]]; then
    warn "Failed to create or find the OSAC AAP execution environment"
    return 1
  fi
  if ! curl -sS -f -X PATCH "${api}/execution_environments/${ee_id}/" \
    -H "Authorization: Bearer ${awx_token}" -H "Content-Type: application/json" \
    -d "$(execution_environment_image_json)" >/dev/null; then
    warn "Failed to configure execution environment ${ee_id}"
    return 1
  fi
  log "OSAC AAP execution environment configured: ${OSAC_EE_IMAGE} (pull: ${OSAC_EE_PULL})"

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

  # Create the same 35 templates as the production AAP configuration. Kind
  # uses localhost in each production inventory group; backend-specific jobs
  # remain available but require their corresponding Kind backend to run.
  local inventory_id
  declare -A inventory_ids

  local compute_extra_vars
  compute_extra_vars="tenant_storage_classes:
  - name: standard
    tier: local"
  local name playbook template_inventory workflow_status
  local template_id extra_vars extra_vars_json template_payload template_specs
  if ! template_specs=$(controller_template_specs); then
    warn "Failed to load production controller template definitions"
    return 1
  fi
  if [[ -z "${template_specs}" ]]; then
    warn "Production controller template definitions are empty"
    return 1
  fi
  while IFS='|' read -r name playbook template_inventory workflow_status; do
    extra_vars=""
    if [[ "${name}" == "osac-create-compute-instance" || "${name}" == "osac-delete-compute-instance" ]]; then
      extra_vars="${compute_extra_vars}"
    elif [[ -n "${workflow_status:-}" ]]; then
      extra_vars="workflow_status: ${workflow_status}"
    fi
    extra_vars_json='""'
    [[ -n "${extra_vars}" ]] && extra_vars_json=$(printf '%s' "${extra_vars}" | json_string)
    if [[ -z "${inventory_ids[${template_inventory}]:-}" ]]; then
      inventory_ids["${template_inventory}"]=$(ensure_inventory "${template_inventory}" "${api}" "${awx_token}")
    fi
    inventory_id="${inventory_ids[${template_inventory}]}"
    template_payload=$(cat <<EOF
{
  "name": "${name}", "organization": 1, "inventory": ${inventory_id},
  "project": ${project_id}, "playbook": "${playbook}",
  "execution_environment": ${ee_id}, "ask_variables_on_launch": true,
  "extra_vars": ${extra_vars_json}
}
EOF
)
    template_id=$(curl -s -H "Authorization: Bearer ${awx_token}" "${api}/job_templates/?name=${name}" | \
      python3 -c "import json,sys; d=json.load(sys.stdin); print(d['results'][0]['id'] if d.get('results') else '')")
    if [[ -z "${template_id}" ]]; then
      template_id=$(curl -s -X POST "${api}/job_templates/" -H "Authorization: Bearer ${awx_token}" \
        -H "Content-Type: application/json" -d "${template_payload}" | \
        python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))" 2>/dev/null || true)
    fi
    [[ -n "${template_id}" ]] || { warn "Failed to create or find template ${name}"; return 1; }
    if ! curl -sS -f -X PATCH "${api}/job_templates/${template_id}/" \
      -H "Authorization: Bearer ${awx_token}" -H "Content-Type: application/json" \
      -d "${template_payload}" >/dev/null; then
      warn "Failed to update job template ${template_id} (${name})"
      return 1
    fi
    log "  template: ${name}"
  done <<< "${template_specs}"
  log "Production OSAC job templates derived from controller.yml"

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
        \"ssl_ca_cert\": $(printf '%s' "${cluster_ca}" | json_string)
      }
    }" | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))")

  # Attach credential to all job templates.
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
