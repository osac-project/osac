# Connect Codex to the experimental OSAC MCP endpoint

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

For the local Kind `PROFILE=dev` installation, use this copyable path outside
the repository:

```bash
export KUBECONFIG="$HOME/.kube/osac-dev-kind.kubeconfig"
mkdir -p "$HOME/.config/osac"
kubectl -n osac get configmap ca-bundle \
  -o go-template='{{ index .data "bundle.pem" }}' \
  > "$HOME/.config/osac/ca-bundle.pem"
export CODEX_CA_CERTIFICATE="$HOME/.config/osac/ca-bundle.pem"
```

Launch Codex from a shell with that environment variable set.

Verify TLS and the MCP discovery document before configuring Codex. Replace
the placeholders with the values for your deployment:

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

For the Kind `PROFILE=dev` installation, set
`service.mcp.externalHostname=mcp.osac.localhost` and
`service.mcp.externalPort=8443`. Its MCP URL is
`https://mcp.osac.localhost:8443`. The Keycloak issuer is
`https://keycloak.keycloak.svc.cluster.local:8443/realms/osac`; on the
workstation, map `keycloak.keycloak.svc.cluster.local` to `127.0.0.1` in
`/etc/hosts` so Codex and the browser can reach that issuer through the Kind
gateway. The browser must also trust the development CA. The `PROFILE=dev-full`
values instead configure a browser-facing `keycloak.osac.localhost` issuer.

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

## When the connection fails

- A certificate error usually means the MCP route or OAuth issuer is not
  covered by the CA bundle available to the Codex process. Check both `curl`
  requests above and the process environment. A desktop app launched outside
  the shell may not inherit a shell export. The Codex CLI may also reuse a
  background app server started before `CODEX_CA_CERTIFICATE` was set. To use
  the current shell's CA setting without that daemon, exit Codex and run
  `codex --no-daemon resume --last`, then check `/mcp` again.
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
