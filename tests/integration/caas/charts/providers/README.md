# CaaS provider simulation

Installs the Go simulator, discovery HTTP server, and dedicated Metal3 namespace
for the local CaaS suite. The suite owns hardware and discovery-data fixtures.

See the [CaaS guide](../../README.md) for setup and test commands, and
[values.yaml](values.yaml) for chart options.

## Case settings

The chart creates no response settings. Each test creates a settings ConfigMap
bound to its InfraEnv and HostedCluster UIDs.

See [simulation profiles](../../README.md#simulation-profiles) for behavior.
