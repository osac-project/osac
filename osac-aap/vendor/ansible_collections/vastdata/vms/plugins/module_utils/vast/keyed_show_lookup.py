# Copyright: (c) 2026, VAST Data
# Apache License 2.0 (see LICENSE or https://www.apache.org/licenses/LICENSE-2.0)
# SPDX-License-Identifier: Apache-2.0
"""Show-first existence lookup with optional scoped LIST fallback."""

from typing import Any, Dict, List, Optional, Tuple

from .client import RESTFailure
from .diff import compute_patch, normalize_resource
from .errors import VastAPIError, VastNotFoundError


class KeyedShowCrudMixin:
    show_sub_path: str = "show"
    show_query_fields: Tuple[str, ...] = ()
    required_show_query_fields: Optional[Tuple[str, ...]] = None
    not_found_statuses: Tuple[int, ...] = (400, 404)
    not_found_body_substrings: Tuple[str, ...] = ()
    list_fallback_scope_fields: Optional[Tuple[str, ...]] = None
    list_fallback_match_field: Optional[str] = None

    def run_present_only(
        self,
        identity: Tuple[str, ...],
        delete_module: str,
        supports_modify: bool = True,
    ) -> None:
        """Lifecycle for id-less, present-only resources keyed by body fields.

        These resources have no ``id`` and no ``state=absent`` (deletion lives in
        a dedicated ``*_delete`` module named by ``delete_module``). Existence is
        resolved via :meth:`find_current`; a missing row is created, an existing
        row is (optionally) reconciled with a body-keyed PATCH. ``identity`` is
        the tuple of key fields (e.g. ``("database_name", "name")``) never sent in
        the PATCH body. Set ``supports_modify=False`` for create-only resources
        (e.g. schemas) where an existing row is left untouched.

        Preserves check_mode (no writes), diff output, and second-run no-op.
        """
        if self.params.get("state", "present") == "absent":
            self.module.fail_json(msg=f"{self.resource_name} does not support state=absent; use {delete_module}")
        self.validate_run_params()
        current = self.find_current()

        changed = False
        diff_before: Dict[str, Any] = {}
        diff_after: Dict[str, Any] = {}

        if not current:
            desired = self.build_desired_state(operation="create", current_state=None)
            if not self.check_mode:
                # Some create endpoints return an empty body; fall back to desired.
                result_data = self.create(desired) or dict(desired)
            else:
                result_data = dict(desired)
            changed = True
            diff_after = dict(result_data)
        elif supports_modify:
            result_data, changed, diff_before, diff_after = self._reconcile_present(current, identity)
        else:
            result_data = current

        result = {"changed": changed, self.resource_name: result_data}
        if changed and (diff_before or diff_after):
            result["diff"] = {"before": diff_before, "after": diff_after}
        self.module.exit_json(**result)

    def _reconcile_present(
        self, current: Dict[str, Any], identity: Tuple[str, ...]
    ) -> Tuple[Dict[str, Any], bool, Dict[str, Any], Dict[str, Any]]:
        """Body-keyed PATCH an existing row toward desired state (idempotent)."""
        desired = self.build_desired_state(operation="update", current_state=current)
        current_normalized = normalize_resource(current, self.overrides, exclude_immutable=True, user_resource=desired)
        desired_normalized = normalize_resource(
            desired,
            self.overrides,
            exclude_immutable=True,
            include_ephemeral=self.include_ephemeral_in_updates,
        )
        patch = compute_patch(current_normalized, desired_normalized, self.overrides, clear_fields=self._fields_to_clear())
        for key in identity:
            patch.pop(key, None)
        if not patch:
            return current, False, {}, {}

        id_params = {key: self.params[key] for key in identity if self.params.get(key) is not None}
        if not self.check_mode:
            try:
                self.client.api[self.resource_name].patch(**{**id_params, **patch})
            except VastAPIError as e:
                self.module.fail_json(msg=f"Failed to update {self.singular} {id_params}: {e}")
        result_data = {**current, **patch}
        return result_data, True, dict(current), dict(result_data)

    def find_current(self) -> Optional[Dict[str, Any]]:
        """Return current row via show, optionally falling back to scoped LIST."""
        required_fields = self.show_query_fields if self.required_show_query_fields is None else self.required_show_query_fields
        if any(self.params.get(field) is None for field in required_fields):
            return None
        query = {}
        for field in self.show_query_fields:
            value = self.params.get(field)
            if value is not None:
                query[field] = value
        api = self.client.api[self.resource_name]
        try:
            detail = api[self.show_sub_path].first(**query)
            if isinstance(detail, dict):
                return detail
            if detail is not None:
                raise VastAPIError(f"{self.resource_name} show lookup returned {type(detail).__name__}; expected dict")
            # detail is None: fall through to optional scoped LIST fallback.
        except VastNotFoundError:
            pass
        except RESTFailure as e:
            body = e.body or ""
            status_miss = e.status in self.not_found_statuses
            # Deliberate rule exception (no error-body matching): APIs that return 500 instead of 404; file API ticket for 404 then delete this.
            body_miss = bool(self.not_found_body_substrings) and any(substr in body for substr in self.not_found_body_substrings)
            if not (status_miss or body_miss):
                raise
        if self.list_fallback_scope_fields is None:
            return None
        return self._find_via_scoped_list()

    def _find_via_scoped_list(self) -> Optional[Dict[str, Any]]:
        scope = {field: self.params[field] for field in self.list_fallback_scope_fields if self.params.get(field) is not None}
        match_field = self.list_fallback_match_field
        match_value = self.params.get(match_field) if match_field else None
        rows = self._unwrap_list_rows(self.client.api[self.resource_name].get(**scope))
        if match_field is None:
            return rows[0] if rows else None
        return next(
            (row for row in rows if isinstance(row, dict) and row.get(match_field) == match_value),
            None,
        )

    @staticmethod
    def _unwrap_list_rows(raw: Any) -> List[Dict[str, Any]]:
        rows: List[Dict[str, Any]] = []
        for item in raw if isinstance(raw, list) else [raw]:
            if isinstance(item, dict) and isinstance(item.get("results"), list):
                rows.extend(item["results"])
            elif isinstance(item, dict):
                rows.append(item)
        return rows
