# pyright: reportExplicitAny=false

import json
import re
import subprocess
import yaml

from typing import Any
from typing import cast
from typing import Literal
from typing import Self
from typing import TypedDict

from collections.abc import Generator

from pathlib import Path
from enum import StrEnum

import pydantic

from ansible.utils.display import Display
from ansible.errors import AnsibleFilterError

display = Display()

AnsibleArgumentType = Literal[
    "str",
    "string",
    "list",
    "dict",
    "bool",
    "int",
    "float",
    "path",
    "raw",
    "json",
    "jsonarg",
    "bytes",
    "bits",
]


# Type hint for TemplateParameter.from_argspec method
class AnsibleArgumentSpecEntry(TypedDict):
    name: str
    short_description: str | None
    description: str | None
    type: AnsibleArgumentType
    required: bool
    default: Any
    choices: list[Any]
    options: dict[str, "AnsibleArgumentSpecEntry"]  # Recursive definition


# Type hint for reading argument_specs.yaml files
class AnsibleArgumentSpec(TypedDict):
    argument_specs: dict[str, AnsibleArgumentSpecEntry]


# Type hint for the output of `ansible-galaxy collection list`
AnsibleCollectionList = dict[str, dict[str, dict[str, str]]]


class ProtobufType(StrEnum):
    BOOL = "type.googleapis.com/google.protobuf.BoolValue"
    INT = "type.googleapis.com/google.protobuf.Int64Value"
    FLOAT = "type.googleapis.com/google.protobuf.DoubleValue"
    STRING = "type.googleapis.com/google.protobuf.StringValue"
    BYTEARRAY = "type.googleapis.com/google.protobuf.BytesValue"
    ANY = "type.googleapis.com/google.protobuf.Value"


# This maps Ansible argument types [1] and Python types to the protobuf types
# [2] used in the fulfillment service.
#
# [1]: https://docs.ansible.com/ansible/latest/dev_guide/developing_program_flow_modules.html#argument-spec
# [2]: https://googleapis.dev/nodejs/analytics-admin/latest/google.protobuf.html
TypeMapping: dict[AnsibleArgumentType | type, ProtobufType] = {
    "str": ProtobufType.STRING,
    str: ProtobufType.STRING,
    "list": ProtobufType.ANY,
    "dict": ProtobufType.ANY,
    list: ProtobufType.ANY,
    dict: ProtobufType.ANY,
    "bool": ProtobufType.BOOL,
    bool: ProtobufType.BOOL,
    "int": ProtobufType.INT,
    int: ProtobufType.INT,
    "float": ProtobufType.FLOAT,
    float: ProtobufType.FLOAT,
    "path": ProtobufType.STRING,
    "json": ProtobufType.STRING,
    "string": ProtobufType.STRING,
    "bytes": ProtobufType.BYTEARRAY,
}


class Base(pydantic.BaseModel):
    """Base model with common Pydantic configuration for all models."""

    model_config = pydantic.ConfigDict(
        # Keep flexible for Ansible data (YAML can have various formats)
        strict=False,
        # Validate on assignment for better error detection
        validate_assignment=True,
        # Allow arbitrary types (needed for Path objects)
        arbitrary_types_allowed=True,
    )


class ProtobufAnyValue(Base):
    type: ProtobufType = pydantic.Field(
        ProtobufType.STRING, serialization_alias="@type"
    )
    value: Any


class TemplateParameter(Base):
    """TemplateParameter represents a single template parameter"""

    name: str
    title: str | None
    description: str | None
    required: bool = False
    type: ProtobufType = ProtobufType.STRING
    default: ProtobufAnyValue | None = None

    @classmethod
    def from_argspec(cls, name: str, spec: AnsibleArgumentSpecEntry) -> Self:
        """Given an option name and Ansible argument spec, return a TemplateParameter"""
        return cls(
            name=name,
            title=spec.get("short_description"),
            description=spec.get("description"),
            required=spec.get("required", False),
            default=spec.get("default"),
            type=TypeMapping[spec.get("type", "str")],
        )

    @classmethod
    def from_definition(cls, defn: "TemplateParameterDefinition") -> Self:
        """Create a TemplateParameter from a TemplateParameterDefinition (osac.yaml)."""
        return cls(
            name=defn.name,
            title=defn.title,
            description=defn.description,
            required=defn.required,
            default=defn.default,
            type=TypeMapping.get(defn.type, ProtobufType.STRING),
        )

    @pydantic.field_validator("default", mode="before")
    @classmethod
    def validate_default(cls, value: Any) -> ProtobufAnyValue | None:
        """The 'default' field in the fulfillment API is using the `protobufAny` schema
        defined in [1]. This requires a value of the form:

            {
                "@type": "type.googleapis.com/google.protobuf.StringValue",
                "value": "my default value"
            }

        We handle this with a "before" field validator which transforms a
        Python variable into a ProtobufAnyValue object by mapping the variable
        type to the protobuf type string via the `TypeMapping` table, and then
        storing the actual value in the "value" key.

        Scalar values are mapped to their specific protobuf wrappers
        (StringValue, BoolValue, etc.) while list and dict values are
        mapped to google.protobuf.Value (ANY).

        [1]: https://raw.githubusercontent.com/osac-project/fulfillment-api/refs/heads/main/openapi/v3/openapi.yaml
        """
        if value is not None:
            try:
                return ProtobufAnyValue(type=TypeMapping[type(value)], value=value)
            except KeyError as err:
                raise ValueError(
                    f"Unsupported default value type: {err}") from None
        return None


class NodeRequest(Base):
    """NodeRequest represents the bare metal resources requested for a cluster"""

    resource_class: str = pydantic.Field(..., validation_alias="resourceClass")
    number_of_nodes: int = pydantic.Field(...,
                                          validation_alias="numberOfNodes")


class HostTypeReference(Base):
    """Typed reference to a HostType resource (matches the proto HostTypeReference message)."""

    name: str


class NodeSet(Base):
    """NodeSet represents the template's default bare metal resources"""

    host_type: HostTypeReference
    size: int


class ComputeInstanceImage(Base):
    """Image configuration for compute instance spec defaults."""

    source_type: str = pydantic.Field(
        default="registry",
        validation_alias=pydantic.AliasChoices("sourceType", "source_type"),
        serialization_alias="source_type",
    )
    source_ref: str = pydantic.Field(
        ...,
        validation_alias=pydantic.AliasChoices("sourceRef", "source_ref"),
        serialization_alias="source_ref",
    )


class ComputeInstanceDisk(Base):
    """Disk configuration for compute instance spec defaults."""

    size_gib: int = pydantic.Field(
        ...,
        validation_alias=pydantic.AliasChoices("sizeGiB", "sizeGib", "size_gib"),
        serialization_alias="size_gib",
    )


class ComputeInstanceTemplateSpecDefaults(Base):
    """Default values for compute instance spec fields.

    Maps from Ansible camelCase (defaults/main.yaml) to proto snake_case.

    Note: cores/memory_gib are intentionally absent — they are reserved
    (removed) on the fulfillment-service ComputeInstanceTemplateSpecDefaults
    proto. instance_type is now the sole way to size a ComputeInstance.
    """

    image: ComputeInstanceImage | None = None
    boot_disk: ComputeInstanceDisk | None = pydantic.Field(
        default=None,
        validation_alias=pydantic.AliasChoices("bootDisk", "boot_disk"),
        serialization_alias="boot_disk",
    )
    run_strategy: str | None = pydantic.Field(
        default=None,
        validation_alias=pydantic.AliasChoices("runStrategy", "run_strategy"),
        serialization_alias="run_strategy",
    )


class ClusterNetwork(Base):
    """Network CIDR configuration for cluster spec defaults."""

    pod_cidr: str | None = None
    service_cidr: str | None = None


class ClusterTemplateSpecDefaults(Base):
    """Default values for cluster spec fields.

    Matches the fulfillment-service ClusterTemplateSpecDefaults proto message.
    """

    pull_secret: str | None = None
    ssh_public_key: str | None = None
    release_image: str | None = None
    network: ClusterNetwork | None = None


class ParameterValidation(Base):
    """Validation rules for a template parameter."""

    pattern: str | None = None


class TemplateParameterDefinition(Base):
    """A parameter definition as written in osac.yaml."""

    name: str
    title: str | None = None
    description: str | None = None
    type: str = "string"
    required: bool = False
    default: str | int | float | bool | list[Any] | dict[str, Any] | None = None
    validation: ParameterValidation | None = None


class TemplateTypeEnum(StrEnum):
    cluster = "cluster"
    compute_instance = "compute_instance"
    network = "network"
    storage_provider = "storage_provider"
    bare_metal_instance = "bare_metal_instance"


class SecurityRule(Base):
    """A security rule for ingress or egress."""

    protocol: str
    port_from: int | None = None
    port_to: int | None = None
    ipv4_cidr: str | None = None
    ipv6_cidr: str | None = None


class NetworkDefaults(Base):
    """Default networking configuration for tenant onboarding."""

    virtual_network_ipv4_cidr: str | None = None
    virtual_network_ipv6_cidr: str | None = None
    subnet_ipv4_cidr: str | None = None
    subnet_ipv6_cidr: str | None = None
    enable_nat_gateway: bool = False
    ingress_rules: list[SecurityRule] | None = None
    egress_rules: list[SecurityRule] | None = None


class NetworkClassCapabilities(Base):
    """Capabilities supported by a network class"""

    supports_ipv4: bool = False
    supports_ipv6: bool = False
    supports_dual_stack: bool = False


class Metadata(Base):
    """Metadata about the template"""

    metadata_name: str | None = pydantic.Field(None, validation_alias="name")
    title: str
    description: str | None = None
    template_type: TemplateTypeEnum = pydantic.Field(
        default=TemplateTypeEnum.cluster, exclude=True
    )
    default_node_request: list[NodeRequest] = pydantic.Field(default_factory=list)
    allowed_resource_classes: list[str] | None = None
    # Network-specific fields
    implementation_strategy: str | None = None
    fabric_manager: str | None = None
    k8s_manager: str | None = None
    is_default: bool = False
    capabilities: NetworkClassCapabilities | None = None
    defaults: NetworkDefaults | None = None
    parameters: list[TemplateParameterDefinition] = pydantic.Field(default_factory=list)

    # spec_defaults is used to set optional default values for the related spec fields associated
    # with the template type. Stored as a raw dict and parsed into the type-specific model
    # (ClusterTemplateSpecDefaults or ComputeInstanceTemplateSpecDefaults) in templates().
    spec_defaults: dict[str, Any] | None = None


class BaseTemplate(Base):
    """Base class for all template types"""

    collection: str = pydantic.Field(..., exclude=True)
    path: Path = pydantic.Field(..., exclude=True)
    name: str = pydantic.Field(..., exclude=True)
    metadata_name: str | None = pydantic.Field(None, exclude=True)
    title: str | None = None
    description: str | None = None
    template_type: TemplateTypeEnum = pydantic.Field(exclude=True)
    parameters: list[TemplateParameter]

    @pydantic.field_serializer("path")
    def serialize_path(self, value: Path):
        return str(value)

    @pydantic.computed_field
    def id(self) -> str:
        return f"{self.collection}.{self.name}"

    @pydantic.computed_field
    def metadata(self) -> dict[str, str]:
        name = self.metadata_name if self.metadata_name else self.name.replace("_", "-")
        return {"name": name}


class ClusterTemplate(BaseTemplate):
    """Template for cluster deployments"""

    template_type: Literal[TemplateTypeEnum.cluster] = pydantic.Field(
        default=TemplateTypeEnum.cluster, exclude=True
    )
    default_node_request: list[NodeRequest] = pydantic.Field(default=[], exclude=True)
    allowed_resource_classes: list[str] | None = pydantic.Field(None, exclude=True)
    spec_defaults: ClusterTemplateSpecDefaults | None = None

    @pydantic.computed_field
    def node_sets(self) -> dict[str, NodeSet] | None:
        ret = {
            nr.resource_class: NodeSet(
                host_type=HostTypeReference(name=nr.resource_class),
                size=nr.number_of_nodes,
            )
            for nr in self.default_node_request
        }
        return ret if ret else None


class ComputeInstanceTemplate(BaseTemplate):
    """Template for ComputeInstance deployments"""

    template_type: Literal[TemplateTypeEnum.compute_instance] = pydantic.Field(
        default=TemplateTypeEnum.compute_instance, exclude=True
    )
    spec_defaults: ComputeInstanceTemplateSpecDefaults | None = None


class BareMetalInstanceTemplate(BaseTemplate):
    """Template for BareMetalInstance deployments"""

    template_type: Literal[TemplateTypeEnum.bare_metal_instance] = pydantic.Field(
        default=TemplateTypeEnum.bare_metal_instance, exclude=True
    )
    # BareMetalInstanceTemplate API does not support parameters field
    parameters: list[TemplateParameter] = pydantic.Field(default_factory=list, exclude=True)


class NetworkClassSpec(Base):
    """Spec for a NetworkClass, containing defaults."""

    defaults: NetworkDefaults | None = None


class NetworkClassTemplate(Base):
    """Template for NetworkClass registration.

    Unlike cluster/compute_instance templates, NetworkClass resources have a different
    shape (implementation_strategy, capabilities) rather than parameters. This model
    serializes directly to the NetworkClass API payload.
    """

    collection: str = pydantic.Field(..., exclude=True)
    path: Path = pydantic.Field(..., exclude=True)
    name: str = pydantic.Field(..., exclude=True)
    metadata_name: str | None = pydantic.Field(None, exclude=True)
    template_type: Literal[TemplateTypeEnum.network] = pydantic.Field(
        default=TemplateTypeEnum.network, exclude=True
    )
    defaults: NetworkDefaults | None = pydantic.Field(default=None, exclude=True)
    title: str
    description: str | None = None
    implementation_strategy: str
    fabric_manager: str | None = None
    k8s_manager: str | None = None
    is_default: bool = False
    capabilities: NetworkClassCapabilities
    spec: NetworkClassSpec | None = None

    @pydantic.model_validator(mode="after")
    def _nest_defaults_in_spec(self) -> "NetworkClassTemplate":
        if self.defaults is None:
            return self
        if self.spec is None:
            self.spec = NetworkClassSpec(defaults=self.defaults)
        elif self.spec.defaults is None:
            self.spec.defaults = self.defaults
        return self

    @pydantic.field_serializer("path")
    def serialize_path(self, value: Path):
        return str(value)

    @pydantic.computed_field
    def metadata(self) -> dict[str, str]:
        name = self.metadata_name if self.metadata_name else self.implementation_strategy.replace("_", "-")
        return {"name": name}


def _validate_collection_name(name: str) -> None:
    """Validate that collection name follows namespace.collection format.

    Args:
        name: Collection name to validate

    Raises:
        AnsibleFilterError: If collection name format is invalid
    """
    if not re.match(r'^[a-zA-Z0-9_]+\.[a-zA-Z0-9_]+$', name):
        raise AnsibleFilterError(
            f"Invalid collection name format: '{name}'. "
            f"Expected format: namespace.collection (e.g., 'osac.service')"
        )


class Collection(Base):
    """Collection represents an Ansible collection"""

    parent_path: Path
    name: str

    def _read_yaml(self, path: Path, subdir: str, name: str) -> dict[str, Any] | None:
        """Find and load a YAML file from a role subdirectory.

        Tries .yaml then .yml extensions, returning the parsed contents of
        the first file found, or None if no file exists or parsing fails.

        Args:
            path: Path to the role directory
            subdir: Subdirectory within the role (e.g. "meta", "defaults")
            name: Filename without extension (e.g. "main", "osac")

        Returns:
            Parsed YAML dict if found and valid, None otherwise
        """
        for ext in (".yaml", ".yml"):
            filepath = path / subdir / f"{name}{ext}"
            if filepath.exists():
                break
        else:
            return None

        try:
            with filepath.open("r", encoding="utf-8") as fd:
                data = yaml.safe_load(fd)
        except yaml.YAMLError as e:
            display.warning(f"Failed to parse {filepath}: {e}")
            return None
        except (PermissionError, OSError) as e:
            display.warning(f"Error reading {filepath}: {e}")
            return None

        if data and isinstance(data, dict):
            return data

        return None

    def read_metadata_for_role(self, path: Path) -> Metadata | None:
        """Read metadata for a role from osac.yaml/yml file."""
        data = self._read_yaml(path, "meta", "osac")
        if data is None:
            display.vvv(f"No metadata file found for role at {path}")
            return None

        try:
            return Metadata.model_validate(data)
        except Exception as e:
            display.warning(f"Invalid metadata for role at {path}: {e}")
            return None

    def read_params_for_role(self, path: Path) -> list[TemplateParameter]:
        """Read template parameters for a role from argument_specs.yaml/yml file."""
        data = self._read_yaml(path, "meta", "argument_specs")
        if data is None:
            return []

        template_params: list[TemplateParameter] = []

        # Navigate the nested structure to find template_parameters
        # Missing keys at any level are valid - just means no parameters defined
        for name, spec in (
            data.get("argument_specs", {})
            .get("main", {})
            .get("options", {})
            .get("template_parameters", {})
            .get("options", {})
            .items()
        ):
            try:
                template_params.append(TemplateParameter.from_argspec(name, spec))
            except Exception as e:
                display.warning(
                    f"Failed to parse template parameter '{name}' in {path}: {e}"
                )
                continue

        return template_params

    def templates(self) -> Generator[BaseTemplate | NetworkClassTemplate, None, None]:
        """Generate Template objects for all roles in this collection.

        Yields:
            BaseTemplate or NetworkClassTemplate objects for each valid role found
        """
        roles_dir = self.parent_path / self.name.replace(".", "/") / "roles"

        # Validate roles directory exists
        if not roles_dir.exists():
            display.vvv(f"No roles directory found for collection '{self.name}' at {roles_dir}")
            return

        if not roles_dir.is_dir():
            display.warning(f"Expected directory but found file at {roles_dir}")
            return

        for path in roles_dir.glob("*"):
            # Only process directories (roles must be directories)
            if not path.is_dir():
                display.vvv(f"Skipping non-directory item in roles: {path.name}")
                continue

            metadata = self.read_metadata_for_role(path)
            if metadata is not None:
                try:
                    if metadata.parameters:
                        params = [
                            TemplateParameter.from_definition(d)
                            for d in metadata.parameters
                        ]
                    else:
                        params = self.read_params_for_role(path)

                    common = {
                        "collection": self.name,
                        "path": path,
                        "name": path.name,
                        "metadata_name": metadata.metadata_name,
                        "title": metadata.title,
                        "description": metadata.description,
                        "parameters": params,
                    }

                    if metadata.template_type == TemplateTypeEnum.cluster:
                        cluster_spec_defaults = None
                        if metadata.spec_defaults:
                            cluster_spec_defaults = ClusterTemplateSpecDefaults.model_validate(
                                metadata.spec_defaults
                            )
                        yield ClusterTemplate(
                            **common,
                            default_node_request=metadata.default_node_request,
                            allowed_resource_classes=metadata.allowed_resource_classes,
                            spec_defaults=cluster_spec_defaults,
                        )
                    elif metadata.template_type == TemplateTypeEnum.network:
                        if not metadata.implementation_strategy:
                            display.warning(
                                f"Network role '{path.name}' in collection '{self.name}' "
                                f"is missing required 'implementation_strategy' in osac.yaml"
                            )
                            continue
                        yield NetworkClassTemplate(
                            collection=self.name,
                            path=path,
                            name=path.name,
                            metadata_name=metadata.metadata_name,
                            title=metadata.title,
                            description=metadata.description,
                            implementation_strategy=metadata.implementation_strategy,
                            fabric_manager=metadata.fabric_manager,
                            k8s_manager=metadata.k8s_manager,
                            is_default=metadata.is_default,
                            capabilities=metadata.capabilities or NetworkClassCapabilities(),
                            defaults=metadata.defaults,
                        )
                    elif metadata.template_type == TemplateTypeEnum.storage_provider:
                        # Storage provider roles are not yielded as compute instance or
                        # network templates — they are dispatched via osac.service.storage_provider.
                        display.vvv(
                            f"Skipping storage_provider role '{path.name}' in collection '{self.name}'"
                        )
                        continue
                    elif metadata.template_type == TemplateTypeEnum.bare_metal_instance:
                        yield BareMetalInstanceTemplate(**common)
                    elif metadata.template_type == TemplateTypeEnum.compute_instance:
                        compute_spec_defaults = None
                        if metadata.spec_defaults:
                            compute_spec_defaults = ComputeInstanceTemplateSpecDefaults.model_validate(
                                metadata.spec_defaults
                            )
                        yield ComputeInstanceTemplate(**common, spec_defaults=compute_spec_defaults)
                    else:
                        display.warning(
                            f"Unknown template_type '{metadata.template_type}' for role '{path.name}' "
                            f"in collection '{self.name}'"
                        )
                except Exception as e:
                    display.warning(
                        f"Failed to create template for role '{path.name}' in collection '{self.name}': {e}"
                    )
                    continue


def find_template_roles(requested: list[str]) -> Generator[BaseTemplate | NetworkClassTemplate, None, None]:
    """Find template roles in requested Ansible collections.

    Args:
        requested: List of collection names to search

    Yields:
        BaseTemplate or NetworkClassTemplate objects found in the collections
    """
    display.vv(f"Searching for templates in collections: {', '.join(requested)}")

    collections: list[Collection] = []
    for collection in requested:
        # Validate collection name format
        try:
            _validate_collection_name(collection)
        except AnsibleFilterError as e:
            display.warning(str(e))
            continue

        display.vvv(f"Querying ansible-galaxy for collection: {collection}")

        try:
            output = subprocess.check_output(
                [
                    "ansible-galaxy",
                    "collection",
                    "list",
                    collection,
                    "--format",
                    "json",
                ],
                stdin=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                timeout=30,
            )
            info: AnsibleCollectionList = cast(
                AnsibleCollectionList,
                json.loads(output)
            )
        except subprocess.TimeoutExpired:
            display.warning(
                f"Timeout querying ansible-galaxy for collection '{collection}' (30s limit exceeded)"
            )
            continue
        except subprocess.CalledProcessError as e:
            stderr_msg = e.stderr.decode('utf-8', errors='replace') if e.stderr else "No error output"
            display.warning(
                f"Failed to query collection '{collection}': {stderr_msg}"
            )
            continue
        except FileNotFoundError:
            raise AnsibleFilterError(
                "ansible-galaxy command not found. Ensure Ansible is properly installed."
            )
        except json.JSONDecodeError as e:
            display.warning(
                f"Invalid JSON response from ansible-galaxy for collection '{collection}': {e}"
            )
            continue

        if info:
            # If `ansible-galaxy collection list` finds the collection in multiple paths,
            # we will select the first one and warn the user.
            collection_paths = list(info.keys())

            if len(collection_paths) > 1:
                display.warning(
                    f"Collection '{collection}' found in multiple locations: {collection_paths}. "
                    f"Using first location: {collection_paths[0]}"
                )

            collection_path = Path(collection_paths[0])
            display.vvv(f"Found collection '{collection}' at {collection_path}")
            collections.append(
                Collection(parent_path=collection_path, name=collection)
            )
        else:
            display.vv(f"Collection '{collection}' not found")

    for collection in collections:
        yield from collection.templates()


def find_template_roles_filter(template_type: TemplateTypeEnum):
    """Factory function that returns a filter for the specified template type.

    Args:
        template_type: The type of template to filter for

    Returns:
        A filter function that accepts a list of collection names and returns
        matching template role dictionaries
    """
    def filter_func(requested: list[str]) -> list[dict[str, Any]]:
        try:
            roles = (
                role for role in find_template_roles(requested)
                if role.template_type == template_type
            )
            result = [
                role.model_dump(by_alias=True, exclude_none=True)
                for role in roles
            ]
            display.vv(f"Returning {len(result)} {template_type} template(s)")
            return result

        except AnsibleFilterError:
            raise
        except Exception as e:
            display.error(f"Unexpected error in find_template_roles filter: {e}")
            raise AnsibleFilterError(f"Template discovery failed: {str(e)}")

    return filter_func


def find_network_class_roles_filter(requested: list[str]) -> list[dict[str, Any]]:
    """Filter that discovers network class roles and returns NetworkClass API payloads.

    Args:
        requested: List of collection names to search

    Returns:
        List of NetworkClass dictionaries ready for the fulfillment service API
    """
    try:
        roles = (
            role for role in find_template_roles(requested)
            if isinstance(role, NetworkClassTemplate)
        )
        result = [
            role.model_dump(by_alias=True, exclude_none=True)
            for role in roles
        ]
        display.vv(f"Returning {len(result)} network class(es)")
        return result

    except AnsibleFilterError:
        raise
    except Exception as e:
        display.error(f"Unexpected error in find_network_class_roles filter: {e}")
        raise AnsibleFilterError(f"Network class discovery failed: {str(e)}")


class FilterModule:
    """Ansible filter plugin for finding template roles."""

    def filters(self) -> dict[str, Any]:
        """Return the available filter functions.

        Returns:
            Dictionary mapping filter names to filter functions
        """
        return {
            "find_cluster_template_roles": find_template_roles_filter(TemplateTypeEnum.cluster),
            "find_compute_instance_template_roles": find_template_roles_filter(TemplateTypeEnum.compute_instance),
            "find_bare_metal_instance_template_roles": find_template_roles_filter(TemplateTypeEnum.bare_metal_instance),
            "find_network_class_roles": find_network_class_roles_filter,
        }


if __name__ == "__main__":
    import sys

    # Usage: python find_template_roles.py --type cluster|compute_instance|bare_metal_instance|network collection1 collection2 ...
    if "--type" not in sys.argv:
        print("Error: --type parameter is required", file=sys.stderr)
        print("Usage: python find_template_roles.py --type cluster|compute_instance|bare_metal_instance|network collection1 collection2 ...", file=sys.stderr)
        sys.exit(1)

    type_idx = sys.argv.index("--type")
    if type_idx + 1 >= len(sys.argv):
        print("Error: --type requires a value (cluster, compute_instance, bare_metal_instance, or network)", file=sys.stderr)
        sys.exit(1)

    template_type = sys.argv[type_idx + 1]
    collections = sys.argv[1:type_idx] + sys.argv[type_idx + 2:]

    if not collections:
        print("Error: At least one collection name is required", file=sys.stderr)
        print("Usage: python find_template_roles.py --type cluster|compute_instance|bare_metal_instance|network collection1 collection2 ...", file=sys.stderr)
        sys.exit(1)

    if template_type == TemplateTypeEnum.cluster:
        filter_func = find_template_roles_filter(TemplateTypeEnum.cluster)
    elif template_type == TemplateTypeEnum.compute_instance:
        filter_func = find_template_roles_filter(TemplateTypeEnum.compute_instance)
    elif template_type == TemplateTypeEnum.bare_metal_instance:
        filter_func = find_template_roles_filter(TemplateTypeEnum.bare_metal_instance)
    elif template_type == TemplateTypeEnum.network:
        filter_func = find_network_class_roles_filter
    else:
        print(f"Error: Invalid template type '{template_type}'. Must be 'cluster', 'compute_instance', 'bare_metal_instance', or 'network'", file=sys.stderr)
        sys.exit(1)

    found = filter_func(collections)
    print(json.dumps(found))
