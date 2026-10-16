# OSAC test cluster and Codex MCP access

This guide summarizes the OSAC test-cluster setup and remote Codex MCP access
worked through in this session. Exact routes, bastion names, ingress addresses,
and kubeconfig paths depend on the host. Read them from `cluster-tool` and the
local Codex configuration; do not copy live credentials into this repository.

For the local Kind MCP setup, see [Connect Codex and MCP Inspector to a local
OSAC MCP endpoint](./mcp-local-clients.md).

## Cluster access

The `cluster-tool` inventory showed `dev` and `mcp`; `mcp` was the cluster used
for the deployed MCP service. Find the configured server and running clusters
with:

```sh
cluster-tool servers
cluster-tool list --server <server-alias>
cluster-tool verify mcp --server <server-alias>
```

The kubeconfig observed for `mcp` in this session was
`$HOME/.kube/mcp.kubeconfig`. `cluster-tool boot` prints the kubeconfig path
for a newly booted cluster; use that path explicitly rather than relying on
whatever context `oc` currently selects.

```sh
export KUBECONFIG="$HOME/.kube/mcp.kubeconfig"
oc config current-context
oc whoami
oc get nodes
oc get co
oc get tenants -A
oc get pods -n <osac-namespace>
```

For service troubleshooting, list deployments and then read the relevant
deployment's logs:

```sh
oc get deployments -n <osac-namespace>
oc logs -n <osac-namespace> deployment/<deployment> \
  --all-containers --tail=100
```

Use `cluster-tool` for supported cluster lifecycle operations. Use `oc` for
cluster troubleshooting and inspection. For tenant-facing resource discovery
and lifecycle work, use the OSAC MCP tools when available. Inspect the catalog
item and selectable references before creation, and approve each write when
Codex prompts.

## Network path from another machine

The Codex host needs HTTPS access to both the MCP endpoint and the Keycloak
issuer advertised by its OAuth metadata. When direct DNS/routing is not
available, the session used a local SSH forward to cluster ingress. Replace
the placeholders with values for the current environment:

```sh
sudo --preserve-env=SSH_AUTH_SOCK ssh \
  -o ExitOnForwardFailure=yes \
  -N -L 127.0.0.1:443:<cluster-ingress-address>:443 \
  -i <identity-file> <ssh-user>@<reachable-bastion>
```

Keep the forward running while Codex is using MCP. Map the configured MCP and
Keycloak hostnames to the local forwarded address when the client cannot
resolve them through the cluster DNS. Verify that port 443 is listening:

```sh
sudo ss -ltnp 'sport = :443'
```

`ExitOnForwardFailure=yes` catches a local bind failure. A listening local
port does not prove SSH reached the bastion or that the cluster ingress is
reachable. A previous SSH connection attempt timed out, so check port 22
reachability if the forward command hangs or exits before creating the
listener.

Port 443 matched the configured HTTPS URLs and hostname routing in this
setup. A different local port is possible only if the MCP and Keycloak URLs
and routing are also configured to use it.

## Codex configuration and login

In the Codex user's `~/.codex/config.toml`, preserve the existing server URL
and set the requested OAuth scope to `offline_access`:

```toml
[mcp_servers.osac]
enabled = true
url = "https://<mcp-route-host>/"
scopes = ["offline_access"]
```

This server's login worked with only the `offline_access` scope. See the
[Codex MCP configuration guide](https://developers.openai.com/codex/mcp/) for
the supported CLI and `config.toml` setup.

The Codex host must trust the CA that signs the MCP and Keycloak routes. The
bundle used in this session was `/tmp/osac-codex-ca-bundle.pem`; because it is
under `/tmp`, it may need to be recreated after cleanup. Set the path in the
same shell that starts the login and Codex process:

```sh
export SSL_CERT_FILE=/path/to/osac-codex-ca-bundle.pem
codex mcp login --scopes offline_access osac
codex mcp list
```

If the Codex host has no browser, use manual callback mode from the same shell
that has the CA setting:

```sh
export SSL_CERT_FILE=/path/to/osac-codex-ca-bundle.pem
codex mcp login osac --scopes offline_access --no-browser
```

Open the printed authorization URL on a browser-capable device and sign in.
Copy the full callback URL from the browser address bar and paste it into the
waiting CLI prompt. If the callback page does not load because its
`127.0.0.1:<port>` points to the browser device, the full URL can still be
pasted; manual callback mode does not require forwarding that browser's
loopback port to the Codex host. Do not save or share the callback URL because
it contains a one-time authorization code. If using normal browser-launch
mode instead, run Codex in a shell where it can launch a browser and receive
the callback on its own loopback listener.

After authentication, start Codex in the OSAC checkout:

```sh
cd /path/to/osac
export SSL_CERT_FILE=/path/to/osac-codex-ca-bundle.pem
codex
```

Check `codex mcp list` for the enabled `osac` server and its OAuth state. Login
is stored under the Codex user's home, so run login and new sessions as the
same Unix user with the same `CODEX_HOME` and CA environment. Seeing the server
or tool names alone does not prove an MCP request works. Verify from a fresh,
read-only CLI process with a call such as:

```sh
SSL_CERT_FILE=/path/to/osac-codex-ca-bundle.pem \
  codex exec --ephemeral --sandbox read-only -C /path/to/osac \
  'Call osac.list_resources with resource_type=compute_instance_catalog_item and page_size=1. Report whether the call succeeds.'
```

## What we found and changed

- `cluster-tool list` showed the `dev` and `mcp` clusters. The OSAC MCP
  deployment was running on `mcp`.
- The MCP and Keycloak HTTPS routes were reachable from the host after using
  the configured CA bundle. A separate remote client needed DNS and tunnel
  setup; its local DNS pointed the route names at loopback, and the SSH
  forward was not always listening.
- A tenant mapping was corrected in the live Keycloak realm by adding the
  test account to its intended organization and `/members` group. This was a
  runtime change, not a checked-in realm seed change. A fresh login is needed
  to receive the updated membership, and a recreated cluster may need the
  mapping seeded again.
- A broad scope request was rejected with `invalid_scope`; the Codex
  configuration was changed to request only `offline_access`. An earlier
  dynamic client-registration attempt was also rejected by Keycloak policy.
- The Keycloak client `osac-mcp-client` initially had fixed-port callback
  entries (`127.0.0.1:6274` and `localhost:8091`). Codex used an ephemeral
  loopback port (`45357` or `45991`) with a `/callback/<nonce>` path, so
  Keycloak rejected `redirect_uri`. We added
  `http://127.0.0.1/callback/*` to the live client's redirect list. Keycloak
  documents loopback redirects for native apps that bind a random port in its
  [OIDC documentation](https://www.keycloak.org/securing-apps/oidc-layers).
  This was a live Keycloak edit; no realm seed or deployment source was changed
  in this checkout.
- The completed login used `SSL_CERT_FILE=/tmp/osac-codex-ca-bundle.pem` and
  only `offline_access`:
  `codex mcp login osac --scopes offline_access --no-browser`. Sign-in in a
  separate browser succeeded after pasting the full callback URL into the
  waiting CLI. The browser callback page itself did not need to load.
- Verification on 2026-10-15 confirmed that both issuer metadata and MCP
  protected-resource metadata validated with the CA bundle. A fresh
  `codex exec --ephemeral --sandbox read-only` process then successfully made
  an `osac.list_resources` call. `codex mcp list` and seeing tool names are
  useful configuration checks, but the successful read-only request is the
  end-to-end health check.
- Two attempts to create `test-1` returned `public API request failed`; the VM
  list was empty afterward. No successful VM creation was confirmed. Check
  the catalog and tenant-selectable resources through MCP before trying again.

Do not store passwords, pull secrets, OAuth tokens, private keys, or live
infrastructure credentials in this guide or in the repository.
