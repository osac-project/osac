package aap_test

import (
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
