# Enclave Wizard Pipeline

Any feature that adds or modifies Helm values in `osac-installer` must consider the Enclave Wizard pipeline. The Wizard renders configuration controls automatically from the Helm chart's JSON Schema — no custom UI code is needed for standard fields.

**Pipeline:** `osac-installer` schema change → enclave OSAC plugin picks up the change → Enclave Wizard UI renders the control.

**When it applies:** Any feature that adds or modifies installer Helm values that operators configure during deployment (e.g., DNS provider, storage backend, feature toggles).

## Schema-type-to-control mapping

| JSON Schema construct | Wizard UI control | Example |
|----------------------|-------------------|---------|
| `enum` | Dropdown | DNS provider: `route53`, `infoblox` |
| `boolean` | Checkbox | Enable bundled PostgreSQL |
| `string` (no enum) | Free text input | External hostname |
| `integer` / `number` | Numeric input | Worker node count |

The schema source is [values.schema.json](../../osac-installer/charts/osac/values.schema.json),
with deployment defaults in [values.yaml](../../osac-installer/charts/osac/values.yaml).
Validation rules and descriptions come from the schema. Confirm that the
consuming Enclave plugin version picks up the changed schema and that the Wizard
renders and validates the field before declaring the feature available.

## Design decompose artifacts

`/design:decompose` must produce three artifacts when the pipeline applies:

1. **osac-installer task** — add or update the Helm value in both `values.yaml` and `values.schema.json` with proper type, default, and description
2. **Enclave plugin task** — pick up the schema change and expose the parameter, blocked-by the installer task (Component: `Enclave`)
3. **Enclave UI task** — render the control in the Wizard, blocked-by the plugin task (Component: `Enclave`)

Track implementation work as Jira Tasks with the parent Feature's Component;
add `Enclave` where the plugin/UI work requires it. A schema-driven control may
need integration verification rather than custom UI code. Include that
verification in the plugin/UI tasks.

## Complex additions

If the feature requires custom UI logic beyond proxying a Helm value (e.g., multi-step wizards, conditional fields, API calls), flag the UI task as needing design discussion — the schema-driven approach won't cover it.
