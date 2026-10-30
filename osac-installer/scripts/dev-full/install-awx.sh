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

  # Disable project collection/role sync: OSAC's collections are installed in
  # its dedicated execution environment from osac-aap/collections/requirements.yml.
  #
  # AWX_TASK_ENV injects env vars into every job's execution environment. We set
  # ANSIBLE_JINJA2_NATIVE=true because the osac-aap ocp_virt_vm role is authored
  # for jinja2 native mode (e.g. `cores: "{{ vm_cpu_cores }}"` must render as an
  # int, or KubeVirt's virtualmachines-mutator webhook rejects it: "cpu.cores must
  # be of type integer"). osac-aap sets this in osac-aap/ansible.cfg, but AWX clones
  # the whole mono-repo and runs ansible-runner with cwd = repo root (no ansible.cfg
  # there), so that config is never loaded. Injecting the env var restores native
  # mode for all osac-aap job runs.
  # Local AWX workers may lack a service-account namespace file. Use the
  # release namespace for storage config Secrets through the supported override.
  local settings_payload
  settings_payload=$(python3 -c '
import json
import sys

print(json.dumps({
    "AWX_COLLECTIONS_ENABLED": False,
    "AWX_ROLES_ENABLED": False,
    "AWX_TASK_ENV": {
        "ANSIBLE_JINJA2_NATIVE": "true",
        "OSAC_STORAGE_CONFIG_NAMESPACE": sys.argv[1],
    },
}))
' "${NS}")
  curl -fsS -X PATCH "${api}/settings/jobs/" -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" \
    -d "${settings_payload}" >/dev/null

  # Register the OSAC AAP execution environment, which contains the full
  # collection set including vastdata.vms.
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
  curl -s -X PATCH "${api}/execution_environments/${ee_id}/" -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" \
    -d "$(execution_environment_image_json)" >/dev/null
  log "OSAC AAP execution environment configured: ${OSAC_EE_IMAGE} (pull: ${OSAC_EE_PULL})"

  # Project from the osac mono-repo. osac-aap playbooks live under osac-aap/, and
  # AWX's Project API always clones the whole repo, so playbook paths below are
  # prefixed with osac-aap/.
  # Remove the cached checkout before syncing: the repo's graph-latest tag moves,
  # and Git rejects a fetch when that tag differs from AWX's stale local tag.
  local project_id
  project_id=$(curl -s -X POST "${api}/projects/" -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" -d '{
      "name": "osac-aap", "organization": 1, "scm_type": "git",
      "scm_url": "https://github.com/osac-project/osac.git",
      "scm_branch": "main", "scm_update_on_launch": false,
      "scm_delete_on_update": true
    }' | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))" 2>/dev/null || true)
  if [[ -z "$project_id" ]]; then
    project_id=$(curl -s -H "Authorization: Bearer ${awx_token}" "${api}/projects/?name=osac-aap" | \
      python3 -c "import json,sys; d=json.load(sys.stdin); print(d['results'][0]['id'] if d.get('results') else '')")
    if [[ -n "$project_id" ]]; then
      # A project surviving a pre-mono-repo run may still point at the old repo.
      curl -s -X PATCH "${api}/projects/${project_id}/" -H "Authorization: Bearer ${awx_token}" \
        -H "Content-Type: application/json" \
        -d '{"scm_url": "https://github.com/osac-project/osac.git", "scm_branch": "main", "scm_delete_on_update": true}' >/dev/null
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

  # Compute-instance job templates (real playbooks). Leave storage-class
  # variables unset here; the operator supplies the tenant's resolved classes
  # under osac_job_vars whenever it launches a compute job.
  #
  # We deliberately do NOT inject tenant_target_namespace / compute_instance_target_namespace
  # here. As top-level extra_vars they would OUTRANK the ocp_virt_vm role's own
  # set_fact (create.yaml), which resolves the VM namespace from the CI's
  # osac.openshift.io/subnet-target-namespace annotation (falling back to the
  # tenant namespace). The osac-operator ComputeInstance controller looks for the
  # KubeVirt VM in exactly that subnet-target namespace, so forcing a fixed
  # namespace here makes the VM boot where the operator never looks — the CI stays
  # stuck at Provisioned=False/WaitingForVM forever. Let the role resolve it; the
  # subnet AAP job creates the target namespace and API-only CUDN object on Kind.
  local inventory_name inventory_id inv_id
  declare -A inventory_ids
  for inventory_name in \
    osac-cluster-fulfillment osac-config-as-code osac-publish-templates \
    osac-compute-instance-operations osac-networking-operations \
    osac-bare-metal-fulfillment osac-storage-operations; do
    inventory_ids["${inventory_name}"]=$(ensure_inventory "${inventory_name}" "${api}" "${awx_token}")
  done
  inv_id="${inventory_ids[osac-compute-instance-operations]}"
  local entry name playbook template_id template_payload
  for entry in \
    "osac-create-compute-instance:osac-aap/playbook_osac_create_compute_instance.yml" \
    "osac-delete-compute-instance:osac-aap/playbook_osac_delete_compute_instance.yml"; do
    name="${entry%%:*}"; playbook="${entry##*:}"
    template_id=$(curl -fsS -G "${api}/job_templates/" \
      -H "Authorization: Bearer ${awx_token}" \
      --data-urlencode "name=${name}" | \
      python3 -c '
import json
import sys

name = sys.argv[1]
matches = [
    str(template["id"])
    for template in json.load(sys.stdin).get("results", [])
    if template.get("name") == name and template.get("organization") == 1
]
if len(matches) > 1:
    raise SystemExit(f"multiple AWX job templates named {name!r} in organization 1")
print(matches[0] if matches else "")
' "${name}")
    if [[ -n "${template_id}" ]]; then
      curl -fsS -X PATCH "${api}/job_templates/${template_id}/" \
        -H "Authorization: Bearer ${awx_token}" \
        -H "Content-Type: application/json" \
        -d '{"extra_vars":"{}"}' >/dev/null
    else
      template_payload=$(python3 -c '
import json
import sys

print(json.dumps({
    "name": sys.argv[1],
    "organization": 1,
    "inventory": int(sys.argv[3]),
    "project": int(sys.argv[4]),
    "playbook": sys.argv[2],
    "extra_vars": "{}",
    "ask_variables_on_launch": True,
}))
' "${name}" "${playbook}" "${inv_id}" "${project_id}")
      curl -fsS -X POST "${api}/job_templates/" \
        -H "Authorization: Bearer ${awx_token}" \
        -H "Content-Type: application/json" \
        -d "${template_payload}" >/dev/null
    fi
    log "  template: ${name}"
  done

  # Storage templates used by the operator to provision the registered
  # tenant backend and create tenant-labeled StorageClasses for each tier.
  for entry in \
    "osac-create-tenant-storage-backend:osac-aap/playbook_osac_create_tenant_storage_backend.yml" \
    "osac-delete-tenant-storage-backend:osac-aap/playbook_osac_delete_tenant_storage_backend.yml" \
    "osac-create-tenant-cluster-storage:osac-aap/playbook_osac_create_tenant_cluster_storage.yml" \
    "osac-delete-tenant-cluster-storage:osac-aap/playbook_osac_delete_tenant_cluster_storage.yml"; do
    name="${entry%%:*}"; playbook="${entry##*:}"
    curl -s -X POST "${api}/job_templates/" -H "Authorization: Bearer ${awx_token}" \
      -H "Content-Type: application/json" -d "{
        \"name\": \"${name}\", \"organization\": 1, \"inventory\": ${inv_id},
        \"project\": ${project_id}, \"playbook\": \"${entry##*:}\",
        \"extra_vars\": \"{}\", \"ask_variables_on_launch\": true,
        \"allow_simultaneous\": false
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
        \"extra_vars\": \"{}\", \"ask_variables_on_launch\": true
      }" >/dev/null
    log "  template: ${name}"
  done

  # Complete the production template catalog. The legacy loops above are kept
  # for compatibility with older AWX state; this pass updates every template
  # with its production playbook, inventory, and execution environment.
  local template_specs name playbook template_inventory workflow_status
  local template_id extra_vars extra_vars_json template_payload
  if ! template_specs=$(controller_template_specs); then
    warn "Failed to load production controller template definitions"
    return 1
  fi
  if [[ -z "${template_specs}" ]]; then
    warn "Production controller template definitions are empty"
    return 1
  fi
  while IFS='|' read -r name playbook template_inventory workflow_status; do
    [[ -n "${name}" ]] || continue
    if [[ -z "${inventory_ids[${template_inventory}]:-}" ]]; then
      inventory_ids["${template_inventory}"]=$(ensure_inventory "${template_inventory}" "${api}" "${awx_token}")
    fi
    extra_vars="{}"
    if [[ -n "${workflow_status}" ]]; then
      extra_vars="workflow_status: ${workflow_status}"
    fi
    extra_vars_json=$(printf '%s' "${extra_vars}" | json_string)
    inventory_id="${inventory_ids[${template_inventory}]}"
    template_payload=$(cat <<EOF
{
  "name": "${name}", "organization": 1, "inventory": ${inventory_id},
  "project": ${project_id}, "playbook": "${playbook}",
  "execution_environment": ${ee_id}, "ask_variables_on_launch": true,
  "allow_simultaneous": false,
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
  done <<< "${template_specs}"
  log "Production OSAC job templates derived from controller.yml"

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
        \"ssl_ca_cert\": $(printf '%s' "${cluster_ca}" | json_string)
      }
    }" | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))")

  # Attach the credential to every job template.
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
