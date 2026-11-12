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

## Use in CI

The image is a self-contained test step: it takes a flow tag, runs it against
a deployed OSAC, returns the result as its exit code, and leaves an HTML
report. That is enough to run it from a CI pipeline or a GitHub Actions job.

It has been run end to end on two different hosts (a Fedora workstation with
podman 5.8, and a RHEL 9 lab host with podman 5.4), with both route discovery
methods, for `vmaas_full_happy_basic` and `full_vm_power`. It has not yet run
inside a hosted CI service; the workflow below is a starting template.

### Runner prerequisites

- **Rootless podman.** Docker is not tested. The runner user needs
  subordinate UIDs and GIDs (`/etc/subuid`, `/etc/subgid`) for
  `--userns=keep-id`.
- **Network access to the deployment:** the `osac-ui` and `fulfillment-api`
  routes over HTTPS, plus the Kubernetes API when route discovery uses a
  kubeconfig. Lab clusters are usually reachable only from inside the lab, so
  use a self-hosted runner there; GitHub-hosted runners cannot reach them.
- **Internet access to build the image** (UBI 9 repositories, npm, the
  Playwright Chromium build, `mirror.openshift.com`), about 5 GB of disk for
  the image, and several minutes for the build. Build once per commit under
  test and reuse the image for every flow.
- **Running as a non-root user.** `run-ui-e2e.sh` refuses credentialed runs
  as root.

### Deployment prerequisites

- OSAC is installed and healthy, with the `osac-ui` and `fulfillment-api`
  routes.
- A Keycloak user with the cloud provider admin role (default `admin`). The
  runner uses it for the UI and also logs the bundled `osac` CLI in with it,
  so no CLI session or token needs to be provided.
- Capacity for the VM described in `extravars.yaml`, and a storage backend
  for its storage tier (LVMS in the default values).
- No objects left with the names in `extravars.yaml` (tenant, instance type,
  catalog item). Only one run per deployment at a time: concurrent runs use
  the same names and fail. To run in parallel against one deployment, give
  each run its own `extravars.yaml` with different names.

### Inputs

| Input                          | How to pass it                                                                                                     |
| ------------------------------ | ------------------------------------------------------------------------------------------------------------------ |
| Flow to run                    | `--tag=TAG` (run the image without arguments to list the tags)                                                     |
| Deployment routes              | a kubeconfig mounted and set with `-e KUBECONFIG=...`, or `-e OSAC_UI_BASE_URL=...` and `-e OSAC_API_BASE_URL=...` |
| Admin password                 | a file mounted read-only and passed with `--admin-password-file=PATH`; store it as a CI secret                     |
| Admin user                     | `--admin-user=USER` (default `admin`)                                                                              |
| Test data                      | committed `extravars.yaml`, or a mounted file with `--extravars=PATH`                                              |
| Self-signed route certificates | `--ignore-tls`                                                                                                     |
| Results directory              | a writable directory mounted at `/tmp/test-output`                                                                 |
| `osac` CLI version             | build argument `FULFILLMENT_SERVICE_IMAGE`; pin it to the image the deployment runs                                |

Do not pass `-e CI=true`: with `CI` set, the Playwright configuration
retries a failed test once, and a retried create step fails on the object
the first attempt left behind.

### Outcomes

- **Exit code:** `0` when every test of the flow passed, non-zero otherwise
  (a test failure, or a setup error such as an unreachable route or a failed
  CLI login). The first failure stops the run.
- **Artifacts:** `playwright-report/` (open `index.html`) and
  `test-results/` in the results directory, copied there even when the run
  fails. There is no JUnit output. Tracing and video are off because the
  flows type the admin password.
- **Log:** the container's standard output shows each step as
  `[flow] Starting/Completed/Failed: ...`.
- **Cleanup:** `full_*` flows and `vmaas_full_happy_basic` delete everything
  they created when they pass. `create_*` flows keep it. A failed run leaves
  what it had created, and the `delete_*` tags cannot clean it up from a new
  container: they read the run state from the previous container's `/tmp`,
  which `--rm` removed. Delete the leftovers by name with the `osac` CLI
  (see [Before a run](#before-a-run)).
- **Duration:** about 6 minutes for `vmaas_full_happy_basic` and about
  8 to 9 minutes for `full_vm_power` on a lab deployment, plus the image
  build.

### Suggested pipeline

1. Build the image from the commit under test, with
   `FULFILLMENT_SERVICE_IMAGE` set to the image the deployment runs.
2. Check that no objects with the `extravars.yaml` names exist; stop or clean
   up if they do.
3. Run the image with the flow tag. Its exit code is the job result.
4. Always keep the results directory as a job artifact.
5. If the run failed, delete the objects it left behind before the next run.
6. Remove the password file.

GitHub Actions template for a self-hosted runner in the lab (not yet run in
GitHub Actions; adjust the labels, secrets, and the cleanup step to your
environment):

```yaml
jobs:
  ui-e2e:
    runs-on: [self-hosted, osac-lab]
    steps:
      - uses: actions/checkout@v4
      - name: Build the UI E2E image
        run: |
          podman build \
            -f osac-ui/apps/e2e-tests/container/Containerfile \
            --build-arg FULFILLMENT_SERVICE_IMAGE="${{ vars.FULFILLMENT_SERVICE_IMAGE }}" \
            -t osac-ui-e2e:${{ github.sha }} .
      - name: Run the happy flow
        env:
          OSAC_ADMIN_PASSWORD: ${{ secrets.OSAC_ADMIN_PASSWORD }}
        run: |
          umask 077
          printf %s "$OSAC_ADMIN_PASSWORD" > admin-password
          mkdir -p test-output
          podman run --rm \
            --network=host \
            --userns=keep-id \
            --user "$(id -u):0" \
            -v ./admin-password:/work/admin-password:ro,Z \
            -v ./test-output:/tmp/test-output:Z \
            -e OSAC_UI_BASE_URL="${{ vars.OSAC_UI_BASE_URL }}" \
            -e OSAC_API_BASE_URL="${{ vars.OSAC_API_BASE_URL }}" \
            osac-ui-e2e:${{ github.sha }} \
            --tag=vmaas_full_happy_basic \
            --ignore-tls \
            --admin-password-file=/work/admin-password
      - name: Remove the password file
        if: always()
        run: rm -f admin-password
      - name: Keep the Playwright report
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: ui-e2e-report
          path: test-output/
```
