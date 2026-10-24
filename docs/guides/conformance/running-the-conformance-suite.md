# Running the OSAC conformance suite

OSAC publishes one conformance suite per release, the image
`ghcr.io/osac-project/osac-conformance:<OSAC release>`. It shows that a
component a provider wrote for OSAC works with that release; today that
component is a network Fabric Manager. Run the suite in a lab before you go
live, and again for every OSAC release and every new version of your
component.

## What the suite runs

| Layer | Runs against | What it checks |
|---|---|---|
| Component tests | Your component, run exactly as OSAC runs it (an Ansible role in the execution environment image the lab runs, through OSAC's generic playbook, with the settings and secrets the lab holds), against your backend. No OSAC is involved; a scratch cluster stands in for the hub. | Your component's results, reruns, deletes, and errors, with generated inputs. |
| Profiles | A lab that runs this OSAC release with your component installed, through OSAC's APIs. | One end-to-end scenario per service enabled in the lab. |

The suite reads the services enabled in the lab and the components registered
there, and runs only what applies. A run that does not apply reports `skip`
and does not count against you. A run that applies but reports `skip`, for
example because an optional input is missing, is never a pass. Profiles run
after the component tests, also when a component test failed.

Each area defines its own tests, profiles, and configuration section. For
networking, see
[Run the conformance suite](../networking/fabric-manager-guide.md#run-the-conformance-suite)
in the Fabric Manager Guide.

## Before you run

1. **A lab.** It runs the same OSAC release as the suite image and has your
   component installed (see the
   [installation guide](../installation/customer-install-guide.md)). The lab
   can be shared: the suite works in its own test Tenant,
   `osac-conformance-<run ID>`, deletes it at the end, and leaves other
   Tenants alone.
2. **A scratch cluster** with a writable namespace for the component tests,
   and its kubeconfig.
3. **A runner host with podman.** Start the podman socket
   (`systemctl --user enable --now podman.socket`) and log podman in to the
   registry of your component's image, so the suite can start it. The suite
   starts your image on the runner, in the runner's network, so the runner
   must reach your component's backend API, the lab's fulfillment API and
   hub, and, for networking, the workloads' external addresses.
4. **A configuration directory**, `conformance/`, holding `conformance.yaml`
   and the files it names:
   - `admin-token`: a token of the `admin` service account that the OSAC chart
     creates, long enough for a full run, for example
     `oc create token admin -n <OSAC namespace> --duration 12h`;
   - `api-ca.pem`: the CA that signed the API certificate, for example
     `oc extract secret/default-ca -n cert-manager --keys=tls.crt --to=- > api-ca.pem`;
   - `lab-hub.kubeconfig`: the lab hub, with read access to the OSAC namespace
     (including Secrets) and to what your area reads;
   - the files your area asks for.

   Next to it (not inside it), create a new, empty results directory for each
   run, for example `results/<date>-full/`. Delete the token files after the
   run.

## Write conformance.yaml

The common keys are at the top level; each area has its own section:

```yaml
osacRelease: <OSAC release>                # equal to the suite image tag
lab:
  kubeconfig: /config/lab-hub.kubeconfig
  apiURL: https://<fulfillment internal API host>
  tokenFile: /config/admin-token
  caFile: /config/api-ca.pem
# aap: {url: https://<AAP controller host>, tokenFile: /config/aap-token}   # optional
# timeouts: {delete: 30m}                  # optional overrides of an area's defaults
networking:                                # the networking area's keys
  # see the Fabric Manager Guide
```

| Key | Meaning |
|---|---|
| `osacRelease` | The OSAC release under test. It must equal the image tag and the release the lab runs. |
| `lab.kubeconfig` | The lab hub. |
| `lab.apiURL` | The lab's internal fulfillment API, the host of the `fulfillment-internal-api` route: `oc get route fulfillment-internal-api -n <OSAC namespace> -o jsonpath='{.spec.host}'`. |
| `lab.tokenFile`, `lab.caFile` | The admin token, and the CA that signed the API certificate. |
| `lab.aap` | Optional: the lab's AAP controller URL and a read-only token file. With it, a failed profile's evidence holds the lab's AAP job output; without it, only the job IDs. |
| `timeouts` | Optional. Overrides of the areas' timeouts, as durations such as `15m`; each area names its keys and defaults. |
| `<area>` | That area's keys. |

Do not repeat what the suite reads from the lab: your component's identity,
image, settings, and secrets, and the enabled services.

## Run the suite

```sh
podman run --rm --security-opt label=disable \
  -v "$PWD/conformance:/config:ro" -v "$PWD/results/<date>-full:/results" \
  -v "$XDG_RUNTIME_DIR/podman/podman.sock:/run/podman/podman.sock" \
  ghcr.io/osac-project/osac-conformance:<OSAC release>
```

`--security-opt label=disable` lets the suite open the podman socket on a host
with SELinux enforcing. The exit code tells the outcome:

| Exit code | Meaning |
|---|---|
| `0` | `conformant`; in a focused run, every selected test passed. |
| `1` | `in progress`: a test or profile that counts did not pass, or something was left behind. The summary says which. |
| `2` | Configuration error: no test ran and no report was written. The output names each failing check, for example an unreachable lab, a rejected token, a lab that runs another release, a results directory that is not empty, a key this release does not define, or a missing key that a required run needs. Nothing is written to `results/`. |
| `130` | Interrupted (Ctrl-C): the suite cleaned up and wrote the report. A second Ctrl-C stops it at once, with no cleanup. |

## Follow a run

The suite shows every step as it happens, on the screen and in
`results/conformance.log`, with a UTC timestamp on each line:

```text
2026-11-03T09:12:01Z run k3x9p2qa, test Tenant osac-conformance-k3x9p2qa
2026-11-03T09:12:01Z osac-conformance <OSAC release> (sha256:...) lab https://fulfillment-internal-api-osac.apps.lab.example
2026-11-03T09:12:02Z enabled services: vmaas
2026-11-03T09:12:02Z component networking/acme_fabric: acme.network.fabric 1.0.0, image sha256:...
2026-11-03T09:12:02Z selected: 41 component tests, profile vm; not run: baremetal (BMaaS not enabled), cluster (CaaS not enabled)
2026-11-03T09:12:03Z STARTED (12/43) [networking][component] TC-RETRY-01/create_subnet: returns the same result when run again with the same input
2026-11-03T09:12:03Z STEP: run create_virtual_network (jobs/1-create_virtual_network/job_vars.yaml)
2026-11-03T09:12:19Z STEP: create_virtual_network succeeded in 16s
2026-11-03T09:12:19Z STEP: run create_subnet (jobs/2-create_subnet/job_vars.yaml)
2026-11-03T09:12:41Z STEP: create_subnet succeeded in 22s: network_segment {type: evpn, l2_vni: 10100, l3_vni: 20001}
2026-11-03T09:12:41Z STEP: run create_subnet again with the same input
2026-11-03T09:13:02Z FAILED [networking][component] TC-RETRY-01/create_subnet (59s)
2026-11-03T09:13:02Z   expected: the same network_segment as the first run
2026-11-03T09:13:02Z   observed: l2_vni 10101 (first run: 10100)
2026-11-03T09:13:02Z   evidence: results/tests/TC-RETRY-01/create_subnet/
...
2026-11-03T09:41:10Z SUMMARY component: 40 passed, 1 failed, 9 skipped (not applicable); profiles: vm passed, baremetal skipped, cluster skipped
2026-11-03T09:41:10Z FAILED TC-RETRY-01/create_subnet: observed l2_vni 10101 (first run: 10100)
2026-11-03T09:41:10Z rerun the failed tests: run --run '^\[networking\]\[component\] TC-RETRY-01/create_subnet:'
2026-11-03T09:41:10Z leftovers: none; result: in progress; exit code 1
```

Profiles run in parallel, so each of their lines starts with the test's ID. A
wait logs the observed state each time it changes and at least once a minute.
A failed test always says what it expected, what it observed, the resource's
last state when there is one, and where its evidence is.

## Read the report

The suite writes `conformance-report.yaml` and `junit.xml` to `results/`.
`junit.xml` has one test case per test in the report; a failed test case
holds its message and its log.

```yaml
osacRelease: <OSAC release>
suiteImageDigest: sha256:<digest>
components:
  - {area: networking, name: acme_fabric, ..., result: conformant}
enabledServices: [vmaas]
profiles:
  - {name: vm, area: networking, required: true, result: pass}
  - {name: baremetal, area: networking, required: false, result: skip}
  - {name: cluster, area: networking, required: false, result: skip}
tests:
  - {id: TC-CONTRACT-01/create_subnet, name: "[networking][component] TC-CONTRACT-01/create_subnet: ...", layer: component, profile: "", result: pass, durationSeconds: 41, message: ""}
leftovers: []
focus: ""
result: conformant
```

| Key | Meaning |
|---|---|
| `components[]` | Each of your components found in the lab: its area, its identity (as the area defines it), and its own `result`. |
| `profiles[]` | Each profile, whether it is required in this lab, and its result (`pass`, `fail`, or `skip`). |
| `tests[]` | Each test with its ID, name, result, duration, and message. A failing test's `message` says what failed. |
| `leftovers[]` | What the suite could not delete or kept with `--keep`: an OSAC object as `<kind>/<API ID>`, a backend object of a component test as `<task>/<API ID>`. Delete it by hand. |
| `focus` | The `--run` expression of a focused run; empty for a full run. |
| `result` | `conformant` or `in progress`. |

A component is `conformant` when every component test that applies passes
and every profile its area requires passes; otherwise it is `in progress`.
The overall `result` is `conformant` only when the suite found at least one
component, every component is `conformant`, nothing was left behind, and the
run was not focused.

If a run is killed, read its run ID from the first line of
`results/conformance.log`. Delete the Tenant `osac-conformance-<run ID>`,
every object named `osac-conformance-<run ID>-*` (for networking, the suite's
ExternalIPPools), and anything the run left in your backend. A leftover pool
makes the next run fail its configuration checks.

## Use the evidence

Each test has a directory, `results/tests/<test ID>/`, for example
`results/tests/TC-RETRY-01/create_subnet/`:

| File | Content |
|---|---|
| `test.log` | The test's own lines. |
| `jobs/<n>-<task>/job_vars.yaml` | The exact input your component got. |
| `jobs/<n>-<task>/env.yaml` | The environment variables of the job, with secrets `<redacted>`. |
| `jobs/<n>-<task>/ansible.log` | Your component's output, at verbosity `-v`. |
| `jobs/<n>-<task>/result.yaml` | What your component published: `osac_result` or `osac_error`. |
| `jobs/<n>-<task>/playbook.yml`, `rerun.sh` | The generic playbook, and the exact `podman run` and `ansible-playbook` command of the job. Set the environment variables `rerun.sh` names (your secrets and the scratch kubeconfig path) and run it to reproduce the job by hand. |
| `jobs/<n>-<task>/hub-objects.yaml` | On a failure: the objects in the scratch namespace. |
| `api.log` | For a profile: each API request and response. |
| `resources/<kind>-<API ID>.yaml` | On a failure: the test's OSAC resources with their status and job entries. |
| `lab-jobs/<job ID>.log` | On a profile failure, with `lab.aap` set: the lab's AAP job output. |
| `area/` | On a profile failure: the extra objects the area collects (for networking, the VM objects, the launcher pod's events and log, the guest serial console, and the Subnet's network). |

Secret values (your component's secrets, the tokens, kubeconfig credentials)
are replaced by `<redacted>` everywhere in `results/`.

## List and rerun tests

| Command (after the image name in `podman run`) | What it does |
|---|---|
| `list` | Prints every test the run would select, by name, and every run that does not apply with the reason. It creates nothing and writes nothing to `results/`. |
| `list --run '<regex>'` | Prints only the tests that `run --run '<regex>'` would run. |
| `run --run '<regex>'` | Runs only the tests whose name matches, for example the expression the summary printed. A matched profile test runs its whole profile. Use a new results directory. |
| `run --run '<regex>' --keep` | Also leaves the test Tenant and its resources in place so you can inspect them. Delete them by hand afterwards; the report lists them in `leftovers[]`. |

The component tests always run the image the lab runs. After you fix your
component, build a new image and have the Cloud Infrastructure Admin roll it
out to the lab before you rerun. A focused run is never `conformant`; run the
whole suite once more before you attach the report.

## Use the result

- Attach `conformance-report.yaml` to your component's design. The report is
  self-reported; OSAC does not verify it.
- Each area states when its services are supported; for networking, see
  [Supported configurations](../installation/customer-install-guide.md#91-supported-for-production)
  in the installation guide. OSAC does not enforce this.
- Run the suite again for every OSAC release and every new version of your
  component.
