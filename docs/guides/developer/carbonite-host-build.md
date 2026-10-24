# Optional host image builds from Carbonite

This is a separate, one-time setup phase for developers who want a Carbonite
Level 3 sandbox to request approved image builds in the host's rootless Podman
store. It is **opt-in** and is not part of `tools/bootstrap.sh`. The host
dispatcher accepts a fixed set of component image builds. Registry pushes and
the AAP execution-environment build are outside this phase.

The Carbonite sandbox itself still requires host Podman and the local OpenShell
Podman gateway described in the [root README](../../../README.md). The host
build channel is for OSAC *component* images. A Carbonite image published by
GitHub CI/CD is a separate artifact; Docker runtime support for this local
sandbox remains deferred.

## First-time setup phases

1. From a trusted OSAC checkout, run `tools/bootstrap.sh` for the ordinary
   development environment. Before this opt-in, review the repository enabler,
   manager, and dispatcher at a trusted revision: the sandbox can edit the
   checkout that holds their source, so executing a changed copy on the host
   would bypass the intended restriction. The first enrollment copies the
   reviewed manager and dispatcher outside that checkout.
2. Install and connect the local OpenShell Podman gateway, then run
   `make check-carb`. The gateway must run on the same machine as the checkout
   and host Podman store; a remote gateway is not supported by this channel.
3. Opt in **once on the host** from the OSAC checkout root:

   ```bash
   tools/carbonite/enable-host-build.sh \
     --gateway openshell --workspace default --sandbox osac-carbonite \
     --checkout "$PWD"
   ```

   The enabler creates a dedicated Ed25519 key, pins the local SSH host key,
   installs a forced-command dispatcher and management copy outside the
   sandbox-mounted checkout, and adds one marked key to the selected host
   account's `~/.ssh/authorized_keys`. It does not place keys in Git, in the
   Carbonite image, or in `tools/bootstrap.sh`. It binds this opt-in to the
   gateway, workspace, sandbox, and canonical checkout. Re-running the
   enabler is not part of ordinary shell startup; use the installed host-only
   manager for later maintenance.
4. Enter through `make enter-carb` (or `make claude-carb`). On each entry, the
   installed manager rechecks that binding, adds only the required OpenShell
   `/usr/bin/ssh` access to `host.openshell.internal:22`, uploads the short-lived
   sandbox copy of the key and host pin, and sends a side-effect-free `probe`.
   It repeats this reconciliation after sandbox recreation without generating
   a new host key pair. If preparation fails, shell entry fails closed.
5. Inside Carbonite, run `make -C osac-ui`. This sends the literal `ui` action
   over SSH. The installed host dispatcher builds the fixed image
   `ghcr.io/osac-project/osac-ui:latest` from the bound checkout's
   `osac-ui/Containerfile`. Check it from the host with:

   ```bash
   podman image inspect ghcr.io/osac-project/osac-ui:latest
   ```

   The ordinary host or Distrobox `make -C osac-ui` path still calls Podman
   directly. Sandbox image-tag, Containerfile, version, and container-tool
   overrides are rejected for these fixed recipes.

The other fixed build requests are listed below. They retain direct Podman
recipes on the host and in Distrobox. On 2026-10-08, all listed requests and
the default `make -C osac-ui` target built through a disposable sandbox, and
their expected images appeared in the host Podman store.

| Carbonite Make target | Fixed host image |
| --- | --- |
| `make -C fulfillment-service image-build` | `ghcr.io/osac-project/fulfillment-service:latest` |
| `make -C osac-operator image-build` | `ghcr.io/osac-project/osac-operator:latest` |
| `make -C osac-metering/metering-service image-build` | `ghcr.io/osac-project/metering-service:latest` |
| `make -C osac-metering/adapters image-build-m360-adapter` | `ghcr.io/osac-project/metering-m360-adapter:latest` |
| `make -C osac-metering/adapters image-build-echo-adapter` | `ghcr.io/osac-project/metering-echo-adapter:latest` |
| `make -C osac-installer/charts/osac-devstack build-cli-tools` | `ghcr.io/osac-project/cli-tools:latest` |

The CLI-tools recipe builds a local manifest containing both `linux/amd64` and
`linux/arm64`; its host Podman setup must support both architectures. The
ordinary host/Distrobox push target uses `podman manifest push --all` to publish
both. The devstack `push-cli-tools` and `build-push-cli-tools` targets fail
inside Carbonite before building or pushing.

## What the SSH channel permits

The sandbox gets only the dedicated client key. Its matching host key is
restricted to the installed dispatcher and cannot request an arbitrary shell,
Podman command, port forward, or registry push. The dispatcher accepts `probe`
and the build actions listed above; all other action strings fail. The installed dispatcher and manager
are outside `/sandbox/osac`, where the sandbox can edit files.

This PoC uses the host's **existing SSH service on port 22**. The forced command
limits the dedicated build key, but other authentication methods configured on
that SSH service are also reachable from the opted-in sandbox. Review the
host's SSH configuration separately. The OpenShell rule applies only to
`/usr/bin/ssh` and the gateway host's port 22; it is removed when the opt-in is
revoked. Do not give the sandbox a Podman or Docker socket.

The sandbox can edit the mounted source and `Containerfile`. A fixed host build
still processes those edits, just as a manual host image build would. It can
also edit the root `Makefile` and Carbonite image recipe. **Running `make
enter-carb`, `make image-carb`, or a setup script from that checkout on the host
executes sandbox-writable instructions.** Review those files against a trusted
revision before each host invocation. The forced-command key limits requests
made through that key; it does not protect a separate host command that reads
the editable checkout. A host-only trusted entry launcher is future hardening.
Also note that a Git worktree outside the bound checkout is not yet supported;
the helper must reject it instead of silently building the wrong tree.

## Revoke or repair

Use the **installed host-only manager**, not a possibly sandbox-edited copy of
the repository enabler, to revoke the capability:

```bash
"$HOME/.local/libexec/osac-host-build-manager" revoke
```

Revocation removes the dedicated host authorization first, then the sandbox
credential and network rule. If sandbox cleanup cannot be verified, delete
the affected sandbox before considering the opt-in fully removed. Re-enabling
or rotating a key is a deliberate host action, not a per-shell update. The
reviewed manager, dispatcher, and installation receipt remain in the host-only
directory after revocation; the active key, pin, account file, and enabled
binding are removed.

To adopt a newer reviewed dispatcher or manager, revoke first, then run the
explicit upgrade from the checkout on the host and enable the desired binding
again:

```bash
"$HOME/.local/libexec/osac-host-build-manager" revoke
tools/carbonite/enable-host-build.sh --upgrade
tools/carbonite/enable-host-build.sh \
  --gateway openshell --workspace default --sandbox osac-carbonite \
  --checkout "$PWD"
```

The upgrade refuses an active key or binding and verifies the previous
installed-code receipt. It never silently replaces installed code during shell
entry. Review the checkout sources before running `--upgrade`, because that
explicit step executes code from the checkout on the host. An interrupted
upgrade can be retried while the key remains revoked.
An older prototype installed without an installation receipt cannot use this
upgrade path; revoke its key and inspect/remove that old host-only installation
before enrolling the reviewed version.

If entry reports a gateway or checkout mismatch, check the selected gateway,
workspace, sandbox name, and canonical checkout path; do not copy the key to a
different gateway. If SSH host-key verification fails, verify the host's
actual served Ed25519 key before changing the pin. If a build fails, inspect
the fixed image recipe and host Podman output; the SSH grant does not make
missing build dependencies or registry access available inside Carbonite.
If entry fails partway through preparation, the manager removes the host key
authorization first. After resolving the cause, use the **installed** manager
with its original binding to restore authorization, then enter again:

```bash
"$HOME/.local/libexec/osac-host-build-manager" enable \
  --gateway openshell --workspace default --sandbox osac-carbonite \
  --checkout "$PWD"
```

`osac-aap` execution-environment builds are deferred. Their current Make
target runs `uv` and `ansible-builder` on the host. The future approach is to
generate the EE context inside Carbonite, transfer it to validated host-owned
staging, then request a fixed host image build. That transfer and staging
boundary must be tested before enabling the action.
