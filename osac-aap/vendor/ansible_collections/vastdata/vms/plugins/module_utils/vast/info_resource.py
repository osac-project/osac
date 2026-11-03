"""Base classes for read-only ``*_info`` modules.

Info modules implement the Ansible ``<thing>_info`` convention: read-only,
always ``check_mode`` safe, never ``changed``, no ``state``. They list a
resource (optionally filtered) and return the matching objects.

``BaseInfoResource`` covers top-level resources (``GET /<resource>/``);
``SubEndpointInfoResource`` covers nested paths
(``GET /<parent>/<id>/<sub_path>/``).
"""

from typing import Any, Dict, List, Set

from ansible.module_utils.basic import AnsibleModule

from .errors import VastError
from .schema_overrides import get_overrides, normalize_list_response
from .version import VersionAwareMixin

# Params that are never forwarded to the API as query filters.
_NON_FILTER_PARAMS: Set[str] = {"vms"}


class BaseInfoResource(VersionAwareMixin):
    """Base class for top-level ``*_info`` modules.

    Subclasses must define:
    - resource_name: str  # e.g. "vippools"

    Subclasses may override:
    - return_key: str     # output key holding the list (defaults to resource_name)
    - filter_fields: Set[str]  # if set, only these params are sent as query filters
    """

    resource_name: str = NotImplemented
    return_key: str = ""
    # When non-empty, restricts which user params are forwarded as query params.
    filter_fields: Set[str] = set()

    def __init__(self, module: AnsibleModule):
        self._init_vast_connection(module)
        self.overrides: Dict[str, Any] = get_overrides(self._overrides_key(), self.cluster_mm)

    def _overrides_key(self) -> str:
        """Schema-overrides key for this resource (its own name at top level)."""
        return self.resource_name

    def _version_entity(self) -> str:
        return f"Resource '{self.resource_name}'"

    def _collect_filters(self) -> Dict[str, Any]:
        """Build the query-param dict from non-None, non-connection params."""
        filters: Dict[str, Any] = {}
        for name, value in self.params.items():
            if name in _NON_FILTER_PARAMS or value is None:
                continue
            if self.filter_fields and name not in self.filter_fields:
                continue
            filters[name] = value
        return filters

    def _query(self, filters: Dict[str, Any]) -> List[dict]:
        """Run the list query. Override for non-top-level paths."""
        return self.client.api[self.resource_name].get(**filters)

    def run(self) -> None:
        """List the resource and exit with the results (never changed)."""
        try:
            filters = self._collect_filters()
            results = normalize_list_response(self.overrides, self._query(filters))
        except VastError as e:
            # All client/auth/API failures derive from VastError; surface cleanly.
            self.module.fail_json(msg=str(e))
            return

        key = self.return_key or self.resource_name
        self.module.exit_json(changed=False, **{key: results})


class SubEndpointInfoResource(BaseInfoResource):
    """Base class for sub-endpoint ``*_info`` modules.

    Lists ``GET /<parent_resource>/<parent_id>/<sub_path>/``.

    Subclasses must define:
    - parent_resource: str
    - sub_path: str
    - parent_id_param: str  # name of the module param holding the parent id

    ``path_has_id`` controls whether a parent id segment is inserted.
    """

    parent_resource: str = NotImplemented
    sub_path: str = NotImplemented
    parent_id_param: str = "id"
    path_has_id: bool = True

    def _overrides_key(self) -> str:
        # Sub-endpoint overrides are keyed by the module's own name, not the parent.
        return self.return_key or self.sub_path

    # Sub-endpoint params that address the path rather than filter the query.
    def _path_params(self) -> Set[str]:
        return {self.parent_id_param} if self.path_has_id else set()

    def _collect_filters(self) -> Dict[str, Any]:
        path_params = self._path_params()
        return {k: v for k, v in super()._collect_filters().items() if k not in path_params}

    def _query(self, filters: Dict[str, Any]) -> List[dict]:
        path = self.client.api[self.parent_resource]
        if self.path_has_id:
            parent_id = self.params.get(self.parent_id_param)
            path = path[parent_id]
        path = path[self.sub_path]
        return path.get(**filters)
