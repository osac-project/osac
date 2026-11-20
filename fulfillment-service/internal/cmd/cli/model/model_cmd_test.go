package model

import (
	"strings"
	"testing"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func TestCommandRegistersModelOperations(t *testing.T) {
	command := Cmd()
	want := []string{"list", "get", "create", "update", "delete", "placement-clusters"}
	for _, name := range want {
		if _, _, err := command.Find([]string{name}); err != nil {
			t.Errorf("Find(%q) error = %v", name, err)
		}
	}
}

func TestRenderList(t *testing.T) {
	model := publicv1.Model_builder{
		Id: "granite-3b",
		Metadata: publicv1.Metadata_builder{
			Name: "granite-3b",
		}.Build(),
		Spec: publicv1.ModelSpec_builder{
			SourceUri:      "hf://ibm-granite/granite-3b",
			ServingProfile: "vllm-cuda",
			Placement: publicv1.ModelPlacement_builder{
				Clusters: []string{"site-a", "site-b"},
			}.Build(),
		}.Build(),
		Status: publicv1.ModelStatus_builder{
			Phase: publicv1.ModelPhase_MODEL_PHASE_READY,
		}.Build(),
	}.Build()

	var output strings.Builder
	if err := renderList(&output, []*publicv1.Model{model}); err != nil {
		t.Fatalf("renderList: %v", err)
	}
	normalized := strings.Join(strings.Fields(output.String()), " ")
	for _, value := range []string{
		"ID NAME STATUS INFERENCE CLUSTERS SOURCE",
		"granite-3b granite-3b MODEL_PHASE_READY site-a,site-b hf://ibm-granite/granite-3b",
	} {
		if !strings.Contains(normalized, value) {
			t.Errorf("renderList output %q does not contain %q", normalized, value)
		}
	}
}
