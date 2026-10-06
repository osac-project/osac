# Connect Codex and MCP Inspector to a local OSAC MCP endpoint

This guide applies to the opt-in MCP development endpoint introduced by
[OSAC-5841](https://redhat.atlassian.net/browse/OSAC-5841). It currently
offers `list_resources`, `get_resource`, `create_compute_instance`, and
`delete_compute_instance`. The write tools are experimental; use a development
deployment and an account authorized for the intended tenant. The endpoint is
disabled by default.

The [MCP design](https://github.com/osac-project/enhancement-proposals/pull/341)
assigns the supported deployment contract, separate OAuth clients for each
host, and the `/connect/mcp` setup page to later work, including
[OSAC-5845](https://redhat.atlassian.net/browse/OSAC-5845). This guide takes
its connection values from a running development deployment and does not
define those future interfaces.

## Install the Kind development endpoint

From the repository root, a fresh `dev-full` installation with MCP enabled is
one Make command:

```bash
export PROFILE=dev-full
export KUBECONFIG="$HOME/.kube/osac-dev-kind-root.kubeconfig"
make -C osac-installer install \
  PLATFORM=kind PROFILE="$PROFILE" NS=osac \
  EXTRA_HELM_ARGS='--set service.mcp.enabled=true --set-string service.mcp.externalHostname=mcp.osac.localhost --set service.mcp.externalPort=8443'
```

This profile also installs the local VM stack, seeds a sample catalog, and
onboards `tenant1`; sign in as `tenant1_user` or `tenant1_admin` to see its
resources. The smaller `PROFILE=dev` profile installs the control plane but
does not seed a tenant or any of the nine MCP resource types, so empty lists
are expected on a fresh installation.

MCP runs from the **Fulfillment service image**; it has no separate image to
build or push. For a fresh cluster with a local source build, create the Kind
infrastructure first, then build and load the image before installing OSAC.
Use the same container runtime throughout (see the [installer's local-image
instructions](../../../osac-installer/README.md#full-local-dev-environment-profiledev-full-kind-only)):

```bash
export PROFILE=dev-full
export KUBECONFIG="$HOME/.kube/osac-dev-kind-root.kubeconfig"
export CONTAINER_TOOL=docker  # Or podman, if that is your Kind runtime.
export KIND_EXPERIMENTAL_PROVIDER="$CONTAINER_TOOL"
make -C osac-installer install-infra \
  PLATFORM=kind PROFILE="$PROFILE" NS=osac
make -C fulfillment-service image-build \
  IMG=localhost/fulfillment-service:osac-5841 CONTAINER_TOOL="$CONTAINER_TOOL"
make -C fulfillment-service kind-load-image \
  IMG=localhost/fulfillment-service:osac-5841 CONTAINER_TOOL="$CONTAINER_TOOL"
make -C osac-installer install-osac \
  PLATFORM=kind PROFILE="$PROFILE" NS=osac \
  EXTRA_HELM_ARGS='--set service.mcp.enabled=true --set-string service.mcp.externalHostname=mcp.osac.localhost --set service.mcp.externalPort=8443 --set-string service.images.service.repository=localhost/fulfillment-service --set-string service.images.service.tag=osac-5841 --set service.images.service.pullPolicy=Never'
make -C osac-installer install-devstack \
  PLATFORM=kind PROFILE="$PROFILE" NS=osac
```

On an existing `dev-full` cluster, skip `install-infra` and `install-devstack`
if they are already installed. After rebuilding, `kind-load-image` restarts
workloads that already use the same image tag; run `install-osac` to apply a
new image tag or MCP values. The image override applies to both the
Fulfillment API and MCP server. A `localhost/` image with `pullPolicy=Never`
must be loaded into Kind; no registry push is needed.

## Get connection values

Ask the deployment operator for:

- The **externally reachable HTTPS MCP URL** configured with
  `service.mcp.enabled`, `service.mcp.externalHostname`, and, if needed,
  `service.mcp.externalPort`. The URL must be reachable from the machine
  running Codex. An in-cluster service name is not a workstation URL.
- The HTTPS OAuth issuer URL reachable from both Codex and the browser.
- A public OAuth client ID. The development Keycloak fixture provides
  `osac-mcp-client` when `devFixtures.enabled` is set. That shared fixture is
  for development only; a later installer story will provide host-specific
  clients.
- The CA bundle that signs the MCP and issuer certificates, if your
  workstation does not already trust them. For the development installation,
  the operator can obtain the public CA bundle from the `ca-bundle` ConfigMap
  in the OSAC namespace, key `bundle.pem`. It contains CA certificates, not
  a private key. Keep it outside the repository.

For example, the operator can extract that bundle with:

```bash
oc -n <osac-namespace> get configmap ca-bundle \
  -o go-template='{{ index .data "bundle.pem" }}' > /path/to/osac-ca-bundle.pem
```

For the local Kind `PROFILE=dev-full` installation, use this copyable path
outside the repository and verify both HTTPS endpoints:

```bash
export KUBECONFIG="$HOME/.kube/osac-dev-kind-root.kubeconfig"
export OSAC_MCP_URL='https://mcp.osac.localhost:8443'
export OSAC_ISSUER_URL='https://keycloak.osac.localhost:8443/realms/osac'
mkdir -p "$HOME/.config/osac"
kubectl -n osac get configmap ca-bundle \
  -o go-template='{{ index .data "bundle.pem" }}' \
  > "$HOME/.config/osac/ca-bundle.pem"
export CODEX_CA_CERTIFICATE="$HOME/.config/osac/ca-bundle.pem"
openssl x509 -in "$CODEX_CA_CERTIFICATE" -noout -subject
curl --fail --show-error --cacert "$CODEX_CA_CERTIFICATE" \
  "$OSAC_MCP_URL/.well-known/oauth-protected-resource"
curl --fail --show-error --cacert "$CODEX_CA_CERTIFICATE" \
  "$OSAC_ISSUER_URL/.well-known/openid-configuration"
```

Launch Codex from a shell with that environment variable set.

For `PROFILE=dev`, use `$HOME/.kube/osac-dev-kind.kubeconfig` and
`https://keycloak.keycloak.svc.cluster.local:8443/realms/osac` instead. That
issuer's hostname needs a workstation `/etc/hosts` entry mapping it to
`127.0.0.1`; `dev-full` uses `keycloak.osac.localhost` and needs no such entry.

For another deployment, replace these placeholders and verify its endpoints:

```bash
export OSAC_MCP_URL='https://<mcp-host>'
export OSAC_ISSUER_URL='https://<issuer-host>/realms/osac'
export CODEX_CA_CERTIFICATE='/path/to/osac-ca-bundle.pem'

curl --fail --show-error --cacert "$CODEX_CA_CERTIFICATE" \
  "$OSAC_MCP_URL/.well-known/oauth-protected-resource"
curl --fail --show-error --cacert "$CODEX_CA_CERTIFICATE" \
  "$OSAC_ISSUER_URL/.well-known/openid-configuration"
```

The first response should identify the MCP resource and its authorization
server. Use the actual issuer from the deployment rather than assuming the
example realm path. If either request fails, resolve DNS, routing, or CA trust
before starting OAuth. `CODEX_CA_CERTIFICATE` is Codex's documented PEM CA
bundle setting and also applies to its HTTPS and OAuth traffic; set it in the
environment that launches Codex. If the system already trusts both endpoints,
omit the CA export and the `--cacert` options. Do not disable TLS verification.

For either Kind profile, set
`service.mcp.externalHostname=mcp.osac.localhost` and
`service.mcp.externalPort=8443`. Its MCP URL is
`https://mcp.osac.localhost:8443`. The `dev-full` issuer is
`https://keycloak.osac.localhost:8443/realms/osac`; the plain `dev` issuer and
its host mapping are noted above. The browser must also trust the development
CA.

## Register the callback and sign in

For the Kind development installation, the `osac-mcp-client` fixture already
allows `http://localhost:8091/callback`. Configure Codex with that fixed
callback in `~/.codex/config.toml`:

```toml
[mcp_servers.osac]
url = "https://mcp.osac.localhost:8443"
default_tools_approval_mode = "writes"

[mcp_servers.osac.oauth]
client_id = "osac-mcp-client"
callback_url = "http://localhost:8091/callback"
callback_port = 8091
```

If `codex mcp add` already created these tables, edit the existing entries
instead of adding duplicate tables. Both callback settings matter: the port
in `callback_url` does not configure Codex's local listener. Keycloak must
accept the exact callback Codex sends. The development client uses
authorization code with PKCE and has no client secret.

For another development deployment, `codex mcp add` can set the public client
ID, but it has no dedicated callback URL or port flags:

```bash
codex mcp add osac --url "$OSAC_MCP_URL" \
  --oauth-client-id osac-mcp-client
```

Replace `osac-mcp-client` if the deployment uses a different public client.
Register the **exact callback URL printed by Codex** as an allowed redirect
URI for that client's Keycloak registration before login; the fixture's 8091
entry does not automatically cover a different Codex callback. Do not add a
wildcard redirect. The CLI's `-c` options override configuration for that
invocation and do not persist a server-specific callback port; use
`config.toml` for a repeatable fixed callback. See the
[Codex MCP documentation](https://developers.openai.com/codex/mcp) for callback
selection and issuer support.

Then authenticate as the intended OSAC caller:

```bash
codex mcp login osac
codex mcp list
```

Set `default_tools_approval_mode = "writes"` in the existing
`[mcp_servers.osac]` table if you used `codex mcp add`; it is included in the
Kind example above. This makes Codex ask before invoking tools that are not
marked read-only.
Restart Codex, use `/mcp` to confirm the connection and four available tools,
and try a read request first. For a VM create or delete, inspect the target and
check that Codex presents a separate approval prompt for the write call. A
conversation request alone is not the host approval step. OSAC still
authorizes each call as the signed-in user through the public Fulfillment API.

## Explore with MCP Inspector

The earlier OSAC-4388 prototype launched Inspector with `NODE_EXTRA_CA_CERTS`
pointing at the same public Kind `ca-bundle` ConfigMap used for Codex. Node
does not use `curl --cacert` or `CODEX_CA_CERTIFICATE`. For either Kind profile,
use the CA file extracted above. The commands set their own
MCP URL and CA path so they also work in a new shell. They create only a
temporary Inspector configuration containing the public client ID and MCP
URL. A separate temporary storage directory prevents an earlier Inspector
login from reusing cached OAuth discovery for a different Kind profile:

```bash
export OSAC_MCP_URL='https://mcp.osac.localhost:8443'
export CODEX_CA_CERTIFICATE="$HOME/.config/osac/ca-bundle.pem"
openssl x509 -in "$CODEX_CA_CERTIFICATE" -noout -subject
INSPECTOR_DIR="$(mktemp -d "${TMPDIR:-/tmp}/osac-mcp-inspector.XXXXXX")"
mkdir -p "$INSPECTOR_DIR/storage"
jq -n --arg url "$OSAC_MCP_URL" \
  '{mcpServers:{osac:{type:"http",url:$url,oauth:{clientId:"osac-mcp-client"}}}}' \
  > "$INSPECTOR_DIR/inspector-osac.config.json"
jq -e '.mcpServers.osac.url != ""' "$INSPECTOR_DIR/inspector-osac.config.json"
MCP_STORAGE_DIR="$INSPECTOR_DIR/storage" \
  NODE_EXTRA_CA_CERTS="$CODEX_CA_CERTIFICATE" \
  npx --yes @modelcontextprotocol/inspector@2.6.0 \
    --config "$INSPECTOR_DIR/inspector-osac.config.json" --server osac
```

In the Inspector web UI, connect to `osac`, leave **OAuth Client Metadata
Document** empty, and complete Keycloak login. Its browser callback is
`http://localhost:6274/oauth/callback`, which the development client allows.
The Tools tab should list the four available tools after login.
The browser must trust the Kind CA separately; `NODE_EXTRA_CA_CERTS` applies
to the Node process. `PROFILE=dev-full` uses `keycloak.osac.localhost` as its
issuer, while `PROFILE=dev` uses `keycloak.keycloak.svc.cluster.local`.

## When the connection fails

- A certificate error usually means the MCP route or OAuth issuer is not
  covered by the CA bundle available to the Codex process. Check both `curl`
  requests above and the process environment. A successful `codex mcp login`
  does not prove the shared app server can use the same CA. A desktop app
  launched outside the shell may not inherit a shell export. The Codex CLI
  may also reuse a background app server started before
  `CODEX_CA_CERTIFICATE` was set. On macOS, `launchctl setenv` makes the CA
  setting available to future GUI-launched processes, but does not change an
  already running daemon. To isolate daemon inheritance, run
  `codex --no-daemon resume --last` from the shell with the CA export. If that
  works, exit active Codex sessions and restart the shared daemon from an
  ordinary macOS Terminal:

  ```bash
  export CODEX_CA_CERTIFICATE="$HOME/.config/osac/ca-bundle.pem"
  launchctl setenv CODEX_CA_CERTIFICATE "$CODEX_CA_CERTIFICATE"
  codex app-server daemon stop
  codex app-server daemon start
  codex resume --last
  ```

  Stopping the daemon disconnects other active Codex sessions. If `/mcp` still
  reports zero tools, check that `stop` and `start` succeeded and that the
  previous daemon process exited; the Codex CLI can otherwise reconnect to
  that old process.
- If Inspector opens the old `keycloak.osac.localhost` issuer after switching
  to `PROFILE=dev`, compare the MCP protected-resource metadata with the
  browser URL. Inspector caches OAuth discovery and tokens per MCP URL. Use
  the temporary `MCP_STORAGE_DIR` above for a fresh login, or choose **Clear
  OAuth state and disconnect** under **Server Settings → Authorization** for
  that server. Clearing the stored state requires a new login.
- If Inspector reports `Failed to create transport: Invalid URL`, inspect
  `.mcpServers.osac.url` in the temporary config. An empty value means
  `OSAC_MCP_URL` was unset when `jq` created it; rerun the complete Inspector
  block above.
- An `Invalid parameter: redirect_uri` page means Keycloak rejected the
  callback Codex sent. The Kind development client already allows
  `http://localhost:8091/callback`; configure both `callback_url` and
  `callback_port` as shown above, or register the exact callback printed by
  `codex mcp add`. Avoid a broad redirect wildcard.
- A successful login with denied tools means the caller lacks the required
  tenant or resource authorization. Use an appropriately authorized account;
  do not replace the caller with a privileged service token.

For current Codex OAuth and CA behavior, consult the official
[MCP setup](https://developers.openai.com/codex/mcp) and
[custom CA](https://learn.chatgpt.com/docs/auth#custom-ca-bundles) guidance.
