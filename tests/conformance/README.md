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

The image builds from the repository root with either runtime:

```sh
podman build -f tests/conformance/Containerfile -t osac-conformance:dev .
# or
docker build -f tests/conformance/Containerfile -t osac-conformance:dev .
```

The image runs as UID 1001. It reports `suiteImageDigest` by inspecting its own
container through a container runtime socket, so mount the socket of the
runtime that started the container, plus a results directory that UID can
write. Podman and Docker are both supported.

Podman is agentless and does not listen on a socket until its service is
started. For a rootless user:

```sh
systemctl --user start podman.socket
# or, without systemd: podman system service --time=0 &
mkdir -p results
podman run --rm --userns=keep-id:uid=1001,gid=1001 \
  -v "$XDG_RUNTIME_DIR/podman/podman.sock:/run/podman/podman.sock" \
  -v "$PWD/config:/config:ro" -v "$PWD/results:/results" \
  osac-conformance:dev run
```

With Docker:

```sh
mkdir -p results
docker run --rm --user 1001 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$PWD/config:/config:ro" -v "$PWD/results:/results" \
  osac-conformance:dev run
```

The socket must be readable by the image UID. For Docker, add
`--group-add "$(stat -c '%g' /var/run/docker.sock)"` (Linux) when the socket
is group-owned by `docker`.

Use `--runtime-socket PATH` when the socket lives somewhere else, or set
`OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST` to the image digest to skip the socket
lookup entirely.
