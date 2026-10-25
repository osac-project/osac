# External Services Support Guide

**Audience:** Administrators deploying and operating OSAC with its backing
services — PostgreSQL, OpenBao, Keycloak, and Kafka — running **outside**
the OpenShift/Kubernetes cluster, for example as podman containers on a
services host.

The main body of this guide is written for a **clean deployment**: the
external-services values are set before the first `helm install`, so OSAC
comes up against external services from the start. That is what a customer
needs to know — common prerequisites, the per-service **external-mode
configuration** (exactly which files and parameters to modify),
verification, and surviving `helm upgrade`. Ready-to-run container recipes
for all four services are collected in
[Appendix A](#appendix-a-running-the-services-as-containers). Already
running OSAC on the bundled services and moving them out instead? That
path — quiesce, data migration, cutover, backup, restore, and rollback —
is [Appendix B](#appendix-b-migrating-a-bundled-deployment-to-external-services).
Section 6 implements the broker contract from the [external Kafka
configuration guide](kafka-configuration.md); for OpenBao/Vault background
see the [secrets management configuration
guide](secrets-management-configuration.md), and for the OSAC realm layout
see the [Keycloak configuration guide](../keycloak-configuration.md).

Every procedure was validated end-to-end on a lab deployment (single-node
OpenShift hub plus a podman services host, October 2026, installer chart
pin `fc7a09f`; PostgreSQL 18, Keycloak 26.6.4, OpenBao 2.6.2, Kafka
4.2.0) — in its migration form: a live bundled deployment moved to
external services (the sequencing Appendix B carries). The clean-install
values-first flow in the main body uses the same chart interface but was
not separately exercised end-to-end; prove it on a scratch install before
relying on it in production. Appendix A carries generic versions of the
same recipes; the as-deployed scripts and live values from that
deployment are collected in an internal site-specific companion runbook.

This guide contains no credentials: secrets always appear as
`<placeholders>`, and each section names where the real value lives
(root-only files on the services host, or cluster Secrets).

---

## Table of Contents

1. [Overview and integration patterns](#1-overview-and-integration-patterns)
2. [Common prerequisites](#2-common-prerequisites)
3. [PostgreSQL](#3-postgresql)
4. [OpenBao](#4-openbao)
5. [Keycloak](#5-keycloak)
6. [Kafka](#6-kafka)
7. [Helm values reference](#7-helm-values-reference)
8. [Surviving `helm upgrade` — the reapply pattern](#8-surviving-helm-upgrade--the-reapply-pattern)
9. [Verification battery](#9-verification-battery)
10. [Backups](#10-backups)
11. [Gotchas compendium](#11-gotchas-compendium)

Appendix A. [Running the services as containers](#appendix-a-running-the-services-as-containers)
Appendix B. [Migrating a bundled deployment to external services](#appendix-b-migrating-a-bundled-deployment-to-external-services)

**Conventions used throughout:** `<host-ip>` is the address on the services
host that cluster nodes can reach; `<keycloak-route-host>` is the Keycloak
route hostname; everything in angle brackets is a value to substitute. The
namespaces (`osac`, `osac-infra`, `keycloak`, …) and object names
(`external-kafka`, `osac-db-config`, …) are chart-level constants — use them
as-is.

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
easily holds 300+ Gi of PVCs), host-level backup and ops tooling, isolating
stateful services from cluster reinstalls, and lab resource constraints.
What typically **stays** in-cluster: AAP, CNV/KubeVirt, cert-manager,
trust-manager, MetalLB, MCE (multicluster engine for Kubernetes) with the
workload clusters it manages, the operator, and the UI.

### 1.2 The three integration patterns

Everything OSAC needs to talk to an external service falls into one of three
patterns. Choosing correctly up front decides how much re-work each
`helm upgrade` costs you later.

| Pattern | How it works | Used for | Survives `helm upgrade`? |
|---|---|---|---|
| **A — supported values path** | chart flag disables the bundled instance + a values key points at the external endpoint | OpenBao (`bundledVault.enabled: false` + `service.vault.endpoint`), Kafka (`kafka.enabled: false` + `service.kafka.connection`), PostgreSQL (`bundledPostgres.enabled: false` + `dbInit.host` + Secrets) | **yes** — nothing imperative |
| **B — selectorless Service + manual Endpoints** | the chart-owned Service keeps its name; remove its `spec.selector`; write a manual `Endpoints` object pointing at `<host-ip>` | PostgreSQL or Keycloak when you want **zero** in-cluster configuration change | **no** — re-apply after every upgrade (§8) |
| **C — hostname preservation (Keycloak-specific refinement of B)** | the external Keycloak serves the **same route hostname and certificate** and imports the **same realm**, so issuer URLs never change | Keycloak | the endpoints re-apply, but nothing else |

**How to choose:** always prefer pattern A — it is the chart-supported path.
Use pattern B only when the chart lacks an endpoint knob for that service or
when you must keep the in-cluster DNS name and TLS material identical (the
PostgreSQL cert-auth case below). For Keycloak, combine B with C and you
avoid re-pointing deployments, OpenBao JWT config, and client Secrets
entirely.

> **Clean install vs migration:** everything in §3–§6 assumes the values
> are set before the first `helm install` — pattern A from the start, no
> bundled instance to remove, nothing to re-apply but the Keycloak
> endpoints. Moving a deployment that already runs the bundled services is
> the same values plus sequencing (quiesce, data migration, rollback
> points): [Appendix B](#appendix-b-migrating-a-bundled-deployment-to-external-services).

**Why Keycloak has no pattern-A row:** the chart exposes no external-IdP
values mode — no endpoint knob exists to point at an external Keycloak —
and the one flag it does have works against you: `keycloak.enabled: false`
deletes the Service and route whose hostname every issuer URL depends on
(§1.3). The only chart-supported Keycloak externalization today is keeping
it enabled and swapping the Service's backing address (B) while preserving
hostname, certificate, and realm (C). §8's retirement table tracks when a
values path appears.

### 1.3 What must not change

The in-cluster consumers verify identity, not location:

- **PostgreSQL**: clients authenticate with **client certificates** and
  connect to the address in `osac-db-config` (on a clean install that is
  `<host-ip>`; under pattern B it stays the in-cluster DNS name). The
  external server must use a server certificate valid for that same
  address and the same CA, and the same HBA rules — then the
  `osac-db-config` / `osac-db-client-cert` Secrets keep working verbatim.
- **Keycloak**: JWT issuer URLs (`https://<keycloak-route-host>/realms/osac`)
  are baked into deployments and the OpenBao JWT auth config. Keep the
  hostname and realm keys identical and nothing needs re-pointing.
- **OpenBao / Kafka**: plain endpoints in values — the simplest cases.

---

## 2. Common prerequisites

Cluster-side prerequisites that apply to every external service. The
host-side preliminaries for running the containers themselves — SELinux
labels and systemd unit shapes — are covered in
[Appendix A.1](#a1-host-preliminaries-selinux-and-systemd-units).

### 2.1 Network

- Cluster nodes (and therefore pods) must route to `<host-ip>`. On a KVM
  host running the cluster VM, the bridge address works naturally; on other
  layouts verify with a `TCP` probe from a pod:
  `oc -n osac run netcheck --rm -it --image=quay.io/toolbox/netcat -- nc -vz <host-ip> 5432`.
- Publish each service **only on the specific IP**
  (`-p <host-ip>:<port>:<port>`), never on `0.0.0.0` — keeps the services off
  every other interface the host owns.
- Open the ports in the host firewall for the cluster subnet.

### 2.2 TLS trust model

The cluster's trust root is the cert-manager `ClusterIssuer` `default-ca`,
distributed by trust-manager into the `ca-bundle` ConfigMap in every
namespace. Three ways to make the externals trusted — in order of preference:

1. **Issue the external service's certificate from the cluster** (a
   cert-manager `Certificate` CR with an IP SAN of `<host-ip>`, PKCS#8 for
   Kafka). The existing `ca-bundle` already trusts it; **zero trust
   changes**.
2. **Keycloak route-cert reuse** (pattern C): serve the certificate already
   installed on the Keycloak route (extract it from the in-cluster Secret)
   → zero trust changes.
3. **External CA**: add it as a new source to the `Bundle` CR:

   ```bash
   kubectl create secret generic <svc>-host-cert -n cert-manager \
     --from-file=ca.crt=/path/to/ca.pem --dry-run=client -o yaml | kubectl apply -f -
   kubectl patch bundle.trust.cert-manager.io ca-bundle --type=json -p='[
     {"op":"add","path":"/spec/sources/-",
      "value":{"secret":{"name":"<svc>-host-cert","namespace":"cert-manager","key":"ca.crt"}}}
   ]'
   ```

> Never edit the `ca-bundle` ConfigMap directly — trust-manager reverts it
> within seconds. Always patch the `Bundle` CR. If you add a CA and clients
> still fail with `x509: certificate signed by unknown authority`, restart
> the consuming deployments — some cache the bundle at startup.

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
  `osac-db-config`) — on a clean install that is `<host-ip>`; when
  migrating under pattern B it is the in-cluster DNS name, in which case
  extract the bundled instance's server cert, CA, and client certs from
  the in-cluster Secrets and reuse them verbatim (B.2.1).
- **Client-certificate (mutual TLS) auth** preserved — the HBA rules in
  Appendix A.2 are what the chart's bundled instance uses; keep them
  identical.
- Databases/roles: `osac_service` and `osac_metering`, roles `service` and
  `metering` (the client-cert CNs), superuser password set at container
  creation.

### 3.2 External-mode configuration — what to modify

On a clean install, set these values before the first `helm install`: the
db-init job then runs against the external instance and the chart-consumed
Secrets point at it from the start — no cutover, nothing to re-apply.
Pick **one** integration pattern (§1.2); the table lists every file,
object, and parameter involved:

| # | Where | Object / file | Parameter | Value |
|---|---|---|---|---|
| 1 | `osac-infra` release | values | `bundledPostgres.enabled` | `false` — pattern A and B |
| 2 | `osac` release | values | `dbInit.host` | `<host-ip>` (port 5432) — **pattern A only** |
| 3 | ns `osac-infra` | Service `postgres` | `spec.selector` | **removed** — pattern B only (B.2.1) |
| 4 | ns `osac-infra` | Endpoints `postgres` | `subsets` | `[{ip: <host-ip>, port: 5432}]` — pattern B only (B.2.1) |
| 5 | ns `osac` | Secrets `osac-db-config`, `osac-db-client-cert` | — | **unchanged** — client-cert auth keeps working |

**Pattern A — supported values path (preferred).** Point the chart at the
external database and disable the bundled one:

```yaml
bundledPostgres:
  enabled: false        # osac-infra release
dbInit:
  host: <host-ip>       # osac release; port 5432
```

The `osac-db-config` (URL) and `osac-db-client-cert` (tls.crt/tls.key/ca.crt)
Secrets in ns `osac` are chart-consumed. On a clean install they are created
against `dbInit.host` from the start. When migrating a deployment whose
Secrets still point at the in-cluster DNS name, keep that name valid with
the selectorless Service instead — [B.2.1](#b21-postgresql).

Use a current installer chart pin: pins that predate the fulfillment OAuth
wiring crashloop the operator and metering after any database move until
the credentials are patched in ([B.1](#b1-before-any-migration)).

### 3.3 Verify

```bash
# connections arrive on the external instance from the expected users
podman exec osac-postgres psql -U postgres -Atc \
  "select usename, count(*) from pg_stat_activity where usename in ('service','metering') group by 1"

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
  the `osac` namespace, `secrets enable -namespace=osac kv-v2`, the
  transit mount, JWT auth, and the `lifecycle` policy and role (bound to
  the Keycloak issuer, audience `osac-api`).
- Migrating a bundled instance that already holds real state instead?
  Nothing else is needed — the init state (unseal key, root token, `osac`
  namespace, JWT auth config, lifecycle policy and role) moves with the
  data directory ([B.2.2](#b22-openbao)).

### 4.2 External-mode configuration — what to modify

Pattern A — values only (survives upgrades). Every key involved:

| # | Release | Parameter | Value |
|---|---|---|---|
| 1 | `osac-infra` | `bundledVault.enabled` | `false` |
| 2 | `osac` | `service.vault.endpoint` | `https://<host-ip>:8200` |
| 3 | `osac` | `service.vault.caBundle.configMap` | `ca-bundle` (add the host CA per §2.2 option 3 if self-signed) |
| 4 | `osac` | `service.vault.keycloakIssuerUrl` | `https://<keycloak-route-host>/realms/osac` |
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
      configMap: ca-bundle          # add the host cert per §2.2 option 3
    keycloakIssuerUrl: https://<keycloak-route-host>/realms/osac
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

### 5.1 Strategy: hostname preservation

Everything in the cluster that talks to Keycloak does so through the issuer
URL `https://<keycloak-route-host>/realms/osac` — deployments, the OpenBao
JWT auth config, client Secrets. If the external Keycloak **answers on the
same route hostname with the same certificate and the same realm**, none of
that changes. Concretely:

- extract the **route certificate** from the in-cluster Secret
  (`oc -n keycloak extract secret/<route-cert-secret>` — it is
  `default-ca`-issued and already trusted by the whole cluster);
- set `KC_HOSTNAME=<keycloak-route-host>` on the container (run recipe in
  [Appendix A.4](#a4-keycloak));
- import a **full realm export** so realm keys (and therefore outstanding
  tokens) survive;
- keep the existing passthrough route and make the chart-owned Service
  `keycloak` selectorless → Endpoints → `<host-ip>:8443` (pattern B).

The alternative — re-pointing `--auth-issuer-url` and friends at a new
hostname — touches every consumer and the OpenBao JWT config; it is only
worth it if you cannot reuse the hostname.

### 5.2 External-mode configuration — what to modify

Apply after the external instance serves the realm on the route hostname
(§5.3). Every object and parameter involved:

| # | Where | Object / parameter | Value |
|---|---|---|---|
| 1 | ns `keycloak` | Service `keycloak` `spec.selector` | **removed** (pattern B) |
| 2 | ns `keycloak` | Endpoints `keycloak` `subsets` | `<host-ip>:8443` |
| 3 | `osac-infra` release | `keycloak.enabled` | **stays `true`** — the chart must keep owning the Service and route |
| 4 | container (Appendix A.4) | `KC_HOSTNAME` | `<keycloak-route-host>` — the existing route hostname |
| 5 | container (Appendix A.4) | TLS key/cert | the route certificate, extracted from the in-cluster Secret |
| 6 | cluster | deployments, OpenBao JWT config, client Secrets | **unchanged** — issuer URL preserved |

```bash
# 1. rewire the chart-owned Service (pattern B)
oc -n keycloak patch svc keycloak --type=json -p '[{"op":"remove","path":"/spec/selector"}]'
oc -n keycloak patch endpoints keycloak --type=merge \
  -p '{"subsets":[{"addresses":[{"ip":"<host-ip>"}],"ports":[{"name":"https","port":8443,"protocol":"TCP"}]}]}'
oc -n keycloak delete endpointslice -l kubernetes.io/service-name=keycloak

# 2. verify THROUGH THE ROUTE before touching the bundled instance
curl -sk https://<keycloak-route-host>/realms/osac | head -c 120

# 3. only now scale the bundled instance down (keep its database for rollback)
oc -n keycloak scale deploy keycloak-service --replicas=0
oc -n keycloak scale sts keycloak-database --replicas=0
```

`keycloak.enabled` stays `true` in the `osac-infra` release — the chart
must keep owning the Service and route; that ownership is what preserves
the hostname. Re-apply the endpoints after every `helm upgrade` (§8).

### 5.3 Seed the realm

Import the chart's `realm.json` with its `__OSAC_*__` placeholders resolved
from the `keycloak-client-secrets` Secret, then apply the post-import
fixups (UI client redirect URIs, `profile` and `email` scopes).

Migrating an instance that already holds users instead? A full export
keeps users with password hashes, organizations, client fixups, and realm
keys — [B.2.3](#b23-keycloak).

### 5.4 Verify

```bash
curl -sk https://<keycloak-route-host>/realms/osac \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['realm'])"    # osac

# token end-to-end (ROPC works for tenant users; the UI client is public
# PKCE and cannot do it — use the browser for that flow)
curl -sk -d "client_id=admin-cli&username=<user>&password=<password>&grant_type=password" \
  https://<keycloak-route-host>/realms/osac/protocol/openid-connect/token
```

Then one UI login (full OIDC PKCE) and one API call with the resulting
token. From a node inside the cluster, probe routes with
`curl --resolve <route-host>:443:127.0.0.1` (SNI).

---

## 6. Kafka

### 6.1 The contract

The broker must satisfy the upstream
[external-Kafka configuration](https://github.com/osac-project/osac/blob/main/docs/guides/installation/kafka-configuration.md)
(OSAC-4760):

| Requirement | Value |
|---|---|
| Protocol | `SASL_SSL` with `SCRAM-SHA-512` (only enabled mechanism) |
| Address | `host:port` (no scheme), reachable from pods |
| TLS | CA chain trusted by the cluster, cert matching the broker address, ≥ 1.2 |
| `fulfillment-service` ACLs | topic prefix `osac.events.`: Create/Write/Read/Describe · group prefix `osac.system.`: Read/Describe · cluster: IdempotentWrite |
| `osac-metering` ACLs | topic prefix `osac.metering.`: Write/Describe · cluster: IdempotentWrite |
| Topics | `auto.create.topics.enable: true` (event topics are `osac.events.<tenant>`) |
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

Values (pattern A):

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
# metering: no values-only external path exists upstream; pointing the
# fetch at the pre-created Secret above is the working extension
metering:
  kafka:
    brokers: "<host-ip>:9092"
    clusterName: "<external-broker-name>"
    clusterNamespace: osac
```

On a clean install these values and objects are in place before the first
`helm install`. On a running deployment, apply the objects, then one
surgical upgrade (`helm upgrade osac <chart> -n osac --reuse-values
--timeout 20m`, with `set -o pipefail` before any `| tail` pipeline — the
AAP bootstrap hook needs the 20 m timeout), then the reapply script (§8),
then verify.

### 6.3 Verify

```bash
# the canary: metering publishes one heartbeat event per minute to
# osac.metering.heartbeat — offsets must advance between two runs ~100 s apart
kafka-get-offsets.sh --bootstrap-server <host-ip>:9092 \
  --command-config <admin-props> --topic osac.metering.heartbeat --time -1

# every consumer group live and at lag 0
kafka-consumer-groups.sh --bootstrap-server <host-ip>:9092 \
  --command-config <admin-props> --list
kafka-consumer-groups.sh … --describe --group <group>

# in-cluster: the publisher's kafka config comes from the external objects
oc -n osac get deploy fulfillment-event-publisher -o jsonpath='{.spec.template.spec.volumes[0].projected.sources}'
```

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
  bundled copy of that service. They are only *half* of pattern A.
- **Endpoint keys** (`dbInit.host`, `service.vault.endpoint`,
  `service.kafka.connection`) are the other half — they tell the OSAC
  components where the external instance lives.

PostgreSQL, OpenBao, and Kafka have both halves, so `enabled: false` plus
an endpoint key is the whole integration (pattern A). Keycloak has
neither: no endpoint key exists, and `keycloak.enabled: false` would
*uninstall* the Service and route rather than externalize them — so it
stays `true` and the wiring happens behind the chart-owned Service
instead (patterns B+C, §1.2).

```yaml
# ── osac-infra ──────────────────────────────────────────────────────────
bundledPostgres:
  enabled: false                # external PostgreSQL (§3)
bundledVault:
  enabled: false                # external OpenBao (§4)
kafka:
  enabled: false                # external Kafka (§6): listener cert + hooks
keycloak:
  enabled: true                 # KEEP true — chart must own svc+route (§5)

# ── osac-deps ───────────────────────────────────────────────────────────
kafka:
  enabled: false                # removes ns + OLM subscription + operator

# ── osac ────────────────────────────────────────────────────────────────
service:
  vault:                        # §4
    endpoint: https://<host-ip>:8200
    caBundle: {configMap: ca-bundle}
    keycloakIssuerUrl: https://<keycloak-route-host>/realms/osac
    keycloakClientId: osac-controller
    keycloakAudience: osac-api
    namespace: osac
    kvMountPath: secret
    lifecycleMountPath: jwt
    lifecycleRole: lifecycle
  kafka:                        # §6
    connection:
    - configMap: {name: external-kafka, items: [{key: brokers, param: brokers},
                                                {key: user, param: user}]}
    - secret: {name: external-kafka-credentials, items: [{key: password, param: password}]}
  database:                     # §3, supported path
    connection: […]             # chart-consumed Secrets osac-db-config +
                                # osac-db-client-cert (unchanged under pattern B)
kafka:
  enabled: false
metering:
  kafka: {brokers: "<host-ip>:9092", clusterName: "<name>", clusterNamespace: osac}
```

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
helm upgrade osac charts/osac -n osac --reuse-values --timeout 20m 2>&1 | tail -8 \
  && bash <reapply-script>            # §8 — mandatory
```

---

## 8. Surviving `helm upgrade` — the reapply pattern

`helm upgrade` re-renders every object the chart owns — which silently
reverts anything you wired imperatively. The pattern-B selectorless
Services get their selectors back, env-var patches to deployments
disappear, and chart-version workarounds vanish. The fix is one
**idempotent, check-first reapply script** run after every upgrade:

```bash
#!/usr/bin/env bash
# reapply-after-helm-upgrade.sh — re-assert imperative wiring post-upgrade
set -euo pipefail
export KUBECONFIG=<hub-kubeconfig>

# [1] PostgreSQL: selectorless svc + endpoints (idempotent)
oc -n osac-infra patch svc postgres --type=json -p '[{"op":"remove","path":"/spec/selector"}]'
oc -n osac-infra patch endpoints postgres --type=merge \
  -p '{"subsets":[{"addresses":[{"ip":"<host-ip>"}],"ports":[{"name":"postgres","port":5432,"protocol":"TCP"}]}]}'
# …stale-slice check + re-mirror…

# [2] deployment env patches the chart version lacks (OAuth wiring, …)
#     check-first: only patch when the env key is absent

# [N] verification tail: rollout status of every osac deployment,
#     endpoints IPs, can-i probes — fail loudly on any regression
```

What belongs in it, and when each line retires:

| Re-applied | Retires when |
|---|---|
| PG selectorless svc + Endpoints + slice re-mirror | converged to the `dbInit.host` supported path |
| Keycloak selectorless svc + Endpoints | chart grows an external-IdP mode |
| operator/metering OAuth env | chart pin ≥ the OAuth wiring revision |
| chart-bug workarounds (envoy ALPN order, operator memory, missing RBAC rules) | the corresponding upstream fixes ship |
| rollout + wiring verification | never — this part is the point |

On a clean install configured with pattern A from the start, the PostgreSQL
line never exists — only the Keycloak endpoints (and any chart-bug
workarounds of the day) get re-applied.

The companion runbook's `reapply-after-helm-upgrade.sh` (150 lines) is a
complete working example with eight sections.

---

## 9. Verification battery

The full green-state check — run after any change, from a host with
cluster admin:

```bash
oc get co                          # all Available=True, not progressing/degraded
oc get nodes                       # Ready
oc -n osac get pods                # no CrashLoopBackOff / pending
oc -n osac rollout status deploy/fulfillment-controller   # …and every deploy

# wiring intact (pattern B)
oc -n osac-infra get endpoints postgres -o jsonpath='{.subsets[0].addresses[0].ip}'
oc -n keycloak get endpoints keycloak -o jsonpath='{.subsets[0].addresses[0].ip}'

# external health
curl -sk -o /dev/null -w '%{http_code}\n' https://<host-ip>:8200/v1/sys/health          # 200
curl -sk https://<keycloak-route-host>/realms/osac | python3 -c \
  "import sys,json; print(json.load(sys.stdin)['realm'])"                                # osac
podman exec osac-postgres psql -U postgres -Atc \
  "select count(*) from pg_stat_activity where usename in ('service','metering')"        # >0

# auth chain end-to-end: 401 on a dummy token is the GREEN answer
curl -sk -o /dev/null -w '%{http_code}\n' -H 'Authorization: Bearer dummy' \
  https://<api-route>/api/fulfillment/v1/tenants                                          # 401
# (from a cluster node: add --resolve <api-route>:443:127.0.0.1)

# Kafka canary (§6.3): heartbeat offsets advance, consumer lag 0
```

REST paths are snake_case (`/api/fulfillment/v1/compute_instances/{id}`) —
camelCase 404s and prove nothing.

---

## 10. Backups

| Service | Strategy | Cost | Retention |
|---|---|---|---|
| PostgreSQL | nightly online `pg_dumpall \| gzip` | none | 14 |
| OpenBao | stop → tar data dir → start → health check | seconds of downtime | 14 |
| Keycloak | partial exports (realm/users/orgs/LDAP) + data-dir tar with brief stop | ~40 s | 7 JSON / 3 tars |
| Kafka | none — event log with retention; the heartbeat canary is the health alarm | — | — |

Stagger the cron times so nothing overlaps; have each script verify its own
output (a backup that silently produced nothing is worse than none). All
secrets in the backups are encrypted at rest or hashed — but treat the
backup files as root-only anyway. The scripts, restore procedures, and the
rollback matrix are collected in
[Appendix B.3](#b3-backup-restore-and-rollback).

---

## 11. Gotchas compendium

1. **systemd unit shapes** — the Appendix A.1 table. The two that bite
   hardest: a `--new`-style unit un-does a bare `podman stop` within
   ~100 ms (use `systemctl stop`), and a detached-run container under a
   plain unit needs the oneshot shape with **no** ExecStop.
2. **EndpointSlice re-mirror** — after rewriting Endpoints, delete the
   label-matched slices and let the mirroring controller rebuild them, or
   traffic keeps flowing to the old addresses.
3. **trust-manager owns `ca-bundle`** — patch the `Bundle` CR, never the
   ConfigMap; and restart consuming deployments after adding a CA (cached
   bundles).
4. **Clock skew** breaks JWT validation at ~30 s (§2.3).
5. **Keycloak image runs as uid 1000** — root-owned TLS key files abort
   startup with `AccessDeniedException`.
6. **Keycloak `kc.sh export` drops LDAP federations and org memberships** —
   replay them after import (B.2.3).
7. **Kafka listener-name casing** — `listener.name.*` keys are lowercase;
   a listener named `SASL_SSL` never picks up its config and the broker
   dies with a JAAS lookup error.
8. **Kafka server-side SCRAM JAAS is credential-free** —
   `ScramLoginModule required;` with nothing else; users live in KRaft
   metadata (`kafka-storage.sh --add-scram` needs `name=` inside the
   brackets, `kafka-configs.sh --add-config` takes the name from
   `--entity-name` and must NOT have it).
9. **Kafka SASL listener needs a truststore** — even as a server, the
   channel-builder self-test connects to itself; without
   `listener.name.sasl_ssl.ssl.truststore.*` it dies with
   `PKIX path building failed`.
10. **`podman run` forwards stdin only with `-i`** — piping into
    `kafka-console-producer` without `-i` silently no-ops (exit 0, zero
    messages). Deprecated CLI flags print warnings on **stdout** — compare
    output by grepping for the expected token, never by string equality.
    `kafka.tools.GetOffsetShell` no longer exists — use
    `kafka-get-offsets.sh`.
11. **The generated `server.properties` holds the broker private key** —
    generate it with a script (mode 600), never paste it into docs/tickets.
12. **`--reuse-values` carries values-file changes forward**, but `--set`
    values must be re-passed; and `set -o pipefail` before any
    `helm … | tail` or the reapply runs after a *failed* helm.
13. **AAP bootstrap hook needs `--timeout 20m`** — it alone takes ~7 min;
    helm's 5-min default kills the upgrade.
14. **values.schema.json** — root `additionalProperties: false`; image
    tags live under `service.images.*` (§7).
15. **Envoy ALPN** — chart versions have rendered `[http1.1, h2]`
    (invalid ALPN id, wrong order — gRPC clients pin to HTTP/1.1 and drop
    trailers, which shows up as a UI login spinner). Workaround: patch both
    listeners to `[h2, http/1.1]` in the envoy ConfigMap and restart —
    belongs in the reapply script until the chart fix ships.
16. **REST paths are snake_case** (§9).
17. **Routes from a cluster node need `--resolve`** for SNI (§5.4).
18. **cron PATH is minimal** — scripts run from cron need an explicit PATH
    export or host tools (`iptables` in `/usr/sbin`) silently disappear.

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
├── tls.crt / tls.key          # server certificate
└── client-service.crt/.key, client-metering.crt/.key

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
  -e POSTGRESQL_PASSWORD=<superuser-password> \
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

```bash
openssl req -x509 -newkey rsa:2048 -keyout /root/bao-key.pem -out /root/bao-cert.pem \
  -days 365 -nodes -subj "/CN=<host-ip>" -addext "subjectAltName=IP:<host-ip>"
chmod 644 /root/bao-cert.pem /root/bao-key.pem
mkdir -p /root/bao-data /root/bao-config    # config as above

podman run -d --name osac-openbao \
  -p <host-ip>:8200:8200 -p <host-ip>:8201:8201 \
  --security-opt label=disable \
  -v /root/bao-cert.pem:/bao/tls/tls.crt:z -v /root/bao-key.pem:/bao/tls/tls.key:z \
  -v /root/bao-data:/bao/data:z -v /root/bao-config:/bao/config:z \
  -e HOME=/tmp ghcr.io/openbao/openbao:2.6.2 server -config=/bao/config/bao.hcl

# boot persistence (--new-style unit — see A.1)
podman generate systemd --new --name osac-openbao --restart-policy=always \
  > /etc/systemd/system/container-osac-openbao.service
systemctl daemon-reload && systemctl enable --now container-osac-openbao.service
```

Add an **auto-unseal drop-in**: an `ExecStartPost` helper that polls
`/v1/sys/health` and POSTs the unseal key from a root-only file
(`/root/openbao-init.json`), never printing it:

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
LOG=/var/log/osac-openbao-unseal.log

KEY=$(python3 -c 'import json;print(json.load(open("/root/openbao-init.json"))["unseal_keys_b64"][0])')

for _ in $(seq 1 30); do
  HEALTH=$(curl -sk --max-time 3 "$BASE/v1/sys/health" 2>/dev/null) || { sleep 2; continue; }
  SEALED=$(printf "%s" "$HEALTH" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("sealed"))' 2>/dev/null) || { sleep 2; continue; }
  if [ "$SEALED" = "False" ]; then
    exit 0
  fi
  if [ "$SEALED" = "True" ]; then
    RESP=$(curl -sk --max-time 5 -X PUT -d "{\"key\":\"$KEY\"}" "$BASE/v1/sys/unseal" 2>/dev/null) || RESP=""
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

Remember the A.1 trap: with a `--new`-style unit, **`systemctl stop` is the
only stop systemd will not undo** — any operational script (backups,
maintenance) must go through systemctl.

### A.4 Keycloak

```bash
# the image runs as uid 1000 — it must be able to read the key
chown 1000:0 /root/kc-route-cert.pem /root/kc-route-key.pem
chmod 644 /root/kc-route-cert.pem && chmod 400 /root/kc-route-key.pem
mkdir -p /root/kc-data && chown -R 1000:0 /root/kc-data    # H2 lives here

podman run -d --name keycloak-external \
  -p <host-ip>:8443:8443 \
  --security-opt label=disable \
  -v /root/kc-route-cert.pem:/etc/x509/https/tls.crt \
  -v /root/kc-route-key.pem:/etc/x509/https/tls.key \
  -v /root/kc-data:/opt/keycloak/data \
  -e KEYCLOAK_ADMIN=admin \
  -e KEYCLOAK_ADMIN_PASSWORD=<kc-admin-password> \
  -e KC_HTTPS_CERTIFICATE_FILE=/etc/x509/https/tls.crt \
  -e KC_HTTPS_CERTIFICATE_KEY_FILE=/etc/x509/https/tls.key \
  -e KC_HOSTNAME=<keycloak-route-host> \
  -e KC_HOSTNAME_STRICT=false \
  -e KC_HTTP_ENABLED=false \
  quay.io/keycloak/keycloak:26.6.4 start

podman generate systemd --name keycloak-external --restart-policy=always \
  > /etc/systemd/system/container-keycloak-external.service
systemctl daemon-reload && systemctl enable container-keycloak-external.service
```

`start` (production mode) is deliberate — HTTPS issuer URLs require it. The
H2 database lives in the bind-mounted `/root/kc-data`; on a clean install,
seed the realm per §5.3; when migrating an instance that already holds
users, see B.2.3 (copy the H2 files in and delete stale `*.lock.db`, or
import a realm export). If the container starts and dies with
`AccessDeniedException` on the key file, the chown/chmod above did not
happen.

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
  dnsNames:
    - localhost
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

auto.create.topics.enable=true
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

# helper for the Kafka CLI tools (as admin, host network)
IMAGE=docker.io/apache/kafka:4.2.0
krun() { # krun <tool> [args...]
  local tool=$1; shift
  podman run --rm --security-opt label=disable --user 0:0 --network host \
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
for i in $(seq 1 60); do
  krun kafka-broker-api-versions.sh >/dev/null 2>&1 && break
  sleep 5
done

krun kafka-configs.sh --alter --entity-type users --entity-name fulfillment-service \
  --add-config "SCRAM-SHA-512=[password=${KAFKA_FULFILLMENT_PASSWORD}]"
krun kafka-configs.sh --alter --entity-type users --entity-name osac-metering \
  --add-config "SCRAM-SHA-512=[password=${KAFKA_METERING_PASSWORD}]"
```

**8. ACLs per the §6.1 contract:**

```bash
krun kafka-acls.sh --add --allow-principal User:fulfillment-service \
  --operation Create --operation Write --operation Read --operation Describe \
  --topic osac.events. --resource-pattern-type prefixed
krun kafka-acls.sh --add --allow-principal User:fulfillment-service \
  --operation Read --operation Describe \
  --group osac.system. --resource-pattern-type prefixed
krun kafka-acls.sh --add --allow-principal User:fulfillment-service \
  --operation IdempotentWrite --cluster
krun kafka-acls.sh --add --allow-principal User:osac-metering \
  --operation Write --operation Describe \
  --topic osac.metering. --resource-pattern-type prefixed
krun kafka-acls.sh --add --allow-principal User:osac-metering \
  --operation IdempotentWrite --cluster
```

**9. Pre-create the metering topics** (event topics auto-create):

```bash
mk_topic() { # name partitions retention_ms
  krun kafka-topics.sh --create --topic "$1" --partitions "$2" \
    --replication-factor 1 --config "retention.ms=$3" --config min.insync.replicas=1
}
mk_topic osac.metering.lifecycle   3 2592000000    # 30 days
mk_topic osac.metering.heartbeat   3 2592000000
mk_topic osac.metering.inference   3 2592000000
mk_topic osac.metering.corrections 3 2592000000
mk_topic osac.metering.dlq         6 7776000000    # 90 days
```

A full produce/consume self-test as `fulfillment-service` (auto-created
topic, consumer group, cleanup) is in the companion runbook; when running
it by hand remember the `-i` trap: piping into `kafka-console-producer`
through `podman run` without `-i` silently sends nothing.

---

## Appendix B: Migrating a bundled deployment to external services

The main body assumes a clean install: pattern-A values before the first
`helm install`, no bundled instance to remove, no data to move. This
appendix is the other starting point — OSAC is already installed and
running on the bundled services, and you are moving one or more of them
out. The values and objects are exactly the ones in §3–§6; what migration
adds is sequencing: quiesce the writers, move the data, cut over, verify,
and keep a rollback point at every step.

### B.1 Before any migration

- Record the Helm revision you can roll back to
  (`helm status <release> -n <ns> | awk '/^REVISION:/{print $2}'`).
- Snapshot the data you are about to move — the per-service backup
  procedures are in [B.3](#b3-backup-restore-and-rollback).
- Quiesce the writers: scale down the osac deployments, keeping a record
  of replica counts to restore.

> **Chart-version caveat:** installer chart revisions between roughly
> October 8 and October 14, 2026 predate the OAuth wiring that injects
> `fulfillment-controller-credentials` into the operator and metering
> deployments. On such pins, both crashloop after any database move until
> the credentials are patched in (env `OSAC_FULFILLMENT_CLIENT_ID` /
> `OSAC_FULFILLMENT_CLIENT_SECRET` + `--fulfillment-ca-file` for the
> operator; a credentials volume + issuer/client env for metering). The
> exact patch script is in the companion runbook; the durable fix is a
> newer chart pin.

### B.2 Per-service migration

#### B.2.1 PostgreSQL

Quiesce and move the data (the writers are the osac deployments — B.1):

```bash
export KUBECONFIG=<hub-kubeconfig>

# 1. quiesce the writers, recording replica counts for the scale-back
oc -n osac get deploy -o custom-columns=NAME:.metadata.name,REPLICAS:.spec.replicas \
  | awk 'NR>1 && $2>0 {print "oc -n osac scale deployment/"$1" --replicas=0"}' > quiesce.sh
bash quiesce.sh   # keep the file — it doubles as the undo record

# 2. dump everything from the bundled instance
oc -n osac-infra exec deploy/postgres -- pg_dumpall -U postgres > dumpall.sql

# 3. restore into the external instance
podman exec -i osac-postgres psql -U postgres < dumpall.sql

# 4. verify identical per-table row counts in osac_service and osac_metering
```

Then pick the integration pattern (§3.2). Pattern A (values) is preferred;
if the chart-consumed Secrets still point at the in-cluster DNS name,
pattern B keeps that name valid with zero in-cluster change:

```bash
oc -n osac-infra patch svc postgres --type=json -p '[{"op":"remove","path":"/spec/selector"}]'
oc -n osac-infra patch endpoints postgres --type=merge \
  -p '{"subsets":[{"addresses":[{"ip":"<host-ip>"}],"ports":[{"name":"postgres","port":5432,"protocol":"TCP"}]}]}'
oc -n osac-infra delete endpointslice -l kubernetes.io/service-name=postgres
```

The EndpointSlice delete forces the mirroring controller to rebuild the
slices from the new Endpoints (a stale slice from the pod era otherwise
keeps steering traffic — the #1 cause of "cutover did nothing" symptoms).
Then stop the bundled instance for good and bring the writers back:

```bash
helm upgrade osac-infra <infra-chart> -n osac-infra --reuse-values \
  --set bundledPostgres.enabled=false
bash quiesce-undo   # scale the writers back up (recorded above)
```

#### B.2.2 OpenBao

With file storage on both sides, move the data directory wholesale:

```bash
oc -n osac-infra scale deploy/openbao --replicas=0
oc -n osac-infra exec <openbao-pod> -- tar cf - -C <storage-path> . | tar xf - -C /root/bao-data
systemctl start container-osac-openbao.service
curl -sk https://<host-ip>:8200/v1/sys/health    # 200 = initialized, unsealed, active
```

The init state moves with the data, so the **unseal key, root token, `osac`
namespace, JWT auth config, lifecycle policy and role all carry over** — no
re-bootstrap. (Tarball the pre-migration state first as the rollback point.)

#### B.2.3 Keycloak

**Full export from the existing instance (keeps users with password
hashes, organizations, client fixups, and realm keys):**

```bash
oc -n keycloak exec <kc-pod> -- /opt/keycloak/bin/kc.sh export \
  --realm=osac --file=/tmp/osac-realm-export.json
oc -n keycloak exec <kc-pod> -- cat /tmp/osac-realm-export.json > osac-realm-export.json

KC="https://<host-ip>:8443"
TOKEN=$(curl -sk -d "client_id=admin-cli&username=admin&password=<kc-admin-password>&grant_type=password" \
  "${KC}/realms/master/protocol/openid-connect/token" \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])")
curl -sk -X POST -H "Authorization: Bearer ${TOKEN}" -H "Content-Type: application/json" \
  -d @osac-realm-export.json "${KC}/admin/realms"
```

`kc.sh export` does **not** carry LDAP federations or organization
memberships — replay them after import: create the LDAP user federation per
tenant directory, run sync, create the organizations (their names must
match the fulfillment tenants — the platform maps the token's
`organization` claim to a tenant by name), assign memberships, and grant
the tenant-admin role.

#### B.2.4 Kafka

Only after the external broker is verified. Order matters:

1. Back up all `kafka.strimzi.io` CRDs and objects (the operator's
   finalizers make un-backup'd recovery painful).
2. Delete the `KafkaNodePool`, then the `Kafka` CR **while the operator
   still runs** (it processes the finalizers).
3. Delete the broker PVCs (this is where the storage win is banked).
4. `helm upgrade osac-infra … --set kafka.enabled=false` **first** (the
   listener Certificate deletes while its namespace still lives), then
   `osac-deps … --set kafka.enabled=false` (OLM removes the CSV/operator;
   the `osac-kafka` namespace terminates).
5. Re-apply the CRDs if OLM garbage-collected them — the `osac` release
   renders `KafkaTopic`/`KafkaUser` kinds ungated, so its upgrades need the
   CRDs to exist.
6. Run the reapply script (§8) and re-verify the canary.

### B.3 Backup, restore, and rollback

The strategy table is §10; the procedures, restore paths, and rollback
matrix live here.

#### B.3.1 PostgreSQL backup and rollback

Nightly `pg_dumpall`, gzipped, online (no stop needed):

```bash
#!/usr/bin/env bash
# /usr/local/sbin/osac-pg-backup.sh — nightly full dump, keep 14
set -euo pipefail
DIR=/var/lib/osac/backups; mkdir -p "$DIR"
podman exec osac-postgres pg_dumpall -U postgres | gzip \
  > "$DIR/osac-pg-$(date +%Y%m%d-%H%M%S).sql.gz"
ls -1t "$DIR"/osac-pg-*.sql.gz | tail -n +15 | xargs -r rm -f
```

```cron
17 2 * * * root /usr/local/sbin/osac-pg-backup.sh
```

Restore: `gunzip -c <dump>.sql.gz | podman exec -i osac-postgres psql -U postgres`.

Rollback to the bundled instance: `helm upgrade osac-infra … --set
bundledPostgres.enabled=true` (the chart restores the deployment and the
Service selector), restore a dump into the restarted bundled instance,
restart the osac deployments. Anything written to the external instance
after cutover is lost unless re-dumped.

#### B.3.2 OpenBao backup and rollback

File-storage OpenBao has no snapshot API; back up the data directory with a
brief stop (a few seconds). The contents are encrypted at rest — protect the
backups like the unseal key.

```bash
#!/usr/bin/env bash
# /root/bao-backup/backup-openbao.sh — stop, tar, start, verify (keep 14)
set -euo pipefail
DIR=/root/bao-backup; STAMP=$(date +%Y%m%d-%H%M%S); mkdir -p "$DIR"
systemctl stop container-osac-openbao.service
tar -czf "$DIR/bao-data-$STAMP.tar.gz" -C /root bao-data
systemctl start container-osac-openbao.service
for i in $(seq 1 30); do
  sleep 2
  [ "$(curl -sk -o /dev/null -w '%{http_code}' https://<host-ip>:8200/v1/sys/health)" = 200 ] && break
done
curl -sk https://<host-ip>:8200/v1/sys/health | grep -q '"sealed":false'
echo "OK bao-data-$STAMP.tar.gz"
find "$DIR" -name 'bao-data-*.tar.gz' -mtime +14 -delete
```

Restore: `systemctl stop`, swap the data dir, `systemctl start` (the unseal
drop-in re-seals→unseals; the `:z` mounts relabel), health-check.

Rollback to the bundled instance: `--set bundledVault.enabled=true`, point
`service.vault.endpoint` back at the in-cluster URL, restore the data-dir
tarball if needed. Secrets written after cutover exist only in the external
data dir.

#### B.3.3 Keycloak backup and rollback

Partial exports (structure survives restarts better than full ones) plus a
data-dir tarball with a brief stop:

```bash
#!/usr/bin/env bash
# /root/kc-backup/backup-keycloak.sh — partial exports + data tar (keep 7/3)
set -euo pipefail
export PATH=/usr/sbin:/usr/sbin:$PATH     # cron PATH lacks iptables etc.
DIR=/root/kc-backup; STAMP=$(date +%Y%m%d-%H%M%S); mkdir -p "$DIR"
KC_PASS=$(podman inspect keycloak-external --format \
  '{{range .Config.Env}}{{println .}}{{end}}' | grep '^KEYCLOAK_ADMIN_PASSWORD=' | cut -d= -f2-)
KCMD() { podman exec keycloak-external /opt/keycloak/bin/kcadm.sh \
  config credentials --server http://localhost:8080 --realm master \
  --user admin --password "$KC_PASS" >/dev/null; }

for PART in "realms/osac/partialExport?exportClients=true&exportGroupsAndRoles=true:realm" \
            "realms/osac/users:users"; do :; done   # partialExport via kcadm, then:
#   kcadm.sh create realms/osac/partialExport ... > realm-$STAMP.json
#   repeat for users, orgs, org-members, LDAP components
podman stop keycloak-external
tar -czf "$DIR/kc-data-$STAMP.tar.gz" -C /root kc-data
podman start keycloak-external
# prune: keep newest 7 JSON exports, 3 data tars
```

(The companion runbook carries the complete working script; the sketch
above shows the shape: credential sourced at runtime, partial exports,
brief stop for the data tar, pruned retention. Restore scenarios —
container lost, data corrupted, total loss — re-import the JSON or untar
the data dir.)

Rollback to the bundled instance:

```bash
oc -n keycloak scale deploy keycloak-service --replicas=1
oc -n keycloak rollout status deploy/keycloak-service
oc -n keycloak patch svc keycloak --type=merge \
  -p '{"spec":{"selector":{"app":"keycloak-service"}}}'
oc -n keycloak patch svc keycloak --type=json \
  -p '[{"op":"replace","path":"/spec/ports/0/targetPort","value":8001}]'
oc -n keycloak delete endpoints keycloak
oc -n keycloak delete endpointslice -l kubernetes.io/service-name=keycloak
```

Users and orgs created after cutover exist only in the external H2 —
re-create or re-export them first.

#### B.3.4 The rollback matrix

| Service | Rollback | Data caveat |
|---|---|---|
| PostgreSQL | `bundledPostgres.enabled=true` (chart restores deploy + Service selector) + restore a dump | writes to the external instance since cutover are lost unless re-dumped |
| OpenBao | `bundledVault.enabled=true` + `service.vault.endpoint` back to in-cluster URL (+ data-dir restore) | secrets written since cutover exist only in the external data dir |
| Keycloak | restore the Service selector + targetPort, scale the bundled instance up (B.3.3) | users/orgs created since cutover exist only in the external H2 |
| Kafka | `helm rollback osac <pre-cutover-rev>` + `kafka.enabled=true` in deps/infra (Strimzi re-installs fresh) | events on the external broker are not migrated back |

General rule: the rollback point is a Helm revision — record it before any
cutover, and tarball any data directory before you move it.
