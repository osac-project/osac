# Model tenant VM demo — post-record manual verification (2026-11-16)

Headless asciinema recordings do not propagate child exit codes, so lab
state is verified directly after the recorded run. Run with:

    cd /home/asathe/work/osac-docs-3921
    export KUBECONFIG=.local/doc2-evidence/osac-demo.kubeconfig

Recorded run: model-tenant-vm-demo.cast (take 11, 2026-11-16, 5.9 min
after timestamp normalization). All checks executed and passed unless
noted.

## §0–§2 prerequisites and catalog objects

- [x] `./fulfillment-service/osac tenant shared`
- [x] `./fulfillment-service/osac get diskimage fedora-latest` — AVAILABLE (id 01a1e43e-720a)
- [x] `./fulfillment-service/osac get instancetype standard-2-8` — ACTIVE
- [x] `./fulfillment-service/osac get externalippool br-sno-pool` — present (id 01a1e380-43df)

## §3 tenant user

- [x] `./fulfillment-service/osac tenant model`
- [x] User model-user (id 01a1e43f-375f, not deleting) — `status.keycloak_user_id` 4e04a980-a705-467a-9f7e-959e32dcfa8b matches the Keycloak account created during the recording (KC createdTimestamp 1794830691175). Requires an admin (private-API) session; the recorded model-user session gets PermissionDenied on `get users` — expected.

## §4–§5 template and catalog item

- [x] `./fulfillment-service/osac tenant shared`
- [x] `./fulfillment-service/osac get computeinstancetemplate ocp-virt-vm` — chart-seeded, id `osac.templates.ocp_virt_vm` (AAP role path; a generated UUID id fails provisioning)
- [x] `./fulfillment-service/osac get computeinstancecatalogitem model-fedora-vm` — published, template id `osac.templates.ocp_virt_vm`

## §6 tenant user session

- [x] `./fulfillment-service/osac whoami` (as the recorded model-user session) — "Logged in as: model-user / Tenant: model" (verified before the admin re-login below)
- [x] `./fulfillment-service/osac get computeinstancecatalogitems` — model-fedora-vm visible (in cast)

## §7 compute instance

- [x] `./fulfillment-service/osac get computeinstance model-vm` — COMPUTE_INSTANCE_STATE_RUNNING, internal 10.200.0.12, external 192.168.100.224
- [x] `oc get computeinstances -n osac` — hub CR vm-kfd4q, `TEMPLATE osac.templates.ocp_virt_vm`, PHASE Running

## §8 ExternalIP and SSH

- [x] `./fulfillment-service/osac get externalip model-vm-ip` — EXTERNAL_IP_STATE_ALLOCATED 192.168.100.224, attached
- [x] `./fulfillment-service/osac get externalipattachment model-vm-att` — present
- [x] `oc get svc -A | grep osac-eip-externalip` — subnet-tc9cw/osac-eip-externalip-52lbw-ingress (LoadBalancer, 192.168.100.224, 22:30510/TCP)
- [x] `timeout 15 ssh ... fedora@192.168.100.224` — timed out as EXPECTED: known OVN UDN north/south platform gap (deviations item 17); the bounded failed attempt and its narration are part of the recording

## §9 final state

- [x] `./fulfillment-service/osac get computeinstances` — model-vm RUNNING
- [x] Pool/subnet/tenant untouched: br-sno-pool present, subnet default-ipv4 used by the VM, tenant model active; chart-seeded template ocp-virt-vm kept

## Known residue (documented, not runbook-blocking)

- Fulfillment User tombstone 01a1e3a1-7434 (name model-user) stuck
  DELETING since 2026-11-16T09:42:30Z — product bug, deviations
  item 13; removal needs a finalizer strip the session permission
  classifier denies.
