# OSAC-7866 — edge-16 final clean run: procedure, evidence, and results

Final validation runs of the gatewayapi ingress driver on the edge-16 SNO
(OCP 4.22.14, CIO-managed Gateway API). This file is the runbook AND the
evidence record: each phase lists the commands, what to expect, and the
captured output of the executed run.

Access from the laptop:

```
export KUBECONFIG=/home/obochan/.kube/edge16.kubeconfig
```

Worktree: /home/obochan/workspace/git/osac-workspace/osac-gw-install,
branch OSAC-7866/gateway-api-install.

Install command (Phase C):

```
cd osac-installer
AAP_LICENSE_FILE=values/vmaas-ci/license.zip make install-osac \
  PLATFORM=openshift PROFILE=vmaas-ci NS=osac INGRESS_DRIVER=gatewayapi \
  EXTRA_HELM_ARGS="--set-string service.httpCorsAllowedOrigins[0]=https://osac-ui-osac.apps.test-infra-cluster-edge16.redhat.com"
```

`INGRESS_DRIVER=gatewayapi` sets `service.ingress.driver` and launches
`scripts/gateway-bridge.sh` in the background around `helm --wait`.

## Background: why the bridge is installer-side

OpenShift's `network.openshift.io/RestrictedEndpointsAdmission` plugin
(restricted CIDRs: pod network 172.30.0.0/16, service network
10.128.0.0/14) rejects an EndpointSlice address inside those ranges for
non-privileged identities, on create AND update. A chart Job with a
ServiceAccount can therefore never set the bridge address. Every
earlier "green" run only worked because admin-applied slices already
existed, so the chart Job performed an allowed no-op patch. The
installer script applies the slices with the admin credentials — the
same model the POC cutover validated.

## Phase A — cleanup

| # | Command | What it removes |
|---|---------|-----------------|
| 1 | `make uninstall-osac PLATFORM=openshift PROFILE=vmaas-ci NS=osac` | The `osac` helm release and everything it owns, plus (since the bridge went installer-side) the bridge EndpointSlices |
| 2 | `oc -n osac delete pvc postgres-15-osac-aap-postgres-15-0` | The AAP postgres PVC. PVCs survive helm uninstall; stale data with a regenerated secret key caused the AAP `run-migrations` `InvalidToken` crash loop |
| 3 | `oc -n osac delete tenant --all --wait=false` then strip finalizers on any stuck tenant: `oc -n osac patch tenant <name> -p '{"metadata":{"finalizers":[]}}' --type=merge` | Tenant CRs carry operator finalizers that only the operator clears — and the operator is uninstalled first, so deletion hangs in Terminating forever |
| 4 | `oc -n osac delete job --all --wait=false` | Completed post-install hook Jobs with no `hook-delete-policy` survive the uninstall |

Kept on purpose: the `osac-infra` release and
`GatewayClass openshift-default`.

## Phase B — verify the system is clean

All checks use `oc` only (helm is not needed; on the edge-16 host use
the node hop below).

```
# 1. No osac helm release left (expect: no output)
oc -n osac get secrets -l owner=helm

# 2. Nothing left running or stored in the osac namespace
oc -n osac get pods,deploy,svc,pvc,sa               # expect: only the 3 default
                                                     # SAs (builder, default,
                                                     # deployer) or nothing
oc -n osac get endpointslices                       # expect: no resources
oc -n osac get tenant.osac.openshift.io             # expect: no resources found

# 3. The osac Gateway and its Service are gone from openshift-ingress
oc -n openshift-ingress get gateway                 # expect: no osac-gateway
oc -n openshift-ingress get svc | grep osac         # expect: no output

# 4. What SHOULD still be there (infra layer, intentionally kept)
oc get gatewayclass openshift-default               # expect: ACCEPTED True
oc -n osac-infra get secrets -l owner=helm          # expect: osac-infra.v1

# 5. Cluster still healthy
oc get clusterversion                               # 4.22.14 Available
```

From the edge-16 host (no oc installed there):

```
ssh -i /home/cluster-tool/flavors/sno-4-22/crypto/cluster-tool.key core@192.168.160.10 \
  "sudo oc --kubeconfig /etc/kubernetes/static-pod-resources/kube-apiserver-certs/secrets/node-kubeconfigs/lb-ext.kubeconfig \
   -n osac get pods,deploy,svc,pvc,sa"
```

## Phase C — final install

See the install command at the top. Expected flow: helm applies the
release; the bridge script logs `waiting for ready gateway Pod IPs`
until the CIO deploys the gateway Pods, then applies the slices; the
caller pods converge inside helm's 40m wait; helm reports
`STATUS: deployed`.

## Phase D — post-install verification

```
# 1. Bridge slices point at the CURRENT gateway Pod IP
oc -n openshift-ingress get endpointslices \
  -l kubernetes.io/service-name=osac-gateway-openshift-default \
  -o jsonpath='{.items[*].endpoints[*].addresses}'
oc -n osac get endpointslices | grep fulfillment

# 2. All pods Running
oc -n osac get pods | grep -viE "running|completed" # expect: header only

# 3. Release state
oc -n osac get secrets -l owner=helm                # osac.v1 present

# 4. External battery (laptop /etc/hosts already has the edge16 apps hosts)
EXT=fulfillment-api-osac.apps.test-infra-cluster-edge16.redhat.com
TOK=$(oc -n osac create token osac-sa --duration=10m)
curl -sk https://$EXT/.well-known/jwks.json -o /dev/null -w 'jwks %{http_code}\n'          # 200
curl -sk -H "authorization: Bearer $TOK" https://$EXT/api/fulfillment/v1/virtual_networks \
  -o /dev/null -w 'REST %{http_code}\n'                                                   # 200
curl -sk -H 'authorization: Bearer bad' https://$EXT/api/fulfillment/v1/virtual_networks \
  -o /dev/null -w 'bad-token %{http_code}\n'                                              # 401
curl -sk https://osac-ui-osac.apps.test-infra-cluster-edge16.redhat.com/ \
  -o /dev/null -w 'UI %{http_code}\n'                                                     # 200
```

Known non-blocker: REST may return 400 `tenant 'osac' doesn't exist` on
a fresh database until the tenant row is seeded (JIT user provisioning).
The response proves the full ingress path (gateway TLS, routing, auth,
backend); it does not fail this validation.

# RUN RECORDS

## Run 1 — 2026-11-22 ~13:20 UTC: installer-side bridge, first green

After moving the bridge to the installer (commit 3d16abceb), clean
greenfield install: release deployed, bridge applied mid-install
against gateway Pod IP 172.30.25.199 (a NEW pod — the script resolves
live state), all 13 deployments 1/1, jwks 200 / bad-token 401 / UI 200
/ Keycloak 302, REST reached the backend (400 tenant seeding,
non-blocker). No chart bridge Job in the cluster.

## Run 2 — 2026-11-22 (this run): full documented cycle

### Phase A evidence — cleanup

```
$ make uninstall-osac PLATFORM=openshift PROFILE=vmaas-ci NS=osac
release "osac" uninstalled
oc delete endpointslice -n osac fulfillment-api-gateway fulfillment-internal-api-gateway --ignore-not-found
endpointslice.discovery.k8s.io "fulfillment-api-gateway" deleted
endpointslice.discovery.k8s.io "fulfillment-internal-api-gateway" deleted

$ oc -n osac delete pvc postgres-15-osac-aap-postgres-15-0
persistentvolumeclaim "postgres-15-osac-aap-postgres-15-0" deleted

$ oc -n osac delete tenant --all --wait=false
tenant.osac.openshift.io "shared" deleted
tenant.osac.openshift.io "system" deleted

$ oc -n osac patch tenant shared -p '{"metadata":{"finalizers":[]}}' --type=merge
tenant.osac.openshift.io/shared patched
$ oc -n osac patch tenant system -p '{"metadata":{"finalizers":[]}}' --type=merge
tenant.osac.openshift.io/system patched

$ oc -n osac delete job --all --wait=false
job.batch "osac-aap-bootstrap" deleted
job.batch "osac-aap-create-token" deleted
```

### Phase B evidence — clean system

```
$ oc -n osac get secrets -l owner=helm
No resources found in osac namespace.

$ oc -n osac get pods,deploy,svc,pvc,sa
NAME                      AGE
serviceaccount/builder    3d3h
serviceaccount/default    3d3h
serviceaccount/deployer   3d3h

$ oc -n osac get endpointslices
No resources found in osac namespace.

$ oc -n osac get tenant.osac.openshift.io
No resources found in osac namespace.

$ oc -n openshift-ingress get gateway
No resources found in openshift-ingress namespace.

$ oc -n openshift-ingress get svc | grep osac
(no output)

$ oc get gatewayclass openshift-default
openshift-default   openshift.io/gateway-controller/v1   True       3d3h

$ oc -n osac-infra get secrets -l owner=helm
sh.helm.release.v1.osac-infra.v1   helm.sh/release.v1   1      3d3h

$ oc get clusterversion
version   4.22.14   True        False         63d     Cluster version is 4.22.14
```

All clean-slate expectations met; infra layer (GatewayClass, osac-infra
release) intentionally retained.

### Phase C evidence — install

(pending)

### Phase D evidence — verification battery

(pending)
