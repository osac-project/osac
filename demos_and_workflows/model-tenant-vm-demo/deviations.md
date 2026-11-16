# Model tenant VM demo — deviations from the runbook (2026-11-16)

Deviations between docs/guides/developer/model-tenant-vm-runbook.md as
recorded and a clean browser-based / UI-driven walkthrough, with the
reason each was necessary. Lab: osac-demo, OSAC 0.0.62.

## Administration plane

1. **Keycloak user created via the Admin API, not the browser console.**
   The recording creates `model-user` with
   `POST /admin/realms/osac/users` using a master-realm admin token from
   the `keycloak/keycloak-admin-credentials` secret (credentials kept in
   /dev/shm, never displayed). Reason: headless recording; same pattern as
   `osac-installer/scripts/dev-full/provision-tenant.sh`.

2. **Admin login via service-account token, not browser OIDC.**
   §1 logs in to the private API with
   `--token-script "oc create token -n osac admin"`. Reason: the private
   (administration) API has no browser OIDC flow in this lab; the platform
   SA is the documented dev-admin identity.

3. **Keycloak org membership added via Admin API.** `osac tenant model`
   requires membership in the Keycloak organization named after the
   tenant; provision-tenant.sh normally adds it at tenant-creation time.
   The recording adds it explicitly
   (`POST .../organizations/<org-id>/members`, 409 = already member).

## §8 connectivity (ExternalIP + MetalLB instead of the tenant subnet)

4. **SSH goes through an ExternalIP from `br-sno-pool` (192.168.100.224/29,
   MetalLB L2), not the VM's tenant-subnet address.** The tenant subnet
   10.200.0.0/20 is an OVN ClusterUserDefinedNetwork: node-originated
   traffic has no route to it (UDN route only in routing table 1010,
   fwmark 0x1001) and NATGateway is unimplemented in the k8s_only
   NetworkClass manager (OSAC-4265), so the site cannot reach the VM
   subnet directly. ExternalIP → MetalLB IPAddressPool + L2Advertisement
   (pool autoAssign false) → LoadBalancer service in the VM namespace
   (selector vm.kubevirt.io/name, tcp/22) is the supported path. The
   provider-administrator ExternalIPPool is a runbook prerequisite.

## CLI/API behaviors that differ from help text or expectations

5. **Security-group egress `protocol=all` without a CIDR is rejected** by
   API validation (`egress[0].ipv4_cidr required`) even though the CLI
   help shows `--egress protocol=all` as an example. All egress rules
   need `ipv4-cidr=0.0.0.0/0` for allow-all.

6. **`osac create -f -` overwrites `metadata.tenant` with the selected
   tenant** (create_cmd.go:211). Catalog objects (DiskImage, Template,
   CatalogItem, InstanceType) live in the reserved `shared` tenant, so
   `osac tenant shared` must be selected *before* their manifest create;
   the manifest's `tenant:` field alone is not enough.

7. **Shared-catalog visibility is per-type.** Shared InstanceTypes are
   visible from other tenants' listings; shared DiskImages, Templates and
   CatalogItems are not (admin context). Regular tenant users DO see
   published shared catalog items in their own listings (verified as
   model-user in §6). The admin's confirm-gets therefore run under
   `tenant shared`.

8. **`osac get` exits 0 and prints `[]` for missing objects** — exit codes
   cannot detect existence. The demo script's `have()` inspects the
   `-o yaml` output instead.

9. **`osac delete` can exit 0 while printing "No objects … found"** —
   deletions are verified by listing, not by exit code.

18. **`osac delete` exits non-zero when the object's async deletion
    outlives the command's internal wait — the deletion still
    completes.** Verified in the take-11 cleanup (2026-11-16): the
    externalip and computeinstance deletes printed "skip" (non-zero
    exit), yet both objects were gone a few minutes later; the AAP
    delete playbook simply needed longer than the CLI's wait. Related:
    deletes of objects still referenced by a deleting ComputeInstance
    (security group, disk image, instance type) fail transiently with
    "in use"; they succeed once the VM deletion finishes, so `--cleanup`
    is safe to re-run. The cleanup loop now prints the error text
    instead of discarding it.

## Keycloak account requirements (realm defaults)

10. **firstName/lastName are required.** The realm's "Verify Profile"
    default action blocks token grants for users without them
    ("Account is not fully set up"). The user-create payload includes
    firstName/lastName/emailVerified.

11. **Password file must have no trailing newline.**
    `osac login --password-file` strips a trailing newline while
    `jq --rawfile` keeps it, silently mismatching the password. The
    recording writes it with `printf '%s'`.

12. **Fulfillment User relink needs a cleared field.** The User reconciler
    only looks up the Keycloak account while `status.keycloak_user_id` is
    empty; a stale value (Keycloak account deleted/recreated) never
    relinks. Repair: clear the field (`osac edit user <id>`) and the
    reconciler re-links the current account within seconds (verified
    2026-11-16: cleared field re-linked to the current KC user in <10 s).

13. **Deleting a fulfillment User deletes its Keycloak account** (user
    reconciler `task.delete` → `idpClient.DeleteUser`). When that account
    is already gone, Keycloak returns 404, the reconciler treats it as an
    error, the `fulfillment-controller` finalizer is never removed and the
    User hangs in `DELETING` forever (controller log 2026-11-16T09:42:30Z:
    `failed to delete user ... API error: status=404`). This is a product
    bug; the demo script never deletes the User (repairs the link instead)
    and resolves Users by id, because a stuck DELETING object with the
    same name makes name-based get/delete ambiguous. One such tombstone
    (id 01a1e3a1-7434-7708-8065-440af05a7920) remains in the lab;
    removing it requires stripping the finalizer, which the session
    permission classifier denied — left for manual cleanup.

## §7 reference resolution (found 2026-11-16, take 8)

14. **Name-based references in a tenant user's create fail server-side
    reference validation.** `--boot-disk-storage-tier local` fails because
    the tier is a shared-tenant object (verified via the private REST API:
    storagetier `local` has `metadata.tenant: shared`) and name lookups
    are scoped to the referencing object's tenant+project
    (RegisterDAOLookup → scopeNameLookup). `security-groups=allow-ssh`
    fails the same way even though the SG lives in tenant `model` and the
    identical CEL filter matches via `osac get securitygroups --filter`
    (root cause not fully pinned; ID-based lookups are visibility-based
    and succeed). Workaround in script + runbook: reference the SG by ID,
    omit the storage tier (the catalog item's editable default_value fills
    it server-side — verified: the created VM's spec shows
    `storage_tier: {id: local, name: local}`).

15. **Template id must be an AAP role path (root cause of the take-8 VM
    failure).** The AAP provisioning playbooks run
    `include_role: name={{ spec.templateID }} tasks_from: create|delete`
    (playbook_osac_create_compute_instance.yml:165,
    playbook_osac_delete_compute_instance.yml:18), and the server fills
    the hub CR's `spec.templateID` from the fulfillment Template's `id`
    (`RefKeyStr`, prefers id). A Template created without an explicit id
    gets a generated UUID, so provisioning fails with
    `the role '<uuid>' was not found in /runner/project/osac-aap` (AAP
    jobs 8152/8153/8175, 2026-11-16) — *after* the CR is accepted, i.e.
    the VM hangs in STARTING with a Failed provisioningJob rather than
    failing fast. The canonical form is the one
    `osac-installer/scripts/dev-full/seed-catalog.sh` seeds:
    `id: osac.templates.ocp_virt_vm` (role path in the osac-aap project).
    The runbook/script now create the Template with that explicit id.
    Verified live 2026-11-16: with the role-path templateID the create
    job (8173) SUCCEEDED and the delete playbook also completes. Product
    gaps worth a Jira issue: (a) the fulfillment API accepts Template
    creates with arbitrary generated ids that can never provision, and
    (b) the ComputeInstance CRD pattern `^[a-zA-Z_][a-zA-Z0-9._]*$`
    (computeinstance_types.go:114) rejects hyphens — consistent with
    role-path ids, but nothing tells the author of a Template that its id
    is a role contract. The lab CRD was patched
    (`^[a-zA-Z0-9._-]+$`, applied by the user 2026-11-16 10:42Z) while
    debugging; with the correct template id the patch is unnecessary
    (the demo no longer depends on it). Follow-up finding (take 9): the
    chart's publish-templates hook already seeds
    `osac.templates.ocp_virt_vm` (name `ocp-virt-vm`, created
    2026-11-02), and the id is a primary key — creating a second
    template with the same id fails `AlreadyExists`. The runbook
    therefore does not create a template at all: §4 inspects the
    chart-seeded one and §5's catalog item references it by id.

16. **The fulfillment reconciler reverts manual hub-CR spec edits.**
    Patching `spec.templateID` on the hub CR is overwritten back to the
    database record when a provisioning job's feedback lands (observed
    2026-11-16: patch held ~5 min through a create job, then reverted on
    the job-completion feedback). The authoritative fix must be at the
    fulfillment object / Template level; CR patches only work as a
    transient window (used to let the delete playbook resolve the role
    during cleanup).

## §8 ExternalIP SSH — BLOCKED by platform dataplane gap (2026-11-16, take 10)

17. **LoadBalancer service with UDN endpoints is unreachable north-south
    AND east-west; §8 SSH cannot work in this lab.** Take 10 was the
    first run to reach §8: VM provisioning works (§7 RUNNING in 65 s),
    the ExternalIP allocates from br-sno-pool (192.168.100.224), and the
    attachment creates the LB service in the VM namespace
    (subnet-tc9cw, selector vm.kubevirt.io/name, tcp/22). The dataplane
    then breaks:
    - The service's primary EndpointSlice points at the virt-launcher
      pod's cluster-network IP (10.132.1.154, eth0) — nothing listens
      on 22 there; the VM's sshd is on the UDN interface (10.200.0.9,
      ovn-udn1). OVN's endpointslice-mirror-controller
      (endpointslice-mirror-controller.k8s.ovn.org) mirrors the correct
      UDN slice (10.200.0.9), and the metallb_l2 role's relabel
      workaround ran (namespace subnet-tc9cw has the
      k8s.ovn.org/primary-user-defined-network label; the mirror slice
      carries kubernetes.io/service-name).
    - OVN NB programs the VIP ONLY on the UDN's load balancer
      (ovn-nbctl lb-list: `192.168.100.224:22 → 10.200.0.9:22` and the
      nodeport `192.168.100.11:30840 → 10.200.0.9:22`, LB
      b55915e7/20132e6e named cluster_udn_subnet-tc9cw...). No shared
      gateway router LB and no host-side plumbing exists for this
      service: the node has no iptables OVN-KUBE-EXTERNALIP/NODEPORT
      rules for VIP .224 or nodeport 30840 (a stale rule for an older
      cluster-network SSH service on 30022 exists, showing the
      mechanism works for cluster-network backends), and no route in
      the UDN table 1010 (only 10.200.0.0/20 via ovn-k8s-mp1) sends VIP
      traffic into the UDN gateway router.
    - Observed: `ssh fedora@192.168.100.224` times out during banner
      exchange (TCP "connect" succeeds locally only because sshuttle
      terminates it); from the SNO node itself both the VIP and the
      nodeport path time out; a cluster pod (default namespace)
      curl-telnet to the service ClusterIP 172.31.223.90:22 also times
      out — the multi-network service is unreachable even east-west
      from the cluster network.
    Conclusion: ExternalIP SSH for UDN-backed VMs does not work on this
    lab's OVN/OCP (4.20-era ovn-kubernetes). The missing piece is
    host-side classification of VIP/nodeport/clusterIP traffic into the
    UDN gateway router (upstream multi-network services N/S support).
    The SG may additionally need to allow the UDN host-transit source
    (10.200.0.2, ovn-k8s-mp1) — untestable until routing exists.
    Lab state left for debugging: model-vm RUNNING (CR vm-qj9gj, VMI
    10.200.0.9, launcher pod subnet-tc9cw/virt-launcher-vm-qj9gj-d9g2x),
    externalip model-vm-ip ALLOCATED 192.168.100.224, attachment
    model-vm-att, service subnet-tc9cw/osac-eip-externalip-5x7c6-ingress.

    Recording decision (user, 2026-11-16): the demo is recorded through
    §8's ExternalIP allocation + attachment; the SSH connect is then
    attempted with a 15 s timeout and shown failing as documented
    evidence of the gap, followed by an on-screen explanation and the
    serial-console alternative. The runbook §8 carries a matching
    "Known limitation" callout. Filing the product bug (OVN
    multi-network services N/S) remains open follow-up work; items 13
    and 15 describe further product gaps worth tickets.
