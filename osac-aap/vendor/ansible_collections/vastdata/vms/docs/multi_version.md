# Multi-version VAST support

The collection targets a band of VAST product versions (currently 5.4.x - 5.5.x)
with a single set of modules, rather than shipping separate modules per version.
This document explains how that works and how it is maintained.

## Generation: union schema

Modules are generated from the union of every supported swagger spec:

```bash
python tools/generate_from_swagger.py \
  --swagger 5.4=api/swagger/5.4/vast_swagger_api.yaml 5.5=api/swagger/5.5/vast_swagger_api.yaml \
  --with-tests
```

`merge_lifecycles()` combines the per-version analyses into one
`ResourceLifecycle` and records, per generated module:

- `generated_min_version` - the oldest version the module was generated for.
- `field_versions` - fields whose availability deviates from that floor
  (`{"min": (5, 5)}`, etc.).
- `field_transforms` - on-wire type coercions for older clusters.
- `required_versions` - fields whose required-ness is version-bounded.

## Runtime: version-aware behavior

At module run time (`VersionAwareMixin` + `BaseResource`):

1. The cluster's product version is read from `clusters[0].sw_version` and
   validated against the supported band (`ensure_supported_version`).
2. `_enforce_version_compatibility()` fails fast if the cluster is below the
   generation floor or outside a resource's min/max.
3. `build_desired_state()` drops fields not supported on the target cluster
   (emitting a warning) so unknown parameters are never sent to older clusters.
4. `transform_request` / `transform_response` apply `field_transforms` so the
   wire format matches what the cluster speaks.

## Schema overrides and versioning

`schema_overrides.py` carries idempotency metadata (read-only / immutable /
set-like fields, lookup keys, normalizers). The override resolver
(`get_overrides(resource, cluster_mm)`) supports version-keyed overrides, so a
field that becomes read-only in a newer release can be expressed per version.
The resolution infrastructure exists today; populate version-keyed entries as
behavioral differences between supported versions arise.

## Adding a new VAST version

1. Drop the new swagger under `api/swagger/<version>/`.
2. Run `make check-swagger-diff` to surface breaking changes (type changes,
   enum removals, renames) that need an explicit decision.
3. Add the spec to `SWAGGER_SPECS` in the `Makefile` and regenerate
   (`make generate-all`).
4. Update the supported band in `plugins/module_utils/vast/version.py`
   (`_MIN_VERSION` / `_MAX_VERSION`) and note it in `meta/runtime.yml`.
5. Add a changelog fragment and run the test suite.
