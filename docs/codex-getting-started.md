# Codex Getting Started (OSAC)

OpenAI Codex is a first-class AI tool in this mono-repo, on par with Claude
Code and Cursor. This guide covers getting Codex productive against an OSAC
checkout: install, importing your Claude Code setup, permissions, trusting the
repo's hooks, reconnecting authenticated services, and the workflow
differences worth knowing.

Codex reads `AGENTS.md` natively, so the project's conventions and component
map load without extra configuration. Start there — this guide only covers
Codex-specific setup. Graphify is optional; when `graphify-out/graph.json`
exists, follow the root `AGENTS.md` guidance for code-structure discovery.

## Prerequisites

- A standard OSAC checkout with `tools/bootstrap.sh` already run (see the root
  `README.md` / `AGENTS.md`). Bootstrap vendors `osac-ai-skills`, clones the
  sibling repos, and links skill discovery for every supported tool.
- The Codex CLI installed and authenticated with your OpenAI account.
- The same local toolchain the other tools expect (Go, Node.js, buf, kubectl,
  kind, `jira` CLI, `gh` CLI, `jq`).
- Optional: `graphify` installed (`uv tool install graphifyy` or
  `pipx install graphifyy`) for code-structure discovery when
  `graphify-out/graph.json` exists.

## Skill discovery (`.agents/skills`)

Codex discovers skills under `.agents/skills`. Bootstrap's default fan-out
(`tools/link-agent-skills.sh --all`) already creates `.agents/skills ->
../skills` alongside the Claude/Cursor/Gemini umbrellas. To (re)link only the
Codex umbrella:

```bash
tools/link-agent-skills.sh --codex
```

In Codex, use `/skills` to browse discovered skills or type `$` followed by a
skill name to invoke one directly (for example, `$implement`). Skills do not
become top-level slash commands such as `/implement`.

`.agents/` is gitignored (generated output). If you ever see a real
`.agents/skills` directory (leftover from an older bootstrap), the wrapper
converts it into the symlink umbrella on the next run that selects Codex via
`--codex`, `--all`, or the default fan-out. OSAC owns this repo-local directory;
install personal skills under `$CODEX_HOME/skills` (normally
`~/.codex/skills`), not inside the generated project umbrella.

## Project config (`.codex/config.toml`)

The repo ships `.codex/config.toml` at the root. After you trust the project,
Codex walks from the `.git` root down to your CWD and honors project-level
config. The one setting that matters here is `project_doc_max_bytes`, which is kept at
32 KiB. The current compact root plus component instruction files fit within
that default; keep the setting aligned with the compact instruction design.

## Importing your Claude Code setup (`/import`)

If you already use Claude Code here, run Codex's `/import` to carry over
settings. **Review the result before relying on it** — an import is a starting
point, not a finished config:

- **Permissions / command allowlist — do NOT copy Claude's broad allowlist.**
  Claude Code's settings may auto-approve a wide set of commands that is
  appropriate for its sandboxing model, not Codex's. Copying it verbatim
  removes the approval prompts that keep Codex safe. Start restrictive (see
  below) and widen deliberately.
- **MCP servers** need to be re-authenticated in Codex even if the import
  brings over their definitions (see "Reconnecting authenticated services").
- **Hooks** are trusted separately in Codex (see "Trusting the repo's hooks").

## Permissions

Recommended baseline: **workspace-write with approval-on-request**. Codex can
edit files inside the workspace and asks before running commands that need
broader access. This matches how OSAC contributors work — most changes are
in-tree edits plus scoped build/test commands you can approve as they come up.

Do **not** paste Claude Code's command allowlist into Codex to silence prompts.
Approve commands as they arise and only persist the ones you run constantly.

## Trusting the repo's hooks (`.codex/hooks.json`)

The repo ships native Codex hooks in `.codex/hooks.json`:

- **SessionStart** refreshes the vendored `ai-workflows` context and fetches
  the latest published graphify bundle into `graphify-out/`.
- **PreToolUse (Bash)** nudges you to consult the knowledge graph before broad
  shell exploration (best-effort; it no-ops when `graphify` isn't installed)
  and runs component checks before commits and PR creation.
- **PostToolUse (`apply_patch`)** routes proto, module, and operator API edits
  to the component checks shared with Claude Code.

Codex requires you to trust repo hooks before they run — use `/hooks` in the
Codex CLI to review and trust them. Until you do, none of the repo hooks run:
the session context refresh, graph fetch, and component checks are skipped.
Codex ties trust to the current hook definition, so changes to the config or
hook scripts may require review again.
For non-interactive runs where you can't trust interactively, Codex offers a
bypass flag (`--dangerously-bypass-hook-trust`). Use it only in protected,
reviewed CI or other pre-vetted automation—never for untrusted pull-request
code unless the workflow separately verifies the hook definition and every
referenced script.

Codex reports `apply_patch` edits as patch text in `tool_input.command` rather
than a separate file-path field. The Codex adapter extracts paths from the
supported `Update File`, `Add File`, `Delete File`, and `Move to` patch headers,
then passes them to the shared path router. An unrecognized patch format or
tool with no usable path is a no-op; it doesn't route against unrelated dirty
files in the worktree. PostToolUse runs after the edit, so it can't undo an
edit if a follow-up generator fails.

The shared hook scripts resolve the project root from the event's `cwd`, so
they work from Codex and Claude Code without `CLAUDE_PROJECT_DIR`.

## Reconnecting authenticated services (MCP)

MCP server definitions may carry over via `/import`, but custom authentication
may require you to sign in again. Verify the servers you rely on are connected
at the start of a session rather than discovering missing authorization
mid-task.

## Per-worktree Jira context (`.ai-context/jira.md`)

`osac-new-worktree` writes the current worktree's Jira ticket (key, summary,
type) to `.ai-context/jira.md` at the repo root — an agent-neutral, gitignored
file. AGENTS.md points every tool at it. If it exists, Codex should read it for
the current work item.

## Workflow differences from Claude Code

- **Tool taxonomy:** Codex has no dedicated Read/Glob/Grep tools; file access
  goes through the shell. The graphify PreToolUse nudge therefore maps only to
  the Bash path, not to separate read/search tools.
- **Docs source:** Codex reads `AGENTS.md` natively; it does not read
  `CLAUDE.md`. Anything a tool must know lives in (or is mirrored into)
  `AGENTS.md`. The graphify usage rules, for example, live in both.
- **Hook trust is explicit** (above), whereas Claude Code's are configured via
  its own settings.
- **Skill discovery** is `.agents/skills` for Codex vs `.claude/skills` for
  Claude Code — both are umbrellas over the same `skills/` tree.

## See also

- Root [`AGENTS.md`](../AGENTS.md) — conventions, component map, graphify rules.
- [`README.md`](../README.md) `## AI-assisted development` — bootstrap.
- `osac-ai-skills` README — the recommended skill sequence and the `--codex`
  fan-out flag.
