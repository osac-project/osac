package csidriver_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestFulfillmentTrustDefaults(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", "..", ".."))

	for _, test := range []struct {
		path    string
		enabled bool
	}{
		{"osac-installer/charts/osac/values.yaml", true},
		{"osac-operator/charts/operator/values.yaml", false},
		{"osac-csi-driver/charts/csi-driver/values.yaml", false},
	} {
		values := readValues(t, filepath.Join(repoRoot, test.path))
		global := valueObject(t, values, "global")
		trust := valueObject(t, global, "fulfillmentTrust")
		if trust["enabled"] != test.enabled || trust["tenantNamespace"] != "osac-csi" {
			t.Errorf("%s has unexpected fulfillment trust defaults", test.path)
		}
	}

	schema := readSchema(t, filepath.Join(repoRoot, "osac-installer/charts/osac/values.schema.json"))
	trust := schemaObject(t, schemaObject(t, schemaObject(t, schema, "properties"), "global"), "properties")
	trust = schemaObject(t, trust, "fulfillmentTrust")
	trustProperties := schemaObject(t, trust, "properties")

	enabled := schemaObject(t, trustProperties, "enabled")
	if enabled["type"] != "boolean" || enabled["default"] != true {
		t.Error("installer schema does not schema the enabled fulfillment trust flag")
	}

	tenantNamespace := schemaObject(t, trustProperties, "tenantNamespace")
	if tenantNamespace["type"] != "string" || tenantNamespace["default"] != "osac-csi" {
		t.Error("installer schema does not schema the default fulfillment trust tenant namespace")
	}

	csiValues := readValues(t, filepath.Join(repoRoot, "osac-csi-driver/charts/csi-driver/values.yaml"))
	csiTrust := valueObject(t, valueObject(t, csiValues, "global"), "fulfillmentTrust")
	admission := valueObject(t, csiTrust, "admission")
	resources := valueObject(t, admission, "resources")
	if len(valueObject(t, resources, "requests")) == 0 || len(valueObject(t, resources, "limits")) == 0 {
		t.Error("tenant trust admission resources must define requests and limits")
	}
}

func readValues(t *testing.T, path string) map[string]any {
	t.Helper()

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var values map[string]any
	if err := yaml.Unmarshal(contents, &values); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return values
}

func readSchema(t *testing.T, path string) map[string]any {
	t.Helper()

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var schema map[string]any
	if err := json.Unmarshal(contents, &schema); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return schema
}

func schemaObject(t *testing.T, value map[string]any, key string) map[string]any {
	t.Helper()

	return valueObject(t, value, key)
}

func valueObject(t *testing.T, value map[string]any, key string) map[string]any {
	t.Helper()

	object, ok := value[key].(map[string]any)
	if !ok {
		t.Fatalf("schema key %q is not an object", key)
	}
	return object
}
