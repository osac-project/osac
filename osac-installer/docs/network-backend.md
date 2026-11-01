# Networking Configuration

OSAC networking follows the unified networking design (OSAC-1433): the
fulfillment service stores networking resources, the OSAC operator runs the
network managers' tasks through AAP, and one NetworkClass selects the managers.
This page describes the installer side.

## Helm values

```yaml
global:
  networking:
    provisioningEnabled: true      # fixed at installation
    excludedIpv4Cidrs: []          # provider infrastructure ranges
    fabricManager: acme_fabric     # required: the Fabric Manager registration name
    k8sManager: cudn_evpn          # "" or cudn_evpn; required with VMaaS
    networkClass:
      defaults:
        virtualNetworkIPv4CIDR: 10.200.0.0/16
        subnetIPv4CIDR: 10.200.0.0/20
        ingressRules: []
        egressRules:
        - {protocol: PROTOCOL_ALL, ipv4Cidr: 0.0.0.0/0}
      eastWestCapabilities:
        supportsEastWestEthernet: false
      # addressPlan: {overlay: 0.5, fabric: 0.25, lbVip: 0.25}
aap:
  instanceGroups:
    networkFulfillment:
      enabled: true
      config: {}   # ConfigMap network-fulfillment-ig, written verbatim
      secret: {}   # Secret network-fulfillment-ig, written verbatim
```

- `provisioningEnabled`: with `false`, OSAC runs no networking jobs and
  networking resources become READY without provider work. The fulfillment
  service stores the value at its first start and refuses to start when a
  later rollout changes it.
- `excludedIpv4Cidrs`: canonical IPv4 CIDRs the provider's infrastructure uses
  (management, provisioning and node networks). VirtualNetwork and
  ExternalIPPool CIDRs may not overlap them.
- `fabricManager`: the `name` in the provider's Fabric Manager registration.
  There is no default. With `netris`, the installer chart renders the
  registration `osac-network-fabric-manager-netris`, which declares
  `eastWestEthernet` and `security_group_icmp`.
- `k8sManager`: the OSAC Kubernetes Manager. With `cudn_evpn`, the installer
  chart renders its registration `osac-network-k8s-manager-cudn-evpn`.
  `cudn_evpn` declares no capability, so with provider networking enabled
  SecurityGroups reject `PROTOCOL_ICMP`; use `PROTOCOL_ALL`, which includes
  ICMP.
- `networkClass.defaults` and `networkClass.eastWestCapabilities` become the
  NetworkClass and cannot change after installation. Rules use SecurityGroup
  rule fields: TCP and UDP rules need `portFrom` and `portTo`; ICMP and ALL
  rules have no ports; `ipv4Cidr` is required. With `k8sManager: cudn_evpn` and
  provider networking enabled, the chart rejects `PROTOCOL_ICMP` default rules.
- `networkClass.addressPlan`: optional shares of each Subnet for the
  Kubernetes Manager (`overlay`), the Fabric Manager (`fabric`) and CaaS
  ingress reservations (`lbVip`). Each is a multiple of 0.0625 and they sum to
  1. Without it the installer derives the plan from the enabled services and
  `k8sManager`:

  | Enabled services | With `k8sManager` | Without |
  |---|---|---|
  | VMaaS, BMaaS, CaaS | 0.5 / 0.25 / 0.25 | not allowed |
  | VMaaS, BMaaS | 0.5 / 0.5 / 0 | not allowed |
  | VMaaS | 0.75 / 0.25 / 0 | not allowed |
  | BMaaS, CaaS | 0.5 / 0.25 / 0.25 | 0 / 0.75 / 0.25 |
  | BMaaS | 0.5 / 0.5 / 0 | 0 / 1 / 0 |
  | None of them | 0.75 / 0.25 / 0 | 0 / 1 / 0 |

  The plan sets a prefix limit for `subnetIPv4CIDR`: from /28 (one non-zero
  share) down to /24 (shares in sixteenths).
- `aap.instanceGroups.networkFulfillment.config` / `.secret`: the managers'
  settings and credentials. Each key starts with the manager's registration
  name in upper case (`NETRIS_CONTROLLER_URL`, `NETRIS_PASSWORD`,
  `NETRIS_SITE_ID`, ...); values may span several lines (CA bundles).
  Required with `provisioningEnabled: true`.
- `aap.configAsCode.eeImage`: the execution environment of every OSAC instance
  group; with a provider Fabric Manager, the provider's image built on
  `aap.bootstrap.image`.

## Manager registration

The installer chart renders the registration ConfigMaps of the OSAC-shipped
managers: `osac-network-k8s-manager-cudn-evpn` for `k8sManager: cudn_evpn` and
`osac-network-fabric-manager-netris` for `fabricManager: netris`. Registrations
and implementations of any other manager are owned by the corresponding manager
teams; the installer does not ship them. When `provisioningEnabled` is `true`,
install those registrations and the provider configuration using the manager's
documented procedure. With provider networking disabled, the operator does not
require manager registrations for networking resources to become READY.

## CaaS on Netris

CaaS can run the Netris CaaS steps (`NETWORK_CLASS=netris`) in the
`cluster-fulfillment` jobs. These jobs are not networking jobs: they read the
`cluster-fulfillment-ig` ConfigMap and Secret, not `network-fulfillment-ig`.
The installation-owned Netris connection settings are given once, with the
Netris settings in `aap.instanceGroups.networkFulfillment.config`, and the
installer passes them to AAP config-as-code. Config-as-code sets them as fixed
environment variables of the cluster-fulfillment container group, which runs
with the `netris.controller.protected_linear` strategy so that launch-time
`extra_vars` cannot override them:

| Key under `networkFulfillment.config` | Cluster-fulfillment effect |
|---|---|
| `NETRIS_CONTROLLER_URL` | `NETRIS_CONTROLLER_URL` |
| `NETRIS_VALIDATE_CERTS` | `NETRIS_VALIDATE_CERTS` (default `true`) |
| `NETRIS_CA_CONFIGMAP` | Mounts `bundle.pem` of this customer-created ConfigMap in the AAP namespace read-only at `/etc/netris/ca/bundle.pem` and sets `NETRIS_CA_PATH`. The ConfigMap is required at pod start. |

The installer renders these keys only when they are set, and the render fails
unless `NETRIS_CONTROLLER_URL` is an `https://` URL and `NETRIS_VALIDATE_CERTS`
is `"true"` or `"false"`. The installer reads them even when
`networkFulfillment.enabled` is `false`. `NETRIS_CA_CONFIGMAP` applies only to
cluster-fulfillment (CaaS) jobs: it is also written to `network-fulfillment-ig`,
but networking jobs neither mount nor read it. The three keys cannot be set in
`aap.instanceGroups.clusterFulfillment.config`.
The other CaaS Netris settings go in the cluster-fulfillment instance group:

```yaml
aap:
  instanceGroups:
    clusterFulfillment:
      enabled: true
      config:
        NETWORK_CLASS: netris
        NETWORK_STEPS_COLLECTION: netris.steps
        NETRIS_USERNAME: netris
        NETRIS_SITE_ID: "5"
        NETRIS_TENANT_ID: "1"
        NETRIS_TENANT_NAME: Admin
        NETRIS_MGMT_VPC_ID: "4"
        NETRIS_MGMT_VPC_NAME: RH-Infra
        NETRIS_RESOURCE_CLASS_MAP: '{"fc430": {...}}'
      secret:
        NETRIS_PASSWORD: "<password>"   # or the netris-credentials Secret
    networkFulfillment:
      enabled: true
      config:
        NETRIS_CONTROLLER_URL: https://netris.example.com
        NETRIS_VALIDATE_CERTS: "true"
        NETRIS_CA_CONFIGMAP: customer-netris-ca   # optional
```

Instead of `clusterFulfillment.secret.NETRIS_PASSWORD`, the password can be
kept in a Secret named `netris-credentials` (key `NETRIS_PASSWORD`) that is
managed outside Helm in the AAP namespace; cluster-fulfillment jobs load it
when it exists. Cluster-fulfillment jobs also mount the optional
`agentless-net-inventory` ConfigMap at `/var/config/agentless-net` for the
AgentlessNet CaaS steps.

### Netris CA ConfigMap lifecycle

`NETRIS_CA_CONFIGMAP` names a ConfigMap that the platform administrator creates
and owns. The installer never creates, rotates, or deletes it. The Helm client
and the chart values never carry the PEM contents; the validation hooks read
the ConfigMap in-cluster:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: customer-netris-ca
  namespace: <AAP namespace>
immutable: true
data:
  bundle.pem: |
    -----BEGIN CERTIFICATE-----
    ...
    -----END CERTIFICATE-----
```

#### Validation and protection

With `validation.enabled` and `NETRIS_CA_CONFIGMAP` set, install and upgrade run
these checks:

- The pre-install/pre-upgrade hook (`<release>-netris-ca-preflight`) reads the
  ConfigMap and fails unless it exists, sets `immutable: true`, and has a
  non-empty `data.bundle.pem` that OpenSSL can parse as a certificate bundle.
  Each failure uses a stable `ERROR: NETRIS_CA_*` message.
- Both CA jobs run `.Values.cliImage`. With CA validation enabled that image
  must provide `kubectl`, `sh`, `grep`, `mktemp`, `rm` and `openssl`. The
  default `quay.io/openshift/origin-cli` image does; a custom `cliImage` that
  omits `openssl` fails with `ERROR: NETRIS_CA_OPENSSL_MISSING` instead of
  skipping the bundle check.
- The post-install/post-upgrade hook
  (`<release>-netris-ca-identity-validate`) submits a SubjectAccessReview for
  `system:serviceaccount:<namespace>:<aap.serviceAccounts.osacSa>` (default
  `osac-sa`) and fails with `ERROR: NETRIS_CA_IDENTITY_DENIED` unless that
  identity can `get` the ConfigMap. It runs after the CA read Role exists and
  before the AAP bootstrap job, so the first upgrade cannot fail on missing
  RBAC.

Both hooks are gated by `validation.enabled`. Clear `NETRIS_CA_CONFIGMAP` to
keep the system-trust path unchanged and render no CA validation resources.

A ValidatingAdmissionPolicy protects only the active ConfigMap name. It denies
`CREATE`, `UPDATE`, and `DELETE` of that name unless the requester is in
`global.networking.caBundle.adminGroups`, and it fails closed if the policy
cannot be evaluated. Other ConfigMap names are unaffected, so a new CA version
can be created before the Helm switch. The default is
`["system:masters", "system:cluster-admins"]`: `system:masters` is the
break-glass group of the `system:admin` client certificate, and
`system:cluster-admins` contains the built-in `cluster-admin` subjects
(`kubeadmin` and the default cluster administrator binding). An identity
provider group bound directly to `cluster-admin` is in neither group; add each
such group to `adminGroups` explicitly. The namespace controller may `DELETE`
the object during namespace teardown.

The AAP execution ServiceAccount (`osac-sa` by default) is not in
`adminGroups`, so the policy denies it any write to the active ConfigMap. It
only needs `get`, which the chart grants through the `<release>-netris-ca-read`
Role. Its `cluster-admin` binding still lets that identity edit or delete the
policy itself.

#### Rotation and rollback

Treat each CA version as a separate immutable object:

1. Create a new ConfigMap such as `customer-netris-ca-2026-10` with
   `immutable: true` and the new `bundle.pem`.
2. Set `aap.instanceGroups.networkFulfillment.config.NETRIS_CA_CONFIGMAP` to the
   new name and reconcile the release with `helm upgrade`.
3. Wait for the release hooks to complete, then confirm a new AAP job pod mounts
   the new ConfigMap at `/etc/netris/ca/bundle.pem` and completes a real HTTPS
   Netris operation.
4. Keep the previous ConfigMap for the rollback window. The window closes only
   when the administrator no longer needs to roll back.

To roll back, restore the previous ConfigMap name and reconcile, then repeat the
job-pod and Netris operation check. A rollback needs the previous object to
still exist and remain immutable.

Deleting an old version is safe only after a new job has succeeded against the
replacement and rollback is no longer required. Delete it as a platform
administrator. The admission policy protects only the active ConfigMap name, so
once the Helm switch points at a new name the previous copy has no admission
protection: any identity with ConfigMap delete permission, including `osac-sa`
through its `cluster-admin` binding, can remove it. Keep the rollback copy in
place until the rollback window closes; otherwise a rollback needs the
administrator to recreate the ConfigMap from the PEM.

## Checks during a rollout

Render time (`helm template`/`install`/`upgrade`) rejects values that break
the schema or these rules, naming the field:

- `fabricManager` is set; VMaaS needs `k8sManager`;
  `provisioningEnabled` needs `aap.instanceGroups.networkFulfillment.enabled`;
- the address plan in force: shares, sum, `overlay` > 0 exactly with
  `k8sManager`, `fabric` > 0 with BMaaS or CaaS, `lbVip` > 0 with CaaS;
- canonical default CIDRs, the Subnet inside the VirtualNetwork, the prefix
  limit, and `portFrom <= portTo`.

The installer's NetworkClass step (post-install/post-upgrade hook
`create-network-class`, `files/network-class-step.py`) re-checks the address
plan, the default CIDRs and the rules, and then:

- fails when the default VirtualNetwork CIDR overlaps `excludedIpv4Cidrs`;
- creates the NetworkClass when none exists;
- otherwise fails with "NetworkClass <field>[, <field>] cannot change after
  installation; see Replacing the NetworkClass" when `fabric_manager`,
  `k8s_manager`, `spec.defaults`, `spec.east_west_capabilities` or the address
  plan in force (`spec.address_plan`) differ. The NetworkClass spec is
  immutable after creation.

## Admission policy

With the fulfillment service enabled, the chart creates the
ValidatingAdmissionPolicy `<namespace>-osac-managed-resources` and its binding.
They reject, from identities other than the OSAC controllers (the `hub-access`
service account the fulfillment controllers use, the OSAC operator and BMF
service accounts, and the Kubernetes garbage collector and namespace
controller), with "OSAC manages this resource; change it through the
fulfillment API":

- create, update and delete of every OSAC networking CR and NetworkAttachment;
- delete of ComputeInstance, BareMetalInstance and ClusterOrder CRs;
- any update that removes an `osac.openshift.io/` finalizer.

Install one OSAC release per cluster: cluster-scoped OSAC resources match every
release's policy. `helm uninstall` removes the policy first;
`scripts/teardown.sh` deletes it before deleting OSAC CRs.

## CI profiles

| Profile | Provider networking | `fabricManager` | `k8sManager` |
|---|---|---|---|
| `vmaas-ci`, `caas-ci`, `full-ci`, `dev` | disabled | `netris` | `cudn_evpn` |
| `bmaas-ci` (and `bmaas-netbox-ci`) | disabled | `netris` | `""` |
| `vmaas-ci`, `full-ci` with `netris.yaml` | enabled | `netris` | `cudn_evpn` |
| `bmaas-ci` with `netris.yaml` | enabled | `netris` | `""` |

With provider networking disabled the profiles need no network manager
registrations or provider settings. Manager registrations are required when
provider networking is enabled.
