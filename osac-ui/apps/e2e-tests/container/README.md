# UI E2E runner image

[`Containerfile`](Containerfile) builds an image that runs the OSAC UI
Playwright flows against a deployed cluster. It wraps the two existing
scripts:

- [`prepare-setup-for-ui-run.sh`](../run_scripts/prepare-setup-for-ui-run.sh)
  runs at build time. It installs the workspace dependencies and the browser's
  system dependencies, and checks that Chromium launches.
- [`run-ui-e2e.sh`](../run_scripts/run-ui-e2e.sh) is the run-time command.
  Everything after the image name is passed to it, so its flow tags and
  options work unchanged.

The image is based on UBI 9 with Node.js
(`registry.access.redhat.com/ubi9/nodejs-22`) and adds pnpm, Chromium and its
system libraries, `oc`, and the `osac` CLI. UBI 9 repositories have no Xvfb,
so the image runs headless only; `--headed` stops with `Xvfb is missing`. The
host needs only podman,
a kubeconfig that can read the `osac` namespace, and network access to the
OSAC UI and Fulfillment API routes.

## Build

Build from the repository root. The flows are copied into the image, so build
from the commit you want to test.

The `osac` CLI is copied from a fulfillment-service image. Use the image the
cluster runs; the flows create and delete resources with the CLI, so it must
match the deployed service. To find the image the cluster runs:

```bash
FULFILLMENT_SERVICE_IMAGE=$(oc get pods \
  -n osac \
  -l app=fulfillment-grpc-server \
  -o jsonpath='{.items[0].status.containerStatuses[?(@.name=="grpc-server")].imageID}')
```

Then build the image:

```bash
podman build \
  -f osac-ui/apps/e2e-tests/container/Containerfile \
  --build-arg FULFILLMENT_SERVICE_IMAGE="${FULFILLMENT_SERVICE_IMAGE}" \
  -t osac-ui-e2e \
  .
```

| Build argument              | Default                                           | Purpose                                                                   |
| --------------------------- | ------------------------------------------------- | ------------------------------------------------------------------------- |
| `FULFILLMENT_SERVICE_IMAGE` | `ghcr.io/osac-project/fulfillment-service:latest` | Image the `osac` CLI is copied from                                       |
| `OC_VERSION`                | `4.22.22`                                         | `oc` client version                                                       |
| `PNPM_RELEASE`              | `9.15.9`                                          | pnpm version; keep it equal to `packageManager` in `osac-ui/package.json` |

The build installs RPMs from the UBI 9 repositories, npm packages, the
Playwright Chromium build, and the `oc` client, so it needs internet access. It
does not need a Red Hat subscription. The local `node_modules`, Playwright
login state, results, and `scratch/` specs are excluded from the build context
by the repository `.containerignore`.

## Run

Put the Keycloak admin password in a file only you can read, outside the
repository. Do not pass it on the command line.

```bash
umask 077
read -rs -p 'OSAC admin password: ' p && printf %s "$p" > ./admin-password; echo
mkdir -p test-output
cp "${KUBECONFIG}" ./kubeconfig

podman run --rm \
  --network=host \
  --userns=keep-id \
  --user "$(id -u):0" \
  -v ./kubeconfig:/work/kubeconfig:ro,Z \
  -v ./admin-password:/work/admin-password:ro,Z \
  -v ./test-output:/tmp/test-output:Z \
  -e KUBECONFIG=/work/kubeconfig \
  osac-ui-e2e \
  --tag=vmaas_full_happy_basic \
  --ignore-tls \
  --admin-password-file=/work/admin-password
```

- `--network=host` uses the host's DNS and `/etc/hosts`, so the routes resolve
  inside the container as they do on the host.
- `--userns=keep-id` with `--user "$(id -u):0"` runs the flows as your user,
  so a kubeconfig readable only by you can be read. Group 0 gives write access
  to the checkout in the image. `run-ui-e2e.sh` refuses credentialed runs as
  root.
- The kubeconfig is copied so that `:Z` relabels the copy, not the original.
- `run-ui-e2e.sh` reads the `osac-ui` and `fulfillment-api` routes with the
  kubeconfig. To run without one, see
  [Without a kubeconfig](#without-a-kubeconfig).
- `--ignore-tls` accepts the self-signed certificates of lab routes.
- The admin user defaults to `admin`; change it with `--admin-user=USER`. The
  runner also logs the bundled `osac` CLI in with these credentials, so no CLI
  configuration needs to be mounted.
- The Playwright HTML report and the test results are copied to
  `test-output/playwright-report/` and `test-output/test-results/` when the
  run ends. They are owned by your user; with rootless podman their group
  shows as a subordinate group ID (for example `165536`), which is the
  container's group 0. You can still read and delete them.

### Without a kubeconfig

When you cannot provide a kubeconfig, pass the two routes instead. The image
then needs no `oc` access to the cluster:

```bash
podman run --rm \
  --network=host \
  --userns=keep-id \
  --user "$(id -u):0" \
  -v ./admin-password:/work/admin-password:ro,Z \
  -v ./test-output:/tmp/test-output:Z \
  -e OSAC_UI_BASE_URL=https://osac-ui-osac.apps.<cluster-domain> \
  -e OSAC_API_BASE_URL=https://fulfillment-api-osac.apps.<cluster-domain> \
  osac-ui-e2e \
  --tag=vmaas_full_happy_basic \
  --ignore-tls \
  --admin-password-file=/work/admin-password
```

### Choosing a flow

The flow is selected only by `--tag`; the image and the other options stay the
same. For example, to run the VM Start, Stop and Restart test with the full
flow, change the tag and use a separate output directory so the reports do not
overwrite each other:

```bash
mkdir -p test-output-vm-power
podman run ... \
  -v ./test-output-vm-power:/tmp/test-output:Z \
  osac-ui-e2e --tag=full_vm_power --ignore-tls --admin-password-file=/work/admin-password
```

The flows use the committed
[`extravars.yaml`](../extravars.yaml). To use other values, mount a file and
pass `--extravars=PATH`:

```bash
podman run ... \
  -v ./extravars.yaml:/work/extravars.yaml:ro,Z \
  osac-ui-e2e --tag=vmaas_full_happy_basic --extravars=/work/extravars.yaml ...
```

To see the flow tags and options, run the image without arguments. To list the
tests of a tag without running them, add `--list`; it does not need the admin
password.

### Before a run

The flows create a tenant, instance type, disk image, storage tier, catalog
item, and VM with the names in `extravars.yaml`, and the create steps fail if
those objects already exist. After an interrupted run, delete the leftover
`*-e2e` objects first, for example:

```bash
for r in tenants instancetypes computeinstancecatalogitems diskimages storagetiers users; do
  osac get "${r}" | grep -i e2e
done
```

The image does not run the prepare script's capacity check, which needs a
kubeconfig at build time. Make sure the cluster can schedule the VM that
`extravars.yaml` describes.
