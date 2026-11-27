# AgentlessNet AWX fixture

This fixture lets a networking profile run an AgentlessNet task without a live
switch or provider. The synced project must contain
`tests/integration/fixtures/agentless_switch/ssh`, and the state workspace must
be writable and visible to the AWX execution environment.

## Prepare a job run

From the synced project root, create a unique run name and pass both the
host-side workspace and its path in the execution environment.
`project_root_in_job` is the synced project root as seen by the AWX job.

```python
import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path("tests/integration/common").resolve()))

from agentless_net_awx_fixture import (
    cleanup_agentless_net_awx_fixture,
    prepare_agentless_net_awx_fixture,
)

fixture = prepare_agentless_net_awx_fixture(
    Path("/var/tmp/osac-awx-runs"),
    job_workspace="/runner/artifacts/osac-awx-runs",
    project_root_in_job="/runner/project",
    tenant="agentless-net-contract",
    fixture_name="job-12345",
)
```

The function creates `network.sqlite3` and `switch.json` under
`<workspace>/<fixture_name>`, and reserves a path for the fake SSH failure
marker. The fake SSH executable creates the marker if a configured one-time
failure occurs. The fixture returns:

- `job_environment`: values for `AGENTLESS_NET_VN_INVENTORY`,
  `AGENTLESS_NET_STATE_FILE`, `AGENTLESS_NET_SSH_PRIVATE_KEY`,
  `ANSIBLE_SSH_EXECUTABLE`, `OSAC_AGENTLESS_SWITCH_STATE_FILE`,
  `OSAC_AGENTLESS_SWITCH_FAIL_MARKER`, `PYTHONPATH`,
  `OSAC_AGENTLESS_NET_CONTRACT_TEST_MODE`, and
  `OSAC_AGENTLESS_DHCP_LEASE_FILE`.
- `inventory`: the provider inventory mapping for the network node and fake
  Cumulus switch. The fixture exports this through
  `AGENTLESS_NET_VN_INVENTORY`.
- `job_inventory`: the Ansible job inventory for the generic network playbook.
  It targets `localhost`, uses the local connection, disables privilege
  escalation, and selects the playbook's Python interpreter.

Merge `fixture.job_environment` into the job's existing environment and use
`fixture.job_inventory` for the job inventory. The fixture prepends the stub
directory to the current `PYTHONPATH`, preserving its existing entries. The
returned executable path points into the synced project. The fixture does not replace the AWX execution
environment's `PATH`; if the selected Ansible connection plugin needs to find
`ssh` by name, prepend the executable's directory to `PATH` inside that job.
The Python path loads the contract test's `sitecustomize.py`, which stubs host
DHCP and systemd operations for this fixture. The lease-file path is scoped to
the fixture workspace.

The fixture uses the physical `apply_network_attachment` case from the local
AgentlessNet contract target as its representative operation. The job should
invoke the generic network task playbook with its usual `osac_job_vars` input.

## Clean up

Always clean up after the job, including when it fails:

```python
import os

try:
    run_awx_job(
        environment={**os.environ, **fixture.job_environment},
        inventory=fixture.job_inventory,
    )
finally:
    cleanup_agentless_net_awx_fixture(fixture)
```

Cleanup removes files owned by this fixture and removes its directory only
when empty. It leaves the caller's workspace and any unrelated files intact.
Use a different `fixture_name` for each parallel run.

The SQLite state and fake SSH command handling run locally for real. The fake
SSH script simulates switch VLAN state changes; it does not provide a live
network, DHCP service, or packet delivery. A deployed AWX profile run using
this fixture is owned by [OSAC-7925](https://redhat.atlassian.net/browse/OSAC-7925).
