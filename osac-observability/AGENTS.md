# osac-observability

Shared metrics store and query tier: Thanos Receive, Store Gateway, Compactor
and Query, the admin query Routes, and the helper image that configures the
management cluster.

## Boundaries

- The umbrella chart in `osac-installer` owns the single switch
  `global.observability.enabled`, the cross-field validation, the pre-install
  prerequisite gate, and the post-install remote-write Job.
- This component's chart reads only `global.observability.*`. It never creates
  the `osac-observability` Namespace; `osac-installer` renders and labels it.
- Object storage is referenced by Secret name only. The chart renders no Secret
  and no key material. The credential Secret must expose `AWS_ACCESS_KEY_ID`
  and `AWS_SECRET_ACCESS_KEY` so Thanos can use the AWS credential chain.
- Do not hand-edit `charts/osac-observability/charts/` or any vendored chart:
  run the owning generator instead.

## Pod label contract

NetworkPolicies select pods that this story and later stories render. Every pod
must carry the labels its policy names, or the policy selects nothing and fails
open:

| Pod | Labels |
|---|---|
| store pods | `app.kubernetes.io/name: thanos`, `app.kubernetes.io/component: receive\|store\|query\|compactor` |
| admin authentication proxies | `osac.openshift.io/observability-role: query-auth-proxy` |
| tenant authentication proxy | `osac.openshift.io/observability-role: tenant-auth-proxy` |
| write proxy | `osac.openshift.io/observability-role: write-proxy` |
| authorizer | `osac.openshift.io/observability-role: authorizer` |

## Validation

```bash
make test          # helm lint, helm unittest, render checks
go test ./... -race
```

From the repository root, `make -C osac-installer helm-validate` also renders
this chart through the umbrella values and schema.
