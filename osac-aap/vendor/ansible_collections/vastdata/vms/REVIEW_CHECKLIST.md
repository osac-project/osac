# Pull Request Review Checklist

Use this checklist when reviewing pull requests for the `vastdata.vms`
Ansible collection.

> Note: most modules are auto-generated from the VAST Swagger/OpenAPI
> specification. Changes to generated modules should usually be made in the
> generator (`tools/vast_generator/`) and regenerated, not hand-edited.

## General

- [ ] PR title follows conventional commit format (`feat:`, `fix:`, `docs:`, etc.)
- [ ] PR description clearly explains the change and its motivation
- [ ] No unrelated changes included in the PR
- [ ] A changelog fragment is added under `changelogs/fragments/` (see CONTRIBUTING.md)

## Code Quality

- [ ] `ruff`/lint passes
- [ ] `yamllint` passes
- [ ] `ansible-lint` passes (production profile)
- [ ] No hardcoded credentials or secrets
- [ ] Sensitive parameters use `no_log=True` in the argument spec

## Module Standards

- [ ] Module has `DOCUMENTATION`, `EXAMPLES`, and `RETURN` blocks
- [ ] DOCUMENTATION includes `extends_documentation_fragment: vastdata.vms.conn`
- [ ] DOCUMENTATION includes `version_added` set to the correct release
- [ ] DOCUMENTATION includes `author: VAST Data (@vastdata)`
- [ ] List-type parameters include an `elements:` definition
- [ ] Module supports `check_mode`
- [ ] Module is idempotent (re-running produces no changes)

## Testing

- [ ] Unit tests added or updated for new/modified code
- [ ] All existing unit and sanity tests pass
- [ ] Edge cases considered (resource not found, API errors, version gating)

## Documentation

- [ ] `README.md` updated if adding new modules or roles
- [ ] `docs/` updated for behavioral or multi-version changes
