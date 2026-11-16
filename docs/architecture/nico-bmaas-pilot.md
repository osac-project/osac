# NICo BMaaS simulator pilot

Status: the full OSAC tenant API → bare-metal operator → AWX → NICo
Machine-a-Tron create/status/delete path was proven on 2026-10-27. The public
delete removed the BMI only after the original NICo Machine returned to
unassigned `Ready`. The integration design and REST cleanup contract are in
[nico-bmaas-integration.md](nico-bmaas-integration.md).

## Isolated lab layout

The pilot uses two CentOS Stream 9 guests on a private libvirt NAT network.
NICo Core, REST, and Machine-a-Tron run in a `nico-dev` Kind cluster in one
guest. OSAC and AWX run in an `osac-dev` Kind cluster in the other. They can
reach each other across the private guest network. The existing K3s and
networking lab workloads on the physical host are left running.

The guests and their private addresses are disposable lab resources. Access
details are kept in internal lab notes, not in this public repository. In the
OSAC guest, the Kind kubeconfig and mono-repo checkout are under the test
user's home directory; the NICo guest holds the infra-controller checkout.

The guests were created from the official CentOS Stream 9 GenericCloud image
after verifying its published SHA-256 checksum. Each has a sparse libvirt
overlay, so the pilot can be removed without changing the base image. The NICo
guest has 12 vCPUs, 64 GiB RAM, and a 200 GiB virtual disk. The OSAC guest has
16 vCPUs, 96 GiB RAM, a 300 GiB virtual disk, and `/dev/kvm` for KubeVirt.

## Baseline versions and commands

NICo checkout: `aae4c09e0` (`v2.5.0-pr-72-gaae4c09e0`). Its DevSpace setup
pins Kind `v0.32.0`, Kubernetes `v1.36.1` for the Kind node, kubectl `v1.36.3`,
Helm `v3.21.3`, and DevSpace `v6.3.21`. The CentOS guest uses Docker CE
`29.9.2`. The local bootstrap completed with:

```bash
kind create cluster --name nico-dev --image kindest/node:v1.36.1
cd infra-controller
bash dev/deployment/devspace/bootstrap-prereqs.sh
GODEBUG=tlsmlkem=0 devspace deploy -n nico-system --profile full
```

The NICo setup script's Kind-node containerd TLS compatibility setting was
applied before the bootstrap. `perl-Digest-SHA` supplies `shasum`, which the
DevSpace source preparation hook requires on CentOS. After installing it, the
full DevSpace deployment completed. Core, REST, Machine-a-Tron, PXE, and BMC
proxy pods are running. NICo REST discovered two simulated GB200 Machines in
`Ready`. The local REST and Keycloak port forwards are persistent systemd user
services in the NICo guest.

OSAC checkout: initially `b0b4a3b05`, later fast-forwarded to `572d8f877`
from the upstream mono-repo before applying the local pilot patch. The OSAC guest uses
Docker CE `29.9.2` and the same Kind, kubectl, and Helm client versions. Its
inotify instance limit was raised to 512 as required by the dev-full installer.
The baseline was installed with:

```bash
cd osac/osac-installer
make install PLATFORM=kind PROFILE=dev-full NS=osac
```

The installer exited successfully. Fulfillment service, OSAC operator, AWX
task/web, and UI pods are running; KubeVirt reports `Deployed`. The modified
fulfillment service, bare-metal operator, and AAP execution environment were
built and loaded into Kind. The bare-metal operator is enabled by
`values/dev/kind-instance-nico-pilot.yaml`; its inventory and management
Secrets select the NICo-specific clients, and its CRD includes the durable
NICo create marker and provider IDs. AWX's two BMI job templates have a
pilot-only NICo credential attached. The AWX project checkout remained at
`b0b4a3b05`, so the new roles and module were staged into its project volume
for this run. A project sync would remove those staged files; production
requires the same code revision in the project and execution environment.

## Observed NICo REST lifecycle

One simulation-only tenant account, Instance Type, one-Machine compute
allocation, /24 IPv4 allocation, Ethernet virtualizer VPC, and /28 subnet were
created via NICo REST. The network allocation's `derivedResourceId` identifies
the tenant-owned IP Block; using the provider-owned parent IP Block for Subnet
creation returned HTTP 400. The VPC and Subnet reached `Ready`.

An FNN VPC request failed because the default DevSpace Site has no routing
profile configured for the tenant. A FLAT VPC reached `Ready`, but instance
creation using `autoNetwork: true` failed because the simulated GB200 host has
DPUs. The successful path used `networkVirtualizationType:
ETHERNET_VIRTUALIZER`, one `interfaces[]` entry with the Subnet ID and
`isPhysical: true`, an Instance Type allocation, and an inline iPXE script.
These distinctions must become explicit pilot configuration, rather than a
silent fallback from the intended production FNN path.

The successful REST create returned HTTP 201 with Instance ID
`8bcd18ae-cf4a-49a8-a839-07d9536bc6d7` and Machine ID
`fm100htfl4jvs6sp96lgmqv13tsmm79ca5vc9k3d6vjqnh6gvdl1g9b7ie0`.
The Instance moved from `Pending` to `Ready` in about 101 seconds. NICo
assigned `10.217.98.2` from the Subnet; the Machine was `InUse` and pointed
to that Instance. `phoneHomeEnabled` was false. The inline iPXE was only
`#!ipxe` followed by `exit`, so this proves the simulator's control plane
lifecycle, **not** that an OS was installed or an OSAC workload booted.

Deleting that exact Instance returned HTTP 202. The Instance entered
`Terminating`. During cleanup the Machine temporarily entered `Error` due to
a `site-explorer` `PoweredOff` health alert; the alert subsequently cleared.
At the end, GET on the Instance returned HTTP 404, and GET on the original
Machine returned `status: Ready`, `instanceId: null`, and no health reports.
The Machine reached `Ready` about 8 minutes 36 seconds after the deletion
request. The operator must allow for this asynchronous cleanup and keep the
BMI finalizer if the alert persists or the Machine fails to return to Ready.

The OSAC guest can authenticate to the NICo REST API across the private NAT
network. The DevSpace Keycloak realm issues tokens whose issuer is based on
the request Host header: requesting a token using the private IP directly
produced a token that REST rejected. For the pilot, requesting it with
`Host: localhost:18082` produced a token accepted by REST across the private
IP. A stable issuer URL and TLS should replace that lab-only workaround in
the deployed AAP credential configuration. The DevSpace account has both
tenant and provider roles; production needs separate scoped identities for
tenant Instance operations and provider Machine inspection. The default
service-account client-credentials token has no NICo roles in this DevSpace
realm, so the lab currently uses the documented development user flow.

## OSAC tenant API create proof and remaining work

The `nico.steps.nico_bmaas` role and `bare_metal_instance` module have now
created and deleted one BMI-UID-labelled NICo Instance against Machine-a-Tron.
An attempted delete with a deliberately wrong saved Machine ID stopped before
the REST DELETE. The correct delete returned only after the original Instance
was absent and the Machine was unassigned `Ready` with no blocking alert.
Create uses a durable one-attempt marker in the new bare-metal operator path;
deletion retains the finalizer until the AAP cleanup artifacts confirm the
same saved Instance and Machine IDs. Focused operator envtest and the full
operator test target passed. The fulfillment server's NICo template exemption
from generic fabric-manager validation passed its focused PostgreSQL test.

For this simulator pilot, AAP configuration must bind exactly one OSAC tenant
and Subnet ID to the NICo Tenant, VPC, Instance Type, and Subnet IDs. The role
checks those OSAC annotations and attachment ID before POST or DELETE. The
NICo Instance `Ready` response must include an assigned interface IP, which
the AAP job publishes in `nicoIPAddress`; the operator maps it to the BMI
network attachment status. OSAC's selected SecurityGroup is not translated
to a NICo Network Security Group in this pilot, so do not use this setup to
claim tenant traffic isolation or security policy enforcement.

The tenant-binding increment selects the AWX NICo credential from an
administrator-owned `bmf.configMaps.nicoTenantBindings` ConfigMap. Its
`bindings.yaml` maps each OSAC tenant ID and Subnet ID to a complete list of
AWX credential IDs. The bare-metal operator selects that list at AAP launch;
the AAP role still validates the OSAC tenant annotation and Subnet against the
credential-injected values. Missing or mismatched bindings fail before a NICo
create, and a missing binding during delete retains the cleanup finalizer.
The binding contains no credential values. Numeric AWX IDs and rebinding of
active BMIs remain pilot limitations; see the operator README for the contract.

The pilot published shared template `osac.templates.nico_bm_host_provisioning`,
catalog item `nico-mat-pilot`, and BMI instance type
`nico-mat-control-plane`. The `tenant1` OSAC Subnet
`01a17e53-cbf5-7819-b22c-dd23665b2e80` maps to NICo Subnet
`888623ab-fd78-4cc7-a949-4f98dd5897dc`. The pilot credential binds those
IDs to NICo Tenant `e98ed547-8e9c-4469-8e21-5b9bea6da8f6`, VPC
`7abed105-1426-4a28-93fe-a0acf6b22318`, and Instance Type
`f737c298-da6c-4517-9db0-7c7d6ed4c835`.
The dev-full AWX template publisher lacked its fulfillment issuer/client
environment, so an administrator registered the template and catalog item
through the private API for this run. The OSAC `fedora` DiskImage and the
pilot BMI instance type satisfy OSAC admission only; the role does not
translate their architecture, capacity, or image into a NICo operating-system
installation. The inline iPXE script exits immediately.

The first two OSAC creates failed before invoking the NICo role because the
AWX project checkout did not contain it. AWX stdout showed the role lookup
error and NICo listed no matching Instance. The operator's one-attempt guard
held the cleanup finalizer, as designed for an uncertain create. After those
two independent checks, an operator administrator cleared each failed BMI's
`nicoCreateAttempted` status marker during public deletion; the resulting
objects were removed. This manual recovery must be documented and tested as
an operator procedure; it is not an automatic retry path.

The third `tenant1` public create produced BMI
`01a17eca-38fb-78b7-be36-61242cdab2a3`. AWX job 12 ran
`osac.templates.nico_bm_host_provisioning` and `nico.steps.nico_bmaas` and
returned NICo Instance `120be71b-3eb2-49b6-8f95-8cd9194c61ca`, original
Machine `fm100htfl4jvs6sp96lgmqv13tsmm79ca5vc9k3d6vjqnh6gvdl1g9b7ie0`,
and IP `10.217.98.2`. The operator persisted both IDs, entered `Ready`, and
reported the IP on `data-0`. The public API reported
`BARE_METAL_INSTANCE_STATE_RUNNING` with `READY=true`; NICo independently
reported its Instance and interface `Ready` with the same Machine and IP.
This is simulator control-plane readiness, not an installed guest OS.

The public deletion of that BMI launched AWX job 13 with
`blockDeletionOnFailure: true`. Its role checked the saved provider IDs before
calling NICo DELETE. The NICo Instance entered `Terminating`; the Machine
temporarily reported `Error`, and the OSAC finalizer remained. Job 13 finished
successfully after about 590 seconds with `nicoCleanupStatus: Released` and
the same Instance and Machine IDs. OSAC then removed its BMI CR and the public
API returned an empty BMI list. Independent NICo reads returned HTTP 404 for
the deleted Instance, `status: Ready` and `instanceId: null` for the original
Machine, and an empty Machine health-report list.

This completes one simulator control-plane vertical slice. Failure-path work
remains: lost create response, competing allocation, delete timeout,
persistent Machine health alert, and repeatability across project and EE
redeployments. The pilot still does not install a guest OS, apply the selected
OSAC SecurityGroup in NICo, or prove network traffic isolation.

The full NICo DevSpace profile includes Machine-a-Tron but does not supply OS
boot artifacts. The next OS install test needs a real iPXE script and boot
artifacts compatible with the simulated `aarch64` host; a `Ready` result from
the control plane alone is insufficient.

> **Note:** This section records the simulator run. The BMI path now boots a
> catalog NICo Operating System selected from the BMI DiskImage, never an
> inline iPXE script, and always enables phone-home, so NICo `Ready` means the
> installed OS called back. See the
> [approved standalone boot path](nico-bmaas-launchpad-pilot.md#approved-standalone-boot-path)
> and the [binding contract](../../bare-metal-fulfillment-operator/README.md#nico-bmaas-tenant-bindings-pilot).

## Two-tenant credential binding extension

The next lab run added a second, distinct NICo IdP org, `test-org-2`, with its
own Tenant Admin identity and NICo Tenant
`12822e76-fe65-4a33-b5fe-82a12cdb1452`. A token for that identity received
HTTP 403 when used with the original `test-org` REST path. The DevSpace
Keycloak realm already mapped the custom `oidc_id` user attribute to a token
claim, but its user profile did not allow newly created users to store that
attribute. Registering `oidc_id` as an admin-managed profile attribute made
the new tenant's NICo identity usable.

The provider accepted a Tenant Account for the new org, created a separate
`10.217.99.0/24` IP Block and allocation, and reserved one GB200 Instance Type
slot for the tenant. The second Ready Machine had to be associated with the
Instance Type before NICo accepted that allocation. The tenant then created an
`ETHERNET_VIRTUALIZER` VPC and a `/28` Subnet. The corresponding OSAC `tenant2`
was onboarded through the dev-full tenant script; its separate VirtualNetwork,
Subnet `01a184e7-0758-77f0-aa99-11683ad6fb74`, and SecurityGroup were
created through the tenant CLI. The OSAC Subnet and NICo Subnet both cover
`10.217.99.0/28`.

AWX credential ID 4 remains bound to `tenant1`, and a separate NICo credential
ID 5 binds `tenant2` to NICo Tenant `12822e76-fe65-4a33-b5fe-82a12cdb1452`,
VPC `79e5c2e5-f9f4-465c-96dd-fc431db3fef8`, and Subnet
`e9d5a9df-433f-4859-ba14-ac3f2ca54113`. The shared BMI create/delete
templates now prompt for credentials and have no default NICo credential.
AWX requires their existing `kind-cluster` credential ID 3 to stay in the
launch list, so the administrator-owned binding ConfigMap selects `[3, 4]`
for `tenant1` and `[3, 5]` for `tenant2`. The BMF Deployment was updated to
image revision `54f9dc585` with a read-only binding ConfigMap mount. The
umbrella Helm release was left in place: a dry run of the newer chart would
also have added the unrelated metering stack. The chart wiring is committed
for a coordinated release.

The first `tenant2` BMI launch sent only credential 5 and AWX rejected it with
HTTP 400 because credential 3 was absent. No AWX create job or NICo Instance
was made. After independently checking that the tenant had zero Instances and
the Machine was unassigned `Ready`, the failed BMI was publicly deleted and
its durable one-attempt marker was cleared on the already-deleting CR. The
fresh public create launched AWX job 24 with credentials `[3, 5]`. It reached
OSAC `RUNNING` and NICo Instance `Ready`, reporting `10.217.99.2` on the
OSAC network attachment. AWX artifacts and the BMI status agreed on NICo
Instance `793eb247-f368-4390-bec3-6ce41a5de197` and original Machine
`fm100htfl4jvs6sp96lgmqv13tsmm79ca5vc9k3d6vjqnh6gvdl1g9b7ie0`.
The NICo Instance carries the new Tenant and VPC IDs above and the matching
NICo Subnet ID. OSAC `tenant1` listed no `tenant2` BMI. NICo chose the
previously used Ready Machine despite the new one-Machine allocation: that
allocation limits capacity by Instance Type but does not pin a physical host.

Deleting the fresh BMI through the public `tenant2` CLI launched AWX job 25
with credentials `[3, 5]` and `blockDeletionOnFailure: true`. NICo reported
the Instance as `Terminating` while the original Machine was still assigned;
the BMI remained `Deleting` with its finalizers. After about 599 seconds, job
25 succeeded with `nicoCleanupStatus: Released` and the saved Instance and
Machine IDs. Independent REST reads then returned HTTP 404 for Instance
`793eb247-f368-4390-bec3-6ce41a5de197`, `Ready` with `instanceId: null`
for the original Machine, and zero Machine health reports. The BMI CR was
removed and the public `tenant2` BMI list was empty. This verifies a second
create/delete control-plane slice with tenant-specific NICo credentials; it
does not add evidence of guest OS installation or NICo data-plane isolation.

The later fresh-tenant CLI recording and its OSAC-to-NICo binding walkthrough
are in [NICo-backed BMaaS tenant flow and CLI recording](nico-bmaas-tenant-flow.md).
