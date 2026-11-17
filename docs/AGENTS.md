# OSAC documentation

Never edit this file except for minor corrections. Do not append new content.

Architecture, feature and developer guides live alongside executable dashboard
code in `pr-dashboard/`. Read [README.md](README.md) for navigation; API contract
sources live in [`../proto/`](../proto/), implementations in the owning component.

## Editing Documentation

- Verify commands, payloads and behavior against current source and actual
  consumers, including version context when behavior changed.
- Use relative internal links, hierarchical headings and language-tagged fenced
  code. Check link targets and examples before submitting.
- Keep architecture explanations technical, feature docs user-facing and
  developer procedures reproducible. Place readable diagrams near their text
  with descriptive alt text; PlantUML output is PNG.
- For AI-assisted documentation, read [AI-POLICY.md](AI-POLICY.md) and follow
  the root contribution requirements.

## Diagrams

After changing `.puml` files under `architecture/aap-provisioning/diagrams/`,
run from `docs/`:

```bash
cd architecture/aap-provisioning
./generate_images.sh
```

The script uses Docker or Podman and SELinux-aware `:Z` mounts. Commit both
source and regenerated PNGs under `architecture/aap-provisioning/images/`.
Do not disable host security controls to troubleshoot generation.

## Validation

For prose, review facts, relative links and code examples and run applicable
root pre-commit hooks. Dashboard/publication changes also follow
[pr-dashboard-verify.yml](../.github/workflows/pr-dashboard-verify.yml).
Its repository-root checks include:

```bash
python3 -m unittest discover -s tools/pr-notify -p 'test_*.py'
python3 tools/test_generate_site_index.py
node tools/test_pr_dashboard.js
```

For presentation or publication-input changes, use the same workflow's render,
site-index and example-config/manifest validation steps. Markdown-only changes
do not require live deployments or unrelated dashboard tests.
