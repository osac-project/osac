# Release automation

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers and evidence.
Follow the [workflow guide](../../.github/AGENTS.md#release-safety) for release
safety and focused workflow validation.

Inspect affected packaging, version-resolution and publication paths when they
change. Static workflow checks do not prove hosted scheduling, registry
publication or notification delivery. Use an authorized hosted run when the
changed behavior requires it, and report any unverified boundary concisely.
