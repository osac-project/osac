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

`osac-conformance:dev` is not a published image name. It is only the local tag
passed to `-t` in the build command below; use any tag you like. The release
image from the design is `ghcr.io/osac-project/osac-conformance:<release>`.
Personal builds can go to `quay.io/etabak/osac-conformance:<tag>`.

Build from the repository root:

```sh
podman build -f tests/conformance/Containerfile -t osac-conformance:dev .
# or
docker build -f tests/conformance/Containerfile -t osac-conformance:dev .
```

The image runs as UID 1001. The simplest run passes the image digest
explicitly, so no runtime socket is needed:

```sh
mkdir -p results
digest=$(podman image inspect --format '{{.Digest}}' osac-conformance:dev)

podman run --rm --userns=keep-id:uid=1001,gid=1001 \
  -e OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST="$digest" \
  -v "$PWD/config:/config:ro" -v "$PWD/results:/results" \
  osac-conformance:dev run
```

With rootful podman, make the results directory writable by the image UID and
drop `--userns`:

```sh
mkdir -p results && chown -R 1001:0 results
digest=$(podman image inspect --format '{{.Digest}}' osac-conformance:dev)
podman run --rm -e OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST="$digest" \
  -v "$PWD/config:/config:ro" -v "$PWD/results:/results" \
  osac-conformance:dev run
```

For Docker, resolve the digest with
`docker image inspect --format '{{index .RepoDigests 0}}' <image>` and pass the
part after `@`.

On an SELinux host, append `:Z` to the mounts. When the suite runs the same
image from a registry, use that reference instead:

```sh
podman run --rm --userns=keep-id:uid=1001,gid=1001 \
  -e OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST="$digest" \
  -v "$PWD/config:/config:ro" -v "$PWD/results:/results" \
  quay.io/etabak/osac-conformance:latest run
```

Without `OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST`, the suite inspects its own
container through a Podman or Docker unix socket. It discovers
`CONTAINER_HOST`, `DOCKER_HOST` and `XDG_RUNTIME_DIR`, then
`/run/podman/podman.sock` and `/var/run/docker.sock`; `--runtime-socket PATH`
overrides the list. Podman is agentless and does not listen until its service
is started:

```sh
systemctl --user start podman.socket
# or, without systemd: podman system service --time=0 &

podman run --rm --userns=keep-id:uid=1001,gid=1001 \
  -v "$XDG_RUNTIME_DIR/podman/podman.sock:/run/podman/podman.sock" \
  -v "$PWD/config:/config:ro" -v "$PWD/results:/results" \
  osac-conformance:dev run
```

With Docker:

```sh
docker run --rm --user 1001 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$PWD/config:/config:ro" -v "$PWD/results:/results" \
  osac-conformance:dev run
```

The socket must be readable by the image UID. For Docker, add
`--group-add "$(stat -c '%g' /var/run/docker.sock)"` (Linux) when the socket
is group-owned by `docker`. When no socket is reachable and the digest variable
is unset, `suiteImageDigest` is empty and the run continues.

## Results

The results directory holds `conformance-plan.yaml`, `conformance-report.yaml`,
`junit.xml`, `conformance.log`, per-test evidence under `tests/`, and the
redacted `diagnostics/` bundle.

The report has these top-level keys:

```text
osacRelease, suiteImageDigest, selectors, profiles, tests, checks,
leftovers, complete, result
```

`selectors`, `profiles` and `tests` describe the run's selection, not the whole
catalog. `profiles[]` contains only the profiles the run selected, plus
`generic`, which always applies; a selected profile with no planned tests is
reported `required: false` and `skip`.
