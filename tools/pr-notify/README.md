# PR dashboard data provider

This directory generates the PR data and dashboard manifest published by the
OSAC GitHub Pages workflow. Slack notifications are handled outside this tool.

## How it works

```
GitHub Action (every 30 min, Sun-Fri 06:00-19:30 UTC)
  -> Fetch open PRs via GitHub GraphQL API
  -> Classify each PR (needs review, CI failing, stale, approved, draft)
  -> Generate dashboard data and manifest
  -> Render presentations and assemble the GitHub Pages artifact
  -> GitHub Pages serves the updated dashboard
```

The dashboard is split into two parts:

- **Static HTML** (`docs/pr-dashboard/index.html`) renders each dashboard by
  fetching its `data.json` client-side.
- **Data file** (`docs/<dashboard>/data.json`) is generated into the GitHub
  Pages artifact.

## Scripts

### `generate.py`

Fetches PRs, classifies them, and writes one `data.json` file.

```bash
python3 generate.py --config config.toml --output ../../docs/pr-dashboard/data.json
```

Use `--dry-run` to print JSON, or `--print-data-path` to print the configured
output location.

### `generate_all.py`

Fetches the union of repositories from every dashboard configuration once, then
writes a separate `data.json` for each dashboard. The GitHub Pages workflow
uses this command.

```bash
python3 generate_all.py --output-root ../..
```

### `generate_manifest.py`

Builds the root `dashboards.json` index from the dashboard configurations.

```bash
python3 generate_manifest.py --output ../../_site/dashboards.json
```

## Configuration

Copy an example configuration and edit it:

```toml
repos = [
    "osac-project/osac",
    "osac-project/osac-ui",
    "osac-project/enhancement-proposals",
]

[dashboard]
repo = "osac-project/osac"
branch = "main"
base_url = "https://osac-project.github.io/osac/pr-dashboard"
data_path = "docs/pr-dashboard/data.json"
```

| Field | Used by | Description |
|-------|---------|-------------|
| `repos` | `generate.py`, `generate_all.py` | GitHub repositories to monitor (`owner/name` format). |
| `dashboard.repo` | - | Target repository metadata. |
| `dashboard.branch` | - | Target branch metadata. |
| `dashboard.base_url` | - | Published dashboard URL metadata. |
| `dashboard.data_path` | `generate_all.py`; `generate.py --print-data-path` | Output path for `data.json`, relative to `--output-root` for batch generation. |

## GitHub Pages publication

`.github/workflows/pr-dashboard.yml` publishes the dashboard on its schedule,
on relevant `main` changes, and by manual dispatch. It uses every
`config*.example.toml` file to build dashboard data and a manifest in the Pages
artifact.

Enable GitHub Pages on `osac-project/osac`, select **GitHub Actions** as the
source, restrict the `github-pages` environment to `main`, and configure the
least-privilege `OSAC_BOT_PAT` repository secret. The primary dashboard is
published at `https://osac-project.github.io/osac/pr-dashboard/`.

## Tests

```bash
python3 -m unittest discover -s tools/pr-notify -p 'test_*.py'
```

## Directory layout

```
tools/pr-notify/
  generate.py           # Generate one dashboard data file
  generate_all.py       # Generate all dashboard data from one snapshot
  generate_manifest.py  # Generate the dashboard manifest
  github.py             # GitHub GraphQL PR fetcher
  classifier.py         # PR status classification
  data_formatter.py     # Dashboard JSON formatter
  config.py             # TOML configuration loader
  models.py             # Data models
  config*.example.toml  # Dashboard configuration templates
```
