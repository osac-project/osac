from pathlib import Path

import pydantic
import pytest
import yaml

from find_template_roles import (
    Metadata,
    ProtobufAnyValue,
    ProtobufType,
    TemplateParameter,
    TemplateParameterDefinition,
    TemplateTypeEnum,
    TypeMapping,
)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _make_definition(**overrides: object) -> TemplateParameterDefinition:
    defaults = {"name": "test_param", "title": "Test", "description": "A test param"}
    defaults.update(overrides)
    return TemplateParameterDefinition(**defaults)


def _discover_osac_yamls(roles_dir: Path) -> list[Path]:
    yamls: list[Path] = []
    for ext in ("yaml", "yml"):
        yamls.extend(roles_dir.glob(f"*/meta/osac.{ext}"))
    return sorted(yamls)


def _load_metadata(roles_dir: Path, role_name: str) -> Metadata:
    for ext in ("yaml", "yml"):
        candidate = roles_dir / role_name / "meta" / f"osac.{ext}"
        if candidate.exists():
            with candidate.open("r", encoding="utf-8") as f:
                return Metadata.model_validate(yaml.safe_load(f))
    pytest.fail(f"osac.yaml not found for role {role_name}")


# ---------------------------------------------------------------------------
# TestTemplateParameterDefinitionDefaults
# ---------------------------------------------------------------------------

class TestTemplateParameterDefinitionDefaults:

    @pytest.mark.parametrize(
        ("value", "expected_type"),
        [
            ("hello", str),
            (42, int),
            (3.14, float),
            (True, bool),
        ],
        ids=["str", "int", "float", "bool"],
    )
    def test_scalar_defaults(self, value, expected_type):
        defn = _make_definition(default=value)
        assert defn.default == value
        assert isinstance(defn.default, expected_type)

    @pytest.mark.parametrize(
        ("value", "expected_type"),
        [
            ([], list),
            (["a", "b"], list),
            ({}, dict),
            ({"key": "val"}, dict),
        ],
        ids=["empty_list", "non_empty_list", "empty_dict", "non_empty_dict"],
    )
    def test_collection_defaults(self, value, expected_type):
        defn = _make_definition(default=value)
        assert defn.default == value
        assert isinstance(defn.default, expected_type)

    def test_none_default(self):
        defn = _make_definition()
        assert defn.default is None


# ---------------------------------------------------------------------------
# TestTemplateParameterFromDefinition
# ---------------------------------------------------------------------------

class TestTemplateParameterFromDefinition:

    @pytest.mark.parametrize(
        ("default_value", "ansible_type", "expected_proto_type"),
        [
            ("hello", "string", ProtobufType.STRING),
            (42, "int", ProtobufType.INT),
            (3.14, "float", ProtobufType.FLOAT),
            (True, "bool", ProtobufType.BOOL),
            ([], "list", ProtobufType.ANY),
            ({"k": "v"}, "dict", ProtobufType.ANY),
        ],
        ids=["str", "int", "float", "bool", "list", "dict"],
    )
    def test_type_mapping_and_default(
        self, default_value, ansible_type, expected_proto_type
    ):
        defn = _make_definition(default=default_value, type=ansible_type)
        param = TemplateParameter.from_definition(defn)

        assert param.type == expected_proto_type
        assert param.default is not None
        assert param.default.type == expected_proto_type
        assert param.default.value == default_value

    def test_none_default_produces_none(self):
        defn = _make_definition(default=None, type="string")
        param = TemplateParameter.from_definition(defn)
        assert param.default is None

    def test_unknown_ansible_type_falls_back_to_string(self):
        defn = _make_definition(type="unknown_type")
        param = TemplateParameter.from_definition(defn)
        assert param.type == ProtobufType.STRING


# ---------------------------------------------------------------------------
# TestProtobufAnyValueSerialization
# ---------------------------------------------------------------------------

class TestProtobufAnyValueSerialization:

    @pytest.mark.parametrize(
        ("default_value", "expected_type_url"),
        [
            ("hello", ProtobufType.STRING),
            (42, ProtobufType.INT),
            (3.14, ProtobufType.FLOAT),
            (True, ProtobufType.BOOL),
            ([], ProtobufType.ANY),
            ({"k": "v"}, ProtobufType.ANY),
        ],
        ids=["str", "int", "float", "bool", "list", "dict"],
    )
    def test_serialization_produces_correct_type_url(
        self, default_value, expected_type_url
    ):
        defn = _make_definition(default=default_value)
        param = TemplateParameter.from_definition(defn)
        dumped = param.default.model_dump(by_alias=True)

        assert "@type" in dumped
        assert dumped["@type"] == expected_type_url
        assert dumped["value"] == default_value

    def test_serialization_uses_alias_not_field_name(self):
        pav = ProtobufAnyValue(type=ProtobufType.STRING, value="test")
        dumped = pav.model_dump(by_alias=True)
        assert "@type" in dumped
        assert "type" not in dumped


# ---------------------------------------------------------------------------
# TestRealTemplateMetadata
# ---------------------------------------------------------------------------

def _roles_dir_path() -> Path:
    return (
        Path(__file__).resolve().parents[4]
        / "collections"
        / "ansible_collections"
        / "osac"
        / "templates"
        / "roles"
    )


def pytest_generate_tests(metafunc):
    if "osac_yaml_path" in metafunc.fixturenames:
        roles_dir = _roles_dir_path()
        paths = _discover_osac_yamls(roles_dir)
        metafunc.parametrize(
            "osac_yaml_path",
            paths,
            ids=[p.parent.parent.name for p in paths],
        )


class TestRealTemplateMetadata:

    def test_roles_dir_exists(self, roles_dir):
        assert roles_dir.exists(), f"Template roles directory not found: {roles_dir}"

    def test_at_least_one_osac_yaml_found(self, roles_dir):
        yamls = _discover_osac_yamls(roles_dir)
        assert len(yamls) > 0, "No osac.yaml files found in template roles"

    def test_osac_yaml_parses(self, osac_yaml_path):
        with osac_yaml_path.open("r", encoding="utf-8") as f:
            data = yaml.safe_load(f)
        Metadata.model_validate(data)

    def test_parameters_produce_valid_template_parameters(self, osac_yaml_path):
        with osac_yaml_path.open("r", encoding="utf-8") as f:
            data = yaml.safe_load(f)
        metadata = Metadata.model_validate(data)
        for param_def in metadata.parameters:
            TemplateParameter.from_definition(param_def)

    def test_defaults_produce_valid_protobuf_serialization(self, osac_yaml_path):
        with osac_yaml_path.open("r", encoding="utf-8") as f:
            data = yaml.safe_load(f)
        metadata = Metadata.model_validate(data)
        for param_def in metadata.parameters:
            param = TemplateParameter.from_definition(param_def)
            if param.default is not None:
                dumped = param.default.model_dump(by_alias=True)
                assert "@type" in dumped
                assert "value" in dumped


# ---------------------------------------------------------------------------
# TestTypeMappingCompleteness
# ---------------------------------------------------------------------------

class TestTypeMappingCompleteness:

    @pytest.mark.parametrize(
        "key",
        ["str", "string", "list", "dict", "bool", "int", "float", "path", "json", "bytes"],
    )
    def test_ansible_string_keys_present(self, key):
        assert key in TypeMapping

    @pytest.mark.parametrize(
        "python_type",
        [str, int, float, bool, list, dict],
    )
    def test_python_builtin_types_present(self, python_type):
        assert python_type in TypeMapping


# ---------------------------------------------------------------------------
# TestMetadataTemplateTypes
# ---------------------------------------------------------------------------

class TestMetadataTemplateTypes:

    @pytest.mark.parametrize(
        ("role_name", "expected_type"),
        [
            ("ocp_small", TemplateTypeEnum.cluster),
            ("ocp_virt_vm", TemplateTypeEnum.compute_instance),
            ("cudn_net", TemplateTypeEnum.network),
            ("bm_host_agent_provisioning", TemplateTypeEnum.bare_metal_instance),
            ("vast_storage", TemplateTypeEnum.storage_provider),
        ],
    )
    def test_template_type_parsed_correctly(
        self, roles_dir, role_name, expected_type
    ):
        metadata = _load_metadata(roles_dir, role_name)
        assert metadata.template_type == expected_type

    def test_compute_instance_has_spec_defaults(self, roles_dir):
        metadata = _load_metadata(roles_dir, "ocp_virt_vm")
        assert metadata.spec_defaults is not None
        assert metadata.spec_defaults["boot_disk"] is not None

    def test_cluster_with_parameters(self, roles_dir):
        metadata = _load_metadata(roles_dir, "ocp_4_20_ai_maas")
        assert len(metadata.parameters) > 0
        param_names = [p.name for p in metadata.parameters]
        assert "hardware_profiles" in param_names
        assert "external_models" in param_names

    def test_validate_default_rejects_unsupported_type(self):
        with pytest.raises(pydantic.ValidationError):
            TemplateParameter(
                name="bad",
                title="Bad",
                description="Bad param",
                default=object(),
            )


# ---------------------------------------------------------------------------
# Regression Tests
# ---------------------------------------------------------------------------

class TestRegressions:
    """Regression tests for specific bugs caught in production."""

    def test_list_default_accepted_osac_2816(self):
        """OSAC-2816: list defaults were silently dropped."""
        defn = TemplateParameterDefinition(
            name="tags", title="Tags", description="Tags",
            type="list", required=False, default=["a", "b"],
        )
        param = TemplateParameter.from_definition(defn)
        assert param.default is not None
        assert param.default.value == ["a", "b"]

    def test_dict_default_accepted_osac_2816(self):
        """OSAC-2816: dict defaults were silently dropped."""
        defn = TemplateParameterDefinition(
            name="config", title="Config", description="Config",
            type="dict", required=False, default={"key": "val"},
        )
        param = TemplateParameter.from_definition(defn)
        assert param.default is not None
        assert param.default.value == {"key": "val"}
