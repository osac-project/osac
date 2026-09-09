# pr-notify

PR status monitor for OSAC repositories. Two independent components:

1. **GitHub Action** (`generate.py`) - fetches open PRs, classifies them, and builds `data.json` into the GitHub Pages deployment artifact.
2. **Systemd service** (`notify.py`) - reads the published `data.json` from GitHub Pages and posts a compact summary to Slack.

## How it works

```
GitHub Action (every 30 min, Sun-Fri 06:00-19:30 UTC)
  -> Fetch open PRs via GitHub GraphQL API
  -> Classify each PR (needs review, CI failing, stale, approved, draft)
  -> Generate data.json
  -> Assemble the GitHub Pages deployment artifact
  -> GitHub Pages serves the updated dashboard

Systemd timer (weekday 09:00 UTC, local machine)
  -> Fetch data.json from GitHub Pages
  -> Format compact Slack summary
  -> Post to Slack with dashboard link
```

### Data/presentation separation

The dashboard is split into two parts:

- **Static HTML** (`docs/pr-dashboard/index.html`) - committed once, renders the dashboard client-side by fetching `data.json`. Only changes when the dashboard design is updated.
- **Data file** (`docs/pr-dashboard/data.json`) - generated into the GitHub Pages artifact. Contains all PR statuses, CI health metrics, and timestamps.

### PR classification

Each PR is assigned one status (highest priority wins):

| Priority | Status | Meaning |
|----------|--------|---------|
| 1 | Draft | PR is marked as draft |
| 2 | CI Failing | Last commit has failing checks |
| 3 | Changes Requested | A reviewer requested changes |
| 4 | Needs Re-review | New commits pushed after approval |
| 5 | Approved | All reviewers approved |
| 6 | Needs Review | No meaningful reviews yet |

PRs older than 7 days are flagged as stale; 14+ days as critical.

## Scripts

### generate.py (data collection)

Fetches PRs, classifies them, and writes `data.json`. Used by the GitHub Action.

```bash
python generate.py --config config.toml --output ../../docs/pr-dashboard/data.json
```

Add `--dry-run` to print JSON to stdout without writing.

### generate_all.py (workflow data collection)

Fetches the union of repositories from every dashboard configuration once, then
writes a separate `data.json` for each dashboard. The GitHub Pages workflow
uses this command to avoid repeated GitHub API requests.

```bash
python generate_all.py --output-root ../..
```

### notify.py (Slack notification)

Reads `data.json` from GitHub Pages and posts a compact summary to Slack. Used by the systemd timer.

```bash
python notify.py --config config.toml
```

Add `--dry-run` to print the message without posting.

## Configuration

Copy `config.example.toml` to `config.toml` and edit:

```toml
# Used by generate.py and notify.py
repos = [
    "osac-project/osac",
    "osac-project/osac-ui",
    "osac-project/enhancement-proposals",
]

# Used by notify.py only
slack_channel = "C08ESMFV85Q"
slack_creds_dir = "~/.config/slack/"

# Used by generate.py for output path, notify.py for data URL
[dashboard]
repo = "osac-project/osac"
branch = "main"
base_url = "https://osac-project.github.io/osac/pr-dashboard"
data_path = "docs/pr-dashboard/data.json"
```

| Field | Used by | Description |
|-------|---------|-------------|
| `repos` | both | GitHub repos to monitor (`owner/name` format) |
| `slack_channel` | notify.py | Slack channel ID for posting |
| `slack_creds_dir` | notify.py | Directory containing `xoxc_token` and `d_cookie` files |
| `dashboard.repo` | - | Target repo (informational) |
| `dashboard.branch` | - | Base branch (informational) |
| `dashboard.base_url` | notify.py | GitHub Pages URL for the dashboard |
| `dashboard.data_path` | - | Path to `data.json` within the repo |

## Slack credentials

The notify script uses xoxc token + browser cookie authentication.

**Directory layout:**

```
~/.config/slack/
  xoxc_token    # Slack xoxc token (starts with "xoxc-...")
  d_cookie      # Browser "d" cookie value
```

**Automatic extraction (recommended):**

Use [slack-creds-extractor](https://github.com/tzvatot/slack-creds-extractor) - a Chrome extension that extracts the tokens automatically and saves them to `~/.config/slack/` via native messaging.

**Manual extraction:**

1. Open Slack in your browser and log in.
2. Open DevTools (F12) > Network tab.
3. Send any message or reload the page.
4. Find a request to `https://edgeapi.slack.com/` or `https://slack.com/api/`.
5. Copy the `token` form parameter to `xoxc_token`.
6. Copy the `d` value from the `Cookie` header to `d_cookie`.

Tokens expire periodically. When you see auth errors (`invalid_auth`, `token_revoked`), refresh the credentials. Keep credential files outside this repository and never commit them.

## Prerequisites

- Python 3.11+
- `gh` CLI authenticated (for `generate.py` only)
- Slack credentials in `~/.config/slack/` (for `notify.py` only)

## GitHub Action

The Action runs automatically via `.github/workflows/pr-dashboard.yml`:

- Schedule: every 30 minutes, Sun-Fri 06:00-19:30 UTC
- Manual trigger: `workflow_dispatch`
- Uses each `config*.example.toml` file for its dashboard definition
- Publishes `data.json` and the dashboard manifest in the GitHub Pages artifact (no direct commit is made)

## systemd installation (Slack notifier)

Install as a user service for daily Slack summaries.

**Note:** `pr-notify.service` assumes the repo is cloned at `~/work/src/github/osac`. Edit paths if your checkout is elsewhere.
Before enabling the timer, create and configure the gitignored `config.toml` as described above. The service reads that one notification configuration; it does not run dashboard-only example configurations.

```bash
cp pr-notify.service pr-notify.timer ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now pr-notify.timer
```

Check status:

```bash
systemctl --user status pr-notify.timer
journalctl --user -u pr-notify.service --since today
```

## GitHub Pages setup

Enable GitHub Pages on `osac-project/osac`:

1. Go to Settings > Pages
2. Source: "GitHub Actions"
3. In the `github-pages` environment, restrict deployment branches to `main`

The dashboard will be available at `https://osac-project.github.io/osac/pr-dashboard/`.

## Tests

```bash
python -m pytest -v
```

## Logs

Each script writes to a dated log file in `/tmp/`:

```bash
cat /tmp/pr-generate-$(date -u +%Y-%m-%d).log   # generate.py
cat /tmp/pr-notify-$(date -u +%Y-%m-%d).log      # notify.py
```

Logs are also written to stderr, so `journalctl` captures them for the systemd service.

## Project structure

```
tools/pr-notify/
  generate.py          # Data collection script (used by GitHub Action)
  notify.py            # Slack notification script (used by systemd timer)
  github.py            # GitHub GraphQL PR fetcher
  classifier.py        # PR status classification engine
  data_formatter.py    # JSON data generator for the dashboard
  formatter.py         # Slack message formatters
  slack.py             # Slack API client
  config.py            # TOML config loader
  models.py            # Data models (PRData, ClassifiedPR, Config)
  config.example.toml  # Config template
  pr-notify.service    # Systemd service unit
  pr-notify.timer      # Systemd timer unit

.github/workflows/
  pr-dashboard.yml     # GitHub Action for scheduled data generation

docs/pr-dashboard/
  index.html           # Static dashboard (fetches data.json client-side)
  data.json            # PR data (updated by GitHub Action)
```
