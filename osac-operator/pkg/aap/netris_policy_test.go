package aap_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/osac-project/osac/osac-operator/pkg/aap"
)

func TestValidateProtectedNetrisExtraVarsRejectsAliasesRecursively(t *testing.T) {
	protectedNames := []string{
		"controller_url",
		"netris_controller_url",
		"NETRIS_CONTROLLER_URL",
		"validate_certs",
		"netris_validate_certs",
		"NETRIS_VALIDATE_CERTS",
		"ca_path",
		"netris_ca_path",
		"NETRIS_CA_PATH",
		"NetrisControllerURL",
		"NETRIS.controller_url",
		"netris-controller-url",
	}

	for _, name := range protectedNames {
		t.Run(name, func(t *testing.T) {
			err := aap.ValidateProtectedNetrisExtraVars(map[string]any{
				"osac_job_vars": map[string]any{
					"resource": map[string]any{name: "value"},
				},
			})
			if err == nil {
				t.Fatalf("expected protected key %q to be rejected", name)
			}
		})
	}
}

func TestValidateProtectedNetrisExtraVarsAllowsOrdinaryValues(t *testing.T) {
	payload := map[string]any{
		"osac_job_vars": map[string]any{
			"resource": map[string]any{
				"name":    "network-1",
				"enabled": false,
			},
			"items": []any{"a", 3, nil},
		},
		"empty": "",
	}

	if err := aap.ValidateProtectedNetrisExtraVars(payload); err != nil {
		t.Fatalf("ordinary launch values were rejected: %v", err)
	}
}

func TestValidateProtectedNetrisExtraVarsRejectsProtectedFieldsInTypedValues(t *testing.T) {
	type launchSettings struct {
		ControllerURL string `json:"netris_controller_url"`
	}

	err := aap.ValidateProtectedNetrisExtraVars(map[string]any{
		"settings": &launchSettings{ControllerURL: "https://attacker.example"},
	})
	if err == nil {
		t.Fatal("expected protected field in typed value to be rejected")
	}
}

func TestValidateProtectedNetrisExtraVarsRejectsProtectedFieldsInRawJSON(t *testing.T) {
	err := aap.ValidateProtectedNetrisExtraVars(map[string]any{
		"settings": json.RawMessage(`{"nested":{"NETRIS_VALIDATE_CERTS":false}}`),
	})
	if err == nil {
		t.Fatal("expected protected field in json.RawMessage to be rejected")
	}
}

func TestValidateProtectedNetrisExtraVarsTreatsByteSlicesAsJSONStrings(t *testing.T) {
	err := aap.ValidateProtectedNetrisExtraVars(map[string]any{
		"certificate": make([]byte, 12000),
	})
	if err != nil {
		t.Fatalf("ordinary byte slice was rejected: %v", err)
	}
}

func TestValidateProtectedNetrisExtraVarsRejectsNonJSONValues(t *testing.T) {
	err := aap.ValidateProtectedNetrisExtraVars(map[string]any{
		"callback": func() {},
	})
	if err == nil {
		t.Fatal("expected non-JSON value to be rejected")
	}

	var protectedErr *aap.ProtectedInputError
	if !errors.As(err, &protectedErr) {
		t.Fatalf("expected ProtectedInputError, got %T", err)
	}
	if protectedErr.Reason != "extra_vars is not JSON-serializable" {
		t.Fatalf("unexpected reason: %q", protectedErr.Reason)
	}
}
