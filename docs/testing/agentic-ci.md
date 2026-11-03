# Agentic CI sandbox

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Follow this guide for suite setup and limits; inspect test source for
individual cases. Commands and inline paths are relative to the repository root.

The repository overlay provides early Autofix feedback. It does not replace
authoritative GitHub CI, component integration, or deployed E2E gates. Its
[touched-area map](../../.agentic-ci/AGENTS.md) owns validation of the overlay itself.
All cases below belong to [DEV]
[OSAC-6172](https://redhat.atlassian.net/browse/OSAC-6172).

| Behavior | Tier | Execution and boundary |
|---|---|---|
| API/schema dependency selection, large tracked/untracked streams, Git fallbacks, UI/E2E routing, and failure propagation | Unit | Use the [canonical local command](../../.agentic-ci/AGENTS.md#touched-area-map); real temporary Git repositories and Bash execute the actual YAML validation bodies, while expensive commands are recording doubles |
| Production setup and API/schema/UI/E2E scenarios | Component integration | Use the production Autofix image and OpenShell runner with candidate code and the base-branch sandbox profile; actual toolchains, allowed registries, lint, supported unit tests, and pytest collection run. No OSAC deployment, container services, Kubernetes API, or provider is started |
| Deliberate API test failure | Component integration | Add a failing unit test in an isolated production scenario, verify the go-test validation record fails, and run a clean scenario afterward |
| Dependency copy-back and optional discard | Component integration | Measure three paired trials on the same production image; add exact generated paths only for median savings of at least 10% and five seconds, then verify restoration on reuse. The current overlay discards only `osac-ui/node_modules` and `osac-aap/.venv`. |

Full UI validation remains intentional. E2E collection creates an external venv
and installs root dependencies once, only when selected. The overlay's
[skips](../../.agentic-ci/config.yml) specify container, Envtest, network, live-service
and metering-execution exceptions. Collection proves import readiness for
selected cases; deployed journeys and metering coverage require their own gates.
Selected/deselected counts are revision-specific evidence, not fixed thresholds.

Production acceptance uses candidate code with the `sandbox:` profile from the
base branch, which is loaded before the agent runs. A PR change to
`.agentic-ci/config.yml` therefore does not change that run's profile. No
candidate-overlay selection mode is defined here. For overlay changes, run the
candidate's [local validation](../../.agentic-ci/AGENTS.md#touched-area-map) and
workflow smoke checks; verify the production profile in an Agentic CI run whose
base branch includes the change. Local/Podman runners do not establish this
production acceptance boundary.

Production evidence must identify the candidate/base commits, image digest,
runner revision, effective merged profile, and selected toolchain versions.
Verify both project-selected interpreters (fulfillment 3.14 and AAP 3.13) with
Python downloads disabled. Use a fresh setup. The overlay must be present on the
base branch for the production runner to load it.

Attach `_run/sandbox-setup.json`, `_run/sandbox-validation.json`, and
`_run/toolchains.json`, along with module-selection logs, UI results, E2E counts,
and copy-back measurements. Include `_run/sandbox-discard.json` when paths are
added. A successful overall agent exit does not imply successful validation;
inspect required records for failure, timeout, error, or not-run status. Label
the intentional negative scenario separately. Missing production execution or
profiling evidence remains an acceptance gap under
[OSAC-6172](https://redhat.atlassian.net/browse/OSAC-6172).
