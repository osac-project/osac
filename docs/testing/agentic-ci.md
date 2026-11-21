# Agentic CI sandbox

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers and evidence;
inspect test source for individual cases. Paths are relative to the repository
root.

The overlay provides early Autofix feedback; it does not replace authoritative
GitHub CI, component integration or deployed E2E gates. Its
[touched-area map](../../.agentic-ci/AGENTS.md#touched-area-map) owns overlay
validation. All cases belong to [DEV]
[OSAC-6172](https://redhat.atlassian.net/browse/OSAC-6172).

| Behavior | Tier | Execution and boundary |
|---|---|---|
| Production setup and API/schema/UI/E2E scenarios | Component integration | Production Autofix image and OpenShell runner with candidate code and base-branch sandbox profile; real toolchains, allowed registries, lint, supported unit tests and pytest collection run. No OSAC deployment, services, Kubernetes API or provider starts. |
| Dependency copy-back and optional discard | Component integration | When changing discard behavior, measure the affected setup/copy-back path and verify restoration on reuse before expanding the discarded paths. The current overlay discards only `osac-ui/node_modules` and `osac-aap/.venv`. |

Full UI validation remains intentional. E2E collection creates an external venv
and installs root dependencies once, only when selected. The overlay's
[skips](../../.agentic-ci/config.yml) specify container, Envtest, network,
live-service and metering-execution exceptions; collection proves import
readiness, while deployed journeys and metering need their own gates. Counts are
revision-specific evidence, not fixed thresholds.

Production acceptance uses candidate code with the `sandbox:` profile from the
base branch, loaded before the agent runs, so PR changes to the overlay config do
not change it. No candidate-overlay selection mode is defined here. Overlay
changes need relevant local checks; production verification requires a run
whose base branch includes the change. Local/Podman runners do not establish
this boundary.

When production verification is needed, identify the candidate/base revisions,
runner image and effective profile, then inspect the relevant setup/validation
records. A successful agent exit does not imply successful validation. Report
failure, timeout or unavailable execution without requiring a full profiling
report for unrelated edits.
