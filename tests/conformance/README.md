# OSAC conformance suite core

The suite entry point is `osac-conformance`. It reads the run configuration,
expands the selected profiles from the shipped catalog into selectors, computes
a plan, runs it through the test tree's pytest adapter, applies the named
checks, and writes `conformance-report.yaml` and `junit.xml` to the results
directory.

```text
profiles -> selectors -> tests -> results -> report
```

## Marking a test

Selection is declared with pytest markers. A test joins a profile by carrying
the marker named by one of that profile's selectors. The classification marker
says whether a failure can fail a check.

| Marker | Meaning |
|---|---|
| `conformance_generic` | selected by the `generic` selector, which always applies |
| `conformance_vmaas` | selected by the `vmaas` selector (`vm` profile) |
| `conformance_bmaas` | selected by the `bmaas` selector (`baremetal` profile) |
| `conformance_caas` | selected by the `caas` selector (`cluster` profile) |
| `conformance_netris` | selected by the `netris` selector (`netris` profile) |
| `conformance_blocking` | a failure fails the blocking check; this is the default |
| `conformance_informing` | a failure is reported in `tests[]` but never fails a check |

An informing settings check:

```python
import pytest

@pytest.mark.conformance_netris
@pytest.mark.conformance_informing
def test_netris_settings_are_present() -> None:
    ...
```

A blocking test that counts toward both `generic` and `vm`:

```python
@pytest.mark.conformance_generic
@pytest.mark.conformance_vmaas
@pytest.mark.conformance_blocking
def test_vm_lifecycle() -> None:
    ...
```

- A test with no classification marker is blocking.
- A test with several selector markers counts toward every selected profile
  whose selectors match it.
- Markers are registered in `pyproject.toml`. An unregistered marker only
  produces a `PytestUnknownMarkWarning` in the child run.
- Test IDs in the plan and the report are the canonical pytest node IDs, for
  example `networking/test_netris_settings.py::test_netris_settings_are_present`.

## Catalog

The catalog ships with the image and is not operator-editable. The operator
cannot add, change or remove selectors or profiles:

```yaml
# catalog.yaml
selectors:
  - id: generic
    type: marker
    markers: [conformance_generic]
  - id: vmaas
    type: marker
    markers: [conformance_vmaas]
  - id: bmaas
    type: marker
    markers: [conformance_bmaas]
  - id: caas
    type: marker
    markers: [conformance_caas]
  - id: netris
    type: marker
    markers: [conformance_netris]

profiles:
  - id: generic
    selectors: [generic]
  - id: vm
    selectors: [vmaas]
  - id: baremetal
    selectors: [bmaas]
  - id: cluster
    selectors: [caas]
  - id: netris
    selectors: [netris]
```

`generic` always applies, even when the configuration does not name it. The
plan is the deduplicated union of the selected profiles' selectors.

## Run configuration

```yaml
# /config/conformance.yaml
osacRelease: "0.0.1"
profiles: [netris]
target:
  namespace: osac-e2e-ci                     # defaults to osac
  kubeconfig: /config/hub.kubeconfig         # target installation kubeconfig
  apiURL: https://fulfillment-internal-api-osac-e2e-ci.example
  tokenFile: /config/admin-token             # bearer token for apiURL
  caFile: /config/api-ca.pem                 # CA that signed apiURL's certificate
  # Optional: read job output from AAP when a profile fails.
  # aap:
  #   url: https://aap.example
  #   tokenFile: /config/aap-token
timeouts: {}                                 # optional duration overrides, e.g. {delete: 30m}
```

`tokenFile` and `caFile` are paths inside the container, not values, so the
credentials stay out of the configuration file.

## Commands

```sh
python3 -m tests.conformance list
python3 -m tests.conformance run
python3 -m tests.conformance run --plan /results/conformance-plan.yaml
```

`list` computes the plan and writes it to the results directory. `run` executes
a plan, computing one first when `--plan` is not given. Both exit 2 on a
configuration error before any test runs.

## Running the image

The operator pulls the suite image; it does not build it. The image reference is
`ghcr.io/osac-project/osac-conformance:<release>`, and the release matches the
`osacRelease` in the configuration.

### Step 1: Create the run directory

```sh
mkdir -p conformance-run/config conformance-run/results
cd conformance-run
```

### Step 2: Create the configuration

Write `config/conformance.yaml`:

```yaml
osacRelease: "0.0.1"
profiles: [netris]
target:
  namespace: osac-e2e-ci
  kubeconfig: /config/hub.kubeconfig
  apiURL: https://fulfillment-internal-api-osac-e2e-ci.example
  tokenFile: /config/admin-token
  caFile: /config/api-ca.pem
```

The three target paths are container paths. Put the files they name under
`config/`:

```sh
cp ~/.kube/config config/hub.kubeconfig
kubectl create token <service-account> -n osac-e2e-ci --duration=12h > config/admin-token
cp <ca-bundle>.pem config/api-ca.pem
```

### Step 3: Pull and run the image

```sh
IMAGE=ghcr.io/osac-project/osac-conformance:0.0.1
podman pull "$IMAGE"
digest=$(podman image inspect --format '{{.Digest}}' "$IMAGE")

podman run --rm --userns=keep-id:uid=1001,gid=1001 \
  -e OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST="$digest" \
  -v "$PWD/config:/config:ro" -v "$PWD/results:/results" \
  "$IMAGE" run
```

Passing the digest explicitly means no container runtime socket is needed.

Notes:

- Rootful podman: drop `--userns=keep-id:uid=1001,gid=1001` and run
  `chown -R 1001:0 results` first.
- SELinux: append `:Z` to both `-v` mounts.
- Docker: `docker pull "$IMAGE"` and resolve the digest with
  `docker image inspect --format '{{index .RepoDigests 0}}' "$IMAGE"`, passing
  the part after `@`.

### Step 4: Read the results

```sh
cat results/conformance-report.yaml
cat results/junit.xml
less results/conformance.log
```

- `conformance-plan.yaml`: the tests the run selected.
- `conformance-report.yaml`: verdict, checks and per-test results.
- `junit.xml`: the same results in CI format.
- `conformance.log`: the timestamped live log.
- `tests/`: per-test evidence.
- `diagnostics/`: the redacted diagnostics bundle.

Exit codes: `0` conformant, `1` in progress, `2` configuration error before any
test ran.

### Building from source (developers only)

Operators do not build the image. To exercise a local change, build and tag it
with `-t <name>:<tag>`, then use that reference as `IMAGE` in Step 3 without
pulling:

```sh
podman build -f tests/conformance/Containerfile -t osac-conformance:dev .
IMAGE=osac-conformance:dev
```

## Report shape

The report has these top-level keys:

```text
osacRelease, suiteImageDigest, selectors, profiles, tests, checks,
leftovers, complete, result
```

`selectors`, `profiles` and `tests` describe the run's selection, not the whole
catalog. `profiles[]` contains only the profiles the run selected, plus
`generic`, which always applies; a selected profile with no planned tests is
reported `required: false` and `skip`.
