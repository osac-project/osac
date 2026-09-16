# Open Sovereign AI Cloud

## Structure

[Features](features/): Description of key features and capabilities, some of
which may not yet be implemented.

[Architecture](architecture/): Description of components, how they work
together, and the design decisions that have been made.

[Guides](guides/): Step-by-step configuration and usage guides for
administrators, tenants, and developers.

This directory also contains concise, hand-maintained guidance for
architecture and conventions that span component or repository boundaries
within this mono-repo. Component `AGENTS.md` files own component-scoped agent
rules and invariants and route readers to the referenced README and
documentation for architecture, setup, and detailed conventions. Root
`AGENTS.md` owns cross-component flow and dependency guidance;
[`ARCHITECTURE.md`](ARCHITECTURE.md) and [`CONVENTIONS.md`](CONVENTIONS.md)
provide the detailed cross-component reference. Also in this directory:

- [codex-getting-started.md](codex-getting-started.md) — onboarding OpenAI
  Codex against an OSAC checkout (install, `/import`, permissions, hook trust,
  MCP, and workflow differences).
- [RELEASING.md](RELEASING.md) — how nightly builds work, how to cut a real
  `osac` release, and how to release a new version of a single component.

Regeneration tooling for those two documents is unconfirmed. Any future
regeneration must preserve their cross-component-only scope rather than
replace them with an unfiltered snapshot.

## Terms and Definitions

ACM: Advanced Cluster Management (ACM) is a Red Hat product that simplifies the
provisioning and management of multiple Kubernetes (OpenShift) clusters.

Cloud Service Provider: CSPs are organizations that offer compute resources for
rent to multiple unrelated tenants / customers.

HCP: (HyperShift) Hosted Control Plane refers to an architecture where the
control plane of an OpenShift cluster is decoupled from the worker nodes and run
as Pods on a separate hosting cluster.

MOC: The MassOpen Cloud (MOC) is a public computing cloud where the Open
Sovereign AI Cloud is being deployed.

Tenant: A user or group of users at the same organization with the ability to
self-service provision cloud assets, including VMs and clusters. Acts as the
administrator for infrastructure that they provision. An end user of the OSAC
solution, and a customer of the CSP.

See [personas.md](personas.md) for a description of OSAC personas.

## Contribution Guide

The Open Sovereign AI Cloud [organization on
GitHub](https://github.com/osac-project) is a public location to plan and develop
this solution. Major features, architectural changes, and significant initiatives are proposed and discussed in the [enhancement-proposals](https://github.com/osac-project/enhancement-proposals) repository. GitHub [issues](https://github.com/osac-project/issues/issues) are also used to track community-reported bugs, feature requests, and other project discussions.

This documentation lives in the [osac-project/osac](https://github.com/osac-project/osac)
mono-repo, under `docs/` — it is no longer a separate repository. To
contribute a documentation change, follow the same workflow as any other
change to that mono-repo: first find or open an issue on
https://github.com/osac-project/issues. Prior to beginning work, it is wise to get
feedback from project stakeholders on the change and how it will be implemented.
Then fork `osac-project/osac`, clone it to your local machine, and create a
descriptive feature branch name. Once you’ve made your changes and ensured
that it passes any lint checks, push and open a pull request against `main`
on the upstream repo, linking the issue you opened and summarizing what you
changed. Then your pull request will eventually be reviewed, and you can
respond to comments, add follow-up commits, and re-run tests until all
required checks are green. If the team accepts the pull request, your
contribution will be merged and you will have successfully contributed to
the OSAC project.
