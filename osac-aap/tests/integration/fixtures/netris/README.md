# Netris fake for Kind/AWX tests

`fake_netris.py` is the single stateful Netris API fake used by the local
`netris_fabric` target and Kind/AWX profile tests. The local target starts it
with the default loopback listener. `kind_fixture.py` runs the same file inside
Kind behind a namespaced ClusterIP Service, so AWX job pods can reach it.

Use this only with an isolated Kind test cluster. The fake's admin API has no
authentication; the Service is cluster-internal, and the profile runner's
admin port-forward listens on `127.0.0.1` only.

## Run a profile command

From the OSAC repository root, point `KUBECONFIG` at the disposable Kind
cluster, then run the profile command through the fixture:

```bash
python3 osac-aap/tests/integration/fixtures/netris/kind_fixture.py run \
  --kubeconfig "$KUBECONFIG" \
  --namespace osac-tests \
  --image <AAP execution-environment image available to Kind> \
  -- <profile command>
```

The namespace must already exist. The helper creates a uniquely named
ConfigMap, Deployment, and ClusterIP Service, waits for readiness, and starts a
loopback-only `kubectl port-forward` for admin controls. It runs the supplied
command with these environment variables:

| Variable | Value |
|---|---|
| `NETRIS_CONTROLLER_URL` | Service DNS name reachable by AWX job pods |
| `NETRIS_USERNAME` | Per-run test username (`osac`) |
| `NETRIS_PASSWORD` | Random per-run test password |
| `NETRIS_FAKE_ADMIN_URL` | Loopback URL for fixture state and fault controls |
| `NETRIS_FAKE_NAME` | Kubernetes resource name for this run |
| `NETRIS_FAKE_NAMESPACE` | Namespace containing the fake |

Use `NETRIS_CONTROLLER_URL`, `NETRIS_USERNAME`, and `NETRIS_PASSWORD` with the
existing Netris networking instance-group settings. Store the password through
the profile's Secret path; do not print it or enable shell tracing around it.
See the [AAP Netris settings](../../../../collections/ansible_collections/osac/config_as_code/README.md).

The helper deletes the Deployment, Service, and ConfigMap after the supplied
command exits, including a non-zero exit. The fake's state lives in an
`emptyDir`, so deleting the Pod removes the state. If running the helper's
separate `start` command for manual work, pass the same `--name` and
`--namespace` to `stop` when finished.

## Control and inspect the fake

The profile command can use `NETRIS_FAKE_ADMIN_URL` to seed/reset state,
configure faults, and inspect provider requests. Available endpoints include:

- `GET /__admin/state` and `GET /__admin/calls`
- `POST /__admin/reset`, `/__admin/seed`, `/__admin/update`, `/__admin/remove`
- `POST /__admin/set`, `/__admin/faults`, `/__admin/faults/clear`

For example, a smoke command can read the recorded requests after an AWX job
finishes:

```bash
curl --fail --silent "$NETRIS_FAKE_ADMIN_URL/__admin/calls"
```

The profile owns AWX instance-group configuration and must run
`playbook_osac_network_task.yml` through a real AWX job. OSAC-6194 provides the
Netris service and controls; the all-service Kind/AWX profile and its smoke
evidence belong to OSAC-7925.
