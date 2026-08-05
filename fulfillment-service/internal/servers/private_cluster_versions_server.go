/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strconv"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
)

const maxSemVerLength = 256
const maxImageLength = 512

type PrivateClusterVersionsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.ClusterVersionsServer = (*PrivateClusterVersionsServer)(nil)

type PrivateClusterVersionsServer struct {
	privatev1.UnimplementedClusterVersionsServer

	logger  *slog.Logger
	generic *GenericServer[*privatev1.ClusterVersion]
}

func NewPrivateClusterVersionsServer() *PrivateClusterVersionsServerBuilder {
	return &PrivateClusterVersionsServerBuilder{}
}

func (b *PrivateClusterVersionsServerBuilder) SetLogger(value *slog.Logger) *PrivateClusterVersionsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateClusterVersionsServerBuilder) SetNotifier(value events.Notifier) *PrivateClusterVersionsServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateClusterVersionsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateClusterVersionsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateClusterVersionsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateClusterVersionsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateClusterVersionsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateClusterVersionsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateClusterVersionsServerBuilder) Build() (*PrivateClusterVersionsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.tenancyLogic == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}

	generic, err := NewGenericServer[*privatev1.ClusterVersion]().
		SetLogger(b.logger).
		SetService(privatev1.ClusterVersions_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return nil, err
	}

	return &PrivateClusterVersionsServer{
		logger:  b.logger,
		generic: generic,
	}, nil
}

func (s *PrivateClusterVersionsServer) List(ctx context.Context,
	request *privatev1.ClusterVersionsListRequest) (*privatev1.ClusterVersionsListResponse, error) {
	var response *privatev1.ClusterVersionsListResponse
	err := s.generic.List(ctx, request, &response)
	return response, err
}

func (s *PrivateClusterVersionsServer) Get(ctx context.Context,
	request *privatev1.ClusterVersionsGetRequest) (*privatev1.ClusterVersionsGetResponse, error) {
	var response *privatev1.ClusterVersionsGetResponse
	err := s.generic.Get(ctx, request, &response)
	return response, err
}

func (s *PrivateClusterVersionsServer) Create(ctx context.Context,
	request *privatev1.ClusterVersionsCreateRequest) (*privatev1.ClusterVersionsCreateResponse, error) {
	cv, err := validateClusterVersionCreateRequest(request)
	if err != nil {
		return nil, err
	}

	applyClusterVersionDefaults(cv)

	// Clear caller-provided ID so the DAO always generates a UUID:
	cv.SetId("")

	// Safe to clear existing defaults before Create: the gRPC interceptor wraps the entire
	// RPC in a single transaction, so the clear and Insert commit together — other connections
	// never see a state with zero defaults.
	if cv.GetSpec().GetIsDefault() {
		if err := validateIsDefaultEligibility(cv); err != nil {
			return nil, err
		}
		if err := s.unsetPreviousDefaultClusterVersion(ctx, cv.GetId()); err != nil {
			return nil, err
		}
	}

	var response *privatev1.ClusterVersionsCreateResponse
	err = s.generic.Create(ctx, request, &response)
	return response, err
}

func (s *PrivateClusterVersionsServer) Update(ctx context.Context,
	request *privatev1.ClusterVersionsUpdateRequest) (*privatev1.ClusterVersionsUpdateResponse, error) {
	id := request.GetObject().GetId()
	if id == "" {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
	}

	var getResponse *privatev1.ClusterVersionsGetResponse
	err := s.generic.Get(ctx, &privatev1.ClusterVersionsGetRequest{Id: id}, &getResponse)
	if err != nil {
		return nil, err
	}

	existing := getResponse.GetObject()

	if updateIncludesField(request.GetUpdateMask(), "spec") && request.GetObject().GetSpec() == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "object spec is mandatory when updating spec fields")
	}

	if err := validateClusterVersionImmutability(existing, request); err != nil {
		return nil, err
	}

	// Reject explicit is_default=true on ineligible versions (OBSOLETE or disabled).
	// The auto-clear path in applyClusterVersionStateEffects handles the transition case
	// where is_default is not in the mask.
	if updateIncludesField(request.GetUpdateMask(), "spec.is_default") &&
		request.GetObject().GetSpec().GetIsDefault() {
		if !resolveEnabled(existing, request) {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
				"cannot set 'is_default' on a disabled cluster version")
		}
		if resolveState(existing, request) == privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
				"cannot set 'is_default' on an obsolete cluster version")
		}
	}

	if err := applyClusterVersionStateEffects(existing, request); err != nil {
		return nil, err
	}

	if resolveIsDefault(existing, request) {
		if err := s.unsetPreviousDefaultClusterVersion(ctx, id); err != nil {
			return nil, err
		}
	}

	var response *privatev1.ClusterVersionsUpdateResponse
	err = s.generic.Update(ctx, request, &response)
	if err != nil {
		// Concurrent default-swap: remap AlreadyExists to FailedPrecondition.
		if request.GetObject().GetSpec().GetIsDefault() {
			if st, ok := grpcstatus.FromError(err); ok && st.Code() == grpccodes.AlreadyExists {
				return nil, grpcstatus.Errorf(grpccodes.FailedPrecondition,
					"concurrent default ClusterVersion change detected, please retry")
			}
		}
	}
	return response, err
}

func (s *PrivateClusterVersionsServer) Delete(ctx context.Context,
	request *privatev1.ClusterVersionsDeleteRequest) (*privatev1.ClusterVersionsDeleteResponse, error) {
	if request.GetId() == "" {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
	}

	var response *privatev1.ClusterVersionsDeleteResponse
	err := s.generic.Delete(ctx, request, &response)
	if err != nil {
		return nil, err
	}

	return response, nil
}

func (s *PrivateClusterVersionsServer) Signal(ctx context.Context,
	request *privatev1.ClusterVersionsSignalRequest) (*privatev1.ClusterVersionsSignalResponse, error) {
	var response *privatev1.ClusterVersionsSignalResponse
	err := s.generic.Signal(ctx, request, &response)
	return response, err
}

// unsetPreviousDefaultClusterVersion fetches all non-deleted ClusterVersions with spec.is_default == true,
// except the one with the given excludeID, and clears the is_default flag on each. Used during
// default-swap to ensure only one default exists at a time even if the invariant was previously
// violated.
//
// Concurrent default-swap requests are safe: the unique partial index
// cluster_versions_single_default (migration 74) prevents two concurrent transactions from both
// committing is_default=true. The losing transaction receives AlreadyExists from the DAO;
// callers that need a more specific error code should remap it (see Update).
func (s *PrivateClusterVersionsServer) unsetPreviousDefaultClusterVersion(ctx context.Context, excludeID string) error {
	filter := "this.spec.is_default == true && !has(this.metadata.deletion_timestamp)"
	if excludeID != "" {
		filter += fmt.Sprintf(" && this.id != %s", strconv.Quote(excludeID))
	}
	// At most one item: the unique partial index enforces a single default.
	items, err := s.listAllMatching(ctx, filter)
	if err != nil {
		return err
	}
	for _, cv := range items {
		s.logger.DebugContext(ctx, "unsetting previous default ClusterVersion",
			"old_default_id", cv.GetId(),
		)
		cv.GetSpec().SetIsDefault(false)
		_, err = s.generic.dao.Update().SetObject(cv).Do(ctx)
		if err != nil {
			if _, ok := errors.AsType[*dao.ErrNotFound](err); ok {
				s.logger.DebugContext(ctx, "Skipping deleted ClusterVersion during default clear",
					slog.String("cluster_version_id", cv.GetId()),
				)
				continue
			}
			if _, ok := errors.AsType[*dao.ErrDeadlock](err); ok {
				return grpcstatus.Errorf(grpccodes.Aborted, "concurrent modification detected, please retry")
			}
			s.logger.ErrorContext(ctx, "Failed to clear default on ClusterVersion",
				slog.String("cluster_version_id", cv.GetId()),
				slog.Any("error", err),
			)
			return grpcstatus.Errorf(grpccodes.Internal, "failed to clear existing default ClusterVersions")
		}
	}
	return nil
}

func validateClusterVersionCreateRequest(request *privatev1.ClusterVersionsCreateRequest) (*privatev1.ClusterVersion, error) {
	cv := request.GetObject()
	if cv == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
	}

	spec := cv.GetSpec()
	if spec == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "object spec is mandatory")
	}

	if spec.GetVersion() == "" {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.version' is required")
	}
	if spec.GetImage() == "" {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.image' is required")
	}
	if len(spec.GetImage()) > maxImageLength {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.image' exceeds maximum length of %d characters", maxImageLength)
	}

	// Validate semver format:
	if err := validateSemVer(spec.GetVersion()); err != nil {
		return nil, err
	}

	return cv, nil
}

// resolveState returns the state that will be effective after the update. UNSPECIFIED is
// treated as absent because non-optional proto3 enums have no presence — the zero value is
// indistinguishable from an unset field.
func resolveState(existing *privatev1.ClusterVersion,
	request *privatev1.ClusterVersionsUpdateRequest) privatev1.ClusterVersionState {
	if updateIncludesField(request.GetUpdateMask(), "spec.state") {
		if s := request.GetObject().GetSpec().GetState(); s != privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_UNSPECIFIED {
			return s
		}
	}
	return existing.GetSpec().GetState()
}

// resolveIsDefault returns the is_default value that will be effective after the update.
// If spec.is_default is in the mask, reads from the request; otherwise reads from existing.
func resolveIsDefault(existing *privatev1.ClusterVersion,
	request *privatev1.ClusterVersionsUpdateRequest) bool {
	if updateIncludesField(request.GetUpdateMask(), "spec.is_default") {
		return request.GetObject().GetSpec().GetIsDefault()
	}
	return existing.GetSpec().GetIsDefault()
}

// resolveEnabled returns the enabled value that will be effective after the update. If
// spec.enabled is in the mask, reads from the request (defaulting to true when cleared).
// Otherwise reads from existing (defaulting to true when unset).
func resolveEnabled(existing *privatev1.ClusterVersion,
	request *privatev1.ClusterVersionsUpdateRequest) bool {
	if updateIncludesField(request.GetUpdateMask(), "spec.enabled") {
		if request.GetObject().GetSpec().HasEnabled() {
			return request.GetObject().GetSpec().GetEnabled()
		}
		return true
	}
	if existing.GetSpec().HasEnabled() {
		return existing.GetSpec().GetEnabled()
	}
	return true
}

// validateIsDefaultEligibility rejects is_default on disabled or obsolete versions.
func validateIsDefaultEligibility(cv *privatev1.ClusterVersion) error {
	if !cv.GetSpec().GetEnabled() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cannot set 'is_default' on a disabled cluster version")
	}
	if cv.GetSpec().GetState() == privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cannot set 'is_default' on an obsolete cluster version")
	}
	return nil
}

// applyClusterVersionDefaults defaults enabled to true and state to ACTIVE when unset,
// auto-generates metadata.name from spec.version if not provided, clears any caller-supplied
// deprecation timestamps (OUTPUT_ONLY), and initializes the deprecation/obsolescence timestamp
// matching the initial state.
func applyClusterVersionDefaults(cv *privatev1.ClusterVersion) {
	spec := cv.GetSpec()

	// Default enabled to true if not explicitly set:
	if !spec.HasEnabled() {
		spec.SetEnabled(true)
	}

	// Default is_default to false if not explicitly set:
	if !spec.HasIsDefault() {
		spec.SetIsDefault(false)
	}

	// Default state to ACTIVE if unspecified:
	if spec.GetState() == privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_UNSPECIFIED {
		spec.SetState(privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE)
	}

	// Auto-generate metadata.name from spec.version if not provided:
	if cv.GetMetadata() == nil || cv.GetMetadata().GetName() == "" {
		if cv.GetMetadata() == nil {
			cv.SetMetadata(&privatev1.Metadata{})
		}
		cv.GetMetadata().SetName(generateNameFromVersion(spec.GetVersion()))
	}

	// Set initial timestamps based on state (OUTPUT_ONLY — clear any caller-supplied values first):
	spec.ClearDeprecation()
	switch spec.GetState() {
	case privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_DEPRECATED:
		spec.SetDeprecation(&privatev1.ClusterVersionDeprecation{})
		spec.GetDeprecation().SetDeprecationTimestamp(timestamppb.Now())
	case privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE:
		spec.SetDeprecation(&privatev1.ClusterVersionDeprecation{})
		spec.GetDeprecation().SetObsolescenceTimestamp(timestamppb.Now())
	}
}

// applyClusterVersionStateEffects applies side effects of state and enabled changes:
// deprecation timestamp management and is_default auto-clear on ineligible transitions.
// When a state change occurs, the target state's deprecation timestamp is set and existing
// timestamps are retained as historical record on backward transitions. When no state change
// occurs, any client-supplied deprecation timestamps are replaced with the existing values
// (OUTPUT_ONLY enforcement).
func applyClusterVersionStateEffects(existing *privatev1.ClusterVersion,
	request *privatev1.ClusterVersionsUpdateRequest) error {
	spec := request.GetObject().GetSpec()
	if spec == nil {
		return nil
	}
	oldState := existing.GetSpec().GetState()
	newState := resolveState(existing, request)
	mask := request.GetUpdateMask()

	// Normalize the request's state to the resolved value so the generic server's mask merge
	// persists the correct state (UNSPECIFIED is replaced with the existing or intended value).
	if updateIncludesField(mask, "spec.state") {
		spec.SetState(newState)
	}

	// Same-state update — enforce OUTPUT_ONLY contract on deprecation timestamps by
	// unconditionally restoring existing values, regardless of what the client sent:
	if oldState == newState {
		if existingDep := existing.GetSpec().GetDeprecation(); existingDep != nil {
			spec.SetDeprecation(proto.Clone(existingDep).(*privatev1.ClusterVersionDeprecation))
		} else {
			spec.ClearDeprecation()
		}
	} else {
		// Start from existing deprecation to retain historical timestamps on backward transitions:
		existingDep := existing.GetSpec().GetDeprecation()
		var dep *privatev1.ClusterVersionDeprecation
		if existingDep != nil {
			dep = proto.Clone(existingDep).(*privatev1.ClusterVersionDeprecation)
		} else {
			dep = &privatev1.ClusterVersionDeprecation{}
		}
		spec.SetDeprecation(dep)

		// Set timestamp only for the target state:
		switch newState {
		case privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_DEPRECATED:
			dep.SetDeprecationTimestamp(timestamppb.Now())
		case privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE:
			dep.SetObsolescenceTimestamp(timestamppb.Now())
		case privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE:
			// Retain existing timestamps as historical record.
		default:
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"unsupported cluster version state: %v", newState)
		}

		if mask != nil {
			mask.Paths = append(mask.Paths, "spec.deprecation")
		}
	}

	// Auto-clear is_default if becoming ineligible (OBSOLETE or disabled):
	if resolveIsDefault(existing, request) {
		newEnabled := resolveEnabled(existing, request)
		if newState == privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE || !newEnabled {
			spec.SetIsDefault(false)
			if mask != nil {
				mask.Paths = append(mask.Paths, "spec.is_default")
			}
		}
	}
	return nil
}

func validateClusterVersionImmutability(existing *privatev1.ClusterVersion,
	request *privatev1.ClusterVersionsUpdateRequest) error {
	mask := request.GetUpdateMask()
	update := request.GetObject()
	if updateIncludesField(mask, "spec.version") &&
		update.GetSpec().GetVersion() != existing.GetSpec().GetVersion() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.version' is immutable and cannot be changed from '%s' to '%s'",
			existing.GetSpec().GetVersion(), update.GetSpec().GetVersion())
	}
	if updateIncludesField(mask, "spec.image") &&
		update.GetSpec().GetImage() != existing.GetSpec().GetImage() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.image' is immutable and cannot be changed from '%s' to '%s'",
			existing.GetSpec().GetImage(), update.GetSpec().GetImage())
	}
	if updateIncludesField(mask, "metadata.name") &&
		update.GetMetadata().GetName() != existing.GetMetadata().GetName() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'name' is immutable and cannot be changed from '%s' to '%s'",
			existing.GetMetadata().GetName(), update.GetMetadata().GetName())
	}
	return nil
}

// validateSemVer validates that the version string is a valid Semantic Version 2.0.0 string.
func validateSemVer(version string) error {
	if len(version) > maxSemVerLength {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.version' exceeds maximum length of %d characters", maxSemVerLength)
	}
	_, err := semver.StrictNewVersion(version)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.version' is not a valid SemVer 2.0.0 string: %v", err)
	}
	return nil
}

// generateNameFromVersion converts a semver string to a DNS-1123 compliant name.
// Lowercases, replaces non-[a-z0-9-] with dashes, trims leading/trailing dashes,
// and appends a 4-character FNV-32a hex suffix derived from the original version string.
// If the sanitized base exceeds 58 characters, it is truncated to keep the total within 63.
func generateNameFromVersion(version string) string {
	result := strings.ToLower(version)

	var sanitized strings.Builder
	for _, r := range result {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sanitized.WriteRune(r)
		} else {
			sanitized.WriteRune('-')
		}
	}
	result = strings.Trim(sanitized.String(), "-")

	if len(result) > 58 {
		result = strings.TrimRight(result[:58], "-")
	}

	h := fnv.New32a()
	h.Write([]byte(version))
	return fmt.Sprintf("%s-%04x", result, h.Sum32()%0x10000)
}

// listAllMatching returns all ClusterVersions matching the given CEL filter,
// paginating through all results. The returned objects are in listing order (not sorted).
func (s *PrivateClusterVersionsServer) listAllMatching(ctx context.Context, filter string) ([]*privatev1.ClusterVersion, error) {
	var result []*privatev1.ClusterVersion
	for offset := int32(0); ; offset += 1000 {
		listResponse, err := s.generic.dao.List().
			SetFilter(filter).
			SetLimit(1000).
			SetOffset(offset).
			Do(ctx)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to list ClusterVersions",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to list ClusterVersions")
		}
		result = append(result, listResponse.GetItems()...)
		if len(listResponse.GetItems()) < 1000 {
			break
		}
	}
	return result, nil
}
