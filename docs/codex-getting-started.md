# Codex Getting Started (OSAC)

OpenAI Codex is a first-class AI tool in this mono-repo, on par with Claude
Code and Cursor. This guide covers getting Codex productive against an OSAC
checkout: install, importing your Claude Code setup, permissions, trusting the
repo's hooks, reconnecting authenticated services, and the workflow
differences worth knowing.

Codex reads `AGENTS.md` natively, so the project's conventions, component map,
and graphify rules load without extra configuration. Start there — this guide
only covers Codex-specific setup.

## Prerequisites

- A standard OSAC checkout with `tools/bootstrap.sh` already run (see the root
  `README.md` / `AGENTS.md`). Bootstrap vendors `osac-ai-skills`, clones the
  sibling repos, and links skill discovery for every supported tool.
- The Codex CLI installed and authenticated with your OpenAI account.
- The same local toolchain the other tools expect (Go, Node.js, buf, kubectl,
  kind, `jira` CLI, `gh` CLI, `jq`).
- Optional but recommended: `graphify` installed (`uv tool install graphifyy`
  or `pipx install graphifyy`) so the knowledge-graph hooks do something. See
  AGENTS.md's "Knowledge Graph (graphify brain)" section.

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
config. The one setting that matters here is a raised `project_doc_max_bytes`
— the root `AGENTS.md` plus a component `AGENTS.md` can exceed Codex's 32 KiB
default, which would otherwise truncate the conventions Codex loads. Leave it
in place; don't lower it.

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

The repo ships `.codex/hooks.json`, which mirrors the Claude Code hooks:

- **SessionStart** refreshes the vendored `ai-workflows` context and fetches
  the latest published graphify bundle into `graphify-out/`.
- **PreToolUse (Bash)** nudges you to consult the knowledge graph before broad
  shell exploration (best-effort; it no-ops when `graphify` isn't installed).

Codex requires you to trust repo hooks before they run — use `/hooks` in the
Codex CLI to review and trust them. Until you do, sessions start without the
context refresh and graph fetch (Codex falls back to normal cold exploration).
For non-interactive runs where you can't trust interactively, Codex offers a
bypass flag (`--dangerously-bypass-hook-trust`). Use it only in protected,
reviewed CI or other pre-vetted automation—never for untrusted pull-request
code unless the workflow separately verifies the hook definition and every
referenced script.

The hook scripts are agent-neutral: they resolve the project directory from the
git worktree root when Codex doesn't set `CLAUDE_PROJECT_DIR`, so the same
scripts serve both tools.

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
