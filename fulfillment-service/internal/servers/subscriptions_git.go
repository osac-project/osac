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
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/gitops"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	apierrors "k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/yaml"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const (
	subscriptionsGitDir            = "catalog/subscriptions"
	subscriptionVersionAnnotation  = "ai-grid/version"
	subscriptionLabelKey           = "ai-grid/subscription"
	subscriptionManagedByLabel     = "app.kubernetes.io/managed-by"
	subscriptionManagedByValue     = "ai-grid-provider-backend"
	subscriptionDisplayName        = "openshift.io/display-name"
	subscriptionDescription        = "openshift.io/description"
	maasSubscriptionKind           = "MaaSSubscription"
	maasAuthPolicyKind             = "MaaSAuthPolicy"
	maasModelRefKind               = "MaaSModelRef"
	apiKeyRetainOnDeleteAnnotation = "maas.opendatahub.io/retain-api-keys-on-delete"
)

var subscriptionTokenWindowPattern = regexp.MustCompile(`^[1-9]\d{0,3}(s|m|h)$`)

func tokenWindowSeconds(window string) (int64, bool) {
	if !subscriptionTokenWindowPattern.MatchString(window) {
		return 0, false
	}
	value, err := strconv.ParseInt(window[:len(window)-1], 10, 64)
	if err != nil {
		return 0, false
	}
	switch window[len(window)-1] {
	case 'm':
		value *= 60
	case 'h':
		value *= 3600
	}
	return value, true
}

type subscriptionDoc struct {
	object *publicv1.Subscription
	policy *publicv1.AuthPolicy
}

func subscriptionGitPath(id string) string { return path.Join(subscriptionsGitDir, id+".yaml") }

func subscriptionID(object *publicv1.Subscription) string {
	if object == nil {
		return ""
	}
	if object.GetId() != "" {
		return object.GetId()
	}
	if object.GetMetadata() != nil {
		return object.GetMetadata().GetName()
	}
	return ""
}

func subscriptionPolicyName(id string) string { return id + subscriptionPolicySuffix }

func validateSubscriptionID(id string) error {
	if id == "" {
		return status.Error(codes.InvalidArgument, "id is required")
	}
	if !validModelID(id) {
		return status.Errorf(codes.InvalidArgument, "id %q is not a valid RFC 1123 DNS label", id)
	}
	if len(id) > subscriptionMaxIDLength {
		return status.Errorf(codes.InvalidArgument, "id %q is too long; leave room for the -policy suffix", id)
	}
	return nil
}

func validateSubscriptionSpec(spec *publicv1.SubscriptionSpec) error {
	if spec == nil {
		return status.Error(codes.InvalidArgument, "spec is required")
	}
	owner := spec.GetOwner()
	if owner == nil || (len(owner.GetGroups()) == 0 && len(owner.GetUsers()) == 0) {
		return status.Error(codes.InvalidArgument, "spec.owner must include at least one group or user")
	}
	if spec.GetOrganization() == nil || strings.TrimSpace(spec.GetOrganization().GetId()) == "" {
		return status.Error(codes.InvalidArgument, "spec.organization.id is required")
	}
	if !validModelID(spec.GetOrganization().GetId()) {
		return status.Errorf(codes.InvalidArgument, "spec.organization.id %q is not a valid Tenant id", spec.GetOrganization().GetId())
	}
	if len(spec.GetModelRefs()) == 0 {
		return status.Error(codes.InvalidArgument, "spec.model_refs must include at least one model")
	}
	for i, ref := range spec.GetModelRefs() {
		if ref == nil || ref.GetName() == "" {
			return status.Errorf(codes.InvalidArgument, "spec.model_refs[%d].name is required", i)
		}
		if !validModelID(ref.GetName()) {
			return status.Errorf(codes.InvalidArgument, "spec.model_refs[%d].name %q is not a valid RFC 1123 DNS label", i, ref.GetName())
		}
		if ns := ref.GetNamespace(); ns != "" && !validModelID(ns) {
			return status.Errorf(codes.InvalidArgument, "spec.model_refs[%d].namespace %q is not a valid RFC 1123 DNS label", i, ns)
		}
		limits := ref.GetTokenRateLimits()
		if ref.GetUnlimited() {
			if len(limits) > 0 {
				return status.Errorf(codes.InvalidArgument, "spec.model_refs[%d]: token_rate_limits must not be set when unlimited is true", i)
			}
			continue
		}
		if len(limits) == 0 {
			return status.Errorf(codes.InvalidArgument, "spec.model_refs[%d]: token_rate_limits is required unless unlimited is true", i)
		}
		for j, limit := range limits {
			if limit == nil || limit.GetLimit() < 1 || limit.GetLimit() > 1_000_000_000 {
				return status.Errorf(codes.InvalidArgument, "spec.model_refs[%d].token_rate_limits[%d].limit must be between 1 and 1000000000", i, j)
			}
			seconds, ok := tokenWindowSeconds(limit.GetWindow())
			if !ok || seconds > 366*24*3600 {
				return status.Errorf(codes.InvalidArgument, "spec.model_refs[%d].token_rate_limits[%d].window %q is invalid: use 1 to 9999 followed by s, m or h, at most 8784h", i, j, limit.GetWindow())
			}
		}
	}
	return nil
}

func validateSubscriptionMetadata(meta *publicv1.Metadata) error {
	for key := range meta.GetLabels() {
		if strings.Contains(key, "/") {
			return status.Errorf(codes.InvalidArgument, "metadata.labels key %q is not allowed: only keys without a prefix", key)
		}
	}
	for key := range meta.GetAnnotations() {
		if strings.Contains(key, "/") && key != subscriptionDisplayName && key != subscriptionDescription {
			return status.Errorf(codes.InvalidArgument, "metadata.annotations key %q is not allowed: only keys without a prefix, %s and %s", key, subscriptionDisplayName, subscriptionDescription)
		}
	}
	for key, value := range meta.GetLabels() {
		if errs := validation.IsQualifiedName(key); len(errs) > 0 {
			return status.Errorf(codes.InvalidArgument, "metadata.labels key %q: %s", key, strings.Join(errs, "; "))
		}
		if errs := validation.IsValidLabelValue(value); len(errs) > 0 {
			return status.Errorf(codes.InvalidArgument, "metadata.labels[%s] value %q: %s", key, value, strings.Join(errs, "; "))
		}
	}
	if errs := apierrors.ValidateAnnotations(meta.GetAnnotations(), field.NewPath("metadata", "annotations")); len(errs) > 0 {
		return status.Errorf(codes.InvalidArgument, "%v", errs.ToAggregate())
	}
	return nil
}

func qualifiedSubscriptionGroups(spec *publicv1.SubscriptionSpec) ([]string, error) {
	org := spec.GetOrganization().GetId()
	groups := spec.GetOwner().GetGroups()
	out := make([]string, 0, len(groups))
	seen := make(map[string]struct{}, len(groups))
	for i, group := range groups {
		name := strings.TrimSpace(group)
		if prefix, rest, qualified := strings.Cut(name, "/"); qualified {
			if prefix != org {
				return nil, status.Errorf(codes.InvalidArgument, "spec.owner.groups[%d] %q belongs to another Tenant; send the bare group name", i, group)
			}
			name = strings.TrimSpace(rest)
		}
		if name == "" || strings.Contains(name, "/") {
			return nil, status.Errorf(codes.InvalidArgument, "spec.owner.groups[%d] %q is not a valid group name", i, group)
		}
		qualified := org + "/" + name
		if _, ok := seen[qualified]; ok {
			continue
		}
		seen[qualified] = struct{}{}
		out = append(out, qualified)
	}
	return out, nil
}

func subscriptionUpdatePaths(mask *fieldmaskpb.FieldMask, object *publicv1.Subscription) (subscriptionPaths, error) {
	if mask == nil || len(mask.GetPaths()) == 0 {
		return subscriptionPaths{labels: len(object.GetMetadata().GetLabels()) > 0, annotations: len(object.GetMetadata().GetAnnotations()) > 0, wholeSpec: object.GetSpec() != nil}, nil
	}
	var paths subscriptionPaths
	for _, raw := range mask.GetPaths() {
		p := strings.TrimPrefix(raw, "object.")
		switch {
		case p == "id":
		case p == "metadata":
			paths.labels, paths.annotations = true, true
		case p == "metadata.labels" || strings.HasPrefix(p, "metadata.labels."):
			paths.labels = true
		case p == "metadata.annotations" || strings.HasPrefix(p, "metadata.annotations."):
			paths.annotations = true
		case p == "spec":
			paths.wholeSpec = true
		case p == "spec.owner":
			paths.owner = true
		case strings.HasPrefix(p, "spec.owner."):
			return paths, status.Errorf(codes.InvalidArgument, "update_mask path %q is not supported; use spec.owner", raw)
		case p == "spec.model_refs":
			paths.modelRefs = true
		case strings.HasPrefix(p, "spec.model_refs."):
			return paths, status.Errorf(codes.InvalidArgument, "update_mask path %q is not supported; use spec.model_refs", raw)
		case p == "spec.priority":
			paths.priority = true
		case p == "spec.organization":
			paths.organization = true
		case strings.HasPrefix(p, "spec.organization."):
			return paths, status.Errorf(codes.InvalidArgument, "update_mask path %q is not supported; use spec.organization", raw)
		case p == "spec.token_metadata":
			paths.tokenMetadata = true
		case strings.HasPrefix(p, "spec.token_metadata."):
			return paths, status.Errorf(codes.InvalidArgument, "update_mask path %q is not supported; use spec.token_metadata", raw)
		case p == "spec.inference_priority":
			paths.inferencePriority = true
		case p == "spec.guardrails":
			paths.guardrails = true
		case strings.HasPrefix(p, "spec.guardrails."):
			return paths, status.Errorf(codes.InvalidArgument, "update_mask path %q is not supported; use spec.guardrails", raw)
		default:
			return paths, status.Errorf(codes.InvalidArgument, "unsupported update_mask path %q", raw)
		}
	}
	return paths, nil
}

type subscriptionPaths struct{ labels, annotations, wholeSpec, owner, modelRefs, priority, organization, tokenMetadata, inferencePriority, guardrails bool }

func (p subscriptionPaths) anySpec() bool {
	return p.wholeSpec || p.owner || p.modelRefs || p.priority || p.organization || p.tokenMetadata || p.inferencePriority || p.guardrails
}

func mergeSubscription(stored, incoming *publicv1.Subscription, paths subscriptionPaths) *publicv1.Subscription {
	out := proto.Clone(stored).(*publicv1.Subscription)
	if paths.labels || paths.annotations {
		if out.GetMetadata() == nil {
			out.Metadata = &publicv1.Metadata{}
		}
		if paths.labels {
			out.GetMetadata().Labels = cloneStringMap(incoming.GetMetadata().GetLabels())
		}
		if paths.annotations {
			out.GetMetadata().Annotations = cloneStringMap(incoming.GetMetadata().GetAnnotations())
		}
	}
	if !paths.anySpec() {
		return out
	}
	if paths.wholeSpec {
		if incoming.GetSpec() == nil {
			out.Spec = &publicv1.SubscriptionSpec{}
		} else {
			out.Spec = proto.Clone(incoming.GetSpec()).(*publicv1.SubscriptionSpec)
		}
		return out
	}
	if out.GetSpec() == nil {
		out.Spec = &publicv1.SubscriptionSpec{}
	}
	in := incoming.GetSpec()
	if paths.owner {
		if in.GetOwner() == nil {
			out.GetSpec().Owner = nil
		} else {
			out.GetSpec().Owner = proto.Clone(in.GetOwner()).(*publicv1.SubscriptionOwner)
		}
	}
	if paths.modelRefs {
		out.GetSpec().ModelRefs = cloneModelRefs(in.GetModelRefs())
	}
	if paths.priority {
		out.GetSpec().Priority = in.GetPriority()
	}
	if paths.organization {
		if in.GetOrganization() == nil {
			out.GetSpec().Organization = nil
		} else {
			out.GetSpec().Organization = proto.Clone(in.GetOrganization()).(*publicv1.SubscriptionOrganization)
		}
	}
	if paths.tokenMetadata {
		if in.GetTokenMetadata() == nil {
			out.GetSpec().TokenMetadata = nil
		} else {
			out.GetSpec().TokenMetadata = proto.Clone(in.GetTokenMetadata()).(*publicv1.TokenMetadata)
		}
	}
	if paths.inferencePriority {
		out.GetSpec().InferencePriority = in.InferencePriority
	}
	if paths.guardrails {
		out.GetSpec().Guardrails = cloneGuardrails(in.GetGuardrails())
	}
	return out
}

func cloneModelRefs(in []*publicv1.SubscriptionModelRef) []*publicv1.SubscriptionModelRef {
	out := make([]*publicv1.SubscriptionModelRef, 0, len(in))
	for _, item := range in {
		if item != nil {
			out = append(out, proto.Clone(item).(*publicv1.SubscriptionModelRef))
		}
	}
	return out
}
func cloneGuardrails(in []*publicv1.GuardrailAttachment) []*publicv1.GuardrailAttachment {
	out := make([]*publicv1.GuardrailAttachment, 0, len(in))
	for _, item := range in {
		if item != nil {
			out = append(out, proto.Clone(item).(*publicv1.GuardrailAttachment))
		}
	}
	return out
}

func subscriptionGitError(op, id string, err error) error {
	if status.Code(err) != codes.Unknown {
		return err
	}
	switch {
	case errors.Is(err, gitops.ErrNotConfigured):
		return status.Error(codes.FailedPrecondition, "AI Grid subscription catalog is not configured; set AI_GRID_GIT_URL")
	case errors.Is(err, gitops.ErrNotReady):
		return status.Error(codes.Unavailable, "AI Grid subscription catalog is not ready")
	case errors.Is(err, gitops.ErrConflict):
		return status.Errorf(codes.Aborted, "Subscription %q changed concurrently; reload and retry", id)
	default:
		return status.Errorf(codes.Unavailable, "AI Grid subscription catalog %s failed for Subscription %q: %v", op, id, err)
	}
}

func (s *SubscriptionsServer) gitList(ctx context.Context, reports map[string]ObjectReports) ([]*publicv1.Subscription, error) {
	head, err := s.writer.Head(ctx)
	if err != nil {
		return nil, subscriptionGitError("list", "", err)
	}
	names, err := head.List(subscriptionsGitDir)
	if err != nil && !errors.Is(err, gitops.ErrNotFound) {
		return nil, subscriptionGitError("list", "", err)
	}
	ids := make([]string, 0, len(names))
	for _, name := range names {
		if id, ok := strings.CutSuffix(name, ".yaml"); ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	catalog := subscriptionModelCatalog{head: head, namespace: s.namespace, cache: map[string]*unstructured.Unstructured{}}
	items := make([]*publicv1.Subscription, 0, len(ids))
	for _, id := range ids {
		doc, err := readSubscriptionDoc(head, id)
		if err != nil {
			return nil, err
		}
		catalog.join(doc.object)
		items = append(items, withSubscriptionStatus(doc.object, reports["subscriptions"]))
	}
	return items, nil
}

func (s *SubscriptionsServer) gitGet(ctx context.Context, id string, reports map[string]ObjectReports) (*publicv1.SubscriptionsGetResponse, error) {
	head, err := s.writer.Head(ctx)
	if err != nil {
		return nil, subscriptionGitError("get", id, err)
	}
	doc, err := readSubscriptionDoc(head, id)
	if err != nil {
		return nil, err
	}
	(&subscriptionModelCatalog{head: head, namespace: s.namespace, cache: map[string]*unstructured.Unstructured{}}).join(doc.object)
	response := &publicv1.SubscriptionsGetResponse{
		Object:             withSubscriptionStatus(doc.object, reports["subscriptions"]),
		ModelRegistrations: subscriptionModelRegistrations(doc.object, s.namespace, reports["models"]),
	}
	if doc.policy != nil {
		response.AuthPolicies = []*publicv1.AuthPolicy{withAuthPolicyStatus(doc.policy, reports["policies"])}
	}
	return response, nil
}

func (s *SubscriptionsServer) gitCreate(ctx context.Context, object *publicv1.Subscription) (*publicv1.SubscriptionsCreateResponse, error) {
	id := subscriptionID(object)
	head, err := s.writer.Head(ctx)
	if err != nil {
		return nil, subscriptionGitError("create", id, err)
	}
	if _, err := head.Read(subscriptionGitPath(id)); err == nil {
		return nil, status.Errorf(codes.AlreadyExists, "Subscription %q already exists", id)
	} else if !errors.Is(err, gitops.ErrNotFound) {
		return nil, subscriptionGitError("create", id, err)
	}
	read, err := s.ensureSubscriptionModelsExist(head, "create", object)
	if err != nil {
		return nil, err
	}
	doc, err := s.commitSubscription(ctx, head, "create", object, 1, read)
	if err != nil {
		return nil, err
	}
	reports := s.statusReports(ctx)
	(&subscriptionModelCatalog{head: head, namespace: s.namespace, cache: map[string]*unstructured.Unstructured{}}).join(doc.object)
	return &publicv1.SubscriptionsCreateResponse{Object: withSubscriptionStatus(doc.object, reports["subscriptions"]), AuthPolicy: withAuthPolicyStatus(doc.policy, reports["policies"])}, nil
}

func (s *SubscriptionsServer) gitUpdate(ctx context.Context, request *publicv1.SubscriptionsUpdateRequest, paths subscriptionPaths) (*publicv1.SubscriptionsUpdateResponse, error) {
	id := subscriptionID(request.GetObject())
	head, err := s.writer.Head(ctx)
	if err != nil {
		return nil, subscriptionGitError("update", id, err)
	}
	current, err := readSubscriptionDoc(head, id)
	if err != nil {
		return nil, err
	}
	if request.GetLock() && request.GetObject().GetMetadata().GetVersion() != current.object.GetMetadata().GetVersion() {
		return nil, status.Errorf(codes.Aborted, "Subscription %q has changed; fetch the latest version and retry", id)
	}
	candidate := mergeSubscription(current.object, request.GetObject(), paths)
	candidate.Status = nil
	if err := validateSubscriptionMetadata(candidate.GetMetadata()); err != nil {
		return nil, err
	}
	if paths.anySpec() {
		if err := validateSubscriptionSpec(candidate.GetSpec()); err != nil {
			return nil, err
		}
		if err := validateSubscriptionOrganizationImmutable(current.object.GetSpec(), candidate.GetSpec()); err != nil {
			return nil, err
		}
	}
	if err := s.validateSubscriptionTenant(ctx, candidate.GetSpec().GetOrganization().GetId()); err != nil {
		return nil, err
	}
	if !paths.labels && !paths.annotations && !paths.anySpec() {
		return &publicv1.SubscriptionsUpdateResponse{Object: withSubscriptionStatus(current.object, s.statusReports(ctx)["subscriptions"])}, nil
	}
	read := []string{}
	if paths.anySpec() {
		read, err = s.ensureSubscriptionModelsExist(head, "update", candidate)
		if err != nil {
			return nil, err
		}
	}
	doc, err := s.commitSubscription(ctx, head, "update", candidate, current.object.GetMetadata().GetVersion()+1, read)
	if err != nil {
		return nil, err
	}
	(&subscriptionModelCatalog{head: head, namespace: s.namespace, cache: map[string]*unstructured.Unstructured{}}).join(doc.object)
	return &publicv1.SubscriptionsUpdateResponse{Object: withSubscriptionStatus(doc.object, s.statusReports(ctx)["subscriptions"])}, nil
}

func validateSubscriptionOrganizationImmutable(current, next *publicv1.SubscriptionSpec) error {
	oldID, newID := current.GetOrganization().GetId(), next.GetOrganization().GetId()
	if oldID != "" && oldID != newID {
		return status.Error(codes.InvalidArgument, "spec.organization.id is immutable")
	}
	return nil
}

func (s *SubscriptionsServer) gitDelete(ctx context.Context, id string, version int32) error {
	head, err := s.writer.Head(ctx)
	if err != nil {
		return subscriptionGitError("delete", id, err)
	}
	doc, err := readSubscriptionDoc(head, id)
	if err != nil {
		return err
	}
	if version != 0 && version != doc.object.GetMetadata().GetVersion() {
		return status.Errorf(codes.Aborted, "Subscription %q has changed; fetch the latest version and retry", id)
	}
	if _, err := s.writer.Commit(ctx, head.Revision(), gitops.Change{Delete: []string{subscriptionGitPath(id)}, Message: "subscriptions: delete " + id}); err != nil {
		if errors.Is(err, gitops.ErrConflict) {
			return err
		}
		return subscriptionGitError("delete", id, err)
	}
	return nil
}

func (s *SubscriptionsServer) ensureSubscriptionModelsExist(head gitops.Snapshot, operation string, object *publicv1.Subscription) ([]string, error) {
	var read []string
	for _, ref := range object.GetSpec().GetModelRefs() {
		if ref.GetNamespace() != "" && ref.GetNamespace() != s.namespace {
			continue
		}
		file := modelDeploymentPath(ref.GetName())
		if _, err := head.Read(file); errors.Is(err, gitops.ErrNotFound) {
			return nil, status.Errorf(codes.FailedPrecondition, "spec.model_refs: Model %q does not exist", ref.GetName())
		} else if err != nil {
			return nil, subscriptionGitError(operation, subscriptionID(object), err)
		}
		read = append(read, file)
	}
	return read, nil
}

func (s *SubscriptionsServer) commitSubscription(ctx context.Context, head gitops.Snapshot, operation string, object *publicv1.Subscription, version int32, read []string) (*subscriptionDoc, error) {
	data, err := renderSubscriptionDoc(object, s.namespace, version)
	if err != nil {
		return nil, err
	}
	if _, err := s.writer.Commit(ctx, head.Revision(), gitops.Change{Put: map[string][]byte{subscriptionGitPath(subscriptionID(object)): data}, Read: read, Message: "subscriptions: " + operation + " " + subscriptionID(object)}); err != nil {
		if errors.Is(err, gitops.ErrConflict) {
			return nil, err
		}
		return nil, subscriptionGitError(operation, subscriptionID(object), err)
	}
	doc, err := parseSubscriptionDoc(subscriptionID(object), data)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%s Subscription %q: %v", operation, subscriptionID(object), err)
	}
	return doc, nil
}

func renderSubscriptionDoc(object *publicv1.Subscription, namespace string, version int32) ([]byte, error) {
	qualified := proto.Clone(object).(*publicv1.Subscription)
	groups, err := qualifiedSubscriptionGroups(qualified.GetSpec())
	if err != nil {
		return nil, err
	}
	qualified.GetSpec().GetOwner().Groups = groups
	for _, ref := range qualified.GetSpec().GetModelRefs() {
		if ref.GetNamespace() == "" {
			ref.Namespace = namespace
		}
	}
	subscription := subscriptionToUnstructured(qualified, namespace)
	annotations := subscription.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[subscriptionVersionAnnotation] = strconv.FormatInt(int64(version), 10)
	// Do not set retain-api-keys-on-delete: pruning a removed subscription must
	// revoke grants made only by it within the five-minute access-removal target.
	subscription.SetAnnotations(annotations)
	data, err := marshalSubscriptionYAML(subscription, authPolicyToUnstructured(qualified, namespace))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "render Subscription %q: %v", subscriptionID(object), err)
	}
	return data, nil
}

func readSubscriptionDoc(head gitops.Snapshot, id string) (*subscriptionDoc, error) {
	data, err := head.Read(subscriptionGitPath(id))
	if errors.Is(err, gitops.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "Subscription %q not found", id)
	}
	if err != nil {
		return nil, subscriptionGitError("get", id, err)
	}
	doc, err := parseSubscriptionDoc(id, data)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read %s: %v", subscriptionGitPath(id), err)
	}
	return doc, nil
}

func parseSubscriptionDoc(id string, data []byte) (*subscriptionDoc, error) {
	objects, err := unmarshalSubscriptionYAML(data)
	if err != nil {
		return nil, err
	}
	doc := &subscriptionDoc{}
	for _, object := range objects {
		switch object.GetKind() {
		case maasSubscriptionKind:
			doc.object = subscriptionFromUnstructured(object)
		case maasAuthPolicyKind:
			doc.policy = authPolicyFromUnstructured(object)
		}
	}
	if doc.object == nil || doc.object.GetId() != id {
		return nil, fmt.Errorf("no MaaSSubscription %q in the file", id)
	}
	meta := doc.object.GetMetadata()
	version, err := strconv.ParseInt(meta.GetAnnotations()[subscriptionVersionAnnotation], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("annotation %s: %w", subscriptionVersionAnnotation, err)
	}
	meta.Version = int32(version)
	delete(meta.Annotations, subscriptionVersionAnnotation)
	delete(meta.Annotations, apiKeyRetainOnDeleteAnnotation)
	if len(meta.Annotations) == 0 {
		meta.Annotations = nil
	}
	prefix := doc.object.GetSpec().GetOrganization().GetId() + "/"
	for i, group := range doc.object.GetSpec().GetOwner().GetGroups() {
		doc.object.GetSpec().GetOwner().Groups[i] = strings.TrimPrefix(group, prefix)
	}
	return doc, nil
}

type subscriptionModelCatalog struct {
	head      gitops.Snapshot
	namespace string
	cache     map[string]*unstructured.Unstructured
}

func (c *subscriptionModelCatalog) join(sub *publicv1.Subscription) {
	for _, ref := range sub.GetSpec().GetModelRefs() {
		ref.ModelId, ref.DisplayName, ref.Description = "", "", ""
		if !validModelID(ref.GetName()) {
			continue
		}
		entry, loaded := c.cache[ref.GetName()]
		if !loaded {
			file := modelCatalogPath(ref.GetName())
			data, err := c.head.Read(file)
			if err == nil {
				objects, unmarshalErr := unmarshalSubscriptionYAML(data)
				if unmarshalErr == nil {
					for _, obj := range objects {
						if obj.GetKind() == maasModelRefKind {
							entry = obj
							break
						}
					}
				}
			}
			c.cache[ref.GetName()] = entry
		}
		namespace := ref.GetNamespace()
		if namespace == "" {
			namespace = c.namespace
		}
		if entry == nil || entry.GetName() != ref.GetName() || entry.GetNamespace() != namespace {
			continue
		}
		ref.ModelId = entry.GetName()
		ref.DisplayName = entry.GetAnnotations()[subscriptionDisplayName]
		ref.Description = entry.GetAnnotations()[subscriptionDescription]
	}
}

func subscriptionToUnstructured(sub *publicv1.Subscription, namespace string) *unstructured.Unstructured {
	id := subscriptionID(sub)
	meta := map[string]any{"name": id, "namespace": namespace}
	if len(sub.GetMetadata().GetLabels()) > 0 {
		meta["labels"] = stringMapToInterfaceMap(sub.GetMetadata().GetLabels())
	}
	if len(sub.GetMetadata().GetAnnotations()) > 0 {
		meta["annotations"] = stringMapToInterfaceMap(sub.GetMetadata().GetAnnotations())
	}
	spec := subscriptionSpecToMap(sub.GetSpec())
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "maas.opendatahub.io/v1alpha1", "kind": maasSubscriptionKind, "metadata": meta, "spec": spec}}
}

func subscriptionSpecToMap(spec *publicv1.SubscriptionSpec) map[string]any {
	out := map[string]any{"owner": map[string]any{"groups": []any{}, "users": []any{}}, "modelRefs": []any{}, "priority": int64(0)}
	if spec == nil {
		return out
	}
	groups := make([]any, 0, len(spec.GetOwner().GetGroups()))
	for _, group := range spec.GetOwner().GetGroups() {
		groups = append(groups, map[string]any{"name": group})
	}
	users := make([]any, 0, len(spec.GetOwner().GetUsers()))
	for _, user := range spec.GetOwner().GetUsers() {
		users = append(users, user)
	}
	out["owner"] = map[string]any{"groups": groups, "users": users}
	refs := make([]any, 0, len(spec.GetModelRefs()))
	for _, ref := range spec.GetModelRefs() {
		if ref == nil {
			continue
		}
		entry := map[string]any{"name": ref.GetName(), "namespace": ref.GetNamespace()}
		if ref.GetUnlimited() {
			entry["unlimited"] = true
		} else if len(ref.GetTokenRateLimits()) > 0 {
			limits := make([]any, 0, len(ref.GetTokenRateLimits()))
			for _, lim := range ref.GetTokenRateLimits() {
				if lim != nil {
					limits = append(limits, map[string]any{"limit": lim.GetLimit(), "window": lim.GetWindow()})
				}
			}
			entry["tokenRateLimits"] = limits
		}
		if rate := ref.GetBillingRate(); rate != nil && rate.GetPerToken() != "" {
			entry["billingRate"] = map[string]any{"perToken": rate.GetPerToken()}
		}
		if guardrails := guardrailsToMap(ref.GetGuardrails()); len(guardrails) > 0 {
			entry["guardrails"] = guardrails
		}
		refs = append(refs, entry)
	}
	out["modelRefs"], out["priority"] = refs, int64(spec.GetPriority())
	if org := spec.GetOrganization(); org != nil {
		out["organization"] = map[string]any{"id": org.GetId()}
	}
	if tm := spec.GetTokenMetadata(); tm != nil {
		metadata := map[string]any{}
		if tm.GetOrganizationId() != "" {
			metadata["organizationId"] = tm.GetOrganizationId()
		}
		if tm.GetCostCenter() != "" {
			metadata["costCenter"] = tm.GetCostCenter()
		}
		if len(tm.GetLabels()) > 0 {
			metadata["labels"] = stringMapToInterfaceMap(tm.GetLabels())
		}
		if len(metadata) > 0 {
			out["tokenMetadata"] = metadata
		}
	}
	if spec.InferencePriority != nil {
		out["inferencePriority"] = int64(spec.GetInferencePriority())
	}
	if guardrails := guardrailsToMap(spec.GetGuardrails()); len(guardrails) > 0 {
		out["guardrails"] = guardrails
	}
	return out
}

func authPolicyToUnstructured(sub *publicv1.Subscription, namespace string) *unstructured.Unstructured {
	id := subscriptionID(sub)
	labels := map[string]any{"app.kubernetes.io/managed-by": subscriptionManagedByValue, subscriptionLabelKey: id}
	meta := map[string]any{"name": subscriptionPolicyName(id), "namespace": namespace, "labels": labels}
	annotations := map[string]any{}
	for _, key := range []string{subscriptionDisplayName, subscriptionDescription} {
		if value, ok := sub.GetMetadata().GetAnnotations()[key]; ok {
			annotations[key] = value
		}
	}
	if len(annotations) > 0 {
		meta["annotations"] = annotations
	}
	modelRefs := make([]any, 0, len(sub.GetSpec().GetModelRefs()))
	for _, ref := range sub.GetSpec().GetModelRefs() {
		modelRefs = append(modelRefs, map[string]any{"name": ref.GetName(), "namespace": ref.GetNamespace()})
	}
	groups, _ := qualifiedSubscriptionGroups(sub.GetSpec())
	groupObjects := make([]any, 0, len(groups))
	for _, group := range groups {
		groupObjects = append(groupObjects, map[string]any{"name": group})
	}
	users := make([]any, 0, len(sub.GetSpec().GetOwner().GetUsers()))
	for _, user := range sub.GetSpec().GetOwner().GetUsers() {
		users = append(users, user)
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "maas.opendatahub.io/v1alpha1", "kind": maasAuthPolicyKind, "metadata": meta,
		"spec": map[string]any{"modelRefs": modelRefs, "subjects": map[string]any{"groups": groupObjects, "users": users}},
	}}
}

func guardrailsToMap(items []*publicv1.GuardrailAttachment) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		if item == nil || item.GetRefName() == "" {
			continue
		}
		entry := map[string]any{"ref": map[string]any{"name": item.GetRefName()}}
		if len(item.GetChecks()) > 0 {
			checks := make([]any, 0, len(item.GetChecks()))
			for _, check := range item.GetChecks() {
				checks = append(checks, check)
			}
			entry["checks"] = checks
		}
		out = append(out, entry)
	}
	return out
}

func subscriptionFromUnstructured(item *unstructured.Unstructured) *publicv1.Subscription {
	name := item.GetName()
	meta := &publicv1.Metadata{Name: name, Labels: item.GetLabels(), Annotations: item.GetAnnotations(), Version: unstructuredInt32(item.Object, "metadata", "annotations", subscriptionVersionAnnotation)}
	if created := item.GetCreationTimestamp(); !created.IsZero() {
		meta.CreationTimestamp = timestamppb.New(created.Time)
	}
	if deleted := item.GetDeletionTimestamp(); deleted != nil && !deleted.IsZero() {
		meta.DeletionTimestamp = timestamppb.New(deleted.Time)
	}
	spec := &publicv1.SubscriptionSpec{Priority: unstructuredInt32(item.Object, "spec", "priority"), Owner: ownerFromSubscriptionMap(item.Object)}
	if refs, found, _ := unstructured.NestedSlice(item.Object, "spec", "modelRefs"); found {
		for _, raw := range refs {
			if ref, ok := raw.(map[string]any); ok {
				spec.ModelRefs = append(spec.ModelRefs, modelRefFromSubscriptionMap(ref))
			}
		}
	}
	if id, found, _ := unstructured.NestedString(item.Object, "spec", "organization", "id"); found {
		spec.Organization = &publicv1.SubscriptionOrganization{Id: id}
	}
	if tm, found, _ := unstructured.NestedMap(item.Object, "spec", "tokenMetadata"); found {
		spec.TokenMetadata = tokenMetadataFromMap(tm)
	}
	if value, found, _ := unstructured.NestedFieldNoCopy(item.Object, "spec", "inferencePriority"); found {
		if v, ok := int32FromInt64(unstructuredInt64(map[string]any{"value": value}, "value")); ok {
			spec.InferencePriority = &v
		}
	}
	if guardrails, found, _ := unstructured.NestedSlice(item.Object, "spec", "guardrails"); found {
		spec.Guardrails = guardrailsFromMap(guardrails)
	}
	statusValue := &publicv1.SubscriptionStatus{}
	if phase, found, _ := unstructured.NestedString(item.Object, "status", "phase"); found {
		statusValue.Phase = phase
	}
	if conditions, found, _ := unstructured.NestedSlice(item.Object, "status", "conditions"); found {
		statusValue.Conditions = subscriptionConditionsFromMap(conditions)
	}
	if refs, found, _ := unstructured.NestedSlice(item.Object, "status", "modelRefStatuses"); found {
		for _, raw := range refs {
			if m, ok := raw.(map[string]any); ok {
				statusValue.ModelRefStatuses = append(statusValue.ModelRefStatuses, resourceRefStatusFromMap(m))
			}
		}
	}
	if refs, found, _ := unstructured.NestedSlice(item.Object, "status", "tokenRateLimitStatuses"); found {
		for _, raw := range refs {
			if m, ok := raw.(map[string]any); ok {
				nested, _, _ := unstructured.NestedMap(m, "ref")
				statusValue.TokenRateLimitStatuses = append(statusValue.TokenRateLimitStatuses, &publicv1.TokenRateLimitStatus{Ref: resourceRefStatusFromMap(nested), Model: subscriptionString(m, "model")})
			}
		}
	}
	return &publicv1.Subscription{Id: name, Metadata: meta, Spec: spec, Status: statusValue}
}

func authPolicyFromUnstructured(item *unstructured.Unstructured) *publicv1.AuthPolicy {
	name := item.GetName()
	meta := &publicv1.Metadata{Name: name, Labels: item.GetLabels(), Annotations: item.GetAnnotations()}
	if created := item.GetCreationTimestamp(); !created.IsZero() {
		meta.CreationTimestamp = timestamppb.New(created.Time)
	}
	policy := &publicv1.AuthPolicy{Id: name, Metadata: meta}
	groups, _, _ := unstructured.NestedSlice(item.Object, "spec", "subjects", "groups")
	for _, raw := range groups {
		if m, ok := raw.(map[string]any); ok && subscriptionString(m, "name") != "" {
			policy.SubjectGroups = append(policy.SubjectGroups, subscriptionString(m, "name"))
		}
	}
	users, _, _ := unstructured.NestedStringSlice(item.Object, "spec", "subjects", "users")
	policy.SubjectUsers = append(policy.SubjectUsers, users...)
	refs, _, _ := unstructured.NestedSlice(item.Object, "spec", "modelRefs")
	for _, raw := range refs {
		if m, ok := raw.(map[string]any); ok {
			policy.ModelRefs = append(policy.ModelRefs, &publicv1.AuthPolicyModelRef{Name: subscriptionString(m, "name"), Namespace: subscriptionString(m, "namespace")})
		}
	}
	if phase, found, _ := unstructured.NestedString(item.Object, "status", "phase"); found {
		policy.Phase = phase
	}
	if conditions, found, _ := unstructured.NestedSlice(item.Object, "status", "conditions"); found {
		policy.Conditions = subscriptionConditionsFromMap(conditions)
	}
	return policy
}

func ownerFromSubscriptionMap(object map[string]any) *publicv1.SubscriptionOwner {
	owner := &publicv1.SubscriptionOwner{}
	groups, _, _ := unstructured.NestedSlice(object, "spec", "owner", "groups")
	for _, raw := range groups {
		switch value := raw.(type) {
		case map[string]any:
			if name := subscriptionString(value, "name"); name != "" {
				owner.Groups = append(owner.Groups, name)
			}
		case string:
			if value != "" {
				owner.Groups = append(owner.Groups, value)
			}
		}
	}
	users, _, _ := unstructured.NestedStringSlice(object, "spec", "owner", "users")
	owner.Users = append(owner.Users, users...)
	return owner
}

func modelRefFromSubscriptionMap(raw map[string]any) *publicv1.SubscriptionModelRef {
	ref := &publicv1.SubscriptionModelRef{Name: subscriptionString(raw, "name"), Namespace: subscriptionString(raw, "namespace"), Unlimited: unstructuredBool(raw, "unlimited")}
	limits, _, _ := unstructured.NestedSlice(raw, "tokenRateLimits")
	for _, item := range limits {
		if m, ok := item.(map[string]any); ok {
			ref.TokenRateLimits = append(ref.TokenRateLimits, &publicv1.TokenRateLimit{Limit: unstructuredInt64(m, "limit"), Window: subscriptionString(m, "window")})
		}
	}
	if rate, found, _ := unstructured.NestedString(raw, "billingRate", "perToken"); found && rate != "" {
		ref.BillingRate = &publicv1.BillingRate{PerToken: rate}
	}
	if guards, found, _ := unstructured.NestedSlice(raw, "guardrails"); found {
		ref.Guardrails = guardrailsFromMap(guards)
	}
	return ref
}

func tokenMetadataFromMap(raw map[string]any) *publicv1.TokenMetadata {
	labels, _, _ := unstructured.NestedStringMap(raw, "labels")
	metadata := &publicv1.TokenMetadata{OrganizationId: subscriptionString(raw, "organizationId"), CostCenter: subscriptionString(raw, "costCenter"), Labels: labels}
	if metadata.GetOrganizationId() == "" && metadata.GetCostCenter() == "" && len(metadata.GetLabels()) == 0 {
		return nil
	}
	return metadata
}

func guardrailsFromMap(raw []any) []*publicv1.GuardrailAttachment {
	out := make([]*publicv1.GuardrailAttachment, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _, _ := unstructured.NestedString(m, "ref", "name")
		checks, _, _ := unstructured.NestedStringSlice(m, "checks")
		if name != "" {
			out = append(out, &publicv1.GuardrailAttachment{RefName: name, Checks: checks})
		}
	}
	return out
}

func subscriptionConditionsFromMap(raw []any) []*publicv1.SubscriptionCondition {
	out := make([]*publicv1.SubscriptionCondition, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		c := &publicv1.SubscriptionCondition{Type: subscriptionString(m, "type"), Reason: optionalSubscriptionString(m, "reason"), Message: optionalSubscriptionString(m, "message"), ObservedGeneration: unstructuredInt64(m, "observedGeneration")}
		if state := subscriptionString(m, "status"); state == "True" {
			c.Status = publicv1.ConditionStatus_CONDITION_STATUS_TRUE
		} else if state == "False" {
			c.Status = publicv1.ConditionStatus_CONDITION_STATUS_FALSE
		}
		if timestamp := subscriptionString(m, "lastTransitionTime"); timestamp != "" {
			if parsed, err := time.Parse(time.RFC3339, timestamp); err == nil {
				c.LastTransitionTime = timestamppb.New(parsed)
			}
		}
		out = append(out, c)
	}
	return out
}

func resourceRefStatusFromMap(m map[string]any) *publicv1.ResourceRefStatus {
	return &publicv1.ResourceRefStatus{Name: subscriptionString(m, "name"), Namespace: subscriptionString(m, "namespace"), Ready: unstructuredBool(m, "ready"), Reason: subscriptionString(m, "reason"), Message: subscriptionString(m, "message")}
}

func stringMapToInterfaceMap(in map[string]string) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
func subscriptionString(m map[string]any, key string) string {
	value, _ := m[key].(string)
	return value
}
func optionalSubscriptionString(m map[string]any, key string) *string {
	value := subscriptionString(m, key)
	if value == "" {
		return nil
	}
	return &value
}
func unstructuredBool(m map[string]any, key string) bool { value, _ := m[key].(bool); return value }
func unstructuredInt64(m map[string]any, key string) int64 {
	switch value := m[key].(type) {
	case int:
		return int64(value)
	case int32:
		return int64(value)
	case int64:
		return value
	case float32:
		return int64(value)
	case float64:
		return int64(value)
	default:
		return 0
	}
}
func unstructuredInt32(object map[string]any, path ...string) int32 {
	value, found, _ := unstructured.NestedFieldNoCopy(object, path...)
	if !found {
		return 0
	}
	converted, ok := int32FromInt64(unstructuredInt64(map[string]any{"value": value}, "value"))
	if !ok {
		return 0
	}
	return converted
}

func marshalSubscriptionYAML(objects ...*unstructured.Unstructured) ([]byte, error) {
	var out bytes.Buffer
	for i, object := range objects {
		data, err := yaml.Marshal(object.Object)
		if err != nil {
			return nil, fmt.Errorf("marshal %s: %w", object.GetKind(), err)
		}
		if i > 0 {
			out.WriteString("---\n")
		}
		out.Write(data)
	}
	return out.Bytes(), nil
}

func unmarshalSubscriptionYAML(data []byte) ([]*unstructured.Unstructured, error) {
	var objects []*unstructured.Unstructured
	for _, part := range bytes.Split(data, []byte("\n---\n")) {
		raw := map[string]any{}
		if err := yaml.Unmarshal(part, &raw); err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			objects = append(objects, &unstructured.Unstructured{Object: raw})
		}
	}
	return objects, nil
}
