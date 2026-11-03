# Copyright: (c) 2026, VAST Data
# Apache License 2.0 (see LICENSE or https://www.apache.org/licenses/LICENSE-2.0)
# SPDX-License-Identifier: Apache-2.0
"""Shared helpers for *keyed collection* sub-endpoint CRUD.

Some nested collections expose full CRUD through an instance path
(``/parent/sub/{id}/`` or ``/parent/{pid}/sub/{id}/``), e.g. tenant metric
labels and metric label values. The generic :class:`SubEndpointResource` base
only models action-style / single-object sub-endpoints, so modules opt in by
assigning these functions onto their resource class inside an
``_apply_*_customizations()`` block (see ``tenant_metric_labels``).

A class opts in by defining:

- ``collection_identity``: list of param names that uniquely identify a row
  (used to look up an existing object before create/update/delete).
- ``identity_field_map`` (optional): maps an identity param to the nested path
  it is echoed under in the API response (e.g. ``{"label_id": ["label", "id"]}``)
  when the response shape differs from the request.
"""

from typing import Any, Dict, List

from .client import unwrap_list_envelope
from .diff import has_changes
from .errors import VastAPIError, VastError
from .sub_endpoint_resource import CrudCapability


def _keyed_collection_list_rows(self) -> List[Dict[str, Any]]:
    """List the collection, unwrapping the paginated ``{count, results}`` envelope."""
    try:
        raw = self._api_base.get() or []
    except VastError as e:
        self.module.fail_json(msg=f"Failed to list {self.sub_path}: {str(e)}")
    return unwrap_list_envelope(raw)


def _keyed_collection_identity_matches(self, row: Dict[str, Any], param: str) -> bool:
    """True if ``row`` matches the desired value of identity ``param``."""
    desired = self.params.get(param)
    locator = self.identity_field_map.get(param)
    if locator:
        actual: Any = row
        for segment in locator:
            if not isinstance(actual, dict):
                return False
            actual = actual.get(segment)
    else:
        actual = row.get(param)
    # Coerce types so nested ``label.id`` int still matches Ansible string ``"5"``.
    if actual is not None and desired is not None and type(actual) is not type(desired):
        try:
            return type(desired)(actual) == desired
        except (TypeError, ValueError):
            return False
    return actual == desired


def _keyed_collection_find_existing(self) -> Dict[str, Any]:
    """Return the existing collection row matching all identity params, or ``{}``."""
    for row in self._list_rows():
        if isinstance(row, dict) and all(self._identity_matches(row, p) for p in self.collection_identity):
            return row
    return {}


def _keyed_collection_run(self) -> None:
    """Idempotent CRUD dispatch for a keyed nested collection.

    - ``present``: create when absent; PATCH when a mutable (non-identity)
      field differs and UPDATE is supported; no-op otherwise.
    - ``absent``: DELETE the matching row by id; no-op when already gone.
    """
    state = self.params.get("state", "present")
    # Soft ARGUMENT_SPEC (identity not required after create/PATCH fold) — enforce here.
    missing_identity = [p for p in self.collection_identity if self.params.get(p) is None]
    if missing_identity:
        self.module.fail_json(msg=f"missing required argument: {', '.join(missing_identity)}")
    current = self._find_existing()
    result_key = self.module_result_key

    if state == "absent":
        if not current:
            self.module.exit_json(changed=False, **{result_key: {}})
        if not self.check_mode:
            try:
                self._api_base[current["id"]].delete()
            except VastError as e:
                self.module.fail_json(msg=f"Failed to delete {self.sub_path}: {str(e)}")
        self.module.exit_json(
            changed=True,
            diff={"before": dict(current), "after": {}},
            **{result_key: {}},
        )

    payload = self._build_payload()

    if not current:
        if self.check_mode:
            self.module.exit_json(
                changed=True,
                diff={"before": {}, "after": payload},
                **{result_key: payload},
            )
        try:
            created = self.create(payload)
        except VastAPIError as e:
            self.module.fail_json(msg=str(e))
        self.module.exit_json(
            changed=True,
            diff={"before": {}, "after": dict(created)},
            **{result_key: created},
        )

    # Object exists: reconcile mutable (non-identity) fields when updatable.
    mutable = {k: v for k, v in payload.items() if k not in self.collection_identity}
    can_update = CrudCapability.UPDATE in self.supported_operations
    changed = bool(mutable) and can_update and has_changes(current, mutable, self.overrides)

    if not changed:
        self.module.exit_json(changed=False, **{result_key: current})

    after = {**current, **mutable}
    if self.check_mode:
        self.module.exit_json(
            changed=True,
            diff={"before": dict(current), "after": after},
            **{result_key: after},
        )
    try:
        updated = self._api_base[current["id"]].patch(**mutable)
    except VastError as e:
        self.module.fail_json(msg=f"Failed to update {self.sub_path}: {str(e)}")
    self.module.exit_json(
        changed=True,
        diff={"before": dict(current), "after": dict(updated or after)},
        **{result_key: updated or after},
    )
