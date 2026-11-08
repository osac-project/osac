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
| API/schema dependency selection, large tracked/untracked streams, Git fallbacks, UI/E2E routing, failure propagation | Unit | Use the [canonical local command](../../.agentic-ci/AGENTS.md#touched-area-map); real temporary Git repositories and Bash run the YAML validation bodies, expensive commands are recording doubles. |
| Production setup and API/schema/UI/E2E scenarios | Component integration | Production Autofix image and OpenShell runner with candidate code and base-branch sandbox profile; real toolchains, allowed registries, lint, supported unit tests and pytest collection run. No OSAC deployment, services, Kubernetes API or provider starts. |
| Deliberate API test failure | Component integration | Add a failing unit test in an isolated production scenario, confirm the go-test record fails, then run a clean scenario. |
| Dependency copy-back and optional discard | Component integration | Measure three paired trials on the same production image; add exact generated paths only for median savings of at least 10% and five seconds, then verify restoration on reuse. The current overlay discards only `osac-ui/node_modules` and `osac-aap/.venv`. |

Full UI validation remains intentional. E2E collection creates an external venv
and installs root dependencies once, only when selected. The overlay's
[skips](../../.agentic-ci/config.yml) specify container, Envtest, network,
live-service and metering-execution exceptions; collection proves import
readiness, while deployed journeys and metering need their own gates. Counts are
revision-specific evidence, not fixed thresholds.

Production acceptance uses candidate code with the `sandbox:` profile from the
base branch, loaded before the agent runs, so PR changes to the overlay config do
not change it. No candidate-overlay selection mode is defined here. Overlay
changes need candidate local validation and workflow smoke checks plus a run
whose base branch includes the change; local/Podman runners do not establish
this boundary.

Production evidence must identify candidate/base commits, image digest, runner
revision, effective profile, toolchain versions and the `_run/` setup, validation,
toolchain and optional discard records, plus selection logs, UI/E2E results and
copy-back measurements. Verify each project-selected interpreter with Python
downloads disabled on a fresh setup; the overlay must be present on the base
branch for the production runner to load it. A successful agent exit does not
imply successful validation; inspect records for failure, timeout, error or
not-run status and label the negative scenario separately. Missing execution or
profiling evidence remains a gap under
[OSAC-6172](https://redhat.atlassian.net/browse/OSAC-6172).
