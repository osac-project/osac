/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
either express or implied. See the License for the specific language governing
permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const aiGridHubLabel = "ai-grid"

var _ publicv1.ModelsServer = (*ModelsServer)(nil)

type ModelsServerBuilder struct {
	logger            *slog.Logger
	hubsServer        privatev1.HubsServer
	hubClientProvider HubClientProvider
	backend           modelsBackend
}

type ModelsServer struct {
	publicv1.UnimplementedModelsServer
	logger            *slog.Logger
	hubsServer        privatev1.HubsServer
	hubClientProvider HubClientProvider
	backend           modelsBackend
}

func NewModelsServer() *ModelsServerBuilder { return &ModelsServerBuilder{} }

func (b *ModelsServerBuilder) SetLogger(value *slog.Logger) *ModelsServerBuilder {
	b.logger = value
	return b
}

func (b *ModelsServerBuilder) SetHubsServer(value privatev1.HubsServer) *ModelsServerBuilder {
	b.hubsServer = value
	return b
}

func (b *ModelsServerBuilder) SetHubClientProvider(value HubClientProvider) *ModelsServerBuilder {
	b.hubClientProvider = value
	return b
}

func (b *ModelsServerBuilder) SetBackend(value modelsBackend) *ModelsServerBuilder {
	b.backend = value
	return b
}

func (b *ModelsServerBuilder) Build() (*ModelsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.hubsServer == nil {
		return nil, errors.New("hubs server is mandatory")
	}
	if b.backend == nil {
		if b.hubClientProvider == nil {
			return nil, errors.New("hub client provider is mandatory")
		}
		writer, err := newModelsGitWriterFromEnv()
		if err != nil {
			return nil, err
		}
		b.backend = newGitModelsBackend(writer, b.hubClientProvider, b.logger)
	}
	return &ModelsServer{
		logger: b.logger, hubsServer: b.hubsServer, hubClientProvider: b.hubClientProvider, backend: b.backend,
	}, nil
}

func (s *ModelsServer) List(ctx context.Context, request *publicv1.ModelsListRequest) (*publicv1.ModelsListResponse, error) {
	hubID, err := s.aiGridHubID(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "cannot resolve AI Grid Hub for model status; returning Git catalog", "error", err)
		hubID = ""
	}
	var backendResponse backendModelsListResponse
	backendRequest := backendModelsListRequest{}
	if request.HasOffset() {
		backendRequest.Offset = proto.Int32(request.GetOffset())
	}
	if request.HasLimit() {
		backendRequest.Limit = proto.Int32(request.GetLimit())
	}
	if err := s.backend.call(ctx, hubID, "ai_grid.v1.Models/List", backendRequest, &backendResponse); err != nil {
		return nil, err
	}
	items := make([]*publicv1.Model, 0, len(backendResponse.Items))
	for i := range backendResponse.Items {
		items = append(items, backendResponse.Items[i].toOSAC())
	}
	size, ok := int32FromInt64(int64(len(items)))
	if !ok {
		return nil, status.Error(codes.ResourceExhausted, "model page is too large for an int32 response")
	}
	return publicv1.ModelsListResponse_builder{
		Size: size, Total: backendResponse.Total, Items: items,
	}.Build(), nil
}

func (s *ModelsServer) Get(ctx context.Context, request *publicv1.ModelsGetRequest) (*publicv1.ModelsGetResponse, error) {
	hubID, err := s.aiGridHubID(ctx)
	if err != nil {
		return nil, err
	}
	var response backendModelResponse
	if err := s.backend.call(ctx, hubID, "ai_grid.v1.Models/Get", map[string]string{"id": request.GetId()}, &response); err != nil {
		return nil, err
	}
	return publicv1.ModelsGetResponse_builder{Object: response.Object.toOSAC()}.Build(), nil
}

func (s *ModelsServer) Create(ctx context.Context, request *publicv1.ModelsCreateRequest) (*publicv1.ModelsCreateResponse, error) {
	object := request.GetObject()
	if err := validateOSACModel(object); err != nil {
		return nil, err
	}
	hubID, err := s.aiGridHubID(ctx)
	if err != nil {
		return nil, err
	}
	if clusters := object.GetSpec().GetPlacement().GetClusters(); len(clusters) > 0 {
		if err := s.validateEligibleClusters(ctx, hubID, clusters); err != nil {
			return nil, err
		}
	}
	backendRequest := backendModelRequest{Object: backendModelFromOSAC(object)}
	var response backendModelResponse
	if err := s.backend.call(ctx, hubID, "ai_grid.v1.Models/Create", backendRequest, &response); err != nil {
		return nil, err
	}
	return publicv1.ModelsCreateResponse_builder{Object: response.Object.toOSAC()}.Build(), nil
}

func (s *ModelsServer) Update(ctx context.Context, request *publicv1.ModelsUpdateRequest) (*publicv1.ModelsUpdateResponse, error) {
	if request.GetObject() == nil || request.GetObject().GetMetadata() == nil {
		return nil, status.Error(codes.InvalidArgument, "object and object.metadata are required")
	}
	paths, err := normalizeModelUpdateMask(request.GetUpdateMask())
	if err != nil {
		return nil, err
	}
	hubID, err := s.aiGridHubID(ctx)
	if err != nil {
		return nil, err
	}
	var currentResponse backendModelResponse
	if err := s.backend.call(ctx, hubID, "ai_grid.v1.Models/Get", map[string]string{"id": request.GetObject().GetId()}, &currentResponse); err != nil {
		return nil, err
	}
	current := currentResponse.Object
	if request.GetLock() && request.GetObject().GetMetadata().GetVersion() != current.Metadata.Version {
		return nil, status.Error(codes.Aborted, "model has changed; fetch the latest version and retry")
	}
	merged := current.toOSAC()
	if err := applyModelUpdate(merged, request.GetObject(), paths); err != nil {
		return nil, err
	}
	if containsModelPath(paths, "spec.placement") {
		oldClusters := current.toOSAC().GetSpec().GetPlacement().GetClusters()
		newClusters := merged.GetSpec().GetPlacement().GetClusters()
		var addedClusters []string
		for _, cluster := range newClusters {
			if !slices.Contains(oldClusters, cluster) {
				addedClusters = append(addedClusters, cluster)
			}
		}
		if len(addedClusters) > 0 {
			if err := s.validateEligibleClusters(ctx, hubID, addedClusters); err != nil {
				return nil, err
			}
		}
	}
	if err := validateOSACModel(merged); err != nil {
		return nil, err
	}
	backendRequest := backendModelsUpdateRequest{
		Object:     backendModelForUpdate(merged, current),
		UpdateMask: strings.Join(mapModelUpdateMask(paths), ","),
		Lock:       request.GetLock(),
	}
	var response backendModelResponse
	if err := s.backend.call(ctx, hubID, "ai_grid.v1.Models/Update", backendRequest, &response); err != nil {
		return nil, err
	}
	return publicv1.ModelsUpdateResponse_builder{Object: response.Object.toOSAC()}.Build(), nil
}

func (s *ModelsServer) Delete(ctx context.Context, request *publicv1.ModelsDeleteRequest) (*publicv1.ModelsDeleteResponse, error) {
	hubID, err := s.aiGridHubID(ctx)
	if err != nil {
		return nil, err
	}
	var current backendModelResponse
	if err := s.backend.call(ctx, hubID, "ai_grid.v1.Models/Get", map[string]string{"id": request.GetId()}, &current); err != nil {
		return nil, err
	}
	deleteRequest := backendModelsDeleteRequest{ID: request.GetId(), ResourceVersion: current.Object.Metadata.Version}
	if err := s.backend.call(ctx, hubID, "ai_grid.v1.Models/Delete", deleteRequest, nil); err != nil {
		return nil, err
	}
	return publicv1.ModelsDeleteResponse_builder{}.Build(), nil
}

func (s *ModelsServer) ListPlacementClusters(ctx context.Context, _ *publicv1.ModelsListPlacementClustersRequest) (*publicv1.ModelsListPlacementClustersResponse, error) {
	hubID, err := s.aiGridHubID(ctx)
	if err != nil {
		return nil, err
	}
	clusters, err := s.listEligibleClusters(ctx, hubID)
	if err != nil {
		return nil, err
	}
	result := make([]*publicv1.ModelPlacementCluster, 0, len(clusters))
	for _, cluster := range clusters {
		result = append(result, publicv1.ModelPlacementCluster_builder{Name: cluster.name, Location: cluster.location}.Build())
	}
	return publicv1.ModelsListPlacementClustersResponse_builder{Clusters: result}.Build(), nil
}

func (s *ModelsServer) aiGridHubID(ctx context.Context) (string, error) {
	const pageSize int32 = 100
	var matches []*privatev1.Hub
	for offset := int32(0); ; offset += pageSize {
		response, err := s.hubsServer.List(ctx, privatev1.HubsListRequest_builder{
			Offset: proto.Int32(offset), Limit: proto.Int32(pageSize),
		}.Build())
		if err != nil {
			return "", err
		}
		for _, hub := range response.GetItems() {
			if hub.GetMetadata().GetLabels()[aiGridHubLabel] == "true" {
				matches = append(matches, hub)
			}
		}
		if len(response.GetItems()) < int(pageSize) || offset+pageSize >= response.GetTotal() {
			break
		}
	}
	if len(matches) == 0 {
		return "", status.Error(codes.FailedPrecondition, "no AI Grid Hub is registered; register a Hub with the ai-grid=true label first")
	}
	if len(matches) > 1 {
		return "", status.Error(codes.FailedPrecondition, "multiple AI Grid Hubs are registered; model management requires exactly one AI Grid Hub")
	}
	return matches[0].GetId(), nil
}

func (s *ModelsServer) validateEligibleClusters(ctx context.Context, hubID string, names []string) error {
	clusters, err := s.listEligibleClusters(ctx, hubID)
	if err != nil {
		return err
	}
	for _, name := range names {
		if !slices.ContainsFunc(clusters, func(cluster modelPlacementCluster) bool { return cluster.name == name }) {
			return status.Errorf(codes.InvalidArgument, "cluster %q is not currently an eligible AI Grid inference placement", name)
		}
	}
	return nil
}

func validateOSACModel(model *publicv1.Model) error {
	if model == nil || model.GetMetadata() == nil || model.GetSpec() == nil {
		return status.Error(codes.InvalidArgument, "object, metadata, and spec are required")
	}
	if model.GetId() == "" || model.GetId() != model.GetMetadata().GetName() {
		return status.Error(codes.InvalidArgument, "id must match metadata.name")
	}
	if model.GetSpec().GetSourceUri() == "" {
		return status.Error(codes.InvalidArgument, "spec.source_uri is required")
	}
	if model.GetSpec().GetServingProfile() == "" {
		return status.Error(codes.InvalidArgument, "spec.serving_profile is required")
	}
	if model.GetSpec().HasReplicas() && model.GetSpec().GetScaling() != nil {
		return status.Error(codes.InvalidArgument, "spec.replicas and spec.scaling are mutually exclusive")
	}
	return nil
}

func normalizeModelUpdateMask(mask *fieldmaskpb.FieldMask) ([]string, error) {
	if mask == nil || len(mask.GetPaths()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "update_mask must contain at least one supported path")
	}
	allowed := map[string]bool{
		"metadata.labels": true, "metadata.annotations": true,
		"spec.replicas": true, "spec.compute_resources": true,
		"spec.placement": true, "spec.scaling": true,
	}
	paths := make([]string, 0, len(mask.GetPaths()))
	for _, path := range mask.GetPaths() {
		path = strings.TrimPrefix(path, "object.")
		if !allowed[path] {
			return nil, status.Errorf(codes.InvalidArgument, "update_mask path %q is not supported", path)
		}
		if !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func applyModelUpdate(dst, src *publicv1.Model, paths []string) error {
	for _, path := range paths {
		switch path {
		case "metadata.labels":
			dst.GetMetadata().SetLabels(cloneStringMap(src.GetMetadata().GetLabels()))
		case "metadata.annotations":
			dst.GetMetadata().SetAnnotations(cloneStringMap(src.GetMetadata().GetAnnotations()))
		case "spec.replicas":
			if src.GetSpec().HasReplicas() {
				dst.GetSpec().SetReplicas(src.GetSpec().GetReplicas())
				dst.GetSpec().SetScaling(nil)
			} else {
				dst.GetSpec().ClearReplicas()
			}
		case "spec.compute_resources":
			if src.GetSpec().GetComputeResources() == nil {
				dst.GetSpec().SetComputeResources(&publicv1.ModelComputeResources{})
			} else {
				dst.GetSpec().SetComputeResources(proto.Clone(src.GetSpec().GetComputeResources()).(*publicv1.ModelComputeResources))
			}
		case "spec.placement":
			if src.GetSpec().GetPlacement() == nil {
				dst.GetSpec().SetPlacement(&publicv1.ModelPlacement{})
			} else {
				dst.GetSpec().SetPlacement(proto.Clone(src.GetSpec().GetPlacement()).(*publicv1.ModelPlacement))
			}
		case "spec.scaling":
			if src.GetSpec().GetScaling() == nil {
				dst.GetSpec().SetScaling(nil)
			} else {
				dst.GetSpec().SetScaling(proto.Clone(src.GetSpec().GetScaling()).(*publicv1.ModelScaling))
			}
			dst.GetSpec().ClearReplicas()
		}
	}
	return nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func containsModelPath(paths []string, expected string) bool { return slices.Contains(paths, expected) }

func mapModelUpdateMask(paths []string) []string {
	var result []string
	for _, path := range paths {
		switch path {
		case "metadata.labels", "metadata.annotations", "spec.replicas", "spec.scaling", "spec.placement":
			result = append(result, path)
		case "spec.compute_resources":
			result = append(result, "spec.main.requests", "spec.main.limits")
		}
	}
	return result
}

// backend model DTOs mirror only the backend Connect fields OSAC consumes.
type backendModelsListRequest struct {
	Offset *int32 `json:"offset,omitempty"`
	Limit  *int32 `json:"limit,omitempty"`
}
type backendModelsListResponse struct {
	Total int32          `json:"total"`
	Items []backendModel `json:"items"`
}
type backendModelResponse struct {
	Object backendModel `json:"object"`
}
type backendModelRequest struct {
	Object backendModel `json:"object"`
}
type backendModelsUpdateRequest struct {
	Object     backendModel `json:"object"`
	UpdateMask string       `json:"updateMask"`
	Lock       bool         `json:"lock"`
}
type backendModelsDeleteRequest struct {
	ID              string `json:"id"`
	ResourceVersion int32  `json:"resourceVersion"`
}
type backendModel struct {
	ID       string               `json:"id"`
	Metadata backendModelMetadata `json:"metadata"`
	Spec     backendModelSpec     `json:"spec"`
	Status   backendModelStatus   `json:"status"`
}
type backendModelMetadata struct {
	CreationTimestamp time.Time         `json:"creationTimestamp"`
	DeletionTimestamp *time.Time        `json:"deletionTimestamp,omitempty"`
	Creator           string            `json:"creator,omitempty"`
	Tenant            string            `json:"tenant,omitempty"`
	Name              string            `json:"name"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	Version           int32             `json:"version,omitempty"`
}
type backendModelSpec struct {
	Model     backendModelSource    `json:"model"`
	Replicas  *int32                `json:"replicas,omitempty"`
	BaseRefs  []string              `json:"baseRefs,omitempty"`
	Runtime   string                `json:"runtime,omitempty"`
	Main      backendModelContainer `json:"main"`
	Router    *backendModelRouter   `json:"router,omitempty"`
	Scaling   *backendModelScaling  `json:"scaling,omitempty"`
	Placement backendModelPlacement `json:"placement"`
}
type backendModelSource struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}
type backendModelContainer struct {
	Image    string            `json:"image,omitempty"`
	Requests map[string]string `json:"requests,omitempty"`
	Limits   map[string]string `json:"limits,omitempty"`
	Env      []backendModelEnv `json:"env,omitempty"`
}
type backendModelEnv struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type backendModelRouter struct {
	ManagedRoute bool                  `json:"managedRoute,omitempty"`
	Scheduler    bool                  `json:"scheduler,omitempty"`
	GatewayRefs  []backendModelGateway `json:"gatewayRefs,omitempty"`
}
type backendModelGateway struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace,omitempty"`
	SectionName string `json:"sectionName,omitempty"`
}
type backendModelScaling struct {
	MinReplicas int32                        `json:"minReplicas"`
	MaxReplicas int32                        `json:"maxReplicas"`
	Keda        bool                         `json:"keda"`
	Triggers    []backendModelScalingTrigger `json:"triggers,omitempty"`
}
type backendModelScalingTrigger struct {
	Type     string            `json:"type"`
	Metadata map[string]string `json:"metadata,omitempty"`
}
type backendModelPlacement struct {
	LabelSelector *backendModelLabelSelector `json:"labelSelector,omitempty"`
}
type backendModelLabelSelector struct {
	MatchExpressions []backendModelSelectorRequirement `json:"matchExpressions,omitempty"`
}
type backendModelSelectorRequirement struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
}
type backendModelStatus struct {
	Phase     string                  `json:"phase"`
	Reason    string                  `json:"reason"`
	Message   string                  `json:"message"`
	Clusters  []backendModelCluster   `json:"clusters,omitempty"`
	Ingress   []backendModelIngress   `json:"ingress,omitempty"`
	Placement *backendPlacementStatus `json:"placement,omitempty"`
}
type backendPlacementStatus struct {
	SelectedClusters []string `json:"selectedClusters,omitempty"`
}
type backendModelCluster struct {
	Cluster           string `json:"cluster"`
	Phase             string `json:"phase"`
	Reason            string `json:"reason"`
	Message           string `json:"message"`
	Deployment        string `json:"deployment"`
	DeploymentMessage string `json:"deploymentMessage"`
	Sync              string `json:"sync"`
	SyncMessage       string `json:"syncMessage"`
	State             string `json:"state"`
}
type backendModelIngress struct {
	Cluster string `json:"cluster"`
	State   string `json:"state"`
	Message string `json:"message"`
}

func backendModelFromOSAC(model *publicv1.Model) backendModel {
	spec := model.GetSpec()
	result := backendModel{
		ID: model.GetId(),
		Metadata: backendModelMetadata{
			Name: model.GetMetadata().GetName(), Labels: cloneStringMap(model.GetMetadata().GetLabels()),
			Annotations: cloneStringMap(model.GetMetadata().GetAnnotations()),
		},
		Spec: backendModelSpec{
			Model:    backendModelSource{URI: spec.GetSourceUri(), Name: model.GetId()},
			BaseRefs: nil,
			Main:     backendModelContainer{},
			Placement: backendModelPlacement{LabelSelector: &backendModelLabelSelector{
				MatchExpressions: []backendModelSelectorRequirement{{Key: "name", Operator: "In", Values: slices.Clone(spec.GetPlacement().GetClusters())}},
			}},
		},
	}
	if spec.GetServingProfile() != "default" {
		result.Spec.BaseRefs = []string{spec.GetServingProfile()}
	}
	if spec.HasReplicas() {
		value := spec.GetReplicas()
		result.Spec.Replicas = &value
	}
	resources := spec.GetComputeResources()
	if resources != nil {
		result.Spec.Main.Requests = cloneStringMap(resources.GetRequests())
		result.Spec.Main.Limits = cloneStringMap(resources.GetLimits())
	}
	if scaling := spec.GetScaling(); scaling != nil {
		result.Spec.Scaling = &backendModelScaling{MinReplicas: scaling.GetMinReplicas(), MaxReplicas: scaling.GetMaxReplicas(), Keda: scaling.GetKeda()}
		for _, trigger := range scaling.GetTriggers() {
			result.Spec.Scaling.Triggers = append(result.Spec.Scaling.Triggers, backendModelScalingTrigger{Type: trigger.GetType(), Metadata: cloneStringMap(trigger.GetMetadata())})
		}
	}
	return result
}

func backendModelForUpdate(model *publicv1.Model, current backendModel) backendModel {
	result := backendModelFromOSAC(model)
	result.Metadata.Version = current.Metadata.Version
	// The backend may store more than one preset; keep all immutable references
	// already present when OSAC only exposes the selected profile summary.
	result.Spec.BaseRefs = slices.Clone(current.Spec.BaseRefs)
	result.Spec.Runtime = current.Spec.Runtime
	if current.Spec.Model.Name != "" {
		result.Spec.Model.Name = current.Spec.Model.Name
	}
	result.Spec.Main.Image = current.Spec.Main.Image
	result.Spec.Main.Env = slices.Clone(current.Spec.Main.Env)
	result.Spec.Router = current.Spec.Router
	return result
}

func (m backendModel) toOSAC() *publicv1.Model {
	metadata := publicv1.Metadata_builder{
		Name: m.Metadata.Name, Creator: m.Metadata.Creator, Tenant: m.Metadata.Tenant,
		Labels: cloneStringMap(m.Metadata.Labels), Annotations: cloneStringMap(m.Metadata.Annotations), Version: m.Metadata.Version,
	}.Build()
	if !m.Metadata.CreationTimestamp.IsZero() {
		metadata.SetCreationTimestamp(timestamppb.New(m.Metadata.CreationTimestamp))
	}
	if m.Metadata.DeletionTimestamp != nil {
		metadata.SetDeletionTimestamp(timestamppb.New(*m.Metadata.DeletionTimestamp))
	}
	clusters := backendModelSelectedClusters(m)
	placement := publicv1.ModelPlacement_builder{Clusters: clusters}.Build()
	specBuilder := publicv1.ModelSpec_builder{
		SourceUri: m.Spec.Model.URI, Runtime: m.Spec.Runtime, Placement: placement,
	}
	if len(m.Spec.BaseRefs) > 0 {
		specBuilder.ServingProfile = m.Spec.BaseRefs[0]
	} else {
		specBuilder.ServingProfile = "default"
	}
	if m.Spec.Replicas != nil {
		specBuilder.Replicas = m.Spec.Replicas
	}
	if m.Spec.Main.Requests != nil || m.Spec.Main.Limits != nil {
		specBuilder.ComputeResources = publicv1.ModelComputeResources_builder{
			Requests: cloneStringMap(m.Spec.Main.Requests), Limits: cloneStringMap(m.Spec.Main.Limits),
		}.Build()
	}
	if m.Spec.Scaling != nil {
		scaling := publicv1.ModelScaling_builder{MinReplicas: m.Spec.Scaling.MinReplicas, MaxReplicas: m.Spec.Scaling.MaxReplicas, Keda: m.Spec.Scaling.Keda}
		for _, trigger := range m.Spec.Scaling.Triggers {
			scaling.Triggers = append(scaling.Triggers, publicv1.ModelScalingTrigger_builder{Type: trigger.Type, Metadata: cloneStringMap(trigger.Metadata)}.Build())
		}
		specBuilder.Scaling = scaling.Build()
	}
	statusBuilder := publicv1.ModelStatus_builder{
		Phase: mapBackendModelPhase(m.Status.Phase), Reason: m.Status.Reason, Message: m.Status.Message,
	}
	for _, cluster := range m.Status.Clusters {
		message := cluster.DeploymentMessage
		if message == "" {
			message = cluster.SyncMessage
		}
		if message == "" {
			message = cluster.Message
		}
		statusBuilder.Deployments = append(statusBuilder.Deployments, publicv1.ModelDeploymentStatus_builder{
			Cluster: cluster.Cluster, Phase: mapBackendDeploymentPhase(cluster.Deployment), Reason: firstNonEmpty(cluster.Reason, cluster.State), Message: message,
		}.Build())
	}
	for _, ingress := range m.Status.Ingress {
		statusBuilder.Ingress = append(statusBuilder.Ingress, publicv1.ModelIngressStatus_builder{
			Cluster: ingress.Cluster, Phase: mapBackendMaaSPhase(ingress.State), Reason: ingress.State, Message: ingress.Message,
		}.Build())
	}
	return publicv1.Model_builder{Id: m.ID, Metadata: metadata, Spec: specBuilder.Build(), Status: statusBuilder.Build()}.Build()
}

func backendModelSelectedClusters(model backendModel) []string {
	if model.Status.Placement != nil && len(model.Status.Placement.SelectedClusters) > 0 {
		return slices.Clone(model.Status.Placement.SelectedClusters)
	}
	if model.Spec.Placement.LabelSelector != nil {
		for _, expression := range model.Spec.Placement.LabelSelector.MatchExpressions {
			if expression.Key == "name" && expression.Operator == "In" {
				return slices.Clone(expression.Values)
			}
		}
	}
	return nil
}

func mapBackendModelPhase(value string) publicv1.ModelPhase {
	switch strings.TrimPrefix(value, "MODEL_PHASE_") {
	case "READY":
		return publicv1.ModelPhase_MODEL_PHASE_READY
	case "PARTIALLY_AVAILABLE":
		return publicv1.ModelPhase_MODEL_PHASE_PARTIAL
	case "FAILED":
		return publicv1.ModelPhase_MODEL_PHASE_FAILED
	case "PENDING":
		return publicv1.ModelPhase_MODEL_PHASE_PENDING
	default:
		return publicv1.ModelPhase_MODEL_PHASE_UNSPECIFIED
	}
}
func mapBackendDeploymentPhase(value string) publicv1.ModelDeploymentPhase {
	switch strings.TrimPrefix(value, "MODEL_DEPLOYMENT_STATE_") {
	case "RUNNING":
		return publicv1.ModelDeploymentPhase_MODEL_DEPLOYMENT_PHASE_READY
	case "FAILED":
		return publicv1.ModelDeploymentPhase_MODEL_DEPLOYMENT_PHASE_FAILED
	case "STOPPED":
		return publicv1.ModelDeploymentPhase_MODEL_DEPLOYMENT_PHASE_STOPPED
	default:
		return publicv1.ModelDeploymentPhase_MODEL_DEPLOYMENT_PHASE_PENDING
	}
}
func mapBackendMaaSPhase(value string) publicv1.ModelMaasPhase {
	switch strings.TrimPrefix(value, "MODEL_MAAS_STATE_") {
	case "AVAILABLE":
		return publicv1.ModelMaasPhase_MODEL_MAAS_PHASE_READY
	case "AWAITING_SUBSCRIPTION", "AWAITING_SUBSCRIPTION_AND_BACKEND":
		return publicv1.ModelMaasPhase_MODEL_MAAS_PHASE_AWAITING_SUBSCRIPTION
	case "FAILED":
		return publicv1.ModelMaasPhase_MODEL_MAAS_PHASE_FAILED
	case "UNAVAILABLE":
		return publicv1.ModelMaasPhase_MODEL_MAAS_PHASE_UNAVAILABLE
	default:
		return publicv1.ModelMaasPhase_MODEL_MAAS_PHASE_PENDING
	}
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
