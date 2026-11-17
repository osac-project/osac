# OSAC documentation

Never edit this file except for minor corrections. Do not append new content.

Architecture, feature and developer guides live alongside executable dashboard
code in `pr-dashboard/`. Read [README.md](README.md) for navigation; API contract
sources live in [`../proto/`](../proto/), implementations in the owning component.

<a id="editing-documentation"></a>

## Documentation Conventions

### Markdown Style

- Use one `#` title, `##` for major sections and `###` for task-level
  subsections. Keep steps short enough to follow while running the procedure.
- Fence examples with a language identifier, such as `bash`, `yaml`, `json`
  or `plantuml`. State the command's working directory and prerequisites.
- Prefer relative links within the monorepo. Check heading anchors as well
  as file paths; when renaming a section, update links that point to it.
- Give concrete commands and payloads rather than just describing an action.
  Identify placeholders, expected outcomes and safe cleanup where relevant.

### Content Organization

- Put component relationships and state transitions in `architecture/`;
  diagrams must match implementation and status-feedback ownership.
- Put feature behavior in `features/`; link to setup rather than copying it.
- Put procedures in `guides/developer/`, `guides/admin/` or
  `guides/installation/`. State access requirements and distinguish local
  checks from deployed changes. Update indexes/navigation for new sections.
- Keep cross-component policy in its canonical document, such as
  [CONVENTIONS.md](CONVENTIONS.md) or [INTEGRATION-TESTING.md](INTEGRATION-TESTING.md).
  Link to it instead of creating a competing policy in a feature guide.

### Diagram Conventions

- Embed diagrams near the explanation, with descriptive alt text that names
  the flow or relationship. Keep labels readable without excessive zoom;
  split a crowded diagram rather than shrinking all text.
- Use PNG for the existing PlantUML generator's output; SVG is suitable for
  other vector diagrams. Keep editable source with the generated image.
- For AAP diagrams, use the existing `.puml` files as syntax/style examples.
  Check both the generated image and the surrounding explanation after edits.

### Cross-References and Version Context

- Link API definitions to [`../proto/private/`](../proto/private/), and link
  behavior to the owning implementation, such as `../fulfillment-service/`,
  `../osac-operator/` or `../osac-aap/`. Generated public API files are not the
  editable contract source.
- Link design rationale to the relevant accepted
  [enhancement proposal](https://github.com/osac-project/enhancement-proposals).
  Verify implementation before describing a proposal as available behavior.
- When a release changes behavior, name the verified version and describe
  what differs for older installations. Do not infer support or a docs-branch
  policy from the branch on which a document happens to live.
- For breaking changes, update the architecture explanation and affected
  developer/admin procedures together, including migration or compatibility
  notes where needed.
- For AI-assisted documentation, read [AI-POLICY.md](AI-POLICY.md).

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
