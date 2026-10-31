# External Services Support Guide

**Audience:** Administrators deploying and operating OSAC with its backing
services — PostgreSQL, OpenBao, Keycloak, and Kafka — running **outside**
the OpenShift/Kubernetes cluster, for example as podman containers on a
services host.

This guide covers a **clean deployment**: the external-services values are
set before the first `helm install`, so OSAC comes up against external
services from the start — common prerequisites, the per-service
**external-mode configuration** (exactly which files and parameters to
modify), verification, backups, and surviving `helm upgrade`. Ready-to-run
container recipes for all four services are collected in
[Appendix A](#appendix-a-running-the-services-as-containers).
Section 6 implements the broker contract from the [external Kafka
configuration guide](kafka-configuration.md); for OpenBao/Vault background
see the [secrets management configuration
guide](secrets-management-configuration.md), and for the OSAC realm layout
see the [Keycloak configuration guide](../keycloak-configuration.md).

**Doing a clean install?** Start with the [installation
walkthrough](#start-here-the-installation-walkthrough) — the single
ordered path through this guide — and treat the rest as reference.

**What was validated.** The container recipes in Appendix A and the
external services themselves were exercised end-to-end on a lab deployment
(single-node OpenShift hub plus a podman services host, October 2026,
installer chart pin `fc7a09f`; PostgreSQL 18, Keycloak 26.6.4, OpenBao
2.6.2, Kafka 4.2.0) — but as part of moving a live bundled deployment to
those external services. The clean-install values-first flow this guide
teaches uses the same chart interface and the same services, yet was
**not** separately exercised end-to-end: prove it on a scratch install
before relying on it in production. Two chart and service interface
changes postdate that pin: `metering.enabled` was removed upstream —
metering is now always deployed (§6.2) — and the Kafka pipeline moved
from shared metering topics to per-tenant ones created by the services
themselves (§6.1, A.5 step 9); this guide now describes the current
contract, which the lab did not run. The lab ran with metering on, so
the validated behavior matches current pins for everything except the
per-tenant topic creation, which the greenfield exercise must confirm.
The off-switch is gone regardless. (Migrating an already-bundled
deployment is a different procedure — sequencing, data movement, rollback
points — and is outside this guide's scope.)

This guide contains no credentials: secrets always appear as
`<placeholders>`, and each section names where the real value lives
(root-only files on the services host, or cluster Secrets).

---

## Table of Contents

0. [Start here: the installation walkthrough](#start-here-the-installation-walkthrough)
1. [Overview and integration patterns](#1-overview-and-integration-patterns)
2. [Common prerequisites](#2-common-prerequisites)
3. [PostgreSQL](#3-postgresql)
4. [OpenBao](#4-openbao)
5. [Keycloak](#5-keycloak)
6. [Kafka](#6-kafka)
7. [Helm values reference](#7-helm-values-reference)
8. [Surviving `helm upgrade`](#8-surviving-helm-upgrade)
9. [Verification battery](#9-verification-battery)
10. [Backups](#10-backups)
11. [Gotchas compendium](#11-gotchas-compendium)

Appendix A. [Running the services as containers](#appendix-a-running-the-services-as-containers)

**Conventions used throughout:** `<host-ip>` is the address on the services
host that cluster nodes can reach; `<kc-host>` is the hostname your
external Keycloak serves on port **8443** — every Keycloak URL in this
guide carries the port explicitly; everything in angle brackets
is a value to substitute. The namespaces (`osac`, `osac-infra`,
`keycloak`, …) and object names (`external-kafka`, `osac-db-config`, …)
are chart-level constants — use them as-is.

---

## Start here: the installation walkthrough

The rest of this guide is reference material — each section is complete
for its service. This section is the **single ordered path** through all
of them for a clean install. Follow it top to bottom; every stage ends
with a gate that must be green before you continue, so a failure surfaces
in the stage that caused it instead of three stages later.

It walks alongside the [Helm Deployment
Guide](helm-deployment-guide.md), which owns the cluster-side install
(the `osac-deps` → `osac-infra` → `osac` releases). Vocabulary maps
directly: that guide's `my-infra-values.yaml` absorbs this guide's
`values/external-infra.yaml`, and its `my-values.yaml` absorbs
`values/external-services.yaml` + `values/external-kafka.yaml` (§7).
The prerequisite phases (`osac-deps`, `osac-infra`) install in stage 2
with the external overlay from the start; the external services start
in stage 3; only the final `osac` release installs in stage 4.

How the integration itself works, for every service, is one sentence:
a chart flag (`*.enabled: false`) stops the chart from deploying its
bundled copy, and endpoint keys point the OSAC components at your
external instance — always both actions together (§7 has the full key
reference and what breaks when either is skipped). The wiring is
entirely declarative, so nothing needs re-applying after a
`helm upgrade` (§8). One caveat: for Keycloak, the values point
consumers at the external issuer but do not provision it — the realm
and client-credential Secrets are stage-3 work (§5.1–5.3).

### Before you begin — the worksheet

Complete this before touching any host. Every `<placeholder>` in this
guide resolves to a row here; filling it once prevents the most common
class of install failure (inconsistent names, addresses, and secrets
between stages).

| # | Item | Your value | Decided by / notes |
|---|---|---|---|
| 1 | Installer chart pin + image digests | | a tagged release or tested commit; §7's digest-pinning trap |
| 2 | Services host: OS, resources, address `<host-ip>` | | dedicated machine, §2.1; the worksheet value is what every consumer points at |
| 3 | Privileges you hold | | cluster admin on the hub; root (or sudo) on the services host |
| 4 | DNS names: `<kc-host>`, `<ui-origin>`, `<api-route>` | | `<kc-host>` and `<api-route>` are hostnames, `<ui-origin>` is the full UI origin including the `https://` scheme; all resolvable from cluster **and** from admin/browser machines; §1.2 hostname stability |
| 5 | CA strategy per service | | corporate CA or self-signed per §2.2; same CA signs server + client certs where mTLS applies |
| 6 | Keycloak: `<kc-host>`, port 8443, realm-admin + bootstrap admin credentials, client secrets (5) | | §5.2 rows 1–11; generated, stored in your credential store |
| 7 | PostgreSQL: DB names/users, server + client cert plan | | §3.1; HBA source IPs (cluster egress, `<kc-source-ip>`) |
| 8 | OpenBao: storage (file vs Raft), unseal strategy, init-output escrow location | | A.3; escrow is a §10.4 requirement, not an afterthought |
| 9 | Kafka: listener address, SCRAM users, broker CA | | §6.1/A.5; metering is always on at current pins (§6.2 status note) |
| 10 | Namespaces: `osac`, `osac-infra`, `osac-deps` | | chart constants — use as-is |
| 11 | Backup + escrow destination (off-host, encrypted) | | §10.4; decide before stage 3, use from stage 3 on |
| 12 | Secret values behind every chart Secret | | §5.2/§6.2 object names are fixed; the **values** are yours |

### Stage 1 — Decide and gather

**Run from:** anywhere. **Privileges:** none.

Read §1.1's profiles-and-status matrix and decide, per service, whether
the reference recipe (Appendix A) or your own production-grade instance
is the starting point. Fill the worksheet. Two decisions are hard to
reverse later and worth extra care: the Keycloak hostname (§1.2 — baked
into deployments and OpenBao's JWT config) and the CA strategy (§2.2 —
rotating it later touches every consumer).

**Gate:** the worksheet is complete — every `<placeholder>` you will
meet in stages 2–5 has a value.

### Stage 2 — Prerequisites and cluster foundation

**Run from:** admin host (cluster) and services host (host prep).
**Privileges:** cluster admin; root on the services host.

1. Services host: OS prepared, the stable `<host-ip>` chosen, firewall
   rules scoped per port per §2.1 (this stage **checks the
   configuration** — the reachability probes run in stage 3, after the
   listeners exist; a closed port now means "not started yet", not a
   network fault).
2. Cluster side — install the [Helm Deployment
   Guide](helm-deployment-guide.md)'s prerequisite phases **now**, with
   the external overlay applied from the start: phase 1a (`osac-deps`)
   per that guide, then phase 1b (`osac-infra`) with
   `my-infra-values.yaml` carrying §7's infra block
   (`bundledPostgres/bundledVault/keycloak: false`, `kafka: false`) —
   the bundled operands are skipped, and the pieces the external path
   *does* need from these phases are created: namespaces, cert-manager
   with its ClusterIssuer, and the trust-manager Bundle (§2.2 option 1
   depends on them for cluster-issued certificates).
3. Clock: `chronyc tracking` sane on the services host and the cluster
   nodes (§2.3 — JWT validation breaks at ~30 s skew).
4. Wait for the required resources: operator CSVs succeeded
   (that guide's phase-1a checks), the `osac` namespace exists, the
   ClusterIssuer reports Ready, and the trust Bundle is created (its
   CA entries are added in stage 3, when the service CAs exist).

**Gate:** phase 1a and 1b releases report success; issuer and Bundle
exist. **Stop if:** the infra release fails — nothing in the external
services path will fix a cluster-side prerequisite failure.

### Stage 3 — Start the external services

**Run from:** services host (recipes, realm seed) and admin host
(Secrets, Kafka objects). **Privileges:** root on the services host;
cluster admin.

Everything here must exist **before phase 2 (`helm install` of the
`osac` release)**:

1. Run the Appendix A recipes for the services you are externalizing —
   A.2 PostgreSQL, A.4 Keycloak, A.5 Kafka first — or stand up your own
   instances to the §3.1/§5.2/§6.1 contracts. **Configure OpenBao
   (A.3) last**: its JWT auth and auto-unseal point at Keycloak, so the
   issuer must be up and its realm seeded (§5.3) before OpenBao's
   Keycloak-facing configuration runs.
2. Seed the Keycloak realm (§5.3) and create the client-credential
   Secrets (§5.2) — the part the chart's `keycloak.enabled: false` flag
   does not do for you.
3. Create the Kafka connection objects if Kafka is external (§6.2), and
   the shared inference topics and service users (A.5 steps 7–9).
4. Load the services CAs into the trust Bundle created in stage 2
   (§2.2) — consumers come with stage 4.
5. Start the §10 backup cron jobs and escrow the OpenBao init output
   and credential material per §10.4 — recovery material exists from
   the moment the services do.

**Gate — standalone readiness, not application tests.** The listeners
exist now, so prove the path and each service on its own terms:
§2.1's in-cluster netcheck probe for each port (5432, 8200, 8443,
9092 — from a pod, not from the host); PostgreSQL presenting its server
certificate over TLS (`openssl s_client -connect <host-ip>:5432
-starttls postgres` from the admin host — the client certificates the
db-init job uses do not exist until the chart issues them in stage 4,
so client-certificate authentication is exercised there, not here);
OpenBao health 200/unsealed (§4.3's
health checks); Keycloak serving the realm and issuing a token (§5.4);
Kafka accepting an admin-client connection (A.5 step 7's readiness
check). Checks that need the **application** — OSAC's PG sessions,
OpenBao tenant provisioning, metering heartbeats, UI login — belong to
stage 5; a fresh install cannot pass them here. **Stop if:** a probe
fails — the chart will not improve it, and stage 5's battery will only
re-report it.

### Stage 4 — Install the OSAC application

**Run from:** admin host with the installer checkout.
**Privileges:** cluster admin.

Phase 2 of the [Helm Deployment Guide](helm-deployment-guide.md) —
`helm upgrade --install osac ./charts/osac` — with `my-values.yaml`
carrying §7's complete external-services + Kafka overlays. The
deltas to that guide's bundled defaults:

- `ui.auth.oidcIssuerURL` must be the **full** external issuer URL,
  not the `--set` default that guide shows for the bundled Keycloak.
- **Apply the Strimzi CRDs first** (`KafkaTopic`, `KafkaUser`,
  `Kafka`) if Kafka is external and this cluster never ran the bundled
  stack — the metering subchart renders its `KafkaTopic`/`KafkaUser`
  objects ungated, so the `osac` release fails on unknown kinds without
  them (§6.2 prerequisites). They stay inert without the operator.
- The overlays are the external-services **delta**, not the whole of
  `my-values.yaml` — combine them with the base installation values
  that guide describes (services you deploy, storage, CNV, and so on).
- Do not pass the external keys as one-off `--set` flags — §8's
  `--reuse-values` semantics will faithfully carry typos forward.

**Gate:** the release reports success **and** the workloads are
actually ready: every deployment in `osac` reports
`Available=True` (`oc -n osac get deploy`), no pods remain unready or
CrashLoopBackOff, and the initialization Jobs (db-init, AAP bootstrap)
completed successfully — `oc -n osac get jobs` shows them
`Complete`. **Stop if:** pods crash — read the logs before re-running
helm; the two usual causes are a Secret the chart expects but stage 3
didn't create (§5.2), and trust failures to the services CAs (§2.2,
§11 item 1).

### Stage 5 — Accept and rehearse

**Run from:** admin host. **Privileges:** cluster admin.

1. Run the full §9 verification battery — including the **positive**
   auth control (a real token gets 200), not just the 401 negative one.
   This is where the application-level checks from stage 3's gate
   land: OSAC's database sessions, OpenBao tenant provisioning,
   metering heartbeats, and a UI login.
2. Run one specific smoke workflow end to end — the tenant smoke in
   §9's battery: create a tenant via the API, wait for its break-glass
   Secret to appear (the reconciler's completion signal), list it,
   delete it, and confirm it is gone. If your organization defines
   its own acceptance smoke, run that instead — the requirement is
   one workflow with expected results and cleanup, not an impression.
3. Rehearse recovery per §10.4: restore **each service's** data from
   the backups stage 3 started, on a machine that is not the live one
   if you can, and re-run §9. An unrestorable backup is a defect
   **now**, while fixing it is cheap.

**Gate:** §9 all green, the smoke workflow completes with its expected
results, and every service's restore rehearsal succeeds. Done —
continue operating with §8 (upgrades), §10 (backups), and §11
(gotchas) as the running references.

---

## 1. Overview and integration patterns

### 1.1 What "external services" means

| Service | Role | Typical external form | Port |
|---|---|---|---|
| PostgreSQL | fulfillment + metering databases | podman container, TLS + client-cert auth | 5432 |
| OpenBao | per-tenant secrets, transit, JWT-backed lifecycle tokens | podman container, TLS, file storage | 8200 |
| Keycloak | IdP for users and service accounts | podman container, TLS (production mode) | 8443 |
| Kafka | event bus (fulfillment → metering) | podman container, KRaft, SASL_SSL | 9092 |

Reasons to externalize: free cluster storage (a bundled PG + OpenBao + Kafka
easily holds 300+ Gi of PVCs), host-level backup and ops tooling, and
isolating stateful services from cluster reinstalls.
What typically **stays** in-cluster: AAP, CNV/KubeVirt, cert-manager,
trust-manager, MetalLB, MCE (multicluster engine for Kubernetes) with the
workload clusters it manages, the operator, and the UI.

**Profiles and status — what you are getting.** The Appendix A recipes
are a *reference profile*, not an availability or durability design.
Before following any of them, know which parts are lab-grade, which are
production-grade, and which are experimental:

| Component | Reference recipe (Appendix A) | For production |
|---|---|---|
| PostgreSQL | single container, TLS + cert auth | same shape; size, HA and PITR are yours to add |
| OpenBao | single node, file storage, auto-unseal drop-in | single-node Raft on ≥ 2.7 (A.3 note); unseal-key escrow (§10.4) |
| Keycloak | H2 database — **lab/evaluation only** | PostgreSQL-backed variant (A.4) |
| Kafka | single broker, replication factor 1 | a second durable copy of metering events is **required** for event durability (§10 policy) |
| Off-cluster metering | **experimental** (§6.2) — metering itself is always deployed on current chart pins | not upstream-supported; alternatives in §6.2 |

### 1.2 What must not change

The in-cluster consumers verify identity, not location:

- **PostgreSQL**: clients authenticate with **client certificates** and
  connect to the address in `osac-db-config` — `<host-ip>` on this path.
  The external server must present a certificate valid for that same
  address, signed by a CA the db-init job trusts (§3.1), and keep the
  cert-auth HBA rules — then the `osac-db-config` /
  `osac-db-client-cert` Secrets work as the chart renders them.
- **Keycloak**: JWT issuer URLs (`https://<kc-host>:8443/realms/osac`) are set
  by values and baked into deployments and the OpenBao JWT auth config.
  Keep the hostname stable after install — changing it later means
  re-pointing every consumer.
- **OpenBao / Kafka**: plain endpoints in values — the simplest cases.

---

## 2. Common prerequisites

Cluster-side prerequisites that apply to every external service. The
host-side preliminaries for running the containers themselves — SELinux
labels and systemd unit shapes — are covered in
[Appendix A.1](#a1-host-preliminaries-selinux-and-systemd-units).

### 2.1 Network

The services host is a separate machine from the cluster nodes, on a
network the cluster can reach. Three rules:

- **Plain L3 routing, no NAT.** Cluster nodes (and therefore pods) must
  be able to route to `<host-ip>` directly — a normal routable address
  on your management network, not one translated or hidden behind NAT
  on the path from the cluster. Verify with a `TCP` probe from a pod
  (any image with a shell works; the `/dev/tcp` builtin needs nothing
  else installed):
  `oc -n osac run netcheck --rm -i --restart=Never --image=registry.access.redhat.com/ubi9/ubi-minimal -- bash -c 'exec 3<>/dev/tcp/<host-ip>/5432 && echo reachable'`.
- **Publish each service only on the specific, stable IP**
  (`-p <host-ip>:<port>:<port>`), never on `0.0.0.0` — keeps the
  services off every other interface the host owns, and pins the
  address the cluster and the helm values point at (a DHCP-changing
  address breaks TLS SANs and Endpoints together).
- **Open the ports in the host firewall — scoped per port, not one
  cluster-only rule.** The consumers differ by service (§2.2's trust
  map): PostgreSQL (5432), OpenBao (8200), and Kafka (9092) are
  consumed by cluster workloads and by the admin host this guide's
  probes run from; Keycloak (8443) is additionally reached by
  **browsers** loading the OSAC UI's login redirect. Open each port to
  the cluster subnet plus the admin host, and 8443 additionally to the
  approved browser/client networks — and to nothing else. This does
  not expose the data-plane services to browser or public networks.

### 2.2 TLS trust model

The cluster's trust root is the cert-manager `ClusterIssuer` `default-ca`,
distributed by trust-manager into the `ca-bundle` ConfigMap in the
namespaces the `Bundle` CR's `namespaceSelector` targets. Two ways to
make the externals trusted — in order of preference:

1. **Issue the external service's certificate from the cluster** (a
   cert-manager `Certificate` CR with an IP SAN of `<host-ip>` — or a DNS
   SAN of `<kc-host>` for Keycloak; PKCS#8 for Kafka). The existing
   `ca-bundle` already trusts it; **zero trust changes**.
2. **External CA**: add it as a new source to the `Bundle` CR:

   ```bash
   kubectl create secret generic <svc>-host-cert -n cert-manager \
     --from-file=ca.crt=/path/to/ca.pem --dry-run=client -o yaml | kubectl apply -f -
   kubectl patch bundle.trust.cert-manager.io ca-bundle --type=json -p='[
     {"op":"add","path":"/spec/sources/-",
      "value":{"secret":{"name":"<svc>-host-cert","key":"ca.crt"}}}
   ]'
   ```
   (A source `secret` carries no namespace field — trust-manager reads
   source secrets from its trust namespace, `cert-manager`, which is where
   the command above creates it.)

> Never edit the `ca-bundle` ConfigMap directly — trust-manager reverts it
> within seconds. Always patch the `Bundle` CR. If you add a CA and clients
> still fail with `x509: certificate signed by unknown authority`, restart
> the consuming deployments — some cache the bundle at startup.

**Who has to trust what.** Trusting a CA in one place does not document
it anywhere else — every leg of the picture needs its own decision:

| Connection | Must trust the CA that signs… |
|---|---|
| cluster pods → each external service | the service's server cert — via the `ca-bundle` (this section) |
| OpenBao → Keycloak (JWT discovery) | Keycloak's server cert — `oidc_discovery_ca_pem` in A.3 |
| browser → UI route and `<kc-host>` | the UI route cert and Keycloak's server cert — usually a public/corporate CA |
| administrator host → services | each service's server cert — the `--cacert` flags in this guide's commands |

### 2.3 Clock synchronization

JWT validation fails beyond ~30 s skew between the Keycloak host and cluster
nodes (`token issue time is in the future`). Check with
`podman exec <keycloak-container> date -u` vs
`oc -n osac exec deploy/fulfillment-grpc-server -- date -u`; fix with
`chronyc -a makestep`. OpenShift's `machine-config` operator will flag its
own chrony drift independently — that is a cluster-side symptom of the same
problem.

---

## 3. PostgreSQL

### 3.1 Requirements

The external server must be interchangeable with the bundled instance:

- PostgreSQL major version matching the bundled chart (validated:
  PostgreSQL 18 — run recipe in
  [Appendix A.2](#a2-postgresql)).
- **Server certificate valid for the address clients dial** (the URL in
  `osac-db-config`) — on this path that is `<host-ip>`: issue a
  certificate with an IP SAN of `<host-ip>` from a CA the db-init job
  trusts (§2.2 option 1).
- **Client-certificate (mutual TLS) auth** preserved — the HBA rules in
  Appendix A.2 are what the chart's bundled instance uses; keep them
  identical.
- **Database identities:** the workload users are `osac_service` and
  `osac_metering`. The chart's pre-install db-init job connects as the
  `postgres` superuser (client certificate with CN `postgres`, issued
  in-cluster by the chart's issuer) and creates both roles and both
  databases itself; the workload client certificates it then issues carry
  CNs `osac_service` / `osac_metering` — with `cert` auth the CN must
  equal the role name, and it does.
- **Administrator mTLS prerequisite, before `helm install`:** the external
  server must trust the CA that signs those client certs (its
  `ssl_ca_file` — A.2's `ca-bundle.pem`), present a server certificate
  valid for the `dbInit.host` address signed by a CA the job trusts (the
  cluster's `default-ca` under §2.2 option 1), and keep the A.2 HBA rules
  (`hostssl … cert` for `postgres`, `osac_service`, `osac_metering`).

### 3.2 External-mode configuration — what to modify

Set these values before the first `helm install`: the db-init job then
runs against the external instance and the chart-consumed Secrets point
at it from the start — no cutover, nothing to re-apply. Every file,
object, and parameter involved:

| # | Where | Object / file | Parameter | Value |
|---|---|---|---|---|
| 1 | `osac-infra` release | values | `bundledPostgres.enabled` | `false` |
| 2 | `osac` release | values | `dbInit.host` | `<host-ip>` (port 5432) |
| 3 | ns `osac` | Secrets `osac-db-config`, `osac-db-client-cert` | — | chart-rendered from `dbInit.host` |

```yaml
bundledPostgres:
  enabled: false        # osac-infra release
dbInit:
  host: <host-ip>       # osac release; port 5432
```

The `osac-db-config` (URL) and `osac-db-client-cert` (tls.crt/tls.key/ca.crt)
Secrets in ns `osac` are chart-consumed and are created against
`dbInit.host` from the start.

### 3.3 Verify

```bash
# connections arrive on the external instance from the expected users
podman exec osac-postgres psql -U postgres -Atc \
  "select usename, count(*) from pg_stat_activity where usename in ('osac_service','osac_metering') group by 1"

# writes flow (run twice ~60 s apart; counters must rise)
podman exec osac-postgres psql -U postgres -Atc \
  "select datname, xact_commit from pg_stat_database where datname like 'osac_%'"
```

---

## 4. OpenBao

### 4.1 What the external OpenBao must provide

- A TLS listener on `<host-ip>:8200` with a certificate the cluster trusts
  (§2.2) — the run recipe is in
  [Appendix A.3](#a3-openbao).
- Bootstrap the server and create the objects the values in §4.2 expect:
  initialize it (`bao operator init`, A.3), create the `osac` namespace,
  enable JWT auth in it, and create the `lifecycle` policy and role
  (bound to the Keycloak issuer, audience `osac-api`) — the executable
  commands are in A.3. **No KV or transit mounts in the parent
  namespace** — the controller creates those inside each tenant
  namespace it provisions, named by `kvMountPath` / `transitMountPath`.
  (Creating one by hand for testing? `bao secrets
  enable -namespace=<ns> -path=secret kv-v2` puts the mount at `secret/`;
  without `-path=secret` it lands at `kv-v2/` and the controller cannot
  find it.)

### 4.2 External-mode configuration — what to modify

Values only — the wiring is declarative and survives upgrades (§8).
Every key involved:

| # | Release | Parameter | Value |
|---|---|---|---|
| 1 | `osac-infra` | `bundledVault.enabled` | `false` |
| 2 | `osac` | `service.vault.endpoint` | `https://<host-ip>:8200` |
| 3 | `osac` | `service.vault.caBundle.configMap` | `ca-bundle` (add the host CA per §2.2 option 2 if self-signed) |
| 4 | `osac` | `service.vault.keycloakIssuerUrl` | `https://<kc-host>:8443/realms/osac` |
| 5 | `osac` | `service.vault.keycloakClientId` / `keycloakAudience` | `osac-controller` / `osac-api` |
| 6 | `osac` | `service.vault.namespace` / `kvMountPath` | `osac` / `secret` |
| 7 | `osac` | `service.vault.lifecycleMountPath` / `lifecycleRole` | `jwt` / `lifecycle` |

```yaml
# osac-infra release
bundledVault:
  enabled: false

# osac release
service:
  vault:
    endpoint: https://<host-ip>:8200
    caBundle:
      configMap: ca-bundle          # add the host cert per §2.2 option 2
    keycloakIssuerUrl: https://<kc-host>:8443/realms/osac
    keycloakClientId: osac-controller
    keycloakAudience: osac-api
    namespace: osac
    kvMountPath: secret
    lifecycleMountPath: jwt
    lifecycleRole: lifecycle
```

### 4.3 Verify

`/v1/sys/health` returning 200 only proves the server. Prove the **write
path**: create a throwaway tenant in the UI/API and watch the controller
provision its vault namespace (namespace + KV mount + transit + JWT auth +
policy + role + break-glass secret) — e.g. list
`<namespace>/sys/mounts` with the root token — then read the tenant's
break-glass secret with a front-door tenant token.

---

## 5. Keycloak

### 5.1 The strategy

`keycloak.enabled: false` plus the issuer/IdP/UI values — every consumer
is configured by values from the start; no imperative wiring exists
anywhere on this path. What the flag does **not** cover is everything the
chart's Keycloak machinery did: the hooks that generate the
service-client credentials and the realm import are all gated on
`keycloak.enabled`, so with the flag off **you provision the
client-credential Secrets and the realm yourself, before
`helm install`** — the exact checklist is §5.2 step 1–3 and the seed
procedure is §5.3. (This flow was not exercised end-to-end in the
validation lab — see the provenance note in the introduction.)

### 5.2 Clean install: external issuer

Disable the bundled instance and point every consumer at the external
issuer. The full list of modification points (the credential entries
are structures, not scalars — the exact shapes follow in the YAML
below):

| # | Where | Parameter | Value |
|---|---|---|---|
| 1 | `osac-infra` release | `keycloak.enabled` | `false` |
| 2 | `osac` release | `service.auth.issuerUrl` | `https://<kc-host>:8443/realms/osac` |
| 3 | `osac` release | `service.auth.controllerCredentials` | Secret `fulfillment-controller-credentials` (client-id / client-secret) |
| 4 | `osac` release | `service.idp.provider` | `keycloak` |
| 5 | `osac` release | `service.idp.url` | `https://<kc-host>:8443` |
| 6 | `osac` release | `service.idp.credentials` | same Secret as row 3 |
| 7 | `osac` release | `service.vault.keycloakIssuerUrl` | `https://<kc-host>:8443/realms/osac` |
| 8 | `osac` release | `service.vault.credentials` | Secret `vault-credentials` (client-secret) |
| 9 | `osac` release | `ui.auth.oidcIssuerURL` | `https://<kc-host>:8443/realms/osac` — full URL, not tpl-expanded |
| 10 | ns `osac` | Secrets (yours to create) | rows 3/6/8's Secrets — plus UI-backend / CSI-driver Secrets **only if** your values add credential entries for them; the shape above does not, but §5.3 still assigns their realm clients secrets, so pick values once and keep them consistent |
| 11 | external Keycloak | realm `osac` | seeded per §5.3, clients matching the Secrets' IDs and secrets |

`osac-infra` release:

```yaml
keycloak:
  enabled: false
```

`osac` release — the issuer/IdP/UI keys (the bundled path fills these
with the route hostname; the chart's CI renders exactly this shape):

```yaml
service:
  auth:
    issuerUrl: https://<kc-host>:8443/realms/osac
    controllerCredentials:
    - secret:
        name: fulfillment-controller-credentials
        items:
        - {key: client-id, param: client-id}
        - {key: client-secret, param: client-secret}
  idp:
    provider: keycloak
    url: https://<kc-host>:8443
    credentials:              # the same Secret is fine
    - secret:
        name: fulfillment-controller-credentials
        items:
        - {key: client-id, param: client-id}
        - {key: client-secret, param: client-secret}
  vault:
    keycloakIssuerUrl: https://<kc-host>:8443/realms/osac
    credentials:
    - secret:
        name: vault-credentials
        items:
        - {key: client-secret, param: client-secret}
ui:
  auth:
    oidcIssuerURL: https://<kc-host>:8443/realms/osac   # not tpl-expanded — full URL
```

The fulfillment controller requires `idp.url` + `idp.credentials`; its
deployment projects the credentials to files named by the `param`s
(`client-id`, `client-secret`) and mounts them under
`/etc/fulfillment-controller/idp`.

What the flag does **not** cover (§5.1): the chart's credential hooks
and realm import. Provide them yourself before `helm install`:

1. **Client-credential Secrets** — the ones the values above reference,
   carrying the realm's client IDs and secrets:
   ```bash
   oc -n osac create secret generic fulfillment-controller-credentials \
     --from-literal=client-id=<controller-client-id> \
     --from-literal=client-secret=<controller-client-secret>
   oc -n osac create secret generic vault-credentials \
     --from-literal=client-secret=<controller-client-secret>
   ```
   (`service.vault.keycloakClientId` is the `osac-controller` client, so
   this is the **same secret** as the one above, not a new one. The
   UI-backend and CSI-driver Secrets follow the same shape if your
   values reference them.)
2. **The realm** — seed it per §5.3. Its clients must exist with matching
   IDs and secrets, and the controller client needs the realm-management
   roles `manage-realm`, `manage-users`, `view-realm`, `view-users`.
3. **Trust and reachability** — the issuer host's CA in the cluster trust
   bundle (§2.2), and the host resolvable and reachable from pods.

### 5.3 Seed the realm

Import the chart's `realm.json` (in the installer at
`osac-installer/charts/osac-infra/files/realm.json`; copy it out of the
checkout or read it in place before running the preprocessing below)
with its `__OSAC_*__` placeholders resolved — the realm admin
username/password, the admin/controller/CSI-driver/UI-backend client
secrets, and the UI origin — then apply the post-import fixups
(`profile` and `email` scopes).

In a bundled install the chart does this itself: a
`resolve-realm-secrets` initContainer fills the placeholders from the
`keycloak-client-secrets` Secret and Keycloak starts with
`--import-realm`. On this path there is no such machinery — pick the
client secrets yourself (they must match the Secrets you created in
§5.2), resolve the placeholders, **and fix the `osac-ui` client**: the
`__OSAC_UI_URL__` placeholder covers its `rootUrl`, redirect URIs and
web origins (substitution fills all three), but its
`defaultClientScopes` list omits `profile` and `email`, which the UI's
login requires — patch the scopes in the same preprocessing step:

```bash
# resolve placeholders and patch the osac-ui client into a resolved
# realm file (secret values are yours — they must match the §5.2
# client-credential Secrets). UI_ORIGIN is the FULL origin the browser
# loads the UI from — scheme, host and port, no path, no trailing slash
# (e.g. https://osac-ui.example.com) — the same value as the worksheet's
# <ui-origin> row, used AS-IS with its scheme.
python3 - realm.json > realm-resolved.json <<'PY'
import json, sys
UI_ORIGIN = "<ui-origin>"
subs = {
  "__OSAC_REALM_ADMIN_USERNAME__": "<realm-admin-user>",
  "__OSAC_REALM_ADMIN_PASSWORD__": "<realm-admin-password>",
  "__OSAC_ADMIN_CLIENT_SECRET__":  "<admin-client-secret>",
  "__OSAC_CONTROLLER_CLIENT_SECRET__": "<controller-client-secret>",
  "__OSAC_CSI_DRIVER_CLIENT_SECRET__": "<csi-driver-client-secret>",
  "__OSAC_UI_BACKEND_CLIENT_SECRET__": "<ui-backend-client-secret>",
  "__OSAC_UI_URL__": UI_ORIGIN,
}
text = open(sys.argv[1]).read()
for k, v in subs.items(): text = text.replace(k, v)
realm = json.loads(text)             # fail loudly on a bad resolve

ui = next(c for c in realm["clients"] if c["clientId"] == "osac-ui")
# substitution already filled rootUrl/redirectUris/webOrigins from
# __OSAC_UI_URL__; re-assert them in case an older realm.json shipped
# development-only values, and add the scopes the UI login requires
ui["rootUrl"] = UI_ORIGIN
ui["redirectUris"] = [UI_ORIGIN + "/callback",
                      UI_ORIGIN + "/*"]
ui["webOrigins"] = [UI_ORIGIN]
for scope in ("profile", "email"):
    if scope not in ui["defaultClientScopes"]:
        ui["defaultClientScopes"].append(scope)

json.dump(realm, sys.stdout)
PY
chmod 600 realm-resolved.json    # it contains client secrets

# import via the admin REST API (or `kc.sh import` on the container).
# The import runs against the master realm, so authenticate as the
# bootstrap admin created in A.4 — <realm-admin-*> only becomes usable
# once the osac realm (which defines that user) has been imported.
# CA is the Keycloak server CA provisioned in A.4 — verify, don't -k.
KC="https://<kc-host>:8443"
CA=/root/kc-ca.pem
TOKEN=$(curl -s --cacert "$CA" -d "client_id=admin-cli" -d "username=admin" \
  -d "password=<kc-admin-password>" -d "grant_type=password" \
  "${KC}/realms/master/protocol/openid-connect/token" \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])")
curl -s --cacert "$CA" -X POST -H "Authorization: Bearer ${TOKEN}" -H "Content-Type: application/json" \
  -d @realm-resolved.json "${KC}/admin/realms"
```

(Resolve exactly the placeholders your chart version's `realm.json`
carries — grep it for `__OSAC_` and match the list; the set above is the
one at the validated pin.)

### 5.4 Verify

```bash
curl -s --cacert /root/kc-ca.pem https://<kc-host>:8443/realms/osac \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['realm'])"    # osac

# token end-to-end (ROPC works for tenant users; the UI client is public
# PKCE and cannot do it — use the browser for that flow)
OSAC_TOKEN=$(curl -s --cacert /root/kc-ca.pem \
  -d "client_id=admin-cli&username=<user>&password=<password>&grant_type=password" \
  https://<kc-host>:8443/realms/osac/protocol/openid-connect/token \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])")
```

Then one UI login (full OIDC PKCE) and one API call with the resulting
token. From a node inside the cluster, probe routes with
`curl --resolve <route-host>:443:127.0.0.1` (SNI).

---

## 6. Kafka

### 6.1 The contract

The broker must satisfy the upstream
[external-Kafka configuration](kafka-configuration.md)
(OSAC-4760):

| Requirement | Value |
|---|---|
| Protocol | `SASL_SSL` with `SCRAM-SHA-512` (only enabled mechanism) |
| Address | `host:port` (no scheme), reachable from pods |
| TLS | CA chain trusted by the cluster, cert matching the broker address, ≥ 1.2 |
| `fulfillment-service` ACLs | topic prefix `osac.events.`: Create/Write/Read/Describe/DescribeConfigs · group prefix `osac.system.`: Read/Describe · cluster: IdempotentWrite |
| `osac-metering` ACLs | topic prefix `osac.events.`: Read/Describe · topic prefix `osac.metering.heartbeat.`: Create/Read/Write/Describe/DescribeConfigs · topic prefix `osac.metering.dlq.`: Create/Describe/DescribeConfigs · cluster: IdempotentWrite |
| Topics | created by the services themselves over the admin API — fulfillment creates `osac.events.<tenant>`, metering creates `osac.metering.{heartbeat,dlq}.<tenant>` (one partition, validated retention); broker auto-create is not used |
| No superuser access | the service users get exactly the grants above |

A complete recipe for a single-node KRaft broker satisfying this contract —
`server.properties`, TLS issuance, SCRAM users, ACLs, topics, systemd unit —
is in [Appendix A.5](#a5-kafka).

### 6.2 External-mode configuration — what to modify

Connection objects (out-of-band, ns `osac`) plus values. Everything
involved:

| # | Where | Object / parameter | Value |
|---|---|---|---|
| 1 | ns `osac` | ConfigMap `external-kafka` | `brokers: <host-ip>:9092`, `user: fulfillment-service` |
| 2 | ns `osac` | Secret `external-kafka-credentials` | `password: <fulfillment-service SCRAM password>` |
| 3 | ns `<metering.kafka.clusterNamespace>` | Secret `osac-metering` | `password: <osac-metering SCRAM password>` |
| 4 | `osac-deps` + `osac-infra` releases | `kafka.enabled` | `false` (removes the bundled Strimzi stack) |
| 5 | `osac` release | `kafka.enabled` | `false` (skips installer-managed KafkaUser + credential-copy job) |
| 6 | `osac` release | `service.kafka.connection` | references to objects 1–2 |
| 7 | `osac` release | `metering.kafka.{brokers,clusterName,clusterNamespace}` | `<host-ip>:9092`, broker name, ns of object 3 |

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: external-kafka
  namespace: osac
  labels: {app.kubernetes.io/part-of: osac, app.kubernetes.io/component: external-kafka}
data:
  brokers: "<host-ip>:9092"
  user: "fulfillment-service"
---
apiVersion: v1
kind: Secret
metadata:
  name: external-kafka-credentials
  namespace: osac
  labels: {app.kubernetes.io/part-of: osac, app.kubernetes.io/component: external-kafka}
stringData:
  password: "<fulfillment-service SCRAM password>"
---
# metering reads THIS name (its fetch-kafka-secrets initContainer does
# `oc get secret osac-metering -n <clusterNamespace>`) — so the metering
# password is published under this name in the namespace you point
# clusterNamespace at:
apiVersion: v1
kind: Secret
metadata:
  name: osac-metering
  namespace: osac
stringData:
  password: "<osac-metering SCRAM password>"
```

Values (all keys; the wiring is declarative and survives upgrades, §8):

```yaml
# osac-deps AND osac-infra releases — disable the bundled Strimzi stack
kafka:
  enabled: false

# osac release
kafka:
  enabled: false            # skip installer-managed KafkaUser + credential-copy job
service:
  kafka:
    connection:
    - configMap:
        name: external-kafka
        items:
        - {key: brokers, param: brokers}
        - {key: user, param: user}
    - secret:
        name: external-kafka-credentials
        items:
        - {key: password, param: password}
# metering: not an upstream-supported path (experimental — see
# "Prerequisites and status" below); the fetch reads the Secret above
metering:
  kafka:
    brokers: "<host-ip>:9092"
    clusterName: "<external-broker-name>"
    clusterNamespace: osac
```

**Prerequisites and status.** Two things to know about this shape:

- **The metering subchart renders its CRs ungated.** With `kafka.enabled:
  false` but metering left enabled, the `osac` release still renders the
  `KafkaTopic` and `KafkaUser` objects for the metering topics and user —
  they are not gated on the Kafka flag — so on a **fresh installation**
  the Strimzi CRDs (`KafkaTopic`, `KafkaUser`, `Kafka`) must be applied
  to the cluster **before the first `helm install` of the `osac`
  release**, not merely still present from a previous bundled deployment
  — a cluster that never ran the bundled stack has none of them, and
  helm fails on the unknown kinds. Without the Strimzi operator running
  the CRs stay inert (you create the topics and users on the external
  broker yourself, A.5 steps 7–9); or run the broker Strimzi-managed
  per the [Kafka configuration guide](kafka-configuration.md) — the
  upstream-supported flow — and externalize only what that guide
  covers. (Older chart pins — through `fc7a09f` — also allowed
  disabling metering with `metering.enabled: false`; current pins
  deploy metering unconditionally, and the flag no longer exists.)
- **The off-cluster metering extension is experimental.** The Secret-name
  convention plus `clusterNamespace` pointing at a plain namespace is a
  lab-validated extension, not an upstream-supported path; re-verify it
  after every chart upgrade. Nothing reconciles this wiring for you —
  you own, by hand: the two shared inference topics
  (`osac.metering.inference` and `osac.metering.inference.dlq` —
  chart-managed in the Strimzi flow, A.5 step 9), the
  `osac-metering` Kafka user and its SCRAM credential (including
  rotation — update both the broker and the Secret in
  `clusterNamespace` together), and the billing adapters downstream of
  the metering consumer. The **per-tenant** topics
  (`osac.events.<tenant>`, `osac.metering.heartbeat.<tenant>`,
  `osac.metering.dlq.<tenant>`) are *not* yours to create — the
  services create them over the admin API and validate their
  configuration (A.5 step 9). If any of that is unacceptable, run the
  broker Strimzi-managed (the option above).

On a clean install these values and objects are in place before the first
`helm install`. Applying them to an already-running deployment is the
ordinary upgrade flow of §7/§8: apply the objects, then run the upgrade
command in §7 and the post-upgrade checks in §8.

### 6.3 Verify

Metering is always deployed on current chart pins (§6.2), so the
metering round loop is always present. There is no single shared
heartbeat topic to watch — each tenant gets its own
`osac.metering.heartbeat.<tenant>`, created by metering on first use.
The canary is therefore a **billable workflow on a real tenant**, not a
topic offset:

1. Exercise a known billable workflow for a tenant that is not one of
   the built-in ones (`system`, `shared` — their usage is never billed),
   and let the round loop tick (~1 min).
2. Inspect that tenant's actual heartbeat topic — offsets must advance
   between two runs ~100 s apart:

   ```bash
   kafka-get-offsets.sh --bootstrap-server <host-ip>:9092 \
     --command-config <admin-props> \
     --topic osac.metering.heartbeat.<tenant> --time -1
   ```

3. Check the metering service's own round/outbox/failure metrics
   (Prometheus, or `oc -n osac exec` into the metering pod's metrics
   port) for that tenant:
   `osac_metering_round_last_time_seconds` is recent (billing for the
   tenant is not progressing while it ages),
   `osac_metering_round_failures_total` is not climbing, and
   `osac_metering_outbox_depth` returns to 0.

Alongside the canary, the generic checks still apply — every consumer
group live and at lag 0:

```bash
kafka-consumer-groups.sh --bootstrap-server <host-ip>:9092 \
  --command-config <admin-props> --list
kafka-consumer-groups.sh … --describe --group <group>
```

But treat them as secondary: deployment readiness and zero lag alone do
not prove billing works — only the billable-workflow canary does.

In-cluster, confirm the publisher's Kafka config comes from the
external objects:

```bash
oc -n osac get deploy fulfillment-event-publisher -o jsonpath='{.spec.template.spec.volumes[0].projected.sources}'
```

`<admin-props>` is the client properties file created in A.5 step 4
(`client-admin.properties`) — SCRAM user plus truststore pointing at the
broker CA.

### 6.4 Certificates

Prefer §2.2 option 1: a cert-manager `Certificate` with an IP SAN of
`<host-ip>`, PKCS#8, copied out of the Secret onto the host — the full
issuance and extraction commands are in Appendix A.5. **Renewal is not
synced** — cert-manager may renew the Secret in the cluster while the copy
on the host goes stale; on renewal, re-copy `tls.crt`/`tls.key`/CA out of
the Secret and restart the broker. Long durations (5 y) make this a
once-a-career task, but put it in the calendar.

---

## 7. Helm values reference

Consolidated external-services keys across the three installer releases
(`osac`, `osac-infra`, `osac-deps`). Full values: `helm get values <release>`.

Two different kinds of keys are mixed below, and the difference matters:

- **`*.enabled` flags** do one job: stop the chart from deploying its own
  bundled copy of that service.
- **Endpoint keys** (`dbInit.host`, `service.vault.endpoint`,
  `service.kafka.connection`) do the other: tell the OSAC components
  where the external instance lives.

Moving a service off the cluster always takes **both actions
together** — the flag removes the bundled copy, the endpoint keys make
the components use yours. Skipping either leaves the deployment
inconsistent: the flag alone leaves components pointed at a bundled
service that no longer exists, and the endpoint alone leaves both the
bundled copy and your external instance in play. Keycloak's caveat:
the chart's credential hooks and
realm import stop with the flag, so the client Secrets and the realm
are yours to provision (§5.2).

Split the values by release — three files, no key repeated. Each file
is the **complete external-services overlay** for its release: paste it
as-is and substitute the placeholders (per-key annotation lives in
§3–§6). Note what these files are **not**: the entirety of the release's
values. Combine them with the base installation values the [Helm
Deployment Guide](helm-deployment-guide.md) describes — the overlays
carry only the external-services delta.

```yaml
# ── values/external-infra.yaml — osac-infra release ────────────────────
bundledPostgres:
  enabled: false                # external PostgreSQL (§3)
bundledVault:
  enabled: false                # external OpenBao (§4)
kafka:
  enabled: false                # external Kafka (§6)
keycloak:
  enabled: false                # external issuer, §5.2

# ── values/external-kafka.yaml — osac release ──────────────────────────
kafka:
  enabled: false                # skip installer-managed KafkaUser + job
service:
  kafka:
    connection:                 # consumes the §6.2 objects
    - configMap: {name: external-kafka, items: [{key: brokers, param: brokers},
                                                {key: user, param: user}]}
    - secret: {name: external-kafka-credentials, items: [{key: password, param: password}]}
metering:
  kafka: {brokers: "<host-ip>:9092", clusterName: "<name>", clusterNamespace: osac}

# ── values/external-services.yaml — osac release ───────────────────────
dbInit:
  host: <host-ip>               # db-init + Secrets build here
service:
  database:                     # REQUIRED — the chart default is an empty
    connection:                 # list, so fulfillment gets no database URL
    - secret:                   # or client cert unless this block is present
        name: osac-db-config    # in the values Helm renders; it consumes
        items:                  # the Secrets built from dbInit.host
        - {key: url, param: url}
    - secret:
        name: osac-db-client-cert
        items:
        - {key: tls.crt, param: sslcert}
        - {key: tls.key, param: sslkey}
        - {key: ca.crt, param: sslrootcert}
  vault:                        # §4
    endpoint: https://<host-ip>:8200
    caBundle: {configMap: ca-bundle}
    keycloakIssuerUrl: https://<kc-host>:8443/realms/osac
    credentials:                # §5.2 — OpenBao's Keycloak client
    - secret:
        name: vault-credentials
        items:
        - {key: client-secret, param: client-secret}
    keycloakClientId: osac-controller
    keycloakAudience: osac-api
    namespace: osac
    kvMountPath: secret
    lifecycleMountPath: jwt
    lifecycleRole: lifecycle
  auth:                         # §5.2 — issuer + controller credentials
    issuerUrl: https://<kc-host>:8443/realms/osac
    controllerCredentials:
    - secret:
        name: fulfillment-controller-credentials
        items:
        - {key: client-id, param: client-id}
        - {key: client-secret, param: client-secret}
  idp:                          # §5.2 — fulfillment controller IdP
    provider: keycloak
    url: https://<kc-host>:8443
    credentials:                # the same Secret is fine
    - secret:
        name: fulfillment-controller-credentials
        items:
        - {key: client-id, param: client-id}
        - {key: client-secret, param: client-secret}
ui:
  auth:
    oidcIssuerURL: https://<kc-host>:8443/realms/osac   # §5.2 — full URL, not tpl-expanded
```

The `osac-deps` release takes a single key — `kafka.enabled: false`
(`--set kafka.enabled=false` or its own file).

Two chart-level traps:

- **`values.schema.json` sets `additionalProperties: false` at the root.**
  Image tags live at `service.images.service.tag` and `operator.image.tag` —
  `--set images.service.tag=…` fails validation with
  `additional properties 'images' not allowed` (before any revision is
  created, so it is harmless — but it means the pin did not happen).
- **Pin image digests.** Floating `:latest` tags have been observed to move
  multiple times in a single day; pin both the service and operator images
  by digest in values and re-check after upgrades.

Upgrade procedure:

```bash
cd <installer-checkout>
set -o pipefail                       # BEFORE the pipeline
helm upgrade osac charts/osac -n osac --reuse-values --timeout 20m \
  -f values/external-services.yaml -f values/external-kafka.yaml 2>&1 | tail -8
```

---

## 8. Surviving `helm upgrade`

The integration is entirely declarative, so an upgrade has nothing to
re-wire — but it re-renders every object the chart owns and can change
behavior in ways worth catching immediately. After every upgrade:

1. **Confirm the external values survived.**
   `helm get values osac -n osac -a` (and the `osac-infra` /
   `osac-deps` releases) — the `*.enabled: false` flags and endpoint
   keys must still be present. `--reuse-values` carries the previous
   revision's computed values, so a fresh `-f` file is only needed when
   values actually change.
2. **Confirm the manually-provisioned objects still exist.** The
   Keycloak client-credential Secrets and the realm (§5.2–5.3), the
   Kafka connection objects (§6.2), and any trust-bundle sources (§2.2)
   — the chart does not own these, so an upgrade neither removes nor
   re-creates them; check they are what you expect.
3. **Confirm trust and credentials still validate.** A 401 on the API
   negative control plus a working real token (§9), a successful OpenBao
   tenant provisioning (§4.3), and metering events flowing (§6.3)
   cover the three credential chains.
4. **Rerun the verification battery** (§9) and the per-service checks
   (§3.3, §4.3, §5.4, §6.3).

If a new chart version renders a chart-bug workaround you applied by
hand (the envoy ALPN order workaround — §11), re-check whether the
workaround is still needed and remove it when the fix ships.

---

## 9. Verification battery

The full green-state check — run after any change, from a host with
cluster admin:

```bash
oc get co                          # all Available=True, not progressing/degraded
oc get nodes                       # Ready
oc -n osac get pods                # no CrashLoopBackOff / pending
oc -n osac rollout status deploy/fulfillment-controller   # …and every deploy

# external health — verify the servers, don't -k them (CAs from A.3/A.4)
curl -s --cacert /root/bao-ca.pem -o /dev/null -w '%{http_code}\n' \
  https://<host-ip>:8200/v1/sys/health                                                   # 200
curl -s --cacert /root/kc-ca.pem https://<kc-host>:8443/realms/osac | python3 -c \
  "import sys,json; print(json.load(sys.stdin)['realm'])"                                # osac
podman exec osac-postgres psql -U postgres -Atc \
  "select count(*) from pg_stat_activity where usename in ('osac_service','osac_metering')"   # >0

# auth chain — negative control: 401 on a dummy token is the GREEN answer
# (probe a path the public API actually serves, e.g. compute_instances —
# an unserved path 404s before the auth filter runs and proves nothing)
curl -s --cacert <cluster-ca> -o /dev/null -w '%{http_code}\n' \
  -H 'Authorization: Bearer dummy' \
  https://<api-route>/api/fulfillment/v1/compute_instances                             # 401
# …and positive control: a real token must WORK — a 401 alone proves only
# that the filter exists. Get a token per §5.4 (OSAC_TOKEN there), then:
curl -s --cacert <cluster-ca> -o /dev/null -w '%{http_code}\n' \
  -H "Authorization: Bearer ${OSAC_TOKEN}" \
  https://<api-route>/api/fulfillment/v1/compute_instances                             # 200
# <cluster-ca> is the CA that signed the API route's certificate (from the
# cluster's trust bundle, §2.2) — the probe then also proves the trust
# chain, which -k would skip (from a cluster node: add
# --resolve <api-route>:443:127.0.0.1)

# tenant smoke — the smallest end-to-end workflow (the API's tenant
# endpoints are POST/GET/DELETE /api/fulfillment/v1/tenants). Tenant
# creation is administrator-only (the policy recognizes the realm's
# admins group and service accounts such as osac-controller) — a §5.4
# tenant-user token gets 403 here, so use the realm-admin user or an
# osac-admin client-credentials token for this workflow:
ADMIN_TOKEN=$(curl -s --cacert /root/kc-ca.pem \
  -d "client_id=admin-cli&username=<realm-admin-user>&password=<realm-admin-password>&grant_type=password" \
  https://<kc-host>:8443/realms/osac/protocol/openid-connect/token \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])")
NAME=smoke-$RANDOM
# the create response IS the Tenant (response_body: "object"), so its
# id sits at the top level:
TENANT_ID=$(curl -s --cacert <cluster-ca> -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d "{\"metadata\":{\"name\":\"$NAME\"},\"spec\":{\"domains\":[\"$NAME.example.org\"]}}" \
  https://<api-route>/api/fulfillment/v1/tenants \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
# the reconciler signals completion by setting the tenant's
# spec.break_glass_credentials_secret — poll the tenant API for it
# (it names a Secret in OSAC's own API, not a Kubernetes Secret, so
# `oc get secret -w` never sees it):
for i in $(seq 1 60); do
  BG=$(curl -s --cacert <cluster-ca> -H "Authorization: Bearer ${ADMIN_TOKEN}" \
    https://<api-route>/api/fulfillment/v1/tenants/$TENANT_ID \
    | python3 -c "import sys,json; \
print(json.load(sys.stdin).get('spec',{}).get('breakGlassCredentialsSecret',{}).get('name',''))" 2>/dev/null)
  [ -n "$BG" ] && break
  sleep 5
done
[ -n "$BG" ] || { echo "break-glass Secret never appeared for $NAME" >&2; exit 1; }
curl -s --cacert <cluster-ca> -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  https://<api-route>/api/fulfillment/v1/tenants/$TENANT_ID    # the object
curl -s --cacert <cluster-ca> -X DELETE -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  https://<api-route>/api/fulfillment/v1/tenants/$TENANT_ID
curl -s --cacert <cluster-ca> -o /dev/null -w '%{http_code}\n' \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  https://<api-route>/api/fulfillment/v1/tenants/$TENANT_ID                             # 404

# Kafka canary (§6.3): a billable workflow on a real tenant —
# its heartbeat topic's offsets advance, round metrics healthy, lag 0
```

REST paths are snake_case (`/api/fulfillment/v1/compute_instances/{id}`) —
camelCase 404s and prove nothing.

---

## 10. Backups

| Service | Strategy | Cost | Retention |
|---|---|---|---|
| PostgreSQL | nightly online `pg_dumpall \| gzip` | none | 14 |
| OpenBao | stop → tar data dir → start → health check | seconds of downtime | 14 |
| Keycloak | nightly partial exports + database backup (see §10.3) | none to seconds | 7 JSON / 14 dumps |
| Kafka | none — event log, see the recovery policy below | — | — |

Stagger the cron times so nothing overlaps; have each script verify its
own output (a backup that silently produced nothing is worse than none).
Know what each format actually contains: the PostgreSQL dump is plain SQL
(`gzip` compresses, it does not encrypt), and the Keycloak JSON exports
carry realm keys, client secrets, and password hashes in the clear — only
the OpenBao data directory is encrypted at rest. Treat every backup file
as root-only, and add an encryption layer (gpg, age) before copies leave
the services host. The scripts and restore procedures are §10.1–10.3.

**Kafka recovery policy.** "No backup" needs an explicit recovery
contract, not silence. Two separate controls, often confused:

- **Replay window (retention).** Consuming a Kafka record does **not**
  delete it — records live until retention (time or size) expires.
  Longer retention on the shared inference topics (A.5 `mk_topic`) only
  widens the window consumers
  have to re-read history **while the stored records remain available**.
- **Durability (a second copy).** Retention protects nothing against
  losing the storage itself. This recipe's broker is single-node with
  replication factor 1 — the host disk holds the **only** copy, and a
  disk or host loss destroys all retained events, consumed or not.
  Metering events are not re-derivable from anything in OSAC, so if
  that is unacceptable, keep a second durable copy: mirror the topics
  that matter to another broker, or run an external consumer that
  lands them in a database you already back up.

The §6.3 canary and consumer-lag checks are the detection alarm for the
first kind of trouble; nothing detects the second before it happens.
Decide which tier you are in **before** a failure, and write the choice
down next to the topic configuration (A.5 step 9 for the shared
inference topics; the per-tenant topics carry the services' own
configured retention).

**Verify by restoring.** A backup is not verified until it has been
restored once. After first setup — and after any backup-script change —
rehearse each restore below into a scratch target (a throwaway database,
a stopped OpenBao on another port, a scratch Keycloak container) and
confirm the data lands. Untested backups are a hypothesis.

### 10.1 PostgreSQL

```bash
#!/usr/bin/env bash
# /usr/local/sbin/osac-pg-backup.sh — nightly full dump, keep 14
set -euo pipefail
umask 077                     # dumps carry credentials and tenant data
DIR=/var/lib/osac/backups; mkdir -p "$DIR"
OUT="$DIR/osac-pg-$(date +%Y%m%d-%H%M%S).sql.gz"
podman exec osac-postgres pg_dumpall -U postgres | gzip > "$OUT"
gzip -t "$OUT"                 # verify the archive is intact, not empty
ls -1t "$DIR"/osac-pg-*.sql.gz | tail -n +15 | xargs -r rm -f
```

```cron
17 2 * * * root /usr/local/sbin/osac-pg-backup.sh
```

Restore: `gunzip -c <dump>.sql.gz | podman exec -i osac-postgres psql -U
postgres` (quiesce the writers first — restored-over tables do not
magically reconcile with running services).

### 10.2 OpenBao

File-storage OpenBao has no snapshot API; back up the data directory with
a brief stop (a few seconds). The contents are encrypted at rest — protect
the backups like the unseal key.

```bash
#!/usr/bin/env bash
# /root/bao-backup/backup-openbao.sh — stop, tar, start, verify (keep 14)
set -euo pipefail
umask 077                     # the data dir is encrypted; keep it that way
DIR=/root/bao-backup; STAMP=$(date +%Y%m%d-%H%M%S); mkdir -p "$DIR"
CA=/root/bao-ca.pem           # from A.3 — trust it instead of -k
# never leave OpenBao stopped because the tar failed mid-script
trap 'systemctl is-active --quiet container-osac-openbao.service \
  || systemctl start container-osac-openbao.service' EXIT
systemctl stop container-osac-openbao.service
tar -czf "$DIR/bao-data-$STAMP.tar.gz" -C /root bao-data
systemctl start container-osac-openbao.service
READY=0
for i in $(seq 1 30); do
  sleep 2
  if [ "$(curl -s --cacert "$CA" -o /dev/null -w '%{http_code}' https://<host-ip>:8200/v1/sys/health)" = 200 ]; then
    READY=1; break
  fi
done
# 200 means initialized, unsealed AND active — anything else (501 not
# initialized, 503 sealed, 429 standby) is a failure after the 60 s budget below
[ "$READY" = 1 ] || { echo "ERROR: OpenBao not healthy 60s after start" >&2; exit 1; }
curl -s --cacert "$CA" https://<host-ip>:8200/v1/sys/health | grep -q '"sealed":false'
echo "OK bao-data-$STAMP.tar.gz"
find "$DIR" -name 'bao-data-*.tar.gz' -mtime +14 -delete
```

Restore: `systemctl stop`, swap the data dir, `systemctl start` (the unseal
drop-in re-seals→unseals; the `:z` mounts relabel), health-check. (On a
Raft-backed instance, prefer snapshots over the data-dir tar — the
save/restore commands are at the end of this section.)

**Raft snapshots.** On a single-node Raft instance (the A.3 storage
note), the consistent backup is a snapshot, not a tar of a live data
dir:

```bash
# save (root token or a policy granting update on sys/raft):
BAO_ADDR=https://<host-ip>:8200 BAO_CACERT=/root/bao-ca.pem \
  bao operator raft snapshot save "$DIR/bao-snap-$STAMP.snap"
find "$DIR" -name 'bao-snap-*.snap' -mtime +14 -delete

# restore: stop the server, then (data dir can be empty):
BAO_ADDR=https://<host-ip>:8200 BAO_CACERT=/root/bao-ca.pem \
  bao operator raft snapshot restore "$DIR/bao-snap-$STAMP.snap" /bao/data
# then systemctl start and the usual health check; re-seal/unseal state
# follows the snapshot, and the init-output escrow (§10.4) still applies.
```

**The archive does not contain the unseal material.** `bao-data` holds the
encrypted store, but the init output (`/root/openbao-init.json` — unseal
key shards and root token) lives outside the data directory and outside
this archive. A restored archive without the unseal key is unrecoverable
— escrow that file separately per §10.4.

### 10.3 Keycloak

Two layers, because the deployment options differ (A.4):

- **Partial exports — always, any database.** Realm structure, clients,
  roles, and users via the admin REST API; nothing stops, nothing to
  restore beyond re-importing the JSON.
- **Database backup — the real restore path for a production instance.**
  If Keycloak runs on PostgreSQL (A.4's production variant), back up its
  database with the same `pg_dump` discipline as §10.1 — archiving the
  container's data directory is **not** a database backup. Only the H2
  lab variant is backed up by tarring `/root/kc-data`.

```bash
#!/usr/bin/env bash
# /root/kc-backup/backup-keycloak.sh — partial exports (keep 7)
set -euo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin:$PATH   # cron PATH lacks iptables etc.
umask 077                     # exports carry realm keys and password hashes
DIR=/root/kc-backup; STAMP=$(date +%Y%m%d-%H%M%S); mkdir -p "$DIR"
KC="https://<kc-host>:8443"
CA=/root/kc-ca.pem            # from A.4 — verify the server, don't -k it
KC_PASS=$(podman inspect keycloak-external --format \
  '{{range .Config.Env}}{{println .}}{{end}}' | grep '^KEYCLOAK_ADMIN_PASSWORD=' | cut -d= -f2-)
TOKEN=$(curl -sf --cacert "$CA" -d "client_id=admin-cli&username=admin&password=${KC_PASS}&grant_type=password" \
  "${KC}/realms/master/protocol/openid-connect/token" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["access_token"])')

# realm structure (clients, roles, groups) — partial-export.
# -f makes curl fail on HTTP errors (an expired token would otherwise
# save a JSON error object as tonight's "backup"); the python step then
# validates the payload really is JSON.
curl -sf --cacert "$CA" -X POST -H "Authorization: Bearer ${TOKEN}" \
  "${KC}/admin/realms/osac/partial-export?exportClients=true&exportGroupsAndRoles=true" \
  | python3 -c 'import sys,json; json.dump(json.load(sys.stdin), sys.stdout)' \
  > "$DIR/realm-osac-${STAMP}.json"

# users are NOT included in partial-export — separate, PAGINATED call
# (the admin API caps a single response; without the loop everything
# past the first page is silently missing)
FIRST=0; PAGES=()
while :; do
  PAGE=$(curl -sf --cacert "$CA" -H "Authorization: Bearer ${TOKEN}" \
    "${KC}/admin/realms/osac/users?first=${FIRST}&max=100&briefRepresentation=false")
  COUNT=$(printf '%s' "$PAGE" | python3 -c 'import sys,json; print(len(json.load(sys.stdin)))')
  [ "$COUNT" -eq 0 ] && break
  PAGES+=("$PAGE")
  FIRST=$((FIRST + COUNT))
done
# merge the pages into one valid JSON array (guard the empty realm)
[ ${#PAGES[@]} -eq 0 ] && PAGES=('[]')
printf '%s\n' "${PAGES[@]}" \
  | python3 -c 'import sys,json; json.dump([u for p in map(json.loads,sys.stdin) for u in p], sys.stdout)' \
  > "$DIR/users-osac-${STAMP}.json"
# (extend with the same pattern for organizations + members; their
#  replay after a restore is documented below)
ls -1t "$DIR"/realm-osac-*.json | tail -n +8 | xargs -r rm -f
ls -1t "$DIR"/users-osac-*.json | tail -n +8 | xargs -r rm -f
```

Restores, by failure: container or host lost — re-run the A.4 recipe and
restore the database (or untar `kc-data` on the H2 variant), then
re-import the newest JSON only for anything the database restore missed;
realm misconfigured — re-import the partial export over it; total loss —
database restore plus the §5.3 realm seed.

**Replaying what exports never carry.** Every export mechanism here —
`kc.sh export` and the partial-export API alike — omits LDAP user
federations and organization memberships by design, so after any import
restore, recreate them before users can log in:

1. **LDAP user federation** — re-create the component through the admin
   REST API (or the console's *User federation* page; the upstream
   [LDAP federation guide](../customer-ldap-federation-guide.md)
   covers the configuration in full):
   `POST /admin/realms/osac/components` with `providerId: ldap`,
   `providerType: org.keycloak.storage.UserStorageProvider`, and the
   connection/bind/users-DN config recorded in your worksheet. Then
   trigger a full sync
   (`POST /admin/realms/osac/user-storage/{id}/sync?action=triggerFullSync`)
   and re-apply group memberships — group links live in Keycloak's own
   database, so if the database itself was restored they survive; if
   only the JSON import was replayed, they must be re-added
   (`PUT /admin/realms/osac/users/{id}/groups/{gid}`).
2. **Organizations and memberships** — extend the export script above
   with the same paged GET pattern
   (`/admin/realms/osac/organizations`,
   `/…/organizations/{id}/members`); replay the POST calls in the
   same order: organizations first, then memberships.

### 10.4 Recovery material

"Backups" (§10.1–10.3) cover service data. Recovering from the loss of
the services **host** needs more than data — collect the full catalogue
below, and know where every row lives:

| Category | What | Where it comes from |
|---|---|---|
| Service data | PG dumps, OpenBao data-dir archives / Raft snapshots, Keycloak DB backups or exports | §10.1–10.3 |
| Config files | `server.conf`/`access.conf` (PG), `server.properties` (Kafka), OpenBao config + unseal drop-in, container run definitions (A.1–A.5) | the host, `/etc/…` and `/root/…` |
| Certificates & keys | server certs/keys per service, `ca-bundle.pem`, kc/bao CAs | §2.2, A.2–A.4 |
| Credentials | SCRAM passwords, Keycloak bootstrap admin, DB passwords, client-credential Secrets' values | your credential store — the values behind §5.2's Secrets and A.5's users |
| Systemd units | the generated/hand-written unit files | A.1 |
| Unseal material | OpenBao init output (unseal key shards + root token), escrowed per §10.4 | **outside the data-dir archive** |

Two rules:

- **Store the catalogue off-host, encrypted.** An archive on the machine
  it is a backup of does not survive that machine.
- **Escrow sensitive recovery keys separately from the data.** Unseal
  shards, root tokens, and password material go to a distinct,
  access-controlled location (a vault, a sealed envelope procedure) —
  not into the same tarball as the encrypted data, so one disclosure
  does not unlock both.

Rehearse against the real thing: restoring a data dir on the live
machine only proves the backup mechanism. Host-loss recovery — rebuild
on a fresh machine from the catalogue alone, then run §9 — is the test
that matters, and it is part of the validation exercise noted in the
introduction.

---

## 11. Gotchas compendium

Operational recipe traps (systemd unit shapes, Kafka broker config,
cron environment) are documented inline where you meet them — the A.1
table, A.5's Kafka steps, §10.3's cron script. This list is the
integration-level set: cross-cutting failures whose symptom shows up far
from the cause. Format: symptom → check → remedy.

1. **Cluster workloads reject the external servers' certificates**
   (`unknown ca`, `x509: certificate signed by unknown authority`, TLS
   handshake failures in pod logs) → check `oc -n osac get
   configmap ca-bundle -o jsonpath='{.data}'` actually contains the
   services CA → the trust bundle is owned by trust-manager: patch the
   `Bundle` CR, never the ConfigMap (it reverts), and restart consuming
   deployments after adding a CA — they cache the bundle (§2.2).
2. **API returns 401 for tokens issued moments ago, or auth works then
   fails intermittently** → check clock sync on every host involved
   (`chronyc tracking`, §2.3) — JWT validation breaks at ~30 s of skew
   → fix NTP; a `token issued in the future` log line is the tell.
3. **Keycloak container aborts at startup with `AccessDeniedException`**
   on the TLS key → check the key file's ownership — the image runs as
   uid 1000 and cannot read a root-owned key → `chown 1000:0` the cert
   and key (A.4).
4. **Restored/re-imported Keycloak loses LDAP users or organization
   memberships** → nothing is wrong with the restore — `kc.sh export`
   drops LDAP federations and org memberships by design → replay them
   after import (§10.3).
5. **`helm upgrade` reports success but nothing changed, or upgrade
   checks run after a failed helm** → check for `set -o pipefail` before
   any `helm … | tail` pipeline — without it the pipeline's exit status
   is `tail`'s → also remember `--reuse-values` carries the previous
   revision's computed values including past `--set` overrides; pass only
   what changes (§8).
6. **`helm upgrade` times out at 5 minutes with the AAP bootstrap hook
   still running** → not hung — the hook alone takes ~7 min → use
   `--timeout 20m` (§7).
7. **`--set images.service.tag=…` fails with `additional properties
   'images' not allowed`** → the chart's `values.schema.json` sets
   `additionalProperties: false` at the root → image tags live at
   `service.images.service.tag` and `operator.image.tag` (§7).
8. **UI login spins forever after install/upgrade; gRPC calls from the
   UI backend silently fail** → check the envoy listeners' ALPN order —
   some chart versions render `[http1.1, h2]` (an invalid ALPN id, and
   the wrong order), which pins gRPC clients to HTTP/1.1 and drops
   trailers → patch both listeners to `[h2, http/1.1]` in the envoy
   ConfigMap and restart; re-check after every upgrade until the chart
   fix ships. (Observed at installer pin `fc7a09f`; no fixed version
   identified yet — the greenfield validation exercise will pin
   affected/fixed ranges when it runs.)
9. **REST calls return 404 on a path you know exists** → check the
   casing — REST paths are snake_case
   (`/api/fulfillment/v1/compute_instances/{id}`); camelCase 404s and
   prove nothing (§9).
10. **`curl` to a route from a cluster node fails TLS while working
    elsewhere** → the node resolves the route to itself but SNI still
    needs the hostname → add `--resolve <route>:443:127.0.0.1` (§5.4).
11. **Fulfillment's event publisher or metering fails at startup with a
    topic partition/retention error** (`has %d partitions, want 1`, or
    `retention.ms %s, want %s`) → a per-tenant topic
    (`osac.events.<tenant>`, `osac.metering.{heartbeat,dlq}.<tenant>`)
    was created by hand or by broker auto-create with non-contract
    settings → the services create these topics themselves and refuse
    to use one that differs (A.5 step 9, §6.1). Fixing it may mean
    **deleting the topic and letting the service recreate it — do that
    only after confirming the topic holds nothing you need**: these
    topics can carry billing history. If the records must be kept, copy
    them to a correctly configured scratch topic first, then delete
    and let the service recreate; a retention-only mismatch can also be
    fixed in place with `kafka-configs.sh --alter` instead of deleting.

---

## Appendix A: Running the services as containers

Ready-to-run host-side recipes for the four services. They assume the
§2.1 network rules (publish on `<host-ip>` only, firewall open for the
cluster subnet) and a TLS trust decision made per §2.2. Each recipe was
validated with the image version named in it; adjust to your environment.

### A.1 Host preliminaries: SELinux and systemd units

**SELinux (RHEL hosts):**

- Quick path: run the container with `--security-opt label=disable`.
  Required anyway for Keycloak and OpenBao images on RHEL (they fail with
  `libc.so.6: cannot apply additional memory protection` under default
  labels).
- Cleaner path (used for PostgreSQL): keep normal labels and register the
  non-standard podman graphroot as equivalent to `/var/lib/containers`:

  ```bash
  semanage fcontext -a -e /var/lib/containers /home/containers
  restorecon -R /home/containers
  ```

  Pick one approach per container; do not mix.

**systemd unit shapes** — podman containers under systemd come in three
shapes with very different operational behavior. Internalize this table
before writing any unit:

| Shape | Generated by | Behavior |
|---|---|---|
| old-style | `podman generate systemd --name <c>` | `ExecStart=podman start <c>`, `Type=forking`; the container is a persistent object. Bare `podman stop/start` works; systemd only matters at boot. |
| `--new`-style | `podman generate systemd --new` | `ExecStart=podman run --rm …`, `Restart=always`. **A bare `podman stop` is undone in ~100 ms** — systemd re-creates the container. `systemctl stop` is the only stop that sticks. |
| oneshot | hand-written | `Type=oneshot` + `RemainAfterExit=yes` with `podman run -d` and **no ExecStop**. Needed when the container's main process exits shortly after start (detached Kafka) — any ExecStop would kill the healthy container. Manage with bare `podman stop/start`; `systemctl restart` re-creates cleanly thanks to `--replace`. |

### A.2 PostgreSQL

Host layout:

```text
/etc/osac/postgres/            (owned by the image's postgres uid — 26 for sclorg)
├── server.conf                # ssl=on + pointers below
├── access.conf                # HBA rules
├── ca-bundle.pem              # CA that signs the client certs
└── tls.crt / tls.key          # server certificate

/var/lib/osac/postgres/        # data dir (bind-mounted)
/var/lib/osac/backups/         # nightly dumps
```

```ini
# /etc/osac/postgres/server.conf
ssl = on
ssl_ca_file = '/config/ca/bundle.pem'
ssl_cert_file = '/secrets/cert/tls.crt'
ssl_key_file = '/secrets/cert/tls.key'
hba_file = '/config/access/access.conf'
```

```ini
# /etc/osac/postgres/access.conf
local all all peer
hostssl all all 0.0.0.0/0 cert
hostssl all all ::0/0 cert
```

```bash
podman run -d --name osac-postgres \
  -p <host-ip>:5432:5432 \
  -v /etc/osac/postgres/server.conf:/opt/app-root/src/postgresql-cfg/server.conf:ro \
  -v /etc/osac/postgres/access.conf:/config/access/access.conf:ro \
  -v /etc/osac/postgres/ca-bundle.pem:/config/ca/bundle.pem:ro \
  -v /etc/osac/postgres/tls.crt:/secrets/cert/tls.crt:ro \
  -v /etc/osac/postgres/tls.key:/secrets/cert/tls.key:ro \
  -v /var/lib/osac/postgres:/var/lib/pgsql \
  -e POSTGRESQL_USER=service \
  -e POSTGRESQL_DATABASE=service \
  -e POSTGRESQL_PASSWORD=<bootstrap-user-password> \
  quay.io/sclorg/postgresql-18-c10s

# boot persistence (old-style unit — see A.1)
podman generate systemd --name osac-postgres --restart-policy=on-failure \
  > /etc/systemd/system/container-osac-postgres.service
systemctl daemon-reload && systemctl enable --now container-osac-postgres.service
```

Two durability notes: keep PostgreSQL's defaults (`fsync=on`,
`full_page_writes=on`) — the bundled chart's dev profile disables them, and
an external instance should not inherit that; and do not add
`--security-opt label=disable` to this container — with the fcontext rules
from A.1 the normal labels work.

On identities: the `POSTGRESQL_USER`/`POSTGRESQL_PASSWORD` pair is the
container image's bootstrap login — the account first-boot
initialization runs as, and the owner of the `POSTGRESQL_DATABASE`
database — and is **not** the OSAC `postgres` administrator, nor what
OSAC uses. The chart's db-init job logs in as the `postgres`
superuser over client-cert mTLS (§3.1) and creates the real users —
`osac_service`, `osac_metering` — and their databases; the workload
client certificates' CNs match those role names.

### A.3 OpenBao

```hcl
# /root/bao-config/bao.hcl
ui            = true
disable_mlock = true
api_addr      = "https://<host-ip>:8200"
cluster_addr  = "https://<host-ip>:8201"

storage "file" { path = "/bao/data" }

listener "tcp" {
  address         = "0.0.0.0:8200"
  cluster_address = "0.0.0.0:8201"
  tls_cert_file   = "/bao/tls/tls.crt"
  tls_key_file    = "/bao/tls/tls.key"
}
```

> **Storage backend note:** `file` is what the validation lab ran
> (OpenBao 2.6.2) and what this recipe keeps — but file storage is
> deprecated in OpenBao 2.6.0 and **removed in 2.7.0**, and it deadlocks
> on concurrent namespace operations. A new deployment on ≥ 2.7 should
> use single-node Raft instead: replace the storage block with
> `storage "raft" { path = "/bao/data"  node_id = "osac-external-1" }`
> and keep `cluster_addr` as configured. Backups then use
> `bao operator raft snapshot save` rather than a data-dir tar (§10.2).

```bash
# TLS material — prefer §2.2 option 1: issue from the cluster's default-ca
# (a Certificate CR with an IP SAN of <host-ip>) and copy tls.crt/tls.key
# out of the Secret, exactly like the Kafka recipe in A.5 step 2. The
# self-signed one-liner below is the lab shortcut; with it you must also
# add its CA to the cluster trust bundle (§2.2 option 2).
openssl req -x509 -newkey rsa:2048 -keyout /root/bao-key.pem -out /root/bao-cert.pem \
  -days 365 -nodes -subj "/CN=<host-ip>" -addext "subjectAltName=IP:<host-ip>"
chmod 644 /root/bao-cert.pem
chmod 600 /root/bao-key.pem
# CA that validates the server cert, for clients inside the container:
# with the self-signed shortcut the cert is its own CA; with a
# cluster-issued cert (§2.2 option 1) copy the issuing CA here instead.
cp /root/bao-cert.pem /root/bao-ca.pem
mkdir -p /root/bao-data /root/bao-config    # config as above

podman run -d --name osac-openbao \
  -p <host-ip>:8200:8200 -p <host-ip>:8201:8201 \
  --security-opt label=disable \
  -v /root/bao-cert.pem:/bao/tls/tls.crt:z -v /root/bao-key.pem:/bao/tls/tls.key:z \
  -v /root/bao-ca.pem:/bao/tls/ca.crt:z \
  -v /root/bao-data:/bao/data:z -v /root/bao-config:/bao/config:z \
  -e HOME=/tmp ghcr.io/openbao/openbao:2.6.2 server -config=/bao/config/bao.hcl

# boot persistence (--new-style unit — see A.1)
podman generate systemd --new --name osac-openbao --restart-policy=always \
  > /etc/systemd/system/container-osac-openbao.service
systemctl daemon-reload && systemctl enable --now container-osac-openbao.service
```

Initialize the server once, unseal it, and write the bootstrap file the
unseal helper below reads (unseal key + root token, root-only). Note
**`-format=json`** — without it `bao operator init` writes a human-readable
table that the JSON-parsing helper cannot read:

Every `bao` invocation below runs on the **host**, against the published
address. `podman exec` would need the container to reach back out to
`<host-ip>:8200` (a hairpin-NAT dependency not every host allows), and
dialing `127.0.0.1` from inside the container fails the cert's SAN
check. Copy the binary out of the container once, then point it at the
published address with the host-side CA:

```bash
podman cp osac-openbao:/bin/bao /usr/local/bin/bao
export BAO_ADDR=https://<host-ip>:8200 BAO_CACERT=/root/bao-ca.pem
umask 077
bao operator init -format=json \
  -key-shares=1 -key-threshold=1 > /root/openbao-init.json
chmod 600 /root/openbao-init.json

# first unseal by hand (later ones are the drop-in's job):
KEY=$(python3 -c 'import json;print(json.load(open("/root/openbao-init.json"))["unseal_keys_b64"][0])')
curl -s --cacert /root/bao-ca.pem -X PUT -d "{\"key\":\"$KEY\"}" https://<host-ip>:8200/v1/sys/unseal
curl -s --cacert /root/bao-ca.pem https://<host-ip>:8200/v1/sys/health \
  | grep -q '"initialized":true'   # 200 = initialized, unsealed, active
```

Then bootstrap the objects the §4.2 values expect — the same objects the
chart's bundled instance gets from its own init job (namespace, JWT auth,
lifecycle policy and role):

```bash
ROOT_TOKEN=$(python3 -c 'import json;print(json.load(open("/root/openbao-init.json"))["root_token"])')
BAO="env BAO_ADDR=https://<host-ip>:8200 BAO_CACERT=/root/bao-ca.pem \
  BAO_TOKEN=$ROOT_TOKEN bao"

# existence checks are explicit so a TLS or auth failure is visible instead
# of being masked by an "already exists" fallback
$BAO namespace lookup osac >/dev/null 2>&1 || $BAO namespace create osac
$BAO auth list -namespace=osac | grep -q '^jwt/' \
  || $BAO auth enable -namespace=osac -path=jwt jwt

$BAO policy write -namespace=osac lifecycle - <<'POLICY'
path "sys/namespaces/*" {
  capabilities = ["create", "read", "update", "delete", "list", "sudo"]
}
path "sys/namespaces" {
  capabilities = ["list"]
}
path "+/sys/mounts/*" {
  capabilities = ["create", "read", "update", "delete", "sudo"]
}
path "+/sys/mounts" {
  capabilities = ["read", "list"]
}
path "+/sys/policies/*" {
  capabilities = ["create", "read", "update", "delete", "list", "sudo"]
}
path "+/sys/auth/*" {
  capabilities = ["create", "read", "update", "delete", "list", "sudo"]
}
path "+/auth/*" {
  capabilities = ["create", "read", "update", "delete", "list", "sudo"]
}
POLICY

# point JWT auth at the external Keycloak issuer. The bundled chart's
# bootstrap writes oidc_discovery_ca_pem=@/vault/ca/bundle.pem — pass the
# Keycloak CA here the same way (the A.4 recipe's CA, /root/kc-ca.pem):
# without it, discovery fails against any issuer whose cert is not in
# the host's public trust store (self-signed or cluster-issued)
$BAO write -namespace=osac auth/jwt/config \
  oidc_discovery_url="https://<kc-host>:8443/realms/osac" \
  oidc_discovery_ca_pem=@/root/kc-ca.pem \
  default_role="lifecycle"

$BAO write -namespace=osac auth/jwt/role/lifecycle \
  role_type="jwt" \
  bound_audiences="osac-api" \
  user_claim="sub" \
  policies="lifecycle" \
  token_ttl="1h"
```

Do **not** create KV or transit mounts here — the controller creates
those inside each tenant namespace it provisions (§4.1).

Add an **auto-unseal drop-in**: an `ExecStartPost` helper that polls
`/v1/sys/health` and POSTs the unseal key from that file, never printing
it:

```ini
# /etc/systemd/system/container-osac-openbao.service.d/unseal.conf
[Service]
ExecStartPost=-/usr/local/sbin/osac-openbao-unseal.sh
```

```bash
#!/bin/bash
# /usr/local/sbin/osac-openbao-unseal.sh — ExecStartPost auto-unseal.
# Reads the unseal key from a root-only file; never prints it.
set -u
BASE=https://<host-ip>:8200
CA=/root/bao-ca.pem           # from A.3 — verify the server, don't -k it
LOG=/var/log/osac-openbao-unseal.log

KEY=$(python3 -c 'import json;print(json.load(open("/root/openbao-init.json"))["unseal_keys_b64"][0])')

for _ in $(seq 1 30); do
  HEALTH=$(curl -s --cacert "$CA" --max-time 3 "$BASE/v1/sys/health" 2>/dev/null) || { sleep 2; continue; }
  SEALED=$(printf "%s" "$HEALTH" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("sealed"))' 2>/dev/null) || { sleep 2; continue; }
  if [ "$SEALED" = "False" ]; then
    exit 0
  fi
  if [ "$SEALED" = "True" ]; then
    RESP=$(curl -s --cacert "$CA" --max-time 5 -X PUT -d "{\"key\":\"$KEY\"}" "$BASE/v1/sys/unseal" 2>/dev/null) || RESP=""
    if printf "%s" "$RESP" | grep -q '"sealed":false'; then
      echo "$(date -u +%FT%TZ) unsealed osac-openbao (ExecStartPost)" >> "$LOG"
      exit 0
    fi
  fi
  sleep 2
done
echo "$(date -u +%FT%TZ) ERROR: osac-openbao not healthy/unsealable after 60s" >> "$LOG"
exit 1
```

Activate the drop-in (daemon-reload picks it up; restart the unit once so
it takes effect immediately — the helper re-unseals on the way up):

```bash
chmod 700 /usr/local/sbin/osac-openbao-unseal.sh
systemctl daemon-reload
systemctl restart container-osac-openbao.service
```

Remember the A.1 trap: with a `--new`-style unit, **`systemctl stop` is the
only stop systemd will not undo** — any operational script (backups,
maintenance) must go through systemctl.

### A.4 Keycloak

```bash
# TLS material for the issuer hostname — prefer §2.2 option 1: issue from
# the cluster's default-ca with a DNS SAN of <kc-host> (the hostname your
# issuer URLs use, §5.2) and copy tls.crt/tls.key out of the Secret, as in
# A.5 step 2. A self-signed pair works for a lab but must be added to the
# cluster trust bundle (§2.2 option 2) before anything will trust it.
# The image runs as uid 1000 — it must be able to read the key:
chown 1000:0 /root/kc-cert.pem /root/kc-key.pem
chmod 644 /root/kc-cert.pem && chmod 400 /root/kc-key.pem
# CA that validates the server cert, for host-side clients (backups,
# §5.3 seeding, §9 probes): with the self-signed pair the cert is its
# own CA; with a cluster-issued cert copy the issuing CA here instead.
cp /root/kc-cert.pem /root/kc-ca.pem
mkdir -p /root/kc-data && chown -R 1000:0 /root/kc-data    # H2 lives here

podman run -d --name keycloak-external \
  -p <host-ip>:8443:8443 \
  --security-opt label=disable \
  -v /root/kc-cert.pem:/etc/x509/https/tls.crt \
  -v /root/kc-key.pem:/etc/x509/https/tls.key \
  -v /root/kc-data:/opt/keycloak/data \
  -e KEYCLOAK_ADMIN=admin \
  -e KEYCLOAK_ADMIN_PASSWORD=<kc-admin-password> \
  -e KC_HTTPS_CERTIFICATE_FILE=/etc/x509/https/tls.crt \
  -e KC_HTTPS_CERTIFICATE_KEY_FILE=/etc/x509/https/tls.key \
  -e KC_HOSTNAME=https://<kc-host>:8443 \
  -e KC_HOSTNAME_STRICT=false \
  -e KC_HTTP_ENABLED=false \
  quay.io/keycloak/keycloak:26.6.4 start

podman generate systemd --name keycloak-external --restart-policy=always \
  > /etc/systemd/system/container-keycloak-external.service
systemctl daemon-reload && systemctl enable container-keycloak-external.service
```

`start` (production mode) is deliberate — HTTPS issuer URLs require it.
`KC_HOSTNAME` is a full URL including the port, and must match the §5.2
issuer URLs — **the container publishes 8443 and every URL in this guide
carries `:8443` explicitly** (host port 443 would need a reverse proxy or
a privileged mapping, which this recipe does not add). Verify the
discovery document agrees before wiring any consumer:

```bash
curl -s --cacert /root/kc-ca.pem \
  https://<kc-host>:8443/realms/osac/.well-known/openid-configuration \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['issuer'])"
# must print exactly: https://<kc-host>:8443/realms/osac
```

**Database durability — lab vs production.** The embedded H2 database in
the bind-mounted `/root/kc-data` is fine for a lab or evaluation instance
but not for production — single file, no HA, no point-in-time recovery.
For production, back the container with PostgreSQL (a dedicated database
on an external instance like §3's) by adding `KC_DB=postgres`,
`KC_DB_URL='jdbc:postgresql://<host-ip>:5432/keycloak?ssl=true&sslmode=verify-full&sslrootcert=/etc/x509/pg/ca.pem'`,
`KC_DB_USERNAME=<kc-db-user>`, `KC_DB_PASSWORD=<kc-db-password>` to the
run command (standard Keycloak configuration; not exercised in the
validation lab), and back it up per §10.3. On a clean install, seed the
realm per §5.3. The `sslmode=verify-full` + `sslrootcert` pair makes the
JDBC connection verify the **server** certificate against the CA that
signed it (the same `/root/ca-bundle.pem` A.2 uses — mount it into the
Keycloak container, e.g.
`-v /root/ca-bundle.pem:/etc/x509/pg/ca.pem:ro,z`) and check the
hostname, instead of accepting any certificate; without it the
connection either fails closed or, with `sslmode` relaxed, silently
skips verification. If `verify-full` fails on hostname checking, the
right fix is the certificate, not the mode: the server certificate must
carry a SAN matching the address in the JDBC URL — an IP SAN matches a
URL that dials that IP, a hostname URL needs a DNS SAN (so an
IP-SAN-only cert fails `verify-full` when connected to by name: give
the cert a DNS SAN and connect by name, or re-issue it with the IP).
`sslmode=verify-ca`
exists but skips hostname verification; treat any use of it as a marked,
justified exception in your records, not an ordinary configuration.

Two setup steps make that work against an A.2-style instance:

- **Create the database and user yourself.** The chart's db-init job
  only provisions `osac_service` and `osac_metering`; nothing creates a
  `keycloak` database. As the `postgres` superuser (client-cert mTLS,
  §3.1): `CREATE DATABASE keycloak;` and
  `CREATE ROLE <kc-db-user> LOGIN PASSWORD '<kc-db-password>';` with
  ownership of that database granted to it.
- **Reconcile with the cert-only HBA.** A.2's `access.conf` accepts
  only client-certificate authentication, but Keycloak's JDBC
  connection authenticates with username/password. Add a narrowly
  scoped rule **above** the `cert` lines:

  ```ini
  hostssl keycloak <kc-db-user> <kc-source-ip>/32 scram-sha-256
  ```

  (`<kc-source-ip>` is the address the Keycloak connection arrives from —
  the container's bridge IP when both run on the same host, or the
  Keycloak host's IP otherwise; `0.0.0.0/0` defeats the purpose). Alternatively keep cert-only HBA and configure the JDBC
  connection for client-certificate TLS (`?ssl=true&sslmode=verify-full`
  plus a `sslcert`/`sslkey` pair in the connection properties and a
  role whose name matches the certificate CN) — more moving parts, no
  password on the wire.

If the container starts and dies with `AccessDeniedException` on the
key file, the chown/chmod above did not happen.

### A.5 Kafka

Single-node KRaft, validated with `docker.io/apache/kafka:4.2.0`. Layout:
config and credentials in `/root/osac-kafka-external` (root-only — the
generated files hold keys and passwords), data in `/var/lib/osac/kafka`.

**1. SCRAM credentials — generate once, never print:**

```bash
KROOT=/root/osac-kafka-external
DATA=/var/lib/osac/kafka
mkdir -p "$KROOT/tls" "$DATA/data" && chmod 700 "$KROOT"

if [ ! -f "$KROOT/credentials.env" ]; then
  umask 077
  {
    echo "KAFKA_ADMIN_PASSWORD=$(openssl rand -base64 24)"
    echo "KAFKA_FULFILLMENT_PASSWORD=$(openssl rand -base64 24)"
    echo "KAFKA_METERING_PASSWORD=$(openssl rand -base64 24)"
  } > "$KROOT/credentials.env"
fi
. "$KROOT/credentials.env"
```

**2. TLS — issue from the cluster's `default-ca` (§2.2 option 1) and copy
the material out:**

```bash
cat <<'EOF' | oc apply -f -
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: osac-external-kafka
  namespace: osac
  labels:
    app.kubernetes.io/part-of: osac
    app.kubernetes.io/component: external-kafka
spec:
  secretName: osac-external-kafka-tls
  issuerRef:
    kind: ClusterIssuer
    name: default-ca
  duration: 43800h
  renewBefore: 720h
  privateKey:
    encoding: PKCS8
    rotationPolicy: Always
  ipAddresses:
    - <host-ip>
EOF

# wait for secret osac-external-kafka-tls to become ready, then:
oc -n osac get secret osac-external-kafka-tls -o jsonpath='{.data.tls\.crt}' | base64 -d > "$KROOT/tls/tls.crt"
oc -n osac get secret osac-external-kafka-tls -o jsonpath='{.data.tls\.key}' | base64 -d > "$KROOT/tls/tls.key"
oc -n osac get secret osac-external-kafka-tls -o jsonpath='{.data.ca\.crt}'  | base64 -d > "$KROOT/tls/ca.crt" || true
chmod 600 "$KROOT"/tls/*
```

**3. `server.properties` — always generate, never hand-edit** (the PEM
lines hold the private key; ⚠ marks the four traps):

```bash
#!/bin/bash
# gen-server-properties.sh — writes $KROOT/server.properties
set -euo pipefail
KROOT=/root/osac-kafka-external
emit_pem() { printf '%s=' "$1"; awk '{printf "%s\\n", $0}' "$2"; echo; }
{
  cat <<'EOF'
process.roles=broker,controller
node.id=1
# CONTROLLER is PLAINTEXT bound to the container's own loopback — not
# published, unreachable off-host
controller.quorum.voters=1@127.0.0.1:9093
controller.listener.names=CONTROLLER
listeners=sasl_ssl://0.0.0.0:9092,CONTROLLER://127.0.0.1:9093
advertised.listeners=sasl_ssl://<host-ip>:9092
listener.security.protocol.map=CONTROLLER:PLAINTEXT,sasl_ssl:SASL_SSL
inter.broker.listener.name=sasl_ssl

# single-node: the default replication factor of 3 cannot be satisfied
num.partitions=3
default.replication.factor=1
min.insync.replicas=1
offsets.topic.replication.factor=1
transaction.state.log.replication.factor=1
transaction.state.log.min.isr=1

# topics are created by the services over the admin API with a validated
# config; broker auto-create would make them with broker defaults
# (3 partitions) that the services then refuse — keep it off
auto.create.topics.enable=false
log.dirs=/var/lib/kafka/data
log.retention.hours=168
num.network.threads=3
num.io.threads=8

ssl.keystore.type=PEM
ssl.client.auth=none
ssl.enabled.protocols=TLSv1.2,TLSv1.3
# the SASL channel-builder self-test connects to itself — without a
# listener truststore the broker dies at startup with
# "PKIX path building failed" (certificate value emitted below)
listener.name.sasl_ssl.ssl.truststore.type=PEM
# SASL — listener-name keys are case-sensitive: the listener above is
# named sasl_ssl (lowercase) so these keys apply; server-side SCRAM JAAS
# is credential-free — users are validated against KRaft metadata
sasl.enabled.mechanisms=SCRAM-SHA-512
sasl.mechanism.inter.broker.protocol=SCRAM-SHA-512
listener.name.sasl_ssl.sasl.enabled.mechanisms=SCRAM-SHA-512
listener.name.sasl_ssl.scram-sha-512.sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required;

authorizer.class.name=org.apache.kafka.metadata.authorizer.StandardAuthorizer
# admin is the bootstrapped super-user (kafka-storage --add-scram);
# ANONYMOUS covers the loopback CONTROLLER listener
super.users=User:admin;User:ANONYMOUS
allow.everyone.if.no.acl.found=false
EOF
  emit_pem listener.name.sasl_ssl.ssl.truststore.certificates "$KROOT/tls/ca.crt"
  emit_pem ssl.keystore.certificate.chain "$KROOT/tls/tls.crt"
  emit_pem ssl.keystore.key "$KROOT/tls/tls.key"
} > "$KROOT/server.properties"
chmod 600 "$KROOT/server.properties"
```

Each `emit_pem` line is one property whose value is the whole
certificate/key with `\n`-escaped newlines — that is why the file must be
generated, not hand-edited.

**4. CLI client properties (for every `--command-config`):**

```bash
make_client_props() { # $1=file $2=user $3=password
  {
    echo 'security.protocol=SASL_SSL'
    echo 'sasl.mechanism=SCRAM-SHA-512'
    echo "sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username=\"$2\" password=\"$3\";"
    echo 'ssl.truststore.type=PEM'
    printf 'ssl.truststore.certificates='
    awk '{printf "%s\\n", $0}' "$KROOT/tls/ca.crt"
    echo
  } > "$1"
  chmod 600 "$1"
}
make_client_props "$KROOT/client-admin.properties" admin "$KAFKA_ADMIN_PASSWORD"
make_client_props "$KROOT/client-fulfillment.properties" fulfillment-service "$KAFKA_FULFILLMENT_PASSWORD"

# helper for the Kafka CLI tools. No --network host and no --user 0:0:
# the tools only need to reach the published <host-ip>:9092 like any
# other client, and they run fine as the image's default user
IMAGE=docker.io/apache/kafka:4.2.0
krun() { # krun <tool> [args...]
  local tool=$1; shift
  podman run --rm --security-opt label=disable \
    -v "$KROOT/client-admin.properties:/etc/kafka/client.properties:ro" \
    --entrypoint /bin/bash "$IMAGE" \
    -c "/opt/kafka/bin/$tool --bootstrap-server <host-ip>:9092 --command-config /etc/kafka/client.properties $*"
}
```

**5. KRaft storage format (first run only), seeding the admin SCRAM user**
(the name lives in `name=` brackets **with** `name=` here, unlike
`kafka-configs.sh`):

```bash
CLUSTER_ID=$(podman run --rm --entrypoint /bin/bash "$IMAGE" \
  -c '/opt/kafka/bin/kafka-storage.sh random-uuid')
podman run --rm --security-opt label=disable --user 0:0 \
  -v "$KROOT/server.properties:/etc/kafka/server.properties:ro" \
  -v "$DATA/data:/var/lib/kafka/data" \
  --entrypoint /bin/bash "$IMAGE" \
  -c "/opt/kafka/bin/kafka-storage.sh format -t $CLUSTER_ID -c /etc/kafka/server.properties --add-scram 'SCRAM-SHA-512=[name=admin,password=${KAFKA_ADMIN_PASSWORD}]'"
```

**6. systemd unit — the oneshot shape (A.1).** With `podman run -d` the
main process exits ~1 s after start, so any ExecStop would kill the healthy
container ~60 s later; the unit therefore has none. systemd shows the unit
inactive while the container runs; day-2 management is bare
`podman stop/start`, and `systemctl restart` re-creates cleanly via
`--replace`:

```ini
# /etc/systemd/system/container-osac-kafka.service
[Unit]
Description=OSAC external Kafka (single-node KRaft, SASL_SSL <host-ip>:9092)
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
TimeoutStartSec=300
ExecStartPre=/bin/sh -c 'rm -f %t/%n.ctr-id'
ExecStart=/usr/bin/podman run --cidfile=%t/%n.ctr-id --cgroups=no-conmon \
  --rm --sdnotify=conmon --replace -d --name osac-kafka \
  -p <host-ip>:9092:9092 --security-opt label=disable --user 0:0 \
  -v /root/osac-kafka-external/server.properties:/etc/kafka/server.properties:ro \
  -v /root/osac-kafka-external/tls:/etc/kafka/tls:ro \
  -v /var/lib/osac/kafka/data:/var/lib/kafka/data \
  -e 'KAFKA_HEAP_OPTS=-Xmx1G -Xms512M' \
  --entrypoint /bin/bash docker.io/apache/kafka:4.2.0 \
  -c 'exec /opt/kafka/bin/kafka-server-start.sh /etc/kafka/server.properties'

[Install]
WantedBy=multi-user.target
```

```bash
systemctl daemon-reload && systemctl enable --now container-osac-kafka.service
```

**7. Wait for readiness, then create the service users** (name comes from
`--entity-name`, **no** `name=` inside brackets — the mirror image of the
`kafka-storage.sh` trap):

```bash
# fail loudly if the broker never comes up — without the exit the loop
# below falls through after 60 tries and the user-creation commands
# run against a dead broker, reporting connection errors that point
# away from the real cause
ready=
for i in $(seq 1 60); do
  if krun kafka-broker-api-versions.sh >/dev/null 2>&1; then ready=1; break; fi
  sleep 5
done
[ -n "$ready" ] || { echo "kafka broker never became ready" >&2; exit 1; }

krun kafka-configs.sh --alter --entity-type users --entity-name fulfillment-service \
  --add-config "SCRAM-SHA-512=[password=${KAFKA_FULFILLMENT_PASSWORD}]"
krun kafka-configs.sh --alter --entity-type users --entity-name osac-metering \
  --add-config "SCRAM-SHA-512=[password=${KAFKA_METERING_PASSWORD}]"
```

**8. ACLs per the §6.1 contract:**

```bash
krun kafka-acls.sh --add --allow-principal User:fulfillment-service \
  --operation Create --operation Write --operation Read --operation Describe \
  --operation DescribeConfigs \
  --topic osac.events. --resource-pattern-type prefixed
krun kafka-acls.sh --add --allow-principal User:fulfillment-service \
  --operation Read --operation Describe \
  --group osac.system. --resource-pattern-type prefixed
krun kafka-acls.sh --add --allow-principal User:fulfillment-service \
  --operation IdempotentWrite --cluster
krun kafka-acls.sh --add --allow-principal User:osac-metering \
  --operation Read --operation Describe \
  --topic osac.events. --resource-pattern-type prefixed
krun kafka-acls.sh --add --allow-principal User:osac-metering \
  --operation Create --operation Read --operation Write --operation Describe \
  --operation DescribeConfigs \
  --topic osac.metering.heartbeat. --resource-pattern-type prefixed
krun kafka-acls.sh --add --allow-principal User:osac-metering \
  --operation Create --operation Describe --operation DescribeConfigs \
  --topic osac.metering.dlq. --resource-pattern-type prefixed
krun kafka-acls.sh --add --allow-principal User:osac-metering \
  --operation IdempotentWrite --cluster
```

`DescribeConfigs` is not optional on the topic prefixes: both services
validate the retention of the topics they find and explicitly fail when
that permission is denied.

**9. Topics — only the two shared inference topics are yours to create.**
The per-tenant pipeline creates the rest itself: fulfillment's event
publisher creates `osac.events.<tenant>`, and metering creates
`osac.metering.heartbeat.<tenant>` and `osac.metering.dlq.<tenant>` —
each with **one partition** and a validated configuration
(`retention.ms` set to the service's configured retention,
`retention.bytes=-1`, `cleanup.policy=delete`). Do **not** pre-create
per-tenant topics by hand: a hand-made topic with different settings
(partition count, retention) makes the service refuse to use it.

```bash
mk_topic() { # name partitions retention_ms
  krun kafka-topics.sh --create --topic "$1" --partitions "$2" \
    --replication-factor 1 --config "retention.ms=$3" \
    --config "retention.bytes=-1" --config "cleanup.policy=delete" \
    --config min.insync.replicas=1
}
# the shared MaaS inference topics the metering chart manages in the
# Strimzi flow (chart config: 3 partitions, 30-day retention)
mk_topic osac.metering.inference     3 2592000000    # 30 days
mk_topic osac.metering.inference.dlq 3 2592000000
```

A full produce/consume self-test as `fulfillment-service` (a scratch
topic you create with the `mk_topic` helper, consumer group, cleanup)
doubles as the §6.3 connection-level verification; when
running it by hand remember the `-i` trap: piping into
`kafka-console-producer` through `podman run` without `-i` silently
sends nothing.
