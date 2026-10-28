# Carbonite sandbox credentials (GitHub, Jira, Codex and Claude)

The sandbox holds no GitHub, Jira or Codex token. OpenShell keeps those in the
gateway as *providers*: the sandbox environment contains opaque placeholders
(for example `openshell:resolve:env:...`), and the OpenShell proxy swaps in the
real value on requests to the endpoints named in the provider's *profile*.
Claude Code through Vertex AI is the one exception, described below: its Google
refresh token is copied into the sandbox.
Do not pass tokens with `CARBONITE_CREATE_ARGS='--env ...'`: that would put
them in the sandbox specification.

A token reaches a request only when both checks pass: the sandbox network
policy allows the binary and destination, and the profile's endpoints cover
the host, port and path. This repository ships profiles for `gh`, `jira` and
OpenAI (Codex) in
`tools/carbonite/providers/`.

## One-time setup (per gateway workspace)

Profiles and providers live in the gateway, so they survive sandbox
rebuilds. Run these on the host, with the same gateway and workspace the
Makefile uses (`openshell` and `osac-kind` by default).

1. Import the GitHub profile, and the Jira profile after rendering your Jira
   host into it. The shipped Jira profile holds the reserved placeholder
   `jira.internal.example`; keep the rendered copy outside the repository:

   ```bash
   G="openshell --gateway openshell --workspace osac-kind"
   STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/osac-carbonite-kind"
   mkdir -p "$STATE_DIR/providers"

   $G profile import -f tools/carbonite/providers/osac-github.yaml

   CARBONITE_JIRA_HOST=<your-jira-host> tools/carbonite/render-policy.sh \
     tools/carbonite/providers/osac-jira.yaml \
     "$STATE_DIR/providers/osac-jira.yaml"
   $G profile import -f "$STATE_DIR/providers/osac-jira.yaml"
   ```

2. Create one provider per profile. A `--credential` key with no value reads
   the token from your environment, so it never appears on a command line.
   Use single quotes if you type a token (double quotes let the shell
   interpret it, and any quote characters would become part of the token):

   ```bash
   export GITHUB_TOKEN='<github token>'
   export JIRA_API_TOKEN='<jira api token>'

   $G provider create --name osac-github --type osac-github \
     --credential GITHUB_TOKEN
   $G provider create --name osac-jira --type osac-jira \
     --credential JIRA_API_TOKEN

   unset GITHUB_TOKEN JIRA_API_TOKEN
   ```

   Use an unscoped Jira Cloud API token (id.atlassian.com, Security, API
   tokens). Scoped tokens do not work with basic auth against the site URL.
   Jira tokens inherit your Jira permissions and cannot be narrowed; for
   GitHub, prefer a fine-grained token limited to the repositories you need.

3. Make new sandboxes pick the providers up by exporting this, for example
   from your shell profile:

   ```bash
   export CARBONITE_PROVIDERS="osac-github osac-jira"
   ```

To rotate a token later, export it and run
`$G provider update osac-jira --credential JIRA_API_TOKEN --wait`.

## Every sandbox creation (automatic)

- `CARBONITE_PROVIDERS` attaches each listed provider when `make enter-carb`
  or `make rebuild-carb` creates the sandbox. Attachments belong to one
  sandbox and are revoked when it is deleted, so this is what keeps them
  across rebuilds.
- If `jira-cli` is configured on the host, creation also copies its config
  (`$JIRA_CONFIG_FILE`, else `~/.config/.jira/.config.yml`) to
  `/sandbox/.config/.jira/.config.yml`, so `jira init` is not needed in the
  sandbox. Set `CARBONITE_JIRA_CONFIG` to use another file. The config holds
  the server and login, not the token. The copy is refused before the sandbox
  is created if the file looks like it contains a credential, and a failed
  copy only prints a warning.

For a sandbox that already exists, attach by hand, one provider per call, and
open a new shell inside it (credentials only reach new processes):

```bash
$G sandbox provider attach --wait osac-carbonite-kind osac-github
$G sandbox provider attach --wait osac-carbonite-kind osac-jira
```

## Codex

Codex ships in the image (`@openai/codex`, pinned by the `CODEX_VERSION` build
arg) and reads `OPENAI_API_KEY` from the environment, so it uses an OpenShell
provider exactly like GitHub and Jira. The sandbox holds only a placeholder and
the proxy substitutes the real key on requests to `api.openai.com`; no login
step and no key in the sandbox.

```bash
$G profile import -f tools/carbonite/providers/osac-openai.yaml

read -rs OPENAI_API_KEY; export OPENAI_API_KEY
$G provider create --name osac-openai --type osac-openai \
  --credential OPENAI_API_KEY
unset OPENAI_API_KEY

export CARBONITE_PROVIDERS="osac-github osac-jira osac-openai"
```

Existing sandboxes: `$G sandbox provider attach --wait osac-carbonite-kind
osac-openai`, then open a new shell. Do not run `codex login` or `codex login
--device-auth`: device-code sign-in is not permitted by the OpenAI enterprise
licence, and the provider supplies the key.

## Claude Code (Vertex AI)

For now Claude Code authenticates **only** through Google Vertex AI. The two
non-secret settings are passed to the sandbox at creation, and the host's gcloud
application-default credentials are copied in once, so no per-sandbox login is
needed.

On the host, once:

```bash
gcloud auth application-default login   # ~/.config/gcloud/application_default_credentials.json
```

At sandbox creation:

```bash
export ANTHROPIC_VERTEX_PROJECT_ID=<project>
export CARBONITE_VERTEX=1                 # 0 by default
# CLOUD_ML_REGION defaults to global; override with CARBONITE_VERTEX_REGION
make rebuild-carb
```

Creation passes `CLAUDE_CODE_USE_VERTEX=1`, `CLOUD_ML_REGION` and
`ANTHROPIC_VERTEX_PROJECT_ID`, then uploads the credentials file to
`~/.config/gcloud/application_default_credentials.json` in the sandbox once it
is ready. If the file is missing or has no `refresh_token`, creation warns and
Claude asks for a sign-in. `CARBONITE_GCLOUD_ADC` points at another file.

Two properties to keep in mind:

- The sandbox holds a live Google refresh token, readable by anything inside it,
  agents included. Prefer a dedicated account or project for sandbox use, and
  treat the token as exposed once a sandbox has run.
- `ANTHROPIC_VERTEX_PROJECT_ID` is passed as a sandbox environment variable, so
  it appears in the sandbox specification as well as the process environment.

API-key authentication for Claude is not supported in the sandbox yet; it is
planned. Do not use the interactive `claude` sign-in or any device-code flow.

## Troubleshooting

- **`gh auth status` says not logged in, or Jira returns 401, right after a
  rebuild:** the providers were not attached. Check
  `$G sandbox provider list osac-carbonite-kind`.
- **Attach fails with "not in a state required for the operation":** read the
  gateway log (`journalctl --user -u openshell-gateway`). A message such as
  `rejecting uninspected credentialed endpoint` means a profile endpoint is
  covered by a sandbox policy rule with no `protocol`, so it is L4-only.
  Remove the endpoint from the profile or give the policy rule a `protocol`.
- **Jira `permission denied` on connect:** the policy does not allow the host
  or the binary. `$G logs osac-carbonite-kind` shows the denied connection.
- **Codex returns 401 with a real-looking key:** a stored login from an earlier
  `codex login` takes precedence over the provider placeholder. Run
  `codex logout` in the sandbox, open a new shell, and check that
  `OPENAI_API_KEY` starts with `openshell:resolve`.
- **A provider update seems to have no effect:** compare
  `$G provider get <name>` resource version before and after the update; it must
  increase. The bare `--credential KEY` form reads the value from your shell, so
  an unset or stale variable updates nothing useful.
- **An agent CLI cannot connect (`ECONNREFUSED`, "Unable to connect"):** a
  denial, not a network fault. `$G logs osac-carbonite-kind` names the kernel
  path of the binary that was refused; add that exact path to the rule. A
  global npm install execs a packaged native binary (for Claude Code,
  `.../@anthropic-ai/claude-code/bin/claude.exe`), not the launcher on PATH.
- **Jira 401 with the provider attached:** check the stored token has no
  literal quote characters and is an unscoped token. Compare a request made
  with `curl -u "<email>:<token>"` on the host with one made from the sandbox.
