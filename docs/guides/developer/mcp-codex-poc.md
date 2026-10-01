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

## Register the callback and sign in

The development Keycloak client has redirect URIs from the original PoC
script. Current Codex may use a different callback. Add the MCP server with
the public client ID and copy the **exact callback URL printed by Codex**:

```bash
codex mcp add osac --url "$OSAC_MCP_URL" \
  --oauth-client-id osac-mcp-client
```

In the development Keycloak realm, register that exact callback as an allowed
redirect URI for `osac-mcp-client` before login. Keep the client public with
authorization code + PKCE; do not add a client secret or a wildcard redirect.
If you use a different client, substitute its ID above. Codex can choose a
server-specific callback path when the issuer does not advertise issuer-bound
responses. If you need a fixed local callback port, configure both the
callback URL and `oauth.callback_port` as described in the
[Codex MCP documentation](https://developers.openai.com/codex/mcp).

Then authenticate as the intended OSAC caller:

```bash
codex mcp login osac
codex mcp list
```

In `~/.codex/config.toml`, add this key to the **existing** `[mcp_servers.osac]`
table created by `codex mcp add`:

```toml
default_tools_approval_mode = "writes"
```

This makes Codex ask before invoking tools that are not marked read-only.
Restart Codex, use `/mcp` to confirm the connection and four available tools,
and try a read request first. For a VM create or delete, inspect the target and
check that Codex presents a separate approval prompt for the write call. A
conversation request alone is not the host approval step. OSAC still
authorizes each call as the signed-in user through the public Fulfillment API.

## When the connection fails

- A certificate error usually means the MCP route or OAuth issuer is not
  covered by the CA bundle available to the Codex process. Check both `curl`
  requests above and the process environment. A desktop app launched outside
  the shell may not inherit a shell export.
- A redirect mismatch means the URI accepted by Keycloak differs from the
  callback Codex printed or the active listener port. Compare the exact URI;
  avoid a broad redirect wildcard.
- A successful login with denied tools means the caller lacks the required
  tenant or resource authorization. Use an appropriately authorized account;
  do not replace the caller with a privileged service token.

For current Codex OAuth and CA behavior, consult the official
[MCP setup](https://developers.openai.com/codex/mcp) and
[custom CA](https://learn.chatgpt.com/docs/auth#custom-ca-bundles) guidance.
