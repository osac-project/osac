package servers

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/osac-project/osac/fulfillment-service/internal/gitops"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"sigs.k8s.io/yaml"
)

type failingModelsHubClientProvider struct {
	err error
}

func TestInt32FromInt64(t *testing.T) {
	tests := []struct {
		name  string
		value int64
		want  int32
		ok    bool
	}{
		{name: "minimum", value: -(1 << 31), want: -1 << 31, ok: true},
		{name: "maximum", value: 1<<31 - 1, want: 1<<31 - 1, ok: true},
		{name: "below minimum", value: -(1 << 31) - 1},
		{name: "above maximum", value: 1 << 31},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := int32FromInt64(test.value)
			if got != test.want || ok != test.ok {
				t.Fatalf("int32FromInt64(%d) = (%d, %t), want (%d, %t)", test.value, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestAnyInt32PointerValidatesValues(t *testing.T) {
	valid := []struct {
		name  string
		value any
		want  int32
	}{
		{name: "int", value: int(2), want: 2},
		{name: "int64", value: int64(3), want: 3},
		{name: "integral float", value: float64(4), want: 4},
	}
	for _, test := range valid {
		t.Run(test.name, func(t *testing.T) {
			got, ok := anyInt32Pointer(test.value)
			if !ok || got == nil || *got != test.want {
				t.Fatalf("anyInt32Pointer(%v) = (%v, %t), want (%d, true)", test.value, got, ok, test.want)
			}
		})
	}

	for _, value := range []any{int64(1 << 31), int64(-(1 << 31) - 1), float64(1.5), float64(1 << 31), "2"} {
		if got, ok := anyInt32Pointer(value); ok || got != nil {
			t.Errorf("anyInt32Pointer(%v) = (%v, %t), want (nil, false)", value, got, ok)
		}
	}
	if got, ok := anyInt32Pointer(nil); !ok || got != nil {
		t.Fatalf("anyInt32Pointer(nil) = (%v, %t), want (nil, true)", got, ok)
	}
}

func (p failingModelsHubClientProvider) GetClient(context.Context, string) (*HubClientInfo, error) {
	return nil, p.err
}

func (failingModelsHubClientProvider) EvictClient(string) {}

func TestGitModelsBackendListReturnsCatalogWhenHubIsUnreachable(t *testing.T) {
	ctx := context.Background()
	backend := newGitModelsBackend(gitops.NewMemoryWriter(nil), nil, slog.New(slog.DiscardHandler))
	if _, err := backend.create(ctx, "hub-id", testBackendModel("granite-3b")); err != nil {
		t.Fatalf("create model: %v", err)
	}
	backend.hubClientProvider = failingModelsHubClientProvider{err: context.DeadlineExceeded}

	var listed backendModelsListResponse
	if err := backend.call(ctx, "hub-id", "ai_grid.v1.Models/List", backendModelsListRequest{}, &listed); err != nil {
		t.Fatalf("List returned error while hub is unreachable: %v", err)
	}
	if listed.Total != 1 || len(listed.Items) != 1 || listed.Items[0].ID != "granite-3b" {
		t.Fatalf("List returned total=%d items=%#v, want Git catalog model", listed.Total, listed.Items)
	}
	if listed.Items[0].Status.Reason != "HubUnreachable" {
		t.Fatalf("status reason = %q, want HubUnreachable", listed.Items[0].Status.Reason)
	}
	if !strings.Contains(listed.Items[0].Status.Message, "hub is unreachable") {
		t.Fatalf("status message = %q, want unreachable explanation", listed.Items[0].Status.Message)
	}
}

func TestGitModelsBackendCRUD(t *testing.T) {
	ctx := context.Background()
	writer := gitops.NewMemoryWriter(nil)
	backend := newGitModelsBackend(writer, nil, slog.New(slog.DiscardHandler))
	model := backendModel{
		ID:       "granite-3b",
		Metadata: backendModelMetadata{Name: "granite-3b"},
		Spec: backendModelSpec{
			Model:    backendModelSource{URI: "hf://ibm-granite/granite-3b"},
			Replicas: modelsInt32Ptr(1),
			Main: backendModelContainer{
				Requests: map[string]string{"nvidia.com/gpu": "1"},
				Limits:   map[string]string{"nvidia.com/gpu": "1"},
			},
			Placement: backendModelPlacement{LabelSelector: &backendModelLabelSelector{
				MatchExpressions: []backendModelSelectorRequirement{{Key: "name", Operator: "In", Values: []string{"site-a"}}},
			}},
		},
	}
	var created backendModelResponse
	if err := backend.call(ctx, "hub-id", "ai_grid.v1.Models/Create", backendModelRequest{Object: model}, &created); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Object.Metadata.Version != 1 {
		t.Fatalf("created version = %d, want 1", created.Object.Metadata.Version)
	}
	if _, exists := created.Object.Metadata.Annotations[modelsGitVersionAnnotation]; exists {
		t.Fatal("internal git version annotation leaked in the API response")
	}

	var listed backendModelsListResponse
	if err := backend.call(ctx, "hub-id", "ai_grid.v1.Models/List", backendModelsListRequest{}, &listed); err != nil {
		t.Fatalf("List: %v", err)
	}
	if listed.Total != 1 || len(listed.Items) != 1 || listed.Items[0].Spec.Model.URI != model.Spec.Model.URI {
		t.Fatalf("List returned total=%d items=%#v", listed.Total, listed.Items)
	}

	var current backendModelResponse
	if err := backend.call(ctx, "hub-id", "ai_grid.v1.Models/Get", map[string]string{"id": model.ID}, &current); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.Object.Spec.Main.Requests["nvidia.com/gpu"] != "1" {
		t.Fatalf("Get lost compute resources: %#v", current.Object.Spec.Main)
	}

	updatedModel := current.Object
	updatedModel.Spec.Replicas = modelsInt32Ptr(2)
	var updated backendModelResponse
	if err := backend.call(ctx, "hub-id", "ai_grid.v1.Models/Update", backendModelsUpdateRequest{
		Object: updatedModel, UpdateMask: "spec.replicas", Lock: true,
	}, &updated); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Object.Metadata.Version != 2 || updated.Object.Spec.Replicas == nil || *updated.Object.Spec.Replicas != 2 {
		t.Fatalf("Update returned %#v", updated.Object)
	}

	err := backend.call(ctx, "hub-id", "ai_grid.v1.Models/Delete", backendModelsDeleteRequest{ID: model.ID, ResourceVersion: 2}, nil)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	listed = backendModelsListResponse{}
	if err := backend.call(ctx, "hub-id", "ai_grid.v1.Models/List", backendModelsListRequest{}, &listed); err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if listed.Total != 0 || len(listed.Items) != 0 {
		t.Fatalf("List after delete returned %#v", listed)
	}
}

func TestGitModelsBackendRejectsStaleUpdate(t *testing.T) {
	ctx := context.Background()
	backend := newGitModelsBackend(gitops.NewMemoryWriter(nil), nil, slog.New(slog.DiscardHandler))
	model := testBackendModel("granite-3b")
	if _, err := backend.create(ctx, "hub-id", model); err != nil {
		t.Fatalf("Create: %v", err)
	}
	model.Metadata.Version = 99
	model.Spec.Replicas = modelsInt32Ptr(2)
	err := backend.call(ctx, "hub-id", "ai_grid.v1.Models/Update", backendModelsUpdateRequest{Object: model, Lock: true}, &backendModelResponse{})
	if status.Code(err) != codes.Aborted {
		t.Fatalf("Update error = %v, want Aborted", err)
	}
}

func TestGitModelsBackendRejectsSubscribedDelete(t *testing.T) {
	ctx := context.Background()
	seed := map[string][]byte{
		"catalog/subscriptions/team-a.yaml": []byte("apiVersion: maas.opendatahub.io/v1alpha1\nkind: MaaSSubscription\nmetadata:\n  name: team-a\n  namespace: models-as-a-service\nspec:\n  modelRefs:\n  - name: granite-3b\n    namespace: models-as-a-service\n"),
	}
	backend := newGitModelsBackend(gitops.NewMemoryWriter(seed), nil, slog.New(slog.DiscardHandler))
	if _, err := backend.create(ctx, "hub-id", testBackendModel("granite-3b")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	err := backend.call(ctx, "hub-id", "ai_grid.v1.Models/Delete", backendModelsDeleteRequest{ID: "granite-3b"}, nil)
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "team-a") {
		t.Fatalf("Delete error = %v, want FailedPrecondition naming subscription", err)
	}
}

func TestBackendModelUpdatePreservesUnexposedCatalogFields(t *testing.T) {
	current := backendModel{
		ID:       "granite-3b",
		Metadata: backendModelMetadata{Name: "granite-3b", Version: 3},
		Spec: backendModelSpec{
			Model: backendModelSource{URI: "hf://ibm-granite/granite-3b", Name: "served-granite"},
			Main: backendModelContainer{
				Image: "quay.io/example/model:latest",
				Env:   []backendModelEnv{{Name: "MODEL_MODE", Value: "optimized"}},
			},
			Router: &backendModelRouter{
				Scheduler:   true,
				GatewayRefs: []backendModelGateway{{Name: "custom-gateway", Namespace: "gateway-system", SectionName: "https"}},
			},
			BaseRefs: []string{"granite-serving-profile"},
			Runtime:  "vllm",
		},
	}
	model := publicv1.Model_builder{
		Id:       current.ID,
		Metadata: publicv1.Metadata_builder{Name: current.ID}.Build(),
		Spec: publicv1.ModelSpec_builder{
			SourceUri:        current.Spec.Model.URI,
			ServingProfile:   "granite-serving-profile",
			Placement:        publicv1.ModelPlacement_builder{Clusters: []string{"site-a"}}.Build(),
			ComputeResources: publicv1.ModelComputeResources_builder{}.Build(),
		}.Build(),
	}.Build()

	updated := backendModelForUpdate(model, current)
	if updated.Spec.Model.Name != "served-granite" || updated.Spec.Main.Image != current.Spec.Main.Image {
		t.Fatalf("update lost catalog-only model fields: %#v", updated.Spec)
	}
	if len(updated.Spec.Main.Env) != 1 || updated.Spec.Main.Env[0] != current.Spec.Main.Env[0] {
		t.Fatalf("update lost container environment: %#v", updated.Spec.Main.Env)
	}
	if updated.Spec.Router == nil || !updated.Spec.Router.Scheduler || updated.Spec.Router.GatewayRefs[0] != current.Spec.Router.GatewayRefs[0] {
		t.Fatalf("update lost router configuration: %#v", updated.Spec.Router)
	}
	if updated.Spec.Runtime != current.Spec.Runtime || updated.Spec.BaseRefs[0] != current.Spec.BaseRefs[0] {
		t.Fatalf("update lost serving profile details: %#v", updated.Spec)
	}
}

func TestRenderModelsPreservesSourceRouterAndAnnotations(t *testing.T) {
	model := testBackendModel("granite-3b")
	model.Metadata.Annotations = map[string]string{"example.com/owner": "platform"}
	model.Spec.Model.Name = "served-granite"
	model.Spec.Main.Image = "quay.io/example/model:latest"
	model.Spec.Main.Env = []backendModelEnv{{Name: "MODEL_MODE", Value: "optimized"}}
	model.Spec.Router = &backendModelRouter{
		Scheduler:   true,
		GatewayRefs: []backendModelGateway{{Name: "custom-gateway", Namespace: "gateway-system", SectionName: "https"}},
	}
	model.Metadata.Version = 1

	deployment, err := renderModelsDeployment(model, "ai-grid-internal-models", "redhat-ai-grid")
	if err != nil {
		t.Fatalf("render deployment: %v", err)
	}
	var llm map[string]any
	if err := yaml.Unmarshal(deployment, &llm); err != nil {
		t.Fatalf("parse deployment: %v", err)
	}
	spec, _ := llm["spec"].(map[string]any)
	modelSource, _ := spec["model"].(map[string]any)
	if modelSource["name"] != "served-granite" {
		t.Fatalf("LLMInferenceService model name = %v", modelSource["name"])
	}
	router, _ := spec["router"].(map[string]any)
	if _, found := router["scheduler"]; !found {
		t.Fatalf("LLMInferenceService router lost scheduler: %#v", router)
	}
	template, _ := spec["template"].(map[string]any)
	containers, _ := template["containers"].([]any)
	main, _ := containers[0].(map[string]any)
	if main["image"] != "quay.io/example/model:latest" {
		t.Fatalf("main image = %v", main["image"])
	}

	catalog, err := renderModelsCatalog(model, "models-as-a-service")
	if err != nil {
		t.Fatalf("render catalog: %v", err)
	}
	documents := strings.Split(string(catalog), "---\n")
	var external, modelRef map[string]any
	if err := yaml.Unmarshal([]byte(documents[0]), &external); err != nil {
		t.Fatalf("parse ExternalModel: %v", err)
	}
	if err := yaml.Unmarshal([]byte(documents[1]), &modelRef); err != nil {
		t.Fatalf("parse MaaSModelRef: %v", err)
	}
	externalSpec, _ := external["spec"].(map[string]any)
	providerRefs, _ := externalSpec["externalProviderRefs"].([]any)
	provider, _ := providerRefs[0].(map[string]any)
	if provider["targetModel"] != "served-granite" {
		t.Fatalf("ExternalModel targetModel = %v", provider["targetModel"])
	}
	metadata, _ := modelRef["metadata"].(map[string]any)
	annotations, _ := metadata["annotations"].(map[string]any)
	if annotations["example.com/owner"] != "platform" {
		t.Fatalf("MaaSModelRef annotations = %#v", annotations)
	}
}

func testBackendModel(id string) backendModel {
	return backendModel{
		ID: id, Metadata: backendModelMetadata{Name: id},
		Spec: backendModelSpec{
			Model: backendModelSource{URI: "hf://ibm-granite/granite-3b"},
			Placement: backendModelPlacement{LabelSelector: &backendModelLabelSelector{
				MatchExpressions: []backendModelSelectorRequirement{{Key: "name", Operator: "In", Values: []string{"site-a"}}},
			}},
		},
	}
}

func modelsInt32Ptr(value int32) *int32 { return &value }
