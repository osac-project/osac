# OSAC-7866 gatewayapi install: edge-16 troubleshooting log

Greenfield install of OSAC with the Gateway API ingress driver on the
edge-16 SNO (OCP 4.22.14, CIO-managed Gateway API). This log covers the
failure investigation and fixes found while validating the chart on a
live cluster. Date: 2026-11-19 through 2026-11-22.

Cluster access from the laptop:

```
export KUBECONFIG=/home/obochan/.kube/edge16.kubeconfig
```

## Symptom

Four pods crash-looping in the osac namespace since the first install:

```
$ oc -n osac get pods | grep -viE "running|completed"
fulfillment-rest-gateway   0/1   CrashLoopBackOff   1142
osac-aap-gateway           0/2   Init:CrashLoopBackOff   749
osac-operator              0/1   CrashLoopBackOff   743
osac-ui                    0/1   CrashLoopBackOff   749
```

osac-operator logs:

```
ERROR  setup  unable to verify installation setting
{"error": "read installation networking setting: gRPC status Unavailable"}
```

## Step 1: what is the operator failing to reach

In gatewayapi mode `fulfillment-api` and `fulfillment-internal-api` are
rendered selectorless and bridged to the gateway Service via
EndpointSlices. Checking whether the bridge exists:

```
$ oc -n osac get svc fulfillment-api fulfillment-internal-api -o wide
NAME                       TYPE        CLUSTER-IP      ... SELECTOR
fulfillment-api            ClusterIP   10.131.153.234  ... <none>
fulfillment-internal-api   ClusterIP   10.128.207.248  ... <none>

$ oc -n osac get endpointslices -l kubernetes.io/service-name=fulfillment-api
(no output)
```

No EndpointSlices -> the Services have no endpoints -> every in-cluster
gRPC caller fails. This is the direct cause of the crash loops.

## Step 2: who deleted the EndpointSlices

Audit logs on the SNO node rotate every ~20 minutes (crash-loop churn
accelerates rotation), so the deletion trail from Nov 19-20 was gone:

```
$ sudo ls -ltr /var/log/kube-apiserver/ | head
-rw-------. 1 root root 209713804 Nov 22 03:51 audit-2026-11-22T03-51-05.949.log
...

$ sudo grep -h endpointslices /var/log/kube-apiserver/audit*.log | grep -c DELETE
0
```

Cluster events expire within an hour; journald and shell histories had
nothing. Reconstructing from the session transcripts instead gave the
timeline:

1. Nov 19 14:42 UTC - first install times out at helm's 40m `--wait`;
   the post-install bridge hook never ran; EndpointSlices were applied
   by hand to unblock; the stack came up green.
2. Nov 20 12:32 UTC - the hand-applied slices were deliberately deleted
   (`oc delete endpointslice fulfillment-api-gateway
   fulfillment-internal-api-gateway`) for a clean reinstall with the
   fixed chart. That retry failed and was interrupted on Nov 21.

So nothing deleted the slices on its own; they were removed for the
retry that then failed to recreate them.

## Step 3: why the fixed chart could not recreate them - the missing SA

The Nov 20 retry failed with the bridge Job unable to start:

```
serviceaccount "osac-gateway-bridge" not found
```

The Nov 20 session checked the audit logs and found helm had never sent
a create for the SA, even though the ConfigMap, both Roles and both
RoleBindings from the same template file were created.

Reproduced locally by rendering the umbrella chart:

```
$ helm template osac charts/osac -n osac -f values/vmaas-ci/instance.yaml \
    --set service.ingress.driver=gatewayapi \
    --set service.externalHostname=... --set service.internalHostname=... \
    --set global.clusterDomain=... --set ui.externalHostname=... \
    --set ui.auth.oidcIssuerURL=... > /tmp/render.yaml

$ python3 - <<'EOF'
import yaml
docs = [d for d in yaml.safe_load_all(open('/tmp/render.yaml')) if d]
sa = [d for d in docs if d.get('kind') == 'ServiceAccount'
      and d['metadata']['name'] == 'osac-gateway-bridge']
print('gateway-bridge SA docs:', len(sa))
EOF
gateway-bridge SA docs: 0
```

The rendered text around the SA showed the document separator glued to
the last label line:

```
    app.kubernetes.io/managed-by: Helm---
apiVersion: rbac.authorization.k8s.io/v1
```

Cause: in `osac-installer/charts/osac/templates/gateway-bridge.yaml` the
separator sat on the same template line as the labels include:

```yaml
    {{- include "osac.labels" . | nindent 4 }}---
```

`osac.labels` emits no trailing newline, so the `---` rendered glued to
the label value. Helm splits rendered output into documents only on
`---` at line start, so the ServiceAccount and the following Role merged
into one document; the YAML-to-JSON conversion overwrites duplicate
keys, so the merged document parsed as a Role. The SA silently vanished
at parse time. Fix: put the `---` on its own line.

## Step 4: clean reinstall - the bridge Job waits forever

After the SA fix, a clean reinstall (`make uninstall-osac` then
`make install-osac`) still failed. Events:

```
$ oc -n osac get events --sort-by=.lastTimestamp | grep -iE "bridge|job"
33m  Normal  SuccessfulCreate  job/osac-gateway-bridge  Created pod: osac-gateway-bridge-hx7bj
18m  Normal  Started           pod/osac-gateway-bridge-hx7bj  Container started
12m  Warning DeadlineExceeded  job/osac-gateway-bridge  Job was active longer than specified deadline
```

The container took ~15 minutes to start (node busy with crash-loop
churn), and the Job's `activeDeadlineSeconds: 1200` then killed the
script before it finished. The audit log shows what the script did:

```
$ sudo grep -h osac-gateway-bridge <current audit files> | \
    python3 -c '...filter by SA username, print verb/resource/code...'
2026-11-22T08:04:21Z  get  services  osac-gateway-openshift-default  404
2026-11-22T08:04:32Z  get  services  osac-gateway-openshift-default  200
2026-11-22T08:04:43Z  get  services  osac-gateway-openshift-default  200
...every 11 seconds until the deadline...
```

The Service existed (200) from 08:04:32 onward, but the script never
left its wait loop. The exit condition was broken. Testing the exact
jsonpath from the script:

```
$ oc -n openshift-ingress get svc osac-gateway-openshift-default \
    -o jsonpath='{.spec.clusterIPs\[0\]}'
(empty)

$ oc -n openshift-ingress get svc osac-gateway-openshift-default \
    -o jsonpath='{.spec.clusterIPs[0]}'
10.130.43.156

$ oc -n openshift-ingress get svc osac-gateway-openshift-default \
    -o jsonpath='{.spec.clusterIP}'
10.130.43.156
```

The escaped brackets in `{.spec.clusterIPs\[0\]}` match nothing, so the
wait loop polled forever. This path had never run before: the original
hook version never executed (see the OSAC-7866 deadlock fix), and the
Nov 19 manual unblock applied the slices directly. Fix: use
`{.spec.clusterIP}` (no array indexing, no escaping).

Also bumped `activeDeadlineSeconds` from 1200 to 1800: on a node busy
with crash-looping pods the container alone took ~15 of the 20 minutes.

## Step 5: clean reinstall, second try - admission rejects the address

With the wait loop fixed, the Job ran and reached the EndpointSlice
create, which the API server rejected:

```
2026-11-22T09:00:48Z  get     endpointslices  fulfillment-api-gateway  404
2026-11-22T09:00:48Z  create  endpointslices  fulfillment-api-gateway  403
```

Full audit event:

```
"message": "endpointslices.discovery.k8s.io \"fulfillment-api-gateway\" is
           forbidden: endpoint address 10.130.43.156 is not allowed",
"annotations": {
  "authorization.k8s.io/decision": "allow",
  "authorization.k8s.io/reason": "RBAC: allowed by RoleBinding ... to
                                  ServiceAccount osac-gateway-bridge"
}
```

Ruled out:

- RBAC - the authorization decision is `allow`; `oc auth can-i` as the
  SA returns `yes`
- the address value as such - kubeadmin can create the same slice with
  a service ClusterIP, a gateway Pod IP and a node IP
- ValidatingAdmissionPolicies - no binding matches endpointslices
- ValidatingWebhooks - no webhook matches endpointslices

The POC cutover docs had already recorded the constraint
(`osac/presentations/gateway-api-cutover-steps.md` line 203):

> Destination = gateway **Pod IPs** (an EndpointSlice address may NOT be
> another Service's ClusterIP).

The POC bridge pointed at the gateway Pod IP and was applied by an
admin, so the restriction never fired. The chart implementation chose
the ClusterIP for its stability across gateway Pod replacement - a
stability the API server does not grant to a non-privileged identity.

Fix: `gateway-bridge.sh` resolves the ready gateway Pod IPs from the
gateway Service's own EndpointSlices (jsonpath over
`endpoints[?(@.conditions.ready)].addresses`) and bridges to those; the
Role in the gateway namespace gains `get,list` on endpointslices. Known
limitation, same as the POC: the slices go stale when gateway Pods are
replaced until the next upgrade re-runs the Job.

## Step 6: the clean install exposes the real blocker - admission

After the pod-IP fix, a clean reinstall still failed: the bridge Job's
EndpointSlice create was rejected with 403 while RBAC allowed it:

```
"message": "endpointslices.discovery.k8s.io \"fulfillment-api-gateway\" is
           forbidden: endpoint address 172.30.25.179 is not allowed",
"annotations": {
  "authorization.k8s.io/decision": "allow",
  "authorization.k8s.io/reason": "RBAC: allowed by RoleBinding ... to
                                  ServiceAccount osac-gateway-bridge"
}
```

The enforcer is the kube-apiserver's built-in
`network.openshift.io/RestrictedEndpointsAdmission` plugin:

```
$ oc get kubeapiserver cluster -o yaml | grep -A8 RestrictedEndpointsAdmission
  network.openshift.io/RestrictedEndpointsAdmission:
    configuration:
      restrictedCIDRs:
      - 172.30.0.0/16     (pod network)
      - 10.128.0.0/14     (service network)
```

It rejects an EndpointSlice address anywhere in the pod or service
network for non-privileged identities - ClusterIP and Pod IP alike,
which rules out every address a bridge could use.

Why every earlier run looked green (the audit trail across the day):

```
09:00  v2 chart Job   create (ClusterIP)   403
09:29  admin (manual) create (Pod IP)      created
10:14  v3 chart Job   patch (no-op, slices pre-existed)  200
11:55  clean-run Job  create (Pod IP)      403
```

The v3 "green" was an illusion: the Job only ever no-op patched slices
an admin had applied minutes earlier. Live experiments confirmed the
rule: an SA patch with no address change is allowed; an SA patch that
changes the address is denied with the same message. Granting the SA
`endpoints/restricted` and `endpointslices/restricted` did not help.

Cross-check: the dev cluster where the POC cutover ran has the
identical plugin config - the cutover worked because its slices were
applied by kubeadmin, so the plugin never evaluated a non-admin. The
POC steps doc had left "WHO updates these" as an open question; the
chart commit answered it with an unvalidated Job+SA assumption.

## Final fix - the bridge moves to the installer

The bridge cannot live in the chart at all. It is now
`osac-installer/scripts/gateway-bridge.sh`, run by
`make install-osac INGRESS_DRIVER=gatewayapi` with the installer's
admin credentials, in the background around `helm --wait` (it waits
for the ready gateway Pod IPs, so callers converge inside the wait
window). `make uninstall-osac` deletes the bridge slices explicitly
since they are no longer helm-owned. The chart drops the bridge
Job/SA/RBAC/ConfigMap.

Validated with two clean greenfield runs (2026-11-22), both fully
documented with per-phase evidence in
`gateway-api-edge16-final-run-procedure.md`.

## Fix ledger (branch OSAC-7866/gateway-api-install)

1. `de224c671` - glued `---` separator swallowed the bridge SA at
   render time.
2. `b87ff0e19` - BackendTLSPolicy v1, bridge as chart resource,
   Bundle from ClusterIssuer Secret (found on the Nov 19 install).
3. `6c5b74479` - jsonpath `{.spec.clusterIPs\[0\]}` matched nothing
   (wait loop never exited); deadline 1200 -> 1800; pod-IP bridge
   target (later superseded by 4).
4. `336bcbbc9` - dead `bridge` value removed from values and schemas.
5. `3d16abceb` - the real fix: bridge moved out of the chart to the
   installer script; `INGRESS_DRIVER` wiring; uninstall cleanup.
