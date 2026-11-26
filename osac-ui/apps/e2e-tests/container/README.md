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
(`registry.access.redhat.com/ubi9/nodejs-22-minimal`) and adds pnpm, Chromium and its
system libraries, and `oc`. The matching `osac` CLI is mounted at runtime.
UBI 9 repositories have no Xvfb,
so the image runs headless only; `--headed` stops with `Xvfb is missing`. The
host needs podman, git, tar, python3, and oc for the examples below,
a kubeconfig that can read the `osac` namespace, and network access to the
OSAC UI and Fulfillment API routes.

## Build

Build from the repository root at the commit you want to test. The build
context below contains only files tracked in that commit; it does not include
uncommitted changes or local credentials from the working checkout.

Build the image independently of the fulfillment-service image:

```bash
(
  set -euo pipefail
  build_context=$(mktemp -d /tmp/osac-ui-e2e-build.XXXXXXXX)
  trap 'rm -rf -- "$build_context"' EXIT
  git archive HEAD osac-ui | tar -x --same-permissions -C "$build_context"
  podman build \
    -f "$build_context/osac-ui/apps/e2e-tests/container/Containerfile" \
    -t osac-ui-e2e \
    "$build_context"
)
```

The temporary build context is private. Preserving archive permissions keeps
the entrypoint executable by the container's non-root user even with a
restrictive umask.

| Build argument              | Default                                           | Purpose                                                                   |
| --------------------------- | ------------------------------------------------- | ------------------------------------------------------------------------- |
| `OC_VERSION`                | `4.22.22`                                         | `oc` client version                                                       |
| `PNPM_RELEASE`              | `10.34.5`                                         | pnpm version; keep it equal to `packageManager` in `osac-ui/package.json` |

The build installs RPMs from the UBI 9 repositories, npm packages, the
Playwright Chromium build, and the `oc` client, so it needs internet access. It
does not need a Red Hat subscription. Do not use the working checkout directly
as the build context: it can contain untracked secrets and other local files.

## Run

Extract the CLI from the exact fulfillment-service image used by the
deployment. For a deployment with multiple service pods, check that their
running image IDs agree before selecting one, and repeat the extraction after
the deployment changes. The following reads the first pod's running image ID:

```bash
service_image=$(oc get pods \
  -n osac \
  -l app=fulfillment-grpc-server \
  -o jsonpath='{.items[0].status.containerStatuses[?(@.name=="grpc-server")].imageID}')
service_image=${service_image#docker-pullable://}
```

If this is only a digest, use a matching local image ID or prepend the deployed
image's repository to form `repository@sha256:...`. For a locally built CI
image, use the same local image that was loaded onto the cluster. The helper
accepts a local image or a digest-qualified registry reference; it never pulls
a mutable tag. Pulling a private image requires registry credentials. It pins
the locally resolved image ID, creates a temporary container, and copies the
CLI without starting that container. Record the selected image and printed
local image ID with the test results. Only use a trusted image: the extracted
CLI receives the admin credentials during the run.

Put the Keycloak admin password in a file only you can read, outside the
repository. Do not pass it on the command line.

```bash
umask 077
secret_dir=$(mktemp -d /tmp/osac-ui-e2e-secrets.XXXXXXXX)
tenant_credentials_dir=$(mktemp -d /tmp/osac-ui-e2e-tenant.XXXXXXXX)
cli_file="$secret_dir/osac"
bash osac-ui/apps/e2e-tests/container/extract-osac-cli.sh "$service_image" "$cli_file"
password_file="$secret_dir/admin-password"
read -rs -p 'OSAC admin password: ' p && printf %s "$p" > "$password_file"; echo
unset p
mkdir -p test-output
container_kubeconfig=$(mktemp "$secret_dir/kubeconfig.XXXXXXXX")
if ! KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}" \
  oc config view --raw --flatten > "$container_kubeconfig"; then
  rm -f -- "$container_kubeconfig"
  echo 'Could not flatten the kubeconfig.' >&2
else
  podman run --rm \
    --network=host \
    --userns=keep-id \
    --user "$(id -u):0" \
    -v "$container_kubeconfig":/work/kubeconfig:ro,Z \
    -v "$password_file":/work/admin-password:ro,Z \
    -v "$cli_file":/work/osac-cli:ro,Z \
    -v "$tenant_credentials_dir":/work/tenant-credentials:Z \
    -v ./test-output:/tmp/test-output:Z \
    -e KUBECONFIG=/work/kubeconfig \
    osac-ui-e2e \
    --tag=vmaas_full_happy_basic \
    --ignore-tls \
    --admin-password-file=/work/admin-password
fi
```

- `--network=host` uses the host's DNS and `/etc/hosts`, so the routes resolve
  inside the container as they do on the host.
- `--userns=keep-id` with `--user "$(id -u):0"` runs the flows as your user,
  so a kubeconfig readable only by you can be read. Group 0 gives write access
  to the checkout in the image. `run-ui-e2e.sh` refuses credentialed runs as
  root.
- The kubeconfig is flattened using `oc` on the host into a separate file so
  that `:Z` relabels it, not the original. This merges multiple `KUBECONFIG`
  files and embeds referenced certificates and keys. The output contains
  credentials; keep it private and remove it when no longer needed. External
  authentication helpers or token files, if used, must still be available inside
  the container.
- Remove the private files after the last run with `rm -rf -- "$secret_dir"`.
- Keep the separate tenant credentials directory private. It must be mounted
  for test execution so credentials survive container removal. After a failed
  password update, the original file and its `.pending` sibling contain the
  old and proposed credentials; either may be active. Recover or delete only
  the test tenant before removing the directory. A successful tenant
  deletion removes both files; remove the empty directory afterward.
- `run-ui-e2e.sh` reads the `osac-ui` and `fulfillment-api` routes with the
  kubeconfig. To run without one, see
  [Without a kubeconfig](#without-a-kubeconfig).
- `--ignore-tls` accepts the self-signed certificates of lab routes.
- The admin user defaults to `admin`; change it with `--admin-user=USER`. The
  runner also logs the mounted `osac` CLI in with these credentials, so no CLI
  configuration needs to be mounted.
- The Playwright HTML report and test results, when generated, are copied to
  a fresh `test-output/run-XXXXXXXX/` directory when the run ends. The
  entrypoint prints its path; earlier reports remain in their own directories.
  The files are owned by your user; with rootless podman their group
  shows as a subordinate group ID (for example `165536`), which is the
  container's group 0. You can still read and delete them.

### Without a kubeconfig

When you cannot provide a kubeconfig, pass the two routes instead. The image
then needs no `oc` access to the cluster. Obtain the deployed service image
identity separately and extract its CLI as described above, or use the CLI
already extracted by the CI job from the same locally built service image:

```bash
podman run --rm \
  --network=host \
  --userns=keep-id \
  --user "$(id -u):0" \
  -v "$password_file":/work/admin-password:ro,Z \
  -v "$cli_file":/work/osac-cli:ro,Z \
  -v "$tenant_credentials_dir":/work/tenant-credentials:Z \
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
flow, change the tag. Each run keeps its own reports:

```bash
mkdir -p test-output
podman run ... \
  -v ./test-output:/tmp/test-output:Z \
  osac-ui-e2e --tag=full_vm_power --ignore-tls --admin-password-file=/work/admin-password
```

The flows use the committed
[`extravars.yaml`](../extravars.yaml). To use other values, mount a file and
pass `--extravars=PATH`. If it contains `tenant_password`, keep it private and
outside the repository; do not commit it.

For example, with a non-secret local configuration:

```bash
podman run ... \
  -v ./extravars.yaml:/work/extravars.yaml:ro,Z \
  osac-ui-e2e --tag=vmaas_full_happy_basic --extravars=/work/extravars.yaml ...
```

To see the flow tags and options, run the image without arguments. To list the
tests of a tag without running them, add `--list`; it does not need the admin
password.

### Before a run

The flows create a tenant, instance type, disk image, storage tier, and catalog
item using names from `extravars.yaml`, as well as a VM. The runner appends the
`--instance` index (default `1`) to indexed resource names; for example, the
default tenant is `tenant-item-e2e-1`. Create steps can fail if the resulting
objects already exist. After an interrupted run, check for leftovers before
starting again. The following discovery example requires an `osac` CLI
installed and authenticated on the host with permission to list the resources.
Repeat it for each configured name in `extravars.yaml`, and for the generated
VM and user names (for example, `vm-e2e` and the tenant name):

```bash
read -r -p 'Resource name to search for: ' resource_name
for r in tenants instancetypes computeinstancecatalogitems computeinstances diskimages storagetiers users; do
  osac get "${r}" | grep -iF -- "$resource_name"
done
```

The search can include resources from other runs. Verify the exact names and
ownership before deleting any leftover test resources.

The image does not run the prepare script's capacity check, which needs a
kubeconfig at build time. Make sure the cluster can schedule the VM that
`extravars.yaml` describes.

## Use in CI

The image is a self-contained test step: it takes a flow tag, runs it against
a deployed OSAC, returns the result as its exit code, and exports reports when
generated. It can run from a CI pipeline or a GitHub Actions job.

It has been run end to end on two different hosts (a Fedora workstation with
podman 5.8, and a RHEL 9 lab host with podman 5.4), with both route discovery
methods, for `vmaas_full_happy_basic` and `full_vm_power`. It has not yet run
inside a hosted CI service; the example below is illustrative, not an OSAC
project CI integration. For that integration, follow the authorization, suite
selection, path-filter, check, and merge-queue requirements in the
[OSAC CI guide](../../../../.github/CI.md). Do not expose deployment secrets
to untrusted pull request code.

### Runner prerequisites

- **Rootless podman, git, tar, and python3.** Docker is not tested. The runner user needs
  subordinate UIDs and GIDs (`/etc/subuid`, `/etc/subgid`) for
  `--userns=keep-id`.
- **Network access to the deployment:** the `osac-ui` and `fulfillment-api`
  routes over HTTPS, plus the Kubernetes API when route discovery uses a
  kubeconfig. Lab clusters are usually reachable only from inside the lab, so
  use a self-hosted runner with verified access; a runner fleet label alone
  does not guarantee connectivity to a particular lab deployment.
- **Internet access to build the image** (UBI 9 repositories, npm, the
  Playwright Chromium build, `mirror.openshift.com`), about 5 GB of disk for
  the image, and several minutes for the build. Where possible, build once per
  commit under test and reuse the image for every flow.
- **Running as a non-root user.** `run-ui-e2e.sh` refuses credentialed runs
  as root.

### Deployment prerequisites

- OSAC is installed and healthy, with the `osac-ui` and `fulfillment-api`
  routes.
- A Keycloak user with the cloud provider admin role (default `admin`). The
  runner uses it for the UI and also logs the mounted `osac` CLI in with it,
  so no CLI session or token needs to be provided.
- Capacity for the VM described in `extravars.yaml`, and a storage backend
  for its storage tier (LVMS in the default values).
- No leftover objects with the resulting indexed test names (tenant, instance
  type, catalog item, and other resources). Different `--instance` values or
  different `extravars.yaml` names can avoid naming collisions, but do not
  isolate capacity or other shared state. Prefer a separate ephemeral
  deployment per job. For a shared deployment, coordinate all users of that
  deployment with a shared lock, including other workflows, repositories, and
  manual runs.

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
| `osac` CLI version             | mount the CLI extracted from the fulfillment-service image the deployment runs                                    |
| Tenant credential recovery     | mount a private writable directory at `/work/tenant-credentials`; retain it on failure                           |

Do not pass `-e CI=true`: with `CI` set, the Playwright configuration
retries a failed test once, and a retried create step fails on the object
the first attempt left behind.

### Outcomes

- **Exit code:** `0` when the runner succeeds and artifact export does not
  fail; non-zero for a test failure, setup error, or artifact export failure.
  An existing runner failure code takes precedence over an export failure. A
  successful exit can include skipped tests, and `--list` does not execute
  tests. Check the passed and skipped counts before claiming full coverage.
  The first test failure stops the run.
- **Artifacts:** `playwright-report/` (open `index.html`) and `test-results/`
  are copied to a new `run-XXXXXXXX/` directory within the results directory
  if generated, including after a failed run. The entrypoint prints that
  directory. An early failure can leave no report, so retain the container log too.
  There is no JUnit output. Tracing and video are off for the credentialed
  flows, and Playwright failure snapshots are disabled because they can expose
  passwords. Treat reports and logs as sensitive: they can still contain other
  deployment or test data, and old reports are not retroactively sanitized.
  Playwright debug logging and the inspector are rejected because they can
  expose input values; unset `DEBUG=pw:...` and `PWDEBUG` before running.
- **Log:** the container's standard output shows each step as
  `[flow] Starting/Completed/Failed: ...`.
- **Cleanup:** `full_*` flows and `vmaas_full_happy_basic` include deletion
  steps; verify removal when those steps execute and pass. Skipped steps or a
  failed run can leave resources behind, and `create_*` flows keep them.
  Delete flows differ: `delete_instance_type` uses an indexed name and can
  operate in a new container, but may create the object if it is absent. Full
  VM-flow deletion relies on run-specific state in `/tmp` or the original run
  IDs, and tenant deletion needs its credential file in the private mount.
  `--rm` discards other files stored in the container. Inspect the specific flow and its dependencies before
  using a `delete_*` tag for cleanup. Otherwise, identify and delete only the
  leftover test resources using an authenticated CLI or API (see
  [Before a run](#before-a-run)).
- **Duration:** about 6 minutes for `vmaas_full_happy_basic` and about
  8 to 9 minutes for `full_vm_power` on a lab deployment, plus the image
  build.

### Suggested pipeline

1. Build the runner image from the commit under test. Extract the CLI from the
   same fulfillment-service image used by the deployment and mount it at runtime.
2. Check that no objects with the resulting indexed test names exist; stop or
   clean up verified leftovers if they do.
3. Run the image with the flow tag. Its exit code is the job result.
4. Keep the container log and generated results in restricted storage, including
   on failure. Do not publish them as artifacts of a public workflow run.
5. If the run failed, delete the objects it left behind before the next run.
6. Remove the password file.

Illustrative GitHub Actions job for trusted code on a self-hosted runner (not
yet run in GitHub Actions). Replace `YOUR_RUNNER_LABEL` with a real label for
a runner that can reach the deployment, and configure the variables, secrets,
pre-run checks, and cleanup for your environment. This fragment does not
implement OSAC CI's pull-request authorization or integration requirements.

The example captures the container log and reports in a private temporary
directory on the runner; it does not upload them. For OSAC CI, integrate the
OSAC CI app's private `osac-project/osac-ci-artifacts` storage for both logs
and reports, including on failure. Do not fall back to a public workflow
artifact if private storage is unavailable. Configure private retention and
cleanup before using this example: the directory can remain on a self-hosted
runner after the job. The workflow's other console output must also be kept
free of sensitive data.

The example builds the image in every job, tags it uniquely, and passes the
built image ID to the test step. A runner may reuse local cached layers, but
different runners do not share a build automatically. The tagged image and
build cache remain in the runner's Podman storage after the job, so manage
their retention to prevent disk exhaustion. For a CI integration, build
the E2E image once for the commit under test, publish it to a registry, and
have authorized test jobs pull it by digest. Define registry permissions and
retention, and manage runner storage even when pulling images. This image
publication and digest handoff are not implemented by the example.

For a shared deployment, replace `YOUR_DEPLOYMENT_ID` with the same identifier
in every job in this repository that uses it. The concurrency group coordinates
only jobs in the same repository using that group; it does not lock out other
repositories or manual runs. Use an external lock covering all users where
needed. GitHub can cancel an older pending job when a newer one joins the group.

```yaml
jobs:
  ui-e2e:
    concurrency:
      group: ui-e2e-YOUR_DEPLOYMENT_ID
      cancel-in-progress: false
    runs-on: [self-hosted, YOUR_RUNNER_LABEL]
    steps:
      - uses: actions/checkout@v4
      - name: Build the UI E2E image
        id: build
        run: |
          set -o pipefail
          build_context=$(mktemp -d /tmp/osac-ui-e2e-build.XXXXXXXX)
          trap 'rm -rf -- "$build_context"' EXIT
          git archive HEAD osac-ui | tar -x --same-permissions -C "$build_context"
          podman build \
            -f "$build_context/osac-ui/apps/e2e-tests/container/Containerfile" \
            --iidfile "$build_context/image.iid" \
            -t "osac-ui-e2e:${build_context##*/}" \
            "$build_context"
          printf 'image_id=%s\n' "$(cat "$build_context/image.iid")" >> "$GITHUB_OUTPUT"
      - name: Run the happy flow
        env:
          OSAC_UI_E2E_IMAGE: ${{ steps.build.outputs.image_id }}
          OSAC_ADMIN_PASSWORD: ${{ secrets.OSAC_ADMIN_PASSWORD }}
          FULFILLMENT_SERVICE_IMAGE: ${{ vars.FULFILLMENT_SERVICE_IMAGE }}
        run: |
          umask 077
          password_file=$(mktemp "${RUNNER_TEMP}/osac-ui-e2e-password.XXXXXXXX")
          cli_dir=$(mktemp -d "${RUNNER_TEMP}/osac-ui-e2e-cli.XXXXXXXX")
          result_dir=$(mktemp -d "${RUNNER_TEMP}/osac-ui-e2e-results.XXXXXXXX")
          tenant_credentials_dir=$(mktemp -d "${RUNNER_TEMP}/osac-ui-e2e-tenant.XXXXXXXX")
          trap 'rm -f -- "$password_file" "$cli_dir/osac"; rmdir -- "$cli_dir"' EXIT
          bash osac-ui/apps/e2e-tests/container/extract-osac-cli.sh \
            "$FULFILLMENT_SERVICE_IMAGE" "$cli_dir/osac"
          printf %s "$OSAC_ADMIN_PASSWORD" > "$password_file"
          printf 'Private results directory: %s\n' "$result_dir"
          printf 'Private tenant credentials directory: %s\n' "$tenant_credentials_dir"
          if podman run --rm \
            --network=host \
            --userns=keep-id \
            --user "$(id -u):0" \
            -v "$password_file":/work/admin-password:ro,Z \
            -v "$cli_dir/osac":/work/osac-cli:ro,Z \
            -v "$tenant_credentials_dir":/work/tenant-credentials:Z \
            -v "$result_dir":/tmp/test-output:Z \
            -e OSAC_UI_BASE_URL="${{ vars.OSAC_UI_BASE_URL }}" \
            -e OSAC_API_BASE_URL="${{ vars.OSAC_API_BASE_URL }}" \
            "$OSAC_UI_E2E_IMAGE" \
            --tag=vmaas_full_happy_basic \
            --ignore-tls \
            --admin-password-file=/work/admin-password \
            > "$result_dir/runner.log" 2>&1; then
            rmdir -- "$tenant_credentials_dir" ||
              echo 'Tenant credentials retained for cleanup.'
          else
            rc=$?
            echo 'Tenant credentials retained for recovery.' >&2
            exit "$rc"
          fi
```
