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
