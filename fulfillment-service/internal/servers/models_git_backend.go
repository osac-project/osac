/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package servers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/gitops"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

const (
	modelsGitRoot               = "catalog/models"
	modelsGitCatalogIndex       = "catalog/models/index.yaml"
	modelsGitClusters           = "catalog/clusters"
	modelsGitSubscriptions      = "catalog/subscriptions"
	modelsGitIngressIndex       = "catalog/ingress/kustomization.yaml"
	modelsGitVersionAnnotation  = "ai-grid/version"
	modelsGitClustersAnnotation = "ai-grid/clusters"
	modelsGitLabel              = "ai-grid/model"
	modelsGitManagedByLabel     = "app.kubernetes.io/managed-by"
	modelsGitManagedByValue     = "ai-grid-provider-backend"
	modelCatalogStatusTimeout   = 4 * time.Second
)

type modelsBackend interface {
	call(ctx context.Context, hubID, method string, request, response any) error
}

type gitModelsBackend struct {
	writer                gitops.Writer
	hubClientProvider     HubClientProvider
	logger                *slog.Logger
	modelsNamespace       string
	gridNamespace         string
	subscriptionNamespace string
}

func newGitModelsBackend(writer gitops.Writer, provider HubClientProvider, logger *slog.Logger) *gitModelsBackend {
	return &gitModelsBackend{
		writer:                writer,
		hubClientProvider:     provider,
		logger:                logger,
		modelsNamespace:       envOrDefault("AI_GRID_MODELS_NAMESPACE", "ai-grid-internal-models"),
		gridNamespace:         envOrDefault("AI_GRID_GRID_NAMESPACE", "redhat-ai-grid"),
		subscriptionNamespace: envOrDefault("AI_GRID_SUBSCRIPTION_NAMESPACE", "models-as-a-service"),
	}
}

func (b *gitModelsBackend) call(ctx context.Context, hubID, method string, request, response any) error {
	switch method {
	case "ai_grid.v1.Models/List":
		var req backendModelsListRequest
		if err := decodeModelsValue(request, &req); err != nil {
			return err
		}
		if req.Offset != nil && *req.Offset < 0 {
			return status.Error(codes.InvalidArgument, "offset must be non-negative")
		}
		if req.Limit != nil && *req.Limit < 0 {
			return status.Error(codes.InvalidArgument, "limit must be non-negative")
		}
		items, err := b.list(ctx, hubID)
		if err != nil {
			return err
		}
		total, ok := int32FromInt64(int64(len(items)))
		if !ok {
			return status.Error(codes.ResourceExhausted, "model catalog is too large for an int32 response")
		}
		start := 0
		if req.Offset != nil {
			start = int(*req.Offset)
		}
		if start > len(items) {
			start = len(items)
		}
		end := len(items)
		if req.Limit != nil && int(*req.Limit) < end-start {
			end = start + int(*req.Limit)
		}
		return encodeModelsValue(backendModelsListResponse{Total: total, Items: items[start:end]}, response)
	case "ai_grid.v1.Models/Get":
		var req struct {
			ID string `json:"id"`
		}
		if err := decodeModelsValue(request, &req); err != nil {
			return err
		}
		model, err := b.get(ctx, hubID, req.ID)
		if err != nil {
			return err
		}
		return encodeModelsValue(backendModelResponse{Object: model}, response)
	case "ai_grid.v1.Models/Create":
		var req backendModelRequest
		if err := decodeModelsValue(request, &req); err != nil {
			return err
		}
		model, err := b.create(ctx, hubID, req.Object)
		if err != nil {
			return err
		}
		return encodeModelsValue(backendModelResponse{Object: model}, response)
	case "ai_grid.v1.Models/Update":
		var req backendModelsUpdateRequest
		if err := decodeModelsValue(request, &req); err != nil {
			return err
		}
		model, err := b.update(ctx, req)
		if err != nil {
			return err
		}
		b.applyCatalogStatus(ctx, hubID, []backendModel{model})
		return encodeModelsValue(backendModelResponse{Object: model}, response)
	case "ai_grid.v1.Models/Delete":
		var req backendModelsDeleteRequest
		if err := decodeModelsValue(request, &req); err != nil {
			return err
		}
		return b.delete(ctx, req)
	default:
		return status.Errorf(codes.Unimplemented, "AI Grid model operation %q is not implemented", method)
	}
}

func (b *gitModelsBackend) list(ctx context.Context, hubID string) ([]backendModel, error) {
	head, err := b.writer.Head(ctx)
	if err != nil {
		return nil, modelsGitError("list", "", err)
	}
	ids, err := readModelIDs(head)
	if err != nil {
		return nil, err
	}
	items := make([]backendModel, 0, len(ids))
	for _, id := range ids {
		model, err := readGitBackendModel(head, id)
		if err != nil {
			return nil, err
		}
		items = append(items, model)
	}
	b.applyCatalogStatus(ctx, hubID, items)
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func readModelIDs(head gitops.Snapshot) ([]string, error) {
	data, err := head.Read(modelsGitCatalogIndex)
	if err != nil && !errors.Is(err, gitops.ErrNotFound) {
		return nil, modelsGitError("read", modelsGitCatalogIndex, err)
	}
	if err == nil {
		var index struct {
			Models []string `json:"models"`
		}
		if err := yaml.Unmarshal(data, &index); err != nil {
			return nil, status.Errorf(codes.Internal, "parse model catalog index %q: %v", modelsGitCatalogIndex, err)
		}
		ids := make(map[string]struct{}, len(index.Models))
		for _, id := range index.Models {
			if !validModelID(id) {
				return nil, status.Errorf(codes.Internal, "invalid model id %q in catalog index", id)
			}
			ids[id] = struct{}{}
		}
		return sortedModelIDs(ids), nil
	}
	// Repositories created before the model catalog had its own index listed
	// models through the MaaS ingress index. Use it as the legacy catalog only
	// until the dedicated catalog index is written.
	ids := make(map[string]struct{})
	resources, err := readModelsKustomization(head, modelsGitIngressIndex)
	if err != nil {
		return nil, err
	}
	for _, resource := range resources {
		id := path.Base(path.Dir(resource))
		if !validModelID(id) {
			return nil, status.Errorf(codes.Internal, "invalid model catalog resource path %q", resource)
		}
		ids[id] = struct{}{}
	}
	return sortedModelIDs(ids), nil
}

func sortedModelIDs(ids map[string]struct{}) []string {
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func (b *gitModelsBackend) catalogReaderForHub(ctx context.Context, hubID string) (*CatalogStatusReader, dynamic.Interface, error) {
	if b.hubClientProvider == nil {
		return nil, nil, status.Error(codes.FailedPrecondition, "Hub client provider is not configured")
	}
	hub, err := b.hubClientProvider.GetClient(ctx, hubID)
	if err != nil {
		return nil, nil, err
	}
	if hub.Config == nil {
		return nil, nil, status.Errorf(codes.Internal, "AI Grid Hub %q has no Kubernetes client configuration", hubID)
	}
	client, err := dynamic.NewForConfig(hub.Config)
	if err != nil {
		return nil, nil, status.Errorf(codes.Internal, "cannot configure AI Grid model status client: %v", err)
	}
	reader := NewCatalogStatusReader(client, CatalogApps{
		Namespace: envOrDefault("AI_GRID_CATALOG_APP_NAMESPACE", DefaultCatalogAppNamespace),
		Models:    envOrDefault("AI_GRID_CATALOG_MODELS_APPSET", DefaultCatalogModelsAppSet),
		Ingress:   envOrDefault("AI_GRID_CATALOG_INGRESS_APPSET", DefaultCatalogIngressAppSet),
	}).SetLogger(b.logger)
	return reader, client, nil
}

func (b *gitModelsBackend) applyCatalogStatus(ctx context.Context, hubID string, models []backendModel) {
	if len(models) == 0 {
		return
	}
	if hubID == "" {
		setCatalogStatus(models, "HubUnavailable", "An AI Grid hub could not be resolved; deployment status is unavailable.")
		return
	}
	if b.hubClientProvider == nil {
		setCatalogStatusUnavailable(models, status.Error(codes.Unavailable, "AI Grid Hub status client is not configured"))
		return
	}
	statusCtx, cancel := context.WithTimeout(ctx, modelCatalogStatusTimeout)
	defer cancel()
	reader, client, err := b.catalogReaderForHub(statusCtx, hubID)
	if err != nil {
		b.logger.WarnContext(ctx, "cannot read AI Grid model rollout status", "hub", hubID, "error", err)
		setCatalogStatusUnavailable(models, err)
		return
	}
	inference, err := reader.Read(statusCtx, ModelsSet, kindLLMInferenceService)
	if err != nil {
		b.logger.WarnContext(ctx, "cannot read AI Grid inference rollout status", "hub", hubID, "error", err)
		setCatalogStatusUnavailable(models, err)
		return
	}
	refs, err := reader.Read(statusCtx, IngressSet, kindMaaSModelRef)
	if err != nil {
		b.logger.WarnContext(ctx, "cannot read AI Grid model publication status", "hub", hubID, "error", err)
		setCatalogStatusUnavailable(models, err)
		return
	}
	externals, err := reader.Read(statusCtx, IngressSet, kindExternalModel)
	if err != nil {
		b.logger.WarnContext(ctx, "cannot read AI Grid model gateway status", "hub", hubID, "error", err)
		setCatalogStatusUnavailable(models, err)
		return
	}
	managedClusters, err := client.Resource(aiGridManagedClustersResource).List(statusCtx, metav1.ListOptions{})
	if err != nil {
		b.logger.WarnContext(ctx, "cannot list AI Grid ingress clusters", "hub", hubID, "error", err)
		setCatalogStatusUnavailable(models, err)
		return
	}
	ingressClusters := make([]string, 0)
	for i := range managedClusters.Items {
		item := &managedClusters.Items[i]
		labels := item.GetLabels()
		role := labels["ai-grid/role"]
		if labels["ai-grid/enabled"] == "true" && (role == "ingress" || role == "combined") {
			ingressClusters = append(ingressClusters, item.GetName())
		}
	}
	for i := range models {
		applyBackendModelStatus(&models[i], inference, refs, externals, ingressClusters)
	}
}

func setCatalogStatusUnavailable(models []backendModel, err error) {
	reason := "StatusUnavailable"
	message := "Deployment status could not be read from the AI Grid hub."
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) {
		reason = "HubUnreachable"
		message = "The AI Grid hub is unreachable; deployment status is unavailable."
	}
	setCatalogStatus(models, reason, message)
}

func setCatalogStatus(models []backendModel, reason, message string) {
	for i := range models {
		models[i].Status = backendModelStatus{Phase: "MODEL_PHASE_PENDING", Reason: reason, Message: message}
	}
}

func applyBackendModelStatus(model *backendModel, inference, refs, externals ObjectReports, ingressClusters []string) {
	if model == nil {
		return
	}
	id := model.ID
	statusValue := backendModelStatus{Phase: "MODEL_PHASE_PENDING", Reason: "NotReady"}
	placed := backendModelSelectedClusters(*model)
	if len(placed) == 0 {
		model.Status = backendModelStatus{
			Phase: "MODEL_PHASE_PENDING", Reason: "Unplaced",
			Message: "No inference clusters selected; the model is in the catalog without deployments.",
		}
		return
	}
	deploymentReady, deploymentFailed := 0, false
	var messages []string
	for _, cluster := range placed {
		report, found := inference[cluster][id]
		state := "MODEL_DEPLOYMENT_STATE_PENDING"
		reason := "NotReady"
		if found {
			switch report.State {
			case StateReady:
				state, reason = "MODEL_DEPLOYMENT_STATE_RUNNING", "Ready"
				deploymentReady++
			case StateFailed:
				state, reason = "MODEL_DEPLOYMENT_STATE_FAILED", "Failed"
				deploymentFailed = true
			case StatePending:
				reason = "NotReady"
			}
			if report.Message != "" {
				messages = append(messages, cluster+": "+report.Message)
			}
		}
		statusValue.Clusters = append(statusValue.Clusters, backendModelCluster{Cluster: cluster, Deployment: state, Reason: reason, DeploymentMessage: report.Message, Message: report.Message})
	}
	ingressReady, ingressFailed := 0, false
	for _, cluster := range ingressClusters {
		ref, refFound := refs[cluster][id]
		external, externalFound := externals[cluster][id]
		phase := "MODEL_MAAS_STATE_UNAVAILABLE"
		message := firstNonEmpty(ref.Message, external.Message)
		refReady := refFound && (ref.State == StateReady || ref.NoSubscription)
		if refReady && externalFound && external.State == StateReady {
			if ref.NoSubscription {
				phase = "MODEL_MAAS_STATE_AWAITING_SUBSCRIPTION"
			} else {
				phase = "MODEL_MAAS_STATE_AVAILABLE"
			}
			ingressReady++
		} else if (refFound && ref.State == StateFailed) || (externalFound && external.State == StateFailed) {
			phase = "MODEL_MAAS_STATE_FAILED"
			ingressFailed = true
		}
		if message != "" {
			messages = append(messages, cluster+": "+message)
		}
		statusValue.Ingress = append(statusValue.Ingress, backendModelIngress{Cluster: cluster, State: phase, Message: message})
	}
	switch {
	case deploymentFailed || ingressFailed:
		statusValue.Phase = "MODEL_PHASE_FAILED"
		statusValue.Reason = "Failed"
	case len(placed) > 0 && deploymentReady == len(placed) && len(ingressClusters) > 0 && ingressReady == len(ingressClusters):
		statusValue.Phase = "MODEL_PHASE_READY"
		statusValue.Reason = "Ready"
	case deploymentReady > 0 && len(ingressClusters) > 0 && ingressReady == len(ingressClusters):
		statusValue.Phase = "MODEL_PHASE_PARTIALLY_AVAILABLE"
		statusValue.Reason = "PartiallyAvailable"
	}
	statusValue.Message = strings.Join(messages, "; ")
	model.Status = statusValue
}

func (b *gitModelsBackend) get(ctx context.Context, hubID, id string) (backendModel, error) {
	if !validModelID(id) {
		return backendModel{}, status.Errorf(codes.InvalidArgument, "invalid model id %q", id)
	}
	head, err := b.writer.Head(ctx)
	if err != nil {
		return backendModel{}, modelsGitError("get", id, err)
	}
	model, err := readGitBackendModel(head, id)
	if err != nil {
		return backendModel{}, err
	}
	b.applyCatalogStatus(ctx, hubID, []backendModel{model})
	return model, nil
}

func (b *gitModelsBackend) create(ctx context.Context, hubID string, model backendModel) (backendModel, error) {
	id := model.ID
	if !validModelID(id) {
		return backendModel{}, status.Errorf(codes.InvalidArgument, "invalid model id %q", id)
	}
	clusters := backendModelSelectedClusters(model)
	var created backendModel
	err := gitops.RetryOnConflict(ctx, func() error {
		head, err := b.writer.Head(ctx)
		if err != nil {
			return modelsGitError("create", id, err)
		}
		if _, err := head.Read(modelDeploymentPath(id)); err == nil {
			return status.Errorf(codes.AlreadyExists, "Model %q already exists", id)
		} else if !errors.Is(err, gitops.ErrNotFound) {
			return modelsGitError("create", id, err)
		}
		if err := b.checkModelObjectsAvailable(ctx, hubID, id); err != nil {
			return err
		}
		model.Metadata.Name = id
		model.Metadata.Version = 1
		model.Status = backendModelStatus{Phase: "MODEL_PHASE_PENDING"}
		if err := b.commitModel(ctx, head, "create", model, nil, clusters); err != nil {
			return err
		}
		created = model
		return nil
	})
	if err == nil {
		b.applyCatalogStatus(ctx, hubID, []backendModel{created})
	}
	return created, err
}

func (b *gitModelsBackend) checkModelObjectsAvailable(ctx context.Context, hubID, id string) error {
	if b.hubClientProvider == nil {
		return nil
	}
	reader, _, err := b.catalogReaderForHub(ctx, hubID)
	if err != nil {
		return status.Errorf(codes.Unavailable, "could not check the Hub's Argo CD Applications for model %q", id)
	}
	owners, err := reader.SharedResources(ctx, []ObjectKey{
		{Kind: kindLLMInferenceService, Namespace: b.modelsNamespace, Name: id},
		{Kind: kindNetworkPolicy, Namespace: b.modelsNamespace, Name: id + "-grid"},
		{Kind: kindExternalModel, Namespace: b.subscriptionNamespace, Name: id},
		{Kind: kindMaaSModelRef, Namespace: b.subscriptionNamespace, Name: id},
	})
	if err != nil {
		return status.Errorf(codes.Unavailable, "could not check Argo CD Applications for model %q", id)
	}
	if len(owners) == 0 {
		return nil
	}
	return status.Errorf(codes.AlreadyExists, "Model %q is already deployed outside the AI Grid catalog", id)
}

func (b *gitModelsBackend) update(ctx context.Context, request backendModelsUpdateRequest) (backendModel, error) {
	id := request.Object.ID
	var updated backendModel
	err := gitops.RetryOnConflict(ctx, func() error {
		head, err := b.writer.Head(ctx)
		if err != nil {
			return modelsGitError("update", id, err)
		}
		current, err := readGitBackendModel(head, id)
		if err != nil {
			return err
		}
		if request.Lock && request.Object.Metadata.Version != current.Metadata.Version {
			return status.Errorf(codes.Aborted, "model %q has changed; fetch the latest version and retry", id)
		}
		clusters := backendModelSelectedClusters(request.Object)
		oldClusters := backendModelSelectedClusters(current)
		model := request.Object
		model.Metadata.Version = current.Metadata.Version + 1
		model.Status = current.Status
		if err := b.commitModel(ctx, head, "update", model, oldClusters, clusters); err != nil {
			return err
		}
		updated = model
		return nil
	})
	return updated, err
}

func (b *gitModelsBackend) delete(ctx context.Context, request backendModelsDeleteRequest) error {
	return gitops.RetryOnConflict(ctx, func() error {
		head, err := b.writer.Head(ctx)
		if err != nil {
			return modelsGitError("delete", request.ID, err)
		}
		current, err := readGitBackendModel(head, request.ID)
		if err != nil {
			return err
		}
		if request.ResourceVersion != 0 && request.ResourceVersion != current.Metadata.Version {
			return status.Errorf(codes.Aborted, "model %q has changed; fetch the latest version and retry", request.ID)
		}
		if err := ensureModelNotSubscribed(head, request.ID, b.subscriptionNamespace); err != nil {
			return err
		}
		change := gitops.Change{
			Delete:  []string{modelDeploymentPath(request.ID), modelDeploymentKustomizationPath(request.ID), modelCatalogPath(request.ID), modelCatalogKustomizationPath(request.ID)},
			Put:     map[string][]byte{},
			Read:    []string{modelsGitSubscriptions},
			Message: "models: delete " + request.ID,
		}
		if err := putModelsIndexEntry(head, &change, modelIngressResource(request.ID), modelsGitIngressIndex, false); err != nil {
			return err
		}
		if err := putModelCatalogIndexEntry(head, &change, request.ID, false); err != nil {
			return err
		}
		for _, cluster := range backendModelSelectedClusters(current) {
			if err := putModelsIndexEntry(head, &change, modelClusterResource(request.ID), modelClusterIndex(cluster), false); err != nil {
				return err
			}
		}
		if _, err := b.writer.Commit(ctx, head.Revision(), change); err != nil {
			return modelsGitError("delete", request.ID, err)
		}
		return nil
	})
}

func ensureModelNotSubscribed(head gitops.Snapshot, id, subscriptionNamespace string) error {
	files, err := head.List(modelsGitSubscriptions)
	if err != nil && !errors.Is(err, gitops.ErrNotFound) {
		return modelsGitError("delete", id, err)
	}
	for _, name := range files {
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}
		data, err := head.Read(path.Join(modelsGitSubscriptions, name))
		if err != nil {
			return modelsGitError("delete", id, err)
		}
		var subscription map[string]any
		if err := yaml.Unmarshal(firstModelsYAMLDocument(data), &subscription); err != nil {
			return status.Errorf(codes.Internal, "read MaaS subscription %q while checking model references: %v", name, err)
		}
		metadata, _ := subscription["metadata"].(map[string]any)
		namespace := anyString(metadata["namespace"])
		spec, _ := subscription["spec"].(map[string]any)
		refs, _ := spec["modelRefs"].([]any)
		for _, raw := range refs {
			ref, _ := raw.(map[string]any)
			refNamespace := anyString(ref["namespace"])
			if anyString(ref["name"]) == id && (refNamespace == "" || refNamespace == namespace || refNamespace == subscriptionNamespace) {
				return status.Errorf(codes.FailedPrecondition, "Model %q is referenced by Subscription %q", id, strings.TrimSuffix(name, ".yaml"))
			}
		}
	}
	return nil
}

func (b *gitModelsBackend) commitModel(ctx context.Context, head gitops.Snapshot, operation string, model backendModel, from, to []string) error {
	id := model.ID
	deployment, err := renderModelsDeployment(model, b.modelsNamespace, b.gridNamespace)
	if err != nil {
		return status.Errorf(codes.Internal, "render model %q deployment: %v", id, err)
	}
	catalog, err := renderModelsCatalog(model, b.subscriptionNamespace)
	if err != nil {
		return status.Errorf(codes.Internal, "render model %q catalog: %v", id, err)
	}
	change := gitops.Change{
		Put: map[string][]byte{
			modelDeploymentPath(id):              deployment,
			modelDeploymentKustomizationPath(id): renderModelsKustomization([]string{"deployment.yaml"}),
			modelCatalogPath(id):                 catalog,
			modelCatalogKustomizationPath(id):    renderModelsKustomization([]string{"model.yaml"}),
		},
		Message: "models: " + operation + " " + id,
	}
	if from == nil {
		from = []string{}
	}
	if err := putModelCatalogIndexEntry(head, &change, id, true); err != nil {
		return err
	}
	if len(from) == 0 && len(to) > 0 {
		if err := putModelsIndexEntry(head, &change, modelIngressResource(id), modelsGitIngressIndex, true); err != nil {
			return err
		}
	} else if len(from) > 0 && len(to) == 0 {
		if err := putModelsIndexEntry(head, &change, modelIngressResource(id), modelsGitIngressIndex, false); err != nil {
			return err
		}
	}
	for _, cluster := range from {
		if !slices.Contains(to, cluster) {
			if err := putModelsIndexEntry(head, &change, modelClusterResource(id), modelClusterIndex(cluster), false); err != nil {
				return err
			}
		}
	}
	for _, cluster := range to {
		if err := putModelsIndexEntry(head, &change, modelClusterResource(id), modelClusterIndex(cluster), true); err != nil {
			return err
		}
	}
	if _, err := b.writer.Commit(ctx, head.Revision(), change); err != nil {
		return modelsGitError(operation, id, err)
	}
	return nil
}

func readGitBackendModel(head gitops.Snapshot, id string) (backendModel, error) {
	data, err := head.Read(modelDeploymentPath(id))
	if errors.Is(err, gitops.ErrNotFound) {
		return backendModel{}, status.Errorf(codes.NotFound, "Model %q not found", id)
	}
	if err != nil {
		return backendModel{}, modelsGitError("get", id, err)
	}
	var llm map[string]any
	if err := yaml.Unmarshal(firstModelsYAMLDocument(data), &llm); err != nil {
		return backendModel{}, status.Errorf(codes.Internal, "parse model %q from catalog: %v", id, err)
	}
	metadata, _ := llm["metadata"].(map[string]any)
	annotations := stringMapFromAny(metadata["annotations"])
	labels := stringMapFromAny(metadata["labels"])
	spec, _ := llm["spec"].(map[string]any)
	model := backendModel{
		ID:       id,
		Metadata: backendModelMetadata{Name: id, Labels: labels, Annotations: annotations},
		Spec:     backendModelSpec{Main: backendModelContainer{}},
		Status:   backendModelStatus{Phase: "MODEL_PHASE_PENDING", Message: "model desired state is committed; waiting for Argo CD status"},
	}
	version, err := strconv.ParseInt(annotations[modelsGitVersionAnnotation], 10, 32)
	if err != nil || version < 1 {
		return backendModel{}, status.Errorf(codes.Internal, "model %q has an invalid catalog version", id)
	}
	version32, ok := int32FromInt64(version)
	if !ok {
		return backendModel{}, status.Errorf(codes.Internal, "model %q has an invalid catalog version", id)
	}
	model.Metadata.Version = version32
	clusters := strings.Split(annotations[modelsGitClustersAnnotation], ",")
	clusters = slices.DeleteFunc(clusters, func(cluster string) bool { return cluster == "" })
	model.Spec.Placement = backendModelPlacement{LabelSelector: &backendModelLabelSelector{MatchExpressions: []backendModelSelectorRequirement{{Key: "name", Operator: "In", Values: clusters}}}}
	if modelSpec, ok := spec["model"].(map[string]any); ok {
		model.Spec.Model = backendModelSource{URI: anyString(modelSpec["uri"]), Name: anyString(modelSpec["name"])}
	}
	model.Spec.Runtime = anyString(spec["runtime"])
	replicas, ok := anyInt32Pointer(spec["replicas"])
	if !ok {
		return backendModel{}, status.Errorf(codes.Internal, "model %q has an invalid replica count", id)
	}
	model.Spec.Replicas = replicas
	model.Spec.BaseRefs = modelBaseRefs(spec["baseRefs"])
	model.Spec.Main = modelMainContainer(spec["template"])
	model.Spec.Router = modelRouterFromAny(spec["router"])
	model.Spec.Scaling = modelScalingFromAny(spec["scaling"])
	delete(model.Metadata.Annotations, modelsGitVersionAnnotation)
	delete(model.Metadata.Annotations, modelsGitClustersAnnotation)
	delete(model.Metadata.Labels, modelsGitManagedByLabel)
	return model, nil
}

func readModelsKustomization(head gitops.Snapshot, file string) ([]string, error) {
	data, err := head.Read(file)
	if errors.Is(err, gitops.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, modelsGitError("read", file, err)
	}
	var index struct {
		Resources []string `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &index); err != nil {
		return nil, status.Errorf(codes.Internal, "parse model catalog index %q: %v", file, err)
	}
	return index.Resources, nil
}

func putModelsIndexEntry(head gitops.Snapshot, change *gitops.Change, resource, index string, present bool) error {
	resources, err := readModelsKustomization(head, index)
	if err != nil {
		return err
	}
	if slices.Contains(resources, resource) == present {
		change.Read = append(change.Read, index)
		return nil
	}
	if present {
		resources = append(resources, resource)
		sort.Strings(resources)
	} else {
		resources = slices.DeleteFunc(resources, func(value string) bool { return value == resource })
	}
	change.Put[index] = renderModelsKustomization(resources)
	return nil
}

func putModelCatalogIndexEntry(head gitops.Snapshot, change *gitops.Change, id string, present bool) error {
	models, err := readModelCatalogIndex(head)
	if err != nil {
		return err
	}
	if _, err := head.Read(modelsGitCatalogIndex); errors.Is(err, gitops.ErrNotFound) {
		// The first dedicated index is migrated from ingress. Guard that source
		// against concurrent edits so an old model cannot be omitted.
		change.Read = append(change.Read, modelsGitIngressIndex)
	} else if err != nil {
		return modelsGitError("read", modelsGitCatalogIndex, err)
	}
	if slices.Contains(models, id) == present {
		change.Read = append(change.Read, modelsGitCatalogIndex)
		return nil
	}
	if present {
		models = append(models, id)
		sort.Strings(models)
	} else {
		models = slices.DeleteFunc(models, func(value string) bool { return value == id })
	}
	data, err := yaml.Marshal(map[string]any{"models": models})
	if err != nil {
		return status.Errorf(codes.Internal, "render model catalog index: %v", err)
	}
	change.Put[modelsGitCatalogIndex] = data
	return nil
}

func readModelCatalogIndex(head gitops.Snapshot) ([]string, error) {
	data, err := head.Read(modelsGitCatalogIndex)
	if errors.Is(err, gitops.ErrNotFound) {
		resources, err := readModelsKustomization(head, modelsGitIngressIndex)
		if err != nil {
			return nil, err
		}
		ids := make(map[string]struct{}, len(resources))
		for _, resource := range resources {
			id := path.Base(path.Dir(resource))
			if !validModelID(id) {
				return nil, status.Errorf(codes.Internal, "invalid model catalog resource path %q", resource)
			}
			ids[id] = struct{}{}
		}
		return sortedModelIDs(ids), nil
	}
	if err != nil {
		return nil, modelsGitError("read", modelsGitCatalogIndex, err)
	}
	var index struct {
		Models []string `json:"models"`
	}
	if err := yaml.Unmarshal(data, &index); err != nil {
		return nil, status.Errorf(codes.Internal, "parse model catalog index %q: %v", modelsGitCatalogIndex, err)
	}
	ids := make(map[string]struct{}, len(index.Models))
	for _, id := range index.Models {
		if !validModelID(id) {
			return nil, status.Errorf(codes.Internal, "invalid model id %q in catalog index", id)
		}
		ids[id] = struct{}{}
	}
	return sortedModelIDs(ids), nil
}

func renderModelsKustomization(resources []string) []byte {
	if resources == nil {
		resources = []string{}
	}
	data, _ := yaml.Marshal(map[string]any{"apiVersion": "kustomize.config.k8s.io/v1beta1", "kind": "Kustomization", "resources": resources})
	return data
}

func renderModelsDeployment(model backendModel, namespace, gridNamespace string) ([]byte, error) {
	id := model.ID
	labels := map[string]any{modelsGitLabel: id, modelsGitManagedByLabel: modelsGitManagedByValue}
	for key, value := range model.Metadata.Labels {
		labels[key] = value
	}
	annotations := map[string]any{}
	for key, value := range model.Metadata.Annotations {
		annotations[key] = value
	}
	annotations[modelsGitVersionAnnotation] = strconv.Itoa(int(model.Metadata.Version))
	annotations[modelsGitClustersAnnotation] = strings.Join(backendModelSelectedClusters(model), ",")
	main := map[string]any{"name": "main"}
	if model.Spec.Main.Image != "" {
		main["image"] = model.Spec.Main.Image
	}
	if len(model.Spec.Main.Env) > 0 {
		env := make([]any, 0, len(model.Spec.Main.Env))
		for _, variable := range model.Spec.Main.Env {
			if variable.Name == "" {
				continue
			}
			env = append(env, map[string]any{"name": variable.Name, "value": variable.Value})
		}
		if len(env) > 0 {
			main["env"] = env
		}
	}
	resources := map[string]any{}
	if len(model.Spec.Main.Requests) > 0 {
		resources["requests"] = model.Spec.Main.Requests
	}
	if len(model.Spec.Main.Limits) > 0 {
		resources["limits"] = model.Spec.Main.Limits
	}
	if len(resources) > 0 {
		main["resources"] = resources
	}
	modelName := model.Spec.Model.Name
	if modelName == "" {
		modelName = id
	}
	llmSpec := map[string]any{
		"model":    map[string]any{"uri": model.Spec.Model.URI, "name": modelName},
		"router":   modelRouterToAny(model.Spec.Router),
		"template": map[string]any{"containers": []any{main}},
	}
	if model.Spec.Replicas != nil {
		llmSpec["replicas"] = int64(*model.Spec.Replicas)
	}
	if len(model.Spec.BaseRefs) > 0 {
		refs := make([]any, 0, len(model.Spec.BaseRefs))
		for _, ref := range model.Spec.BaseRefs {
			refs = append(refs, map[string]any{"name": ref})
		}
		llmSpec["baseRefs"] = refs
	}
	if model.Spec.Runtime != "" {
		llmSpec["runtime"] = model.Spec.Runtime
	}
	if model.Spec.Scaling != nil {
		minReplicas := model.Spec.Scaling.MinReplicas
		if minReplicas <= 0 {
			minReplicas = 1
		}
		scaling := map[string]any{"minReplicas": minReplicas, "maxReplicas": model.Spec.Scaling.MaxReplicas}
		if model.Spec.Scaling.Keda {
			triggers := make([]any, 0, len(model.Spec.Scaling.Triggers))
			for _, trigger := range model.Spec.Scaling.Triggers {
				triggers = append(triggers, map[string]any{"type": trigger.Type, "metadata": trigger.Metadata})
			}
			scaling["keda"] = map[string]any{"triggers": triggers}
		}
		llmSpec["scaling"] = scaling
	}
	llm := map[string]any{"apiVersion": "serving.kserve.io/v1alpha2", "kind": "LLMInferenceService", "metadata": map[string]any{"name": id, "namespace": namespace, "labels": labels, "annotations": annotations}, "spec": llmSpec}
	policy := map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": id + "-grid", "namespace": namespace, "labels": map[string]any{modelsGitLabel: id, modelsGitManagedByLabel: modelsGitManagedByValue}},
		"spec":     map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/name": id}}, "policyTypes": []string{"Ingress"}, "ingress": []any{map[string]any{"ports": []any{map[string]any{"port": int64(8000), "protocol": "TCP"}, map[string]any{"port": int64(9090), "protocol": "TCP"}}, "from": []any{map[string]any{"namespaceSelector": map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": gridNamespace}}}}}}},
	}
	return marshalModelsYAMLDocuments(llm, policy)
}

func renderModelsCatalog(model backendModel, namespace string) ([]byte, error) {
	id := model.ID
	targetModel := model.Spec.Model.Name
	if targetModel == "" {
		targetModel = id
	}
	labels := map[string]any{modelsGitLabel: id, modelsGitManagedByLabel: modelsGitManagedByValue}
	external := map[string]any{
		"apiVersion": "inference.opendatahub.io/v1alpha1", "kind": "ExternalModel",
		"metadata": map[string]any{"name": id, "namespace": namespace, "labels": labels},
		"spec":     map[string]any{"externalProviderRefs": []any{map[string]any{"ref": map[string]any{"name": "grid-provider"}, "apiFormat": "openai-chat", "path": "/v1/chat/completions", "targetModel": targetModel, "weight": int64(1)}}},
	}
	modelRefMetadata := map[string]any{"name": id, "namespace": namespace, "labels": labels}
	if len(model.Metadata.Annotations) > 0 {
		annotations := make(map[string]any, len(model.Metadata.Annotations))
		for key, value := range model.Metadata.Annotations {
			annotations[key] = value
		}
		modelRefMetadata["annotations"] = annotations
	}
	modelRef := map[string]any{
		"apiVersion": "maas.opendatahub.io/v1alpha1", "kind": "MaaSModelRef",
		"metadata": modelRefMetadata,
		"spec":     map[string]any{"modelRef": map[string]any{"kind": "ExternalModel", "name": id}},
	}
	return marshalModelsYAMLDocuments(external, modelRef)
}

func marshalModelsYAMLDocuments(objects ...map[string]any) ([]byte, error) {
	var out bytes.Buffer
	for i, object := range objects {
		data, err := yaml.Marshal(object)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			out.WriteString("---\n")
		}
		out.Write(data)
	}
	return out.Bytes(), nil
}

func decodeModelsValue(value, target any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return status.Errorf(codes.Internal, "encode model request: %v", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return status.Errorf(codes.InvalidArgument, "decode model request: %v", err)
	}
	return nil
}

func encodeModelsValue(value, target any) error {
	if target == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return status.Errorf(codes.Internal, "encode model response: %v", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return status.Errorf(codes.Internal, "decode model response: %v", err)
	}
	return nil
}

func modelsGitError(operation, id string, err error) error {
	switch {
	case errors.Is(err, gitops.ErrNotConfigured):
		return status.Error(codes.FailedPrecondition, "AI Grid model catalog is not configured; set AI_GRID_GIT_URL")
	case errors.Is(err, gitops.ErrNotReady):
		return status.Error(codes.Unavailable, "AI Grid model catalog is not ready")
	case errors.Is(err, gitops.ErrConflict):
		return status.Errorf(codes.Aborted, "model %q changed concurrently; retry the request", id)
	default:
		return status.Errorf(codes.Unavailable, "AI Grid model catalog %s failed for model %q: %v", operation, id, err)
	}
}

func modelDeploymentPath(id string) string {
	return path.Join(modelsGitRoot, id, "deployment", "deployment.yaml")
}
func modelDeploymentKustomizationPath(id string) string {
	return path.Join(modelsGitRoot, id, "deployment", "kustomization.yaml")
}
func modelCatalogPath(id string) string { return path.Join(modelsGitRoot, id, "catalog", "model.yaml") }
func modelCatalogKustomizationPath(id string) string {
	return path.Join(modelsGitRoot, id, "catalog", "kustomization.yaml")
}
func modelClusterIndex(cluster string) string {
	return path.Join(modelsGitClusters, cluster, "kustomization.yaml")
}
func modelClusterResource(id string) string { return "../../" + path.Join("models", id, "deployment") }
func modelIngressResource(id string) string { return "../" + path.Join("models", id, "catalog") }

func firstModelsYAMLDocument(data []byte) []byte {
	if index := bytes.Index(data, []byte("\n---")); index >= 0 {
		return data[:index]
	}
	return data
}

func stringMapFromAny(value any) map[string]string {
	values, _ := value.(map[string]any)
	out := make(map[string]string, len(values))
	for key, value := range values {
		if stringValue, ok := value.(string); ok {
			out[key] = stringValue
		}
	}
	return out
}

func anyString(value any) string { result, _ := value.(string); return result }

func int32FromInt64(value int64) (int32, bool) {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, false
	}
	return int32(value), true // #nosec G115 -- checked against the int32 bounds above
}

func anyInt32Pointer(value any) (*int32, bool) {
	if value == nil {
		return nil, true
	}
	switch number := value.(type) {
	case int64:
		result, ok := int32FromInt64(number)
		if !ok {
			return nil, false
		}
		return &result, true
	case int:
		result, ok := int32FromInt64(int64(number))
		if !ok {
			return nil, false
		}
		return &result, true
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number ||
			number < math.MinInt32 || number > math.MaxInt32 {
			return nil, false
		}
		result := int32(number)
		return &result, true
	default:
		return nil, false
	}
}

func modelBaseRefs(value any) []string {
	items, _ := value.([]any)
	refs := make([]string, 0, len(items))
	for _, item := range items {
		if ref, ok := item.(map[string]any); ok && anyString(ref["name"]) != "" {
			refs = append(refs, anyString(ref["name"]))
		} else if text, ok := item.(string); ok {
			refs = append(refs, text)
		}
	}
	return refs
}

func modelMainContainer(value any) backendModelContainer {
	template, _ := value.(map[string]any)
	containers, _ := template["containers"].([]any)
	for _, item := range containers {
		container, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if name := anyString(container["name"]); name != "main" && name != "" {
			continue
		}
		resources, _ := container["resources"].(map[string]any)
		result := backendModelContainer{
			Image:    anyString(container["image"]),
			Requests: stringMapFromAny(resources["requests"]),
			Limits:   stringMapFromAny(resources["limits"]),
		}
		env, _ := container["env"].([]any)
		for _, raw := range env {
			item, ok := raw.(map[string]any)
			if ok {
				result.Env = append(result.Env, backendModelEnv{Name: anyString(item["name"]), Value: anyString(item["value"])})
			}
		}
		return result
	}
	return backendModelContainer{}
}

func modelRouterFromAny(value any) *backendModelRouter {
	router, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	result := &backendModelRouter{}
	if _, found := router["route"]; found {
		result.ManagedRoute = true
	}
	if _, found := router["scheduler"]; found {
		result.Scheduler = true
	}
	if gateway, ok := router["gateway"].(map[string]any); ok {
		refs, _ := gateway["refs"].([]any)
		for _, raw := range refs {
			ref, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			result.GatewayRefs = append(result.GatewayRefs, backendModelGateway{
				Name: anyString(ref["name"]), Namespace: anyString(ref["namespace"]), SectionName: anyString(ref["sectionName"]),
			})
		}
	}
	return result
}

func modelRouterToAny(router *backendModelRouter) map[string]any {
	managedRoute := true
	scheduler := false
	var refs []backendModelGateway
	if router != nil {
		refs = router.GatewayRefs
		scheduler = router.Scheduler
		if router.ManagedRoute || router.Scheduler || len(refs) > 0 {
			managedRoute = router.ManagedRoute
		}
	}
	result := map[string]any{}
	if managedRoute {
		result["route"] = map[string]any{}
	}
	if scheduler {
		result["scheduler"] = map[string]any{}
	}
	if len(refs) == 0 {
		refs = []backendModelGateway{{Name: "openshift-ai-inference", Namespace: "openshift-ingress"}}
	}
	serializedRefs := make([]any, 0, len(refs))
	for _, ref := range refs {
		if ref.Name == "" {
			continue
		}
		entry := map[string]any{"name": ref.Name}
		if ref.Namespace != "" {
			entry["namespace"] = ref.Namespace
		}
		if ref.SectionName != "" {
			entry["sectionName"] = ref.SectionName
		}
		serializedRefs = append(serializedRefs, entry)
	}
	result["gateway"] = map[string]any{"refs": serializedRefs}
	return result
}

func modelScalingFromAny(value any) *backendModelScaling {
	data, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	scaling := &backendModelScaling{MinReplicas: int32Number(data["minReplicas"]), MaxReplicas: int32Number(data["maxReplicas"])}
	keda, _ := data["keda"].(map[string]any)
	scaling.Keda = keda != nil
	triggers, _ := keda["triggers"].([]any)
	for _, item := range triggers {
		trigger, ok := item.(map[string]any)
		if !ok {
			continue
		}
		scaling.Triggers = append(scaling.Triggers, backendModelScalingTrigger{Type: anyString(trigger["type"]), Metadata: stringMapFromAny(trigger["metadata"])})
	}
	return scaling
}

func int32Number(value any) int32 {
	if number, ok := anyInt32Pointer(value); ok && number != nil {
		return *number
	}
	return 0
}

func validModelID(id string) bool {
	if len(id) == 0 || len(id) > 63 || !isLowerAlphaNumeric(id[0]) || !isLowerAlphaNumeric(id[len(id)-1]) {
		return false
	}
	for _, char := range id {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func isLowerAlphaNumeric(char byte) bool {
	return (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')
}
