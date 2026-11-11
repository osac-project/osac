# OSAC conformance suite core

The suite entry point is `osac-conformance`. It reads `conformance.yaml`,
computes a plan from the lab's declared selectors, runs the plan through the
test tree's pytest adapter, applies the named checks, and writes
`conformance-report.yaml` and `junit.xml` to the results directory.

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

Release publication of `ghcr.io/osac-project/osac-conformance:<release>` is
owned by OSAC-7457. Component execution, profile setup and test Tenant cleanup
are owned by OSAC-7455; networking declarations are owned by OSAC-7456.
