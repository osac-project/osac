# OSAC-7866 — edge-16 final clean run: procedure and verification

Final validation run of the gatewayapi ingress driver on the edge-16 SNO
(OCP 4.22.14), after all chart fixes are committed. Run the phases in
order; do not start Phase C until Phase B checks pass.

Access from the laptop:

```
export KUBECONFIG=/home/obochan/.kube/edge16.kubeconfig
```

Worktree: /home/obochan/workspace/git/osac-workspace/osac-gw-install,
branch OSAC-7866/gateway-api-install.

## Phase A — cleanup

| # | Command | What it removes |
|---|---------|-----------------|
| 1 | `make uninstall-osac PLATFORM=openshift PROFILE=vmaas-ci NS=osac` | The `osac` helm release and everything it owns: deployments, pods, Services, the bridge SA/RBAC/Job, EndpointSlices, Secrets, hook resources |
| 2 | `oc -n osac delete pvc postgres-15-osac-aap-postgres-15-0` | The AAP postgres PVC. PVCs survive helm uninstall; stale data with a regenerated secret key caused the AAP `run-migrations` `InvalidToken` crash loop on 2026-11-22 |
| 3 | `oc -n osac delete tenant.osac.openshift.io osac` | Hand-created Tenant CR from debugging the REST 400 (not chart-owned) |
| 4 | `oc -n osac delete endpointslice fulfillment-api-gateway fulfillment-internal-api-gateway` (only if any survive step 1) | No stale bridge pointing at an old gateway Pod IP |

Kept on purpose: the `osac-infra` release and
`GatewayClass openshift-default`. install-infra stays green; no reason
to redo it.

## Phase B — verify the system is clean

```
# 1. No helm release left in osac
helm -n osac list                                   # expect: empty list

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
helm -n osac-infra list                             # expect: osac-infra deployed

# 5. Cluster still healthy
oc get clusterversion                               # 4.22.14 Available
```

## Phase C — final install

```
cd osac-installer
AAP_LICENSE_FILE=values/vmaas-ci/license.zip make install-osac \
  PLATFORM=openshift PROFILE=vmaas-ci NS=osac \
  EXTRA_HELM_ARGS="--set service.ingress.driver=gatewayapi \
    --set-string service.httpCorsAllowedOrigins[0]=https://osac-ui-osac.apps.test-infra-cluster-edge16.redhat.com"
```

Note: this must be a fresh install, not an in-place upgrade. The
pre-cleanup release stored `bridge: service` in its values (dead key,
removed in 336bcbbc9); the new schema rejects it, so upgrading the old
release would fail on values validation.

## Phase D — post-install verification, in order

```
# 1. Bridge: chart-resource Job completes (TTL-deletes itself ~5 min later)
oc -n osac get job osac-gateway-bridge              # Complete 1/1

# 2. Bridge slices point at the CURRENT gateway Pod IP
oc -n openshift-ingress get endpointslices \
  -l kubernetes.io/service-name=osac-gateway-openshift-default \
  -o jsonpath='{.items[*].endpoints[*].addresses}'
oc -n osac get endpointslices | grep fulfillment

# 3. All pods Running
oc -n osac get pods | grep -viE "running|completed" # expect: header only

# 4. Release state
helm -n osac list                                   # osac deployed

# 5. External battery (laptop /etc/hosts already has the edge16 apps hosts)
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
That is an application-layer bootstrap matter, not an ingress issue;
it does not fail this validation.

## Reference

- Full troubleshooting log: `gateway-api-edge16-troubleshooting.md`
- Chart spec: `gateway-api-chart-implementation-spec.md`
- POC cutover constraint (Pod IPs, not ClusterIP):
  `osac/presentations/gateway-api-cutover-steps.md` line 203
