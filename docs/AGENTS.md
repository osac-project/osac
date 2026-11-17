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

### Prose

- Review facts, relative links, heading anchors and code examples against their
  actual consumers. Distinguish documented commands from checks executed.
- From the repository root, run `pre-commit run --all-files`; follow the root
  staged and PR-range secret-scanning requirements separately.
- Markdown-only changes do not require live deployments, diagram regeneration
  or unrelated dashboard tests.

### Dashboard and publication inputs

Dashboard/publication changes follow
[pr-dashboard-verify.yml](../.github/workflows/pr-dashboard-verify.yml).
Its repository-root checks include:

```bash
python3 -m unittest discover -s tools/pr-notify -p 'test_*.py'
python3 tools/test_generate_site_index.py
node tools/test_pr_dashboard.js
```

For presentation or publication-input changes, use the same workflow's render,
site-index and example-config/manifest validation steps. These use Python and
Node; presentation rendering also requires `npm ci --prefix presentations`
from the repository root. Do not treat an isolated generator invocation as
the publication build without checking its workflow inputs.
