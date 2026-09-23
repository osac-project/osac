/*
Copyright (c) 2026 Red Hat, Inc.

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
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const maxClusterAddOnOperators = 32

const maxAddOnOperatorRelationshipEdges = 1024

func addOnOperatorFieldError(field, message string) error {
	status, err := grpcstatus.New(grpccodes.InvalidArgument, message).WithDetails(&errdetails.BadRequest{
		FieldViolations: []*errdetails.BadRequest_FieldViolation{{
			Field:       field,
			Description: message,
		}},
	})
	if err != nil {
		return grpcstatus.Errorf(grpccodes.Internal, "failed to attach add-on operator validation details: %v", err)
	}
	return status.Err()
}

type addOnOperatorResourceResolver struct {
	dao *dao.GenericDAO[*privatev1.AddOnOperator]
	get referenceGetFunc[*privatev1.AddOnOperator]
}

type addOnOperatorGraphResolver struct {
	ctx      context.Context
	resource *addOnOperatorResourceResolver
}

type addOnOperatorNotFoundError struct {
	identifier string
}

func (e *addOnOperatorNotFoundError) Error() string {
	return fmt.Sprintf("add-on operator %q not found", e.identifier)
}

func (e *addOnOperatorNotFoundError) IsNotFound() bool {
	return true
}

func (r *addOnOperatorGraphResolver) resolve(
	ref resourceReference,
	ownerMetadata *privatev1.Metadata,
	field string,
) (*privatev1.AddOnOperator, error) {
	return r.resource.resolve(r.ctx, ref, ownerMetadata, field)
}

func newScopedAddOnOperatorResourceResolver(
	operatorDAO *dao.GenericDAO[*privatev1.AddOnOperator],
) *addOnOperatorResourceResolver {
	return &addOnOperatorResourceResolver{
		dao: operatorDAO,
		get: getReferenceResource[*privatev1.AddOnOperator],
	}
}

func newPublishedScopedAddOnOperatorResourceResolver(
	operatorDAO *dao.GenericDAO[*privatev1.AddOnOperator],
) *addOnOperatorResourceResolver {
	return &addOnOperatorResourceResolver{
		dao: operatorDAO,
		get: getPublishedAddOnOperator,
	}
}

func getPublishedAddOnOperator(
	ctx context.Context,
	operatorDAO *dao.GenericDAO[*privatev1.AddOnOperator],
	id string,
) (*privatev1.AddOnOperator, error) {
	operator, err := getReferenceResource(ctx, operatorDAO, id)
	if err != nil {
		return nil, err
	}
	if !operator.GetPublished() || operator.GetMetadata().GetDeletionTimestamp() != nil {
		return nil, &addOnOperatorNotFoundError{identifier: id}
	}
	return operator, nil
}

func (r *addOnOperatorResourceResolver) resolve(
	ctx context.Context,
	ref resourceReference,
	ownerMetadata *privatev1.Metadata,
	field string,
) (*privatev1.AddOnOperator, error) {
	if ref == nil || (ref.GetId() == "" && ref.GetName() == "") {
		return nil, addOnOperatorFieldError(field, fmt.Sprintf("add-on operator reference in %s must specify id or name", field))
	}
	defaultAddOnOperatorReferenceScope(ref)
	ownerMetadata = addOnOperatorOwnerMetadata(ownerMetadata, ref)
	resolved, err := resolveAndCanonicalizeReferenceWithGet(
		ctx,
		r.dao,
		ownerMetadata,
		ref,
		"add-on operator",
		grpccodes.InvalidArgument,
		r.get,
	)
	if err != nil {
		if status, ok := grpcstatus.FromError(err); ok && (status.Code() == grpccodes.InvalidArgument || status.Code() == grpccodes.NotFound) {
			return nil, addOnOperatorFieldError(field, status.Message())
		}
		return nil, err
	}
	if resolved == nil || resolved.GetId() == "" {
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to resolve add-on operator referenced by %s", field)
	}
	return resolved, nil
}

// defaultAddOnOperatorReferenceScope selects the shared tenant before resolving a name-only
// add-on operator reference. Add-on operators are shared catalog resources, so the generic
// resolver must see this scope selector during lookup rather than after canonicalization.
func defaultAddOnOperatorReferenceScope(ref resourceReference) {
	fullReference, ok := ref.(fullResourceReference)
	if ok && fullReference.GetId() == "" && fullReference.GetProject() == "" && !fullReference.GetShared() {
		fullReference.SetShared(true)
	}
}

func addOnOperatorOwnerMetadata(ownerMetadata *privatev1.Metadata, ref resourceReference) *privatev1.Metadata {
	fullReference, ok := ref.(fullResourceReference)
	if ok && fullReference.GetShared() && fullReference.GetProject() == "" && ownerMetadata.GetProject() != "" {
		ownerMetadata = cloneMessage(ownerMetadata)
		ownerMetadata.SetProject("")
	}
	return ownerMetadata
}

func (s *PrivateClustersServer) resolveClusterVersionForAddOnOperators(
	ctx context.Context,
	cluster *privatev1.Cluster,
) (*privatev1.ClusterVersion, error) {
	versionReference := cluster.GetSpec().GetVersion()
	if versionReference == nil || versionReference.GetId() == "" {
		return nil, grpcstatus.Error(grpccodes.Internal, "cluster version was not canonicalized before add-on operator validation")
	}
	response, err := s.clusterVersionsDao.Get().SetId(versionReference.GetId()).Do(ctx)
	if err != nil {
		if _, ok := grpcstatus.FromError(err); ok {
			return nil, err
		}
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to retrieve cluster version '%s'", versionReference.GetId())
	}
	clusterVersion := response.GetObject()
	if clusterVersion == nil {
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to retrieve cluster version '%s'", versionReference.GetId())
	}
	if err := validateResolvedClusterVersion(clusterVersion, versionReference.GetId(), ""); err != nil {
		return nil, err
	}
	return clusterVersion, nil
}

func (s *PrivateClustersServer) validateAndExpandAddOnOperators(ctx context.Context, cluster *privatev1.Cluster) error {
	requested := cluster.GetSpec().GetAddOnOperators()
	if len(requested) == 0 {
		return nil
	}

	clusterVersion, err := s.resolveClusterVersionForAddOnOperators(ctx, cluster)
	if err != nil {
		return err
	}
	clusterVersionValue, err := semver.NewVersion(clusterVersion.GetSpec().GetVersion())
	if err != nil {
		return grpcstatus.Errorf(grpccodes.Internal,
			"cluster version '%s' has an invalid version value: %v",
			clusterVersion.GetMetadata().GetName(), err)
	}

	selected, selectedFields, selectedOrder, err := s.resolveAndExpandAddOnOperators(ctx, cluster, requested)
	if err != nil {
		return err
	}
	if err := validateSelectedAddOnOperators(selected, selectedFields, selectedOrder, clusterVersion, clusterVersionValue); err != nil {
		return err
	}

	resolvedReferences := make([]*privatev1.AddOnOperatorReference, 0, len(selectedOrder))
	for _, operatorID := range selectedOrder {
		operator := selected[operatorID]
		resolvedReferences = append(resolvedReferences, privatev1.AddOnOperatorReference_builder{
			Id:   operator.GetId(),
			Name: operator.GetMetadata().GetName(),
		}.Build())
	}
	cluster.GetSpec().SetAddOnOperators(resolvedReferences)
	return nil
}

func (s *PrivateClustersServer) resolveAndExpandAddOnOperators(
	ctx context.Context,
	cluster *privatev1.Cluster,
	requested []*privatev1.AddOnOperatorReference,
) (map[string]*privatev1.AddOnOperator, map[string]string, []string, error) {
	resolver := &addOnOperatorGraphResolver{
		ctx:      ctx,
		resource: s.addOnOperators,
	}
	// selected contains the unique operators already expanded, keyed by ID.
	selected := make(map[string]*privatev1.AddOnOperator)
	// selectedFields preserves the originating request field for validation errors.
	selectedFields := make(map[string]string)
	// selectedOrder is dependency-first because operators are appended after their dependencies.
	selectedOrder := make([]string, 0, len(requested))
	// visiting and stack track the active DFS path used to report dependency cycles.
	visiting := make(map[string]int)
	stack := make([]string, 0, len(requested))
	// relationshipEdges bounds dependency traversal work even when metadata is malformed.
	relationshipEdges := 0

	var visit func(*privatev1.AddOnOperator, string) error
	visit = func(operator *privatev1.AddOnOperator, field string) error {
		operatorID := operator.GetId()
		if start, ok := visiting[operatorID]; ok {
			cycle := append(append([]string{}, stack[start:]...), operatorID)
			return addOnOperatorFieldError(field,
				fmt.Sprintf("add-on operator dependency cycle detected: %s", strings.Join(cycle, " -> ")))
		}
		if _, ok := selected[operatorID]; ok {
			return nil
		}
		// The current node is not counted yet, so reject before admitting node 33.
		if len(selected)+len(visiting) >= maxClusterAddOnOperators {
			return addOnOperatorFieldError("spec.add_on_operators",
				fmt.Sprintf("resolved add-on operator set exceeds maximum of %d operators", maxClusterAddOnOperators))
		}

		visiting[operatorID] = len(stack)
		stack = append(stack, operatorID)
		defer func() {
			delete(visiting, operatorID)
			stack = stack[:len(stack)-1]
		}()

		// Post-order DFS selects dependencies before their parent operator.
		for _, dependency := range operator.GetDependencies() {
			relationshipEdges++
			if relationshipEdges > maxAddOnOperatorRelationshipEdges {
				return addOnOperatorFieldError("spec.add_on_operators",
					fmt.Sprintf("add-on operator dependency graph exceeds %d relationships", maxAddOnOperatorRelationshipEdges))
			}
			resolved, err := resolver.resolve(dependency, operator.GetMetadata(), field)
			if err != nil {
				return err
			}
			if err := visit(resolved, field); err != nil {
				return err
			}
		}

		selected[operatorID] = operator
		selectedFields[operatorID] = field
		selectedOrder = append(selectedOrder, operatorID)
		return nil
	}

	for index, reference := range requested {
		field := fmt.Sprintf("spec.add_on_operators[%d]", index)
		operator, err := resolver.resolve(reference, cluster.GetMetadata(), field)
		if err != nil {
			return nil, nil, nil, err
		}
		if err := visit(operator, field); err != nil {
			return nil, nil, nil, err
		}
	}
	return selected, selectedFields, selectedOrder, nil
}

func validateSelectedAddOnOperators(
	selected map[string]*privatev1.AddOnOperator,
	selectedFields map[string]string,
	selectedOrder []string,
	clusterVersion *privatev1.ClusterVersion,
	clusterVersionValue *semver.Version,
) error {
	relationshipEdges := 0
	for _, operatorID := range selectedOrder {
		operator := selected[operatorID]
		field := selectedFields[operatorID]
		for _, exclusion := range operator.GetExclusions() {
			relationshipEdges++
			if relationshipEdges > maxAddOnOperatorRelationshipEdges {
				return addOnOperatorFieldError("spec.add_on_operators",
					fmt.Sprintf("add-on operator exclusion graph exceeds %d relationships", maxAddOnOperatorRelationshipEdges))
			}
			if excludedID, excludedName := selectedExclusion(operator, exclusion, selected); excludedID != "" {
				return addOnOperatorFieldError(field,
					fmt.Sprintf("add-on operators %q and %q are mutually exclusive", operator.GetMetadata().GetName(), selected[excludedID].GetMetadata().GetName()))
			} else if excludedName != "" {
				return addOnOperatorFieldError(field,
					fmt.Sprintf("add-on operators %q and %q are mutually exclusive", operator.GetMetadata().GetName(), excludedName))
			}
		}

		if err := validateAddOnOperatorVersion(operator, field, clusterVersion, clusterVersionValue); err != nil {
			return err
		}
	}
	return nil
}

func validateAddOnOperatorVersion(
	operator *privatev1.AddOnOperator,
	field string,
	clusterVersion *privatev1.ClusterVersion,
	clusterVersionValue *semver.Version,
) error {
	minimum := operator.GetMinOcpVersion()
	if minimum != "" {
		minimumVersion, err := semver.NewVersion(minimum)
		if err != nil {
			return grpcstatus.Errorf(grpccodes.Internal,
				"add-on operator '%s' has an invalid minimum OCP version '%s': %v",
				operator.GetMetadata().GetName(), minimum, err)
		}
		if clusterVersionValue.LessThan(minimumVersion) {
			return addOnOperatorFieldError(field,
				fmt.Sprintf("add-on operator %q requires OCP version >= %s, but cluster version is %s",
					operator.GetMetadata().GetName(), minimum, clusterVersion.GetSpec().GetVersion()))
		}
	}

	maximum := operator.GetMaxOcpVersion()
	if maximum != "" {
		maximumVersion, err := semver.NewVersion(maximum)
		if err != nil {
			return grpcstatus.Errorf(grpccodes.Internal,
				"add-on operator '%s' has an invalid maximum OCP version '%s': %v",
				operator.GetMetadata().GetName(), maximum, err)
		}
		if clusterVersionValue.GreaterThan(maximumVersion) {
			return addOnOperatorFieldError(field,
				fmt.Sprintf("add-on operator %q requires OCP version <= %s, but cluster version is %s",
					operator.GetMetadata().GetName(), maximum, clusterVersion.GetSpec().GetVersion()))
		}
	}
	return nil
}

func selectedExclusion(operator *privatev1.AddOnOperator, exclusion *privatev1.AddOnOperatorLocalReference,
	selected map[string]*privatev1.AddOnOperator) (string, string) {
	if exclusion == nil {
		return "", ""
	}
	if exclusion.GetId() != "" {
		// Self-exclusions are rejected when operators are written; ignore them here for legacy records.
		if candidate, ok := selected[exclusion.GetId()]; ok && candidate.GetId() != operator.GetId() {
			return candidate.GetId(), ""
		}
		return "", ""
	}
	if exclusion.GetName() == "" {
		return "", ""
	}
	for id, candidate := range selected {
		if id == operator.GetId() || candidate.GetMetadata().GetName() != exclusion.GetName() {
			continue
		}
		if candidate.GetMetadata().GetTenant() == operator.GetMetadata().GetTenant() &&
			candidate.GetMetadata().GetProject() == operator.GetMetadata().GetProject() {
			return "", candidate.GetMetadata().GetName()
		}
	}
	return "", ""
}
