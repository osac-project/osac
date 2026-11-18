"""Preserve filesystem selection when reconciling immutable class parameters."""


def storage_class_parameters(parameters, existing_classes, name, block_parameters=None):
    existing = next(
        (entry for entry in existing_classes if entry["metadata"]["name"] == name),
        None,
    )
    filesystem = block_parameters or {}
    if existing is not None:
        filesystem = {
            key: value
            for key, value in (existing.get("parameters") or {}).items()
            if key == "csi.storage.k8s.io/fstype" or key.lower() == "fstype"
        }
    return {**parameters, **filesystem}


class FilterModule:
    def filters(self):
        return {"storage_class_parameters": storage_class_parameters}
