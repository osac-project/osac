package provisioning

import "testing"

func TestValidateNATGatewayResult(t *testing.T) {
	const (
		uid        = "11111111-1111-4111-8111-111111111111"
		generation = int64(3)
	)

	valid := map[string]any{
		"schemaVersion":      "v1",
		"operation":          NATGatewayEnsureOperation,
		"resourceUID":        uid,
		"observedGeneration": generation,
		"data":               map[string]any{},
	}

	cases := []struct {
		name       string
		output     map[string]any
		operation  string
		uid        string
		generation int64
		wantErr    bool
	}{
		{
			name:       "valid ensure",
			output:     map[string]any{aapResultKey: valid},
			operation:  NATGatewayEnsureOperation,
			uid:        uid,
			generation: generation,
		},
		{
			name: "valid delete",
			output: map[string]any{aapResultKey: map[string]any{
				"schemaVersion": "v1", "operation": NATGatewayDeleteOperation,
				"resourceUID": uid, "observedGeneration": generation,
				"data": map[string]any{},
			}},
			operation: NATGatewayDeleteOperation, uid: uid, generation: generation,
		},
		{
			name: "additional artifacts are permitted",
			output: map[string]any{
				aapResultKey:     valid,
				"provider_state": map[string]any{"changed": true},
			},
			operation: NATGatewayEnsureOperation, uid: uid, generation: generation,
		},
		{name: "missing result", output: map[string]any{}, operation: NATGatewayEnsureOperation, uid: uid, generation: generation, wantErr: true},
		{name: "wrong operation", output: map[string]any{aapResultKey: valid}, operation: NATGatewayDeleteOperation, uid: uid, generation: generation, wantErr: true},
		{name: "wrong UID", output: map[string]any{aapResultKey: valid}, operation: NATGatewayEnsureOperation, uid: "22222222-2222-4222-8222-222222222222", generation: generation, wantErr: true},
		{name: "wrong generation", output: map[string]any{aapResultKey: valid}, operation: NATGatewayEnsureOperation, uid: uid, generation: generation + 1, wantErr: true},
		{
			name: "nonempty data",
			output: map[string]any{aapResultKey: map[string]any{
				"schemaVersion": "v1", "operation": NATGatewayEnsureOperation,
				"resourceUID": uid, "observedGeneration": generation,
				"data": map[string]any{"address": "198.51.100.10"},
			}},
			operation: NATGatewayEnsureOperation, uid: uid, generation: generation, wantErr: true,
		},
		{name: "negative generation", output: map[string]any{aapResultKey: valid}, operation: NATGatewayEnsureOperation, uid: uid, generation: -1, wantErr: true},
		{name: "unsupported operation", output: map[string]any{aapResultKey: valid}, operation: "nat_gateway.replace", uid: uid, generation: generation, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateNATGatewayResult(tc.output, tc.operation, tc.uid, tc.generation)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateNATGatewayResult() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
