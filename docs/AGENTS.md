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

- Put component relationships, state machines and control-loop explanations
  in `architecture/`. Explain which component owns each transition and how
  observed status returns to the caller; a sequence diagram should match code.
- Put user-facing feature behavior in `features/`, with links to the relevant
  setup and usage guides rather than copied installation instructions.
- Put reproducible procedures in `guides/`: `developer/` for API/resource
  workflows, `admin/` for backend administration and `installation/` for
  deployment. State required access and distinguish local checks from changes
  to a deployed environment.
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
- For AI-assisted documentation, read [AI-POLICY.md](AI-POLICY.md); contribution
  mechanics are inherited from the root guide.

## Common Tasks

### Add or extend an architecture document

1. Check `architecture/README.md` and related documents before adding a new
   topic. Extend an existing document when it owns the same flow; use a
   topic directory with a `README.md` when diagrams and subtopics need grouping.
2. Explain responsibilities, inputs, state transitions and failure handling
   against the current component sources. Link API contracts and designs.
3. Add diagrams where they clarify interactions. Update the relevant index
   and `docs/README.md` navigation when adding a major section.

### Update a developer guide

1. Find the owning procedure under `guides/developer/` and trace its commands
   through the CLI/API and component configuration used by that procedure.
2. Update prerequisites, ordered steps, payloads and expected results together.
   Keep troubleshooting tied to observable failures, not security bypasses.
3. Check examples with the closest local consumer. Run live examples only in
   an authorized environment; report examples not executed rather than implying
   that prose review validates a deployment.
4. Add version context when behavior changed and check incoming links if a
   heading or file moved.

### Add or update a PlantUML diagram

1. For the existing AAP pipeline, edit or add `.puml` sources under
   `architecture/aap-provisioning/diagrams/` with matching `@startuml` and
   `@enduml` delimiters.
2. Run the generator below, then inspect the PNG under the sibling `images/`
   directory. Include source and output together.
3. Embed the image using a path relative to the Markdown file and descriptive
   alt text. Generation outside this pipeline needs its own verified setup;
   do not assume this script scans all of `docs/`.

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
- Apply the repository-wide checks in the root guide.
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
