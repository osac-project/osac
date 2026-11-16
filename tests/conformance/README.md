# OSAC conformance suite core

The suite entry point is `osac-conformance`. It reads the run configuration,
expands the selected profiles from the shipped catalog into selectors, computes
a plan, runs it through the test tree's pytest adapter, applies the named
checks, and writes `conformance-report.yaml` and `junit.xml` to the results
directory.

The catalog ships with the image and is not operator-editable:

```yaml
# catalog.yaml
selectors:
  - id: netris
    type: marker
    markers:
      - conformance_netris
profiles:
  - id: netris
    selectors:
      - netris
```

The operator writes the run configuration:

```yaml
# /config/conformance.yaml
osacRelease: "0.0.1"
profiles: [netris]
target:
  namespace: osac-e2e-ci
  kubeconfig: /config/hub.kubeconfig
  apiURL: https://fulfillment-internal-api-osac-e2e-ci.example
  tokenFile: /config/admin-token
  caFile: /config/api-ca.pem
```

Local commands:

```sh
python3 -m tests.conformance list
python3 -m tests.conformance run
python3 -m tests.conformance run --plan /results/conformance-plan.yaml
```

The image builds from the repository root:

```sh
podman build -f tests/conformance/Containerfile -t osac-conformance:dev .
```

The image runs as UID 1001. The suite reads the runner's podman socket, so map
the invoking user onto that UID and mount a results directory it can write:

```sh
mkdir -p results
podman run --rm --userns=keep-id:uid=1001,gid=1001 \
  -v "$XDG_RUNTIME_DIR/podman/podman.sock:/run/podman/podman.sock" \
  -v "$PWD/config:/config:ro" -v "$PWD/results:/results" \
  osac-conformance:dev run
```
