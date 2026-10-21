package provisioning

import (
	"testing"
)

func TestValidateExternalIPAttachmentResult(t *testing.T) {
	const (
		uid        = "11111111-1111-4111-8111-111111111111"
		generation = int64(3)
	)

	valid := map[string]any{
		"schemaVersion":      "v1",
		"operation":          ExternalIPAttachmentAttachOperation,
		"resourceUID":        uid,
		"observedGeneration": generation,
		"data":               map[string]any{},
	}

	tests := []struct {
		name       string
		result     any
		operation  string
		uid        string
		generation int64
		wantErr    bool
	}{
		{name: "valid attach", result: valid, operation: ExternalIPAttachmentAttachOperation, uid: uid, generation: generation},
		{name: "valid detach", result: map[string]any{
			"schemaVersion": "v1", "operation": ExternalIPAttachmentDetachOperation,
			"resourceUID": uid, "observedGeneration": generation, "data": map[string]any{},
		}, operation: ExternalIPAttachmentDetachOperation, uid: uid, generation: generation},
		{name: "missing result", result: nil, operation: ExternalIPAttachmentAttachOperation, uid: uid, generation: generation, wantErr: true},
		{name: "wrong operation", result: valid, operation: ExternalIPAttachmentDetachOperation, uid: uid, generation: generation, wantErr: true},
		{name: "wrong UID", result: valid, operation: ExternalIPAttachmentAttachOperation, uid: "22222222-2222-4222-8222-222222222222", generation: generation, wantErr: true},
		{name: "wrong generation", result: valid, operation: ExternalIPAttachmentAttachOperation, uid: uid, generation: generation + 1, wantErr: true},
		{name: "nonempty data", result: map[string]any{
			"schemaVersion": "v1", "operation": ExternalIPAttachmentAttachOperation,
			"resourceUID": uid, "observedGeneration": generation, "data": map[string]any{"address": "198.51.100.1"},
		}, operation: ExternalIPAttachmentAttachOperation, uid: uid, generation: generation, wantErr: true},
		{name: "unsupported operation", result: valid, operation: "external_ip_attachment.replace", uid: uid, generation: generation, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateExternalIPAttachmentResult(
				map[string]any{externalIPAttachmentResultKey: test.result},
				test.operation,
				test.uid,
				test.generation,
			)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateExternalIPAttachmentResult() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
