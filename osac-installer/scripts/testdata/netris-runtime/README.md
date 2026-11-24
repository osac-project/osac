# Netris runtime overlay harness

`produce-overlay.yml` runs the real producer of `values/<profile>/netris-runtime.yaml`:
the `prep-osac` values task file of an `osac-project/osac-test-infra` checkout
(`infra/netris/roles/prep-osac/tasks/generate-values.yml`). It includes that task
file with dummy lab facts and a per-run output directory, so nothing about the
overlay is copied or re-implemented here.

`../test-networking-provisioning-overlay.py` invokes it once per inventory input
(omitted/default, explicit `true`, JSON `false`) and feeds each produced file
into the install-order Helm checks. The regression therefore runs:

```
inventory input -> prep-osac values tasks -> netris-runtime.yaml -> Helm checks
```

## Prerequisites

Both fail the regression when missing; it does not skip.

- An `osac-test-infra` checkout: `OSAC_TEST_INFRA`, or the sibling
  `../osac-test-infra` that `tools/bootstrap.sh` creates. It must contain
  `infra/netris/roles/prep-osac/tasks/generate-values.yml` (the task file
  extracted from `main.yml` for this regression) and
  `infra/netris/inventory/group_vars/all.yml` (the default value under test).
- `ansible-playbook` from `ansible-core` on PATH. CI runs the script with
  `uv run --with ansible-core --project .. python ...`.

## What is a stand-in

The playbook's `vars` hold what `prep-osac` derives from a live lab before it
builds the overlay: the Netris controller URL and site/VPC IDs, the resource
class map, the OCP release CLI image, the lab's excluded ranges, the SSH key
(the role base64-decodes it, so it is base64 here) and the selected profile.
Every credential is a `fixture-…` placeholder; the values are shaped so the
chart schema accepts the produced file. The input under test,
`networking_provisioning_enabled`, is never defaulted here: the script passes
either the value the inventory declares or the EXTRA_VARS override.
