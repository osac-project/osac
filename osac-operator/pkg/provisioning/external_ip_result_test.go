package provisioning_test

import (
	"testing"

	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

func TestValidateExternalIPResult(t *testing.T) {
	valid := map[string]any{
		"schemaVersion":      "v1",
		"operation":          "external_ip.allocate",
		"resourceUID":        "11111111-1111-4111-8111-111111111111",
		"observedGeneration": float64(4),
		"data":               map[string]any{"address": "198.51.100.1"},
	}
	outputs := func(result map[string]any) map[string]any {
		return map[string]any{"osac_result": result}
	}

	address, err := provisioning.ValidateExternalIPResult(
		outputs(valid), "external_ip.allocate", "11111111-1111-4111-8111-111111111111", 4,
	)
	if err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
	if address != "198.51.100.1" {
		t.Fatalf("got address %q", address)
	}

	cases := []struct {
		name   string
		change func(map[string]any)
	}{
		{name: "missing result", change: func(result map[string]any) { delete(result, "schemaVersion") }},
		{name: "wrong schema", change: func(result map[string]any) { result["schemaVersion"] = "v2" }},
		{name: "wrong operation", change: func(result map[string]any) { result["operation"] = "external_ip.release" }},
		{name: "wrong UID", change: func(result map[string]any) { result["resourceUID"] = "22222222-2222-4222-8222-222222222222" }},
		{name: "wrong generation", change: func(result map[string]any) { result["observedGeneration"] = float64(3) }},
		{name: "noninteger generation", change: func(result map[string]any) { result["observedGeneration"] = float64(4.5) }},
		{name: "missing address", change: func(result map[string]any) { result["data"] = map[string]any{} }},
		{name: "empty address", change: func(result map[string]any) { result["data"] = map[string]any{"address": ""} }},
		{name: "non-string address", change: func(result map[string]any) { result["data"] = map[string]any{"address": 1} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := make(map[string]any, len(valid))
			for key, value := range valid {
				result[key] = value
			}
			tc.change(result)
			if _, err := provisioning.ValidateExternalIPResult(
				outputs(result), "external_ip.allocate", "11111111-1111-4111-8111-111111111111", 4,
			); err == nil {
				t.Fatal("expected invalid result to be rejected")
			}
		})
	}

	for _, outputs := range []map[string]any{
		nil,
		{"osac_result": nil},
		{"osac_result": []any{"not", "an", "object"}},
		{"osac_result": map[string]any{
			"schemaVersion": "v1", "operation": "external_ip.allocate",
			"resourceUID":        "11111111-1111-4111-8111-111111111111",
			"observedGeneration": float64(4), "data": []any{"not", "an", "object"},
		}},
	} {
		if _, err := provisioning.ValidateExternalIPResult(
			outputs, "external_ip.allocate", "11111111-1111-4111-8111-111111111111", 4,
		); err == nil {
			t.Errorf("expected malformed result %#v to be rejected", outputs)
		}
	}

	release := map[string]any{
		"schemaVersion":      "v1",
		"operation":          "external_ip.release",
		"resourceUID":        "11111111-1111-4111-8111-111111111111",
		"observedGeneration": float64(4),
		"data":               map[string]any{},
	}
	if address, err := provisioning.ValidateExternalIPResult(
		outputs(release), "external_ip.release", "11111111-1111-4111-8111-111111111111", 4,
	); err != nil || address != "" {
		t.Fatalf("valid release rejected or returned address: address=%q error=%v", address, err)
	}
	release["data"] = map[string]any{"address": "198.51.100.1"}
	if _, err := provisioning.ValidateExternalIPResult(
		outputs(release), "external_ip.release", "11111111-1111-4111-8111-111111111111", 4,
	); err == nil {
		t.Fatal("release result with an address must be rejected")
	}
}

func TestValidateExternalIPAddress(t *testing.T) {
	address, err := provisioning.ValidateExternalIPAddress("198.51.100.1", "198.51.100.0/24")
	if err != nil || address != "198.51.100.1" {
		t.Fatalf("valid address rejected: address=%q error=%v", address, err)
	}

	for _, tc := range []struct{ address, cidr string }{
		{address: "198.51.100.1", cidr: "198.51.101.0/24"},
		{address: "198.51.100.1/24", cidr: "198.51.100.0/24"},
		{address: "198.51.100.01", cidr: "198.51.100.0/24"},
		{address: "2001:db8::1", cidr: "198.51.100.0/24"},
		{address: "0.0.0.0", cidr: "0.0.0.0/0"},
	} {
		if _, err := provisioning.ValidateExternalIPAddress(tc.address, tc.cidr); err == nil {
			t.Errorf("expected address %q in pool %q to be rejected", tc.address, tc.cidr)
		}
	}
}

func TestValidateExternalIPPoolCIDR(t *testing.T) {
	cidr, err := provisioning.ValidateExternalIPPoolCIDR([]string{"198.51.100.0/24"})
	if err != nil || cidr != "198.51.100.0/24" {
		t.Fatalf("valid pool rejected: cidr=%q error=%v", cidr, err)
	}
	for _, cidrs := range [][]string{
		{},
		{"198.51.100.0/24", "203.0.113.0/24"},
		{"198.51.100.1/24"},
		{"2001:db8::/64"},
	} {
		if _, err := provisioning.ValidateExternalIPPoolCIDR(cidrs); err == nil {
			t.Errorf("expected CIDRs %v to be rejected", cidrs)
		}
	}
}
