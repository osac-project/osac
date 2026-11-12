# OSAC conformance suite core

The suite entry point is `osac-conformance`. It reads `conformance.yaml`,
expands the selected profiles from `profiles.yaml` into selectors, computes a
plan, runs it through the test tree's pytest adapter, applies the named checks,
and writes `conformance-report.yaml` and `junit.xml` to the results directory.

Local commands:

```sh
python3 -m tests.conformance list
python3 -m tests.conformance run
python3 -m tests.conformance run --plan /results/conformance-plan.yaml
```

Set `OSAC_NAMESPACE` when the OSAC namespace is not `osac` (default).

The image builds from the repository root:

```sh
podman build -f tests/conformance/Containerfile -t osac-conformance:dev .
```

Profiles are a suite-level catalog in `profiles.yaml`; areas define tests and
the selectors those tests carry. The operator selects profiles, and `generic`
always applies. Release publication of
`ghcr.io/osac-project/osac-conformance:<release>` is owned by OSAC-7457. Component execution, profile setup and test Tenant cleanup
are owned by OSAC-7455; networking declarations are owned by OSAC-7456.
