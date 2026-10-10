/*
Copyright (c) 2026 Red Hat Inc.

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
	"log/slog"
	"net/netip"
	"sort"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	osaclabels "github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateSubnetsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
	hubClientProvider HubClientProvider
}

var _ privatev1.SubnetsServer = (*PrivateSubnetsServer)(nil)

type PrivateSubnetsServer struct {
	privatev1.UnimplementedSubnetsServer

	logger              *slog.Logger
	tenancyLogic        auth.TenancyLogic
	generic             *GenericServer[*privatev1.Subnet]
	virtualNetworkDao   *dao.GenericDAO[*privatev1.VirtualNetwork]
	networkClassDao     *dao.GenericDAO[*privatev1.NetworkClass]
	hubClientProvider   HubClientProvider
	listVirtualMachines func(ctx context.Context, hubID, subnetID string) (int, error)
}

func NewPrivateSubnetsServer() *PrivateSubnetsServerBuilder {
	return &PrivateSubnetsServerBuilder{}
}

func (b *PrivateSubnetsServerBuilder) SetLogger(value *slog.Logger) *PrivateSubnetsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateSubnetsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateSubnetsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateSubnetsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateSubnetsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateSubnetsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateSubnetsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateSubnetsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateSubnetsServerBuilder {
	b.filterDesc = value
	return b
}

// SetHubClientProvider sets the provider used to inspect the VirtualNetwork hub for
// existing KubeVirt VirtualMachine objects. It is required to enforce the cudn_evpn
// multi-Subnet API guard in production.
func (b *PrivateSubnetsServerBuilder) SetHubClientProvider(value HubClientProvider) *PrivateSubnetsServerBuilder {
	b.hubClientProvider = value
	return b
}

func (b *PrivateSubnetsServerBuilder) Build() (result *PrivateSubnetsServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the VirtualNetwork DAO for parent validation:
	virtualNetworkDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}
	networkClassDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the generic server:
	generic, err := NewGenericServer[*privatev1.Subnet]().
		SetLogger(b.logger).
		SetService(privatev1.Subnets_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivateSubnetsServer{
		logger:            b.logger,
		tenancyLogic:      b.tenancyLogic,
		generic:           generic,
		virtualNetworkDao: virtualNetworkDao,
		networkClassDao:   networkClassDao,
		hubClientProvider: b.hubClientProvider,
	}
	if b.hubClientProvider != nil {
		result.listVirtualMachines = result.listHubVirtualMachines
	}
	return
}

func (s *PrivateSubnetsServer) List(ctx context.Context,
	request *privatev1.SubnetsListRequest) (response *privatev1.SubnetsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateSubnetsServer) Get(ctx context.Context,
	request *privatev1.SubnetsGetRequest) (response *privatev1.SubnetsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

// SUB-SVC-01: Create creates a new Subnet with validation
func (s *PrivateSubnetsServer) Create(ctx context.Context,
	request *privatev1.SubnetsCreateRequest) (response *privatev1.SubnetsCreateResponse, err error) {
	subnet := request.GetObject()

	// Validate before creating:
	err = s.validateSubnet(ctx, subnet, nil)
	if err != nil {
		return
	}
	if err = s.validateEvpnSubnetCreation(ctx, subnet); err != nil {
		return
	}

	// SUB-VAL-10: Set owner reference annotation automatically
	if subnet.GetMetadata() == nil {
		subnet.Metadata = &privatev1.Metadata{}
	}
	if subnet.GetMetadata().GetAnnotations() == nil {
		subnet.Metadata.Annotations = make(map[string]string)
	}
	subnet.Metadata.Annotations["osac.openshift.io/owner-reference"] = refKey(subnet.GetSpec().GetVirtualNetwork())

	err = s.generic.Create(ctx, request, &response)
	return
}

// validateEvpnSubnetCreation prevents adding a Subnet to a cudn_evpn
// VirtualNetwork after the oldest Subnet's namespace contains a VirtualMachine.
// The lookup is deliberately only applied to Create; updates and other managers
// retain their existing behavior.
func (s *PrivateSubnetsServer) validateEvpnSubnetCreation(ctx context.Context, subnet *privatev1.Subnet) error {
	virtualNetworkID := refKey(subnet.GetSpec().GetVirtualNetwork())
	virtualNetworkResponse, err := s.virtualNetworkDao.Get().SetId(virtualNetworkID).Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to query VirtualNetwork for sequential Subnet validation",
			slog.String("virtual_network_id", virtualNetworkID), slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate sequential Subnet provisioning")
	}
	virtualNetwork := virtualNetworkResponse.GetObject()
	networkClassID := refKey(virtualNetwork.GetSpec().GetNetworkClass())
	if networkClassID == "" {
		return grpcstatus.Errorf(grpccodes.Internal, "VirtualNetwork %q has no NetworkClass", virtualNetworkID)
	}
	networkClassResponse, err := s.networkClassDao.Get().SetId(networkClassID).Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to query NetworkClass for sequential Subnet validation",
			slog.String("network_class_id", networkClassID), slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate sequential Subnet provisioning")
	}
	if networkClassResponse.GetObject().GetK8SManager() != "cudn_evpn" {
		return nil
	}

	existingSubnets, err := s.listSubnetsForVirtualNetwork(ctx, virtualNetworkID)
	if err != nil {
		return err
	}
	if len(existingSubnets) == 0 {
		return nil
	}
	oldestSubnet := oldestSubnet(existingSubnets)
	hubID := virtualNetwork.GetStatus().GetHub()
	if hubID == "" {
		return grpcstatus.Errorf(grpccodes.Internal,
			"VirtualNetwork %q has no hub assigned; cannot check Subnet %q for VirtualMachines",
			virtualNetworkID, oldestSubnet.GetMetadata().GetName())
	}
	if s.listVirtualMachines == nil {
		return grpcstatus.Errorf(grpccodes.Internal,
			"hub client provider is unavailable; cannot check Subnet %q for VirtualMachines",
			oldestSubnet.GetMetadata().GetName())
	}
	vmCount, err := s.listVirtualMachines(ctx, hubID, oldestSubnet.GetId())
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to inspect oldest Subnet namespace for VirtualMachines",
			slog.String("virtual_network_id", virtualNetworkID),
			slog.String("subnet", oldestSubnet.GetMetadata().GetName()),
			slog.String("hub_id", hubID), slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal,
			"failed to check whether oldest Subnet %q contains VirtualMachines",
			oldestSubnet.GetMetadata().GetName())
	}
	if vmCount > 0 {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"Phase 1 cudn_evpn provisioning allows only one Subnet when the first Subnet %q contains VirtualMachines",
			oldestSubnet.GetMetadata().GetName())
	}
	return nil
}

func (s *PrivateSubnetsServer) listSubnetsForVirtualNetwork(ctx context.Context, virtualNetworkID string) ([]*privatev1.Subnet, error) {
	filter := fmt.Sprintf("this.spec.virtual_network.id == %[1]q || this.spec.virtual_network.name == %[1]q", virtualNetworkID)
	var items []*privatev1.Subnet
	var offset int32
	for {
		request := &privatev1.SubnetsListRequest{}
		request.SetFilter(filter)
		request.SetOffset(offset)
		var response *privatev1.SubnetsListResponse
		if err := s.generic.List(ctx, request, &response); err != nil {
			s.logger.ErrorContext(ctx, "Failed to list sibling Subnets for sequential provisioning",
				slog.String("virtual_network_id", virtualNetworkID), slog.Any("error", err))
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to validate sequential Subnet provisioning")
		}
		items = append(items, response.GetItems()...)
		if offset+response.GetSize() >= response.GetTotal() {
			break
		}
		offset += response.GetSize()
	}
	return items, nil
}

func oldestSubnet(subnets []*privatev1.Subnet) *privatev1.Subnet {
	sort.Slice(subnets, func(i, j int) bool {
		left, right := subnets[i].GetMetadata(), subnets[j].GetMetadata()
		leftTime, rightTime := left.GetCreationTimestamp(), right.GetCreationTimestamp()
		switch {
		case leftTime == nil && rightTime != nil:
			return true
		case leftTime != nil && rightTime == nil:
			return false
		case leftTime != nil && rightTime != nil && !leftTime.AsTime().Equal(rightTime.AsTime()):
			return leftTime.AsTime().Before(rightTime.AsTime())
		default:
			return left.GetName() < right.GetName()
		}
	})
	return subnets[0]
}

func (s *PrivateSubnetsServer) listHubVirtualMachines(ctx context.Context, hubID, subnetID string) (int, error) {
	info, err := s.hubClientProvider.GetClient(ctx, hubID)
	if err != nil {
		return 0, err
	}
	if info == nil || info.Config == nil {
		return 0, fmt.Errorf("hub %q returned no Kubernetes REST config", hubID)
	}
	dynamicClient, err := dynamic.NewForConfig(info.Config)
	if err != nil {
		return 0, fmt.Errorf("create hub Kubernetes client: %w", err)
	}
	// The hub Subnet CR uses a generated name, which is also the Namespace and
	// CUDN name. Resolve it by the fulfillment Subnet ID label first.
	subnetSelector := labels.Set{osaclabels.SubnetUuid: subnetID}.AsSelector().String()
	subnets, err := dynamicClient.Resource(schema.GroupVersionResource{
		Group: "osac.openshift.io", Version: "v1alpha1", Resource: "subnets",
	}).Namespace(info.Namespace).List(ctx, metav1.ListOptions{LabelSelector: subnetSelector})
	if err != nil {
		return 0, fmt.Errorf("list hub Subnet CR for Subnet ID %q: %w", subnetID, err)
	}
	if len(subnets.Items) != 1 {
		return 0, fmt.Errorf("expected one hub Subnet CR for Subnet ID %q, found %d", subnetID, len(subnets.Items))
	}
	namespace := subnets.Items[0].GetName()
	if namespace == "" {
		return 0, fmt.Errorf("hub Subnet CR for Subnet ID %q has no name", subnetID)
	}
	list, err := dynamicClient.Resource(schema.GroupVersionResource{
		Group: "kubevirt.io", Version: "v1", Resource: "virtualmachines",
	}).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, err
	}
	return len(list.Items), nil
}

// SUB-SVC-04: Update updates an existing Subnet with validation
func (s *PrivateSubnetsServer) Update(ctx context.Context,
	request *privatev1.SubnetsUpdateRequest) (response *privatev1.SubnetsUpdateResponse, err error) {
	if request.GetObject().GetId() == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}
	err = s.generic.UpdateWithValidation(ctx, request, &response, s.validateSubnet)
	return
}

func (s *PrivateSubnetsServer) Delete(ctx context.Context,
	request *privatev1.SubnetsDeleteRequest) (response *privatev1.SubnetsDeleteResponse, err error) {
	getRequest := &privatev1.SubnetsGetRequest{}
	getRequest.SetId(request.GetId())
	var getResponse *privatev1.SubnetsGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}
	if err = validateNotDefault(ctx, getResponse.GetObject().GetMetadata().GetLabels(), "subnet"); err != nil {
		return
	}
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateSubnetsServer) Signal(ctx context.Context,
	request *privatev1.SubnetsSignalRequest) (response *privatev1.SubnetsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// validateSubnet validates the Subnet object.
func (s *PrivateSubnetsServer) validateSubnet(ctx context.Context,
	newSubnet *privatev1.Subnet, existingSubnet *privatev1.Subnet) error {

	if newSubnet == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "subnet is mandatory")
	}

	if existingSubnet != nil {
		if err := validateDefaultLabelUpdate(
			existingSubnet.GetMetadata().GetLabels(),
			newSubnet.GetMetadata().GetLabels(),
			nil,
			"subnet",
		); err != nil {
			return err
		}
	}

	spec := newSubnet.GetSpec()
	if spec == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "subnet spec is mandatory")
	}

	if spec.GetIpv6Cidr() != "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.ipv6_cidr': IPv6 and dual-stack networking are not supported")
	}

	// SUB-VAL-11, SUB-VAL-14, SUB-VAL-15: Check immutable fields (only on Update).
	// Run after rejecting non-empty legacy IPv6 values so every attempted IPv6 or dual-stack
	// request receives the same clear unsupported-networking error.
	if err := validateImmutableFieldsSubnet(newSubnet, existingSubnet); err != nil {
		return err
	}

	if spec.GetIpv4Cidr() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.ipv4_cidr' is required and must be a canonical IPv4 CIDR")
	}
	if canonical, err := parseAndValidateCanonicalCIDR(spec.GetIpv4Cidr(), cidrIPv4); err != nil {
		return err
	} else {
		spec.SetIpv4Cidr(canonical)
	}

	// SUB-VAL-04, SUB-VAL-05, SUB-VAL-06, SUB-VAL-07, SUB-VAL-08: Validate parent VirtualNetwork
	// Only on Create (existingSubnet == nil) or if virtual_network differs (SUB-VAL-11 above prevents
	// VN changes on Update, so the second branch is effectively dead but kept for safety).
	if existingSubnet == nil || refKey(spec.GetVirtualNetwork()) != refKey(existingSubnet.GetSpec().GetVirtualNetwork()) {
		if err := s.validateVirtualNetworkReference(ctx, newSubnet); err != nil {
			return err
		}
	}

	return nil
}

// validateCIDRSubset validates that subnetCIDR is a proper subset of parentCIDR.
func validateCIDRSubset(subnetCIDR string, parentCIDR string, ipVersion string) error {
	// Parse subnet CIDR
	subnetPrefix, err := netip.ParsePrefix(subnetCIDR)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"invalid subnet %s CIDR format '%s': %v", ipVersion, subnetCIDR, err)
	}

	// Parse parent CIDR
	parentPrefix, err := netip.ParsePrefix(parentCIDR)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.Internal,
			"invalid parent %s CIDR format '%s': %v", ipVersion, parentCIDR, err)
	}

	// Check parent contains subnet network address
	if !parentPrefix.Contains(subnetPrefix.Addr()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"subnet %s CIDR '%s' is not within parent VirtualNetwork CIDR '%s'",
			ipVersion, subnetCIDR, parentCIDR)
	}

	// Check subnet mask is more specific than parent mask (prevents subnet larger than parent)
	subnetMaskSize := subnetPrefix.Bits()
	parentMaskSize := parentPrefix.Bits()
	if subnetMaskSize < parentMaskSize {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"subnet %s CIDR '%s' (/%d) is less specific than parent CIDR '%s' (/%d)",
			ipVersion, subnetCIDR, subnetMaskSize, parentCIDR, parentMaskSize)
	}

	return nil
}

// validateVirtualNetworkReference validates that the referenced VirtualNetwork exists, is in READY state,
// and has matching IP families.
func (s *PrivateSubnetsServer) validateVirtualNetworkReference(ctx context.Context,
	subnet *privatev1.Subnet) error {
	spec := subnet.GetSpec()

	virtualNetworkID := spec.GetVirtualNetwork()
	if virtualNetworkID == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.virtual_network' is required")
	}

	// SUB-VAL-04: Get parent VirtualNetwork by ID
	getResponse, err := s.virtualNetworkDao.Get().
		SetId(refKey(virtualNetworkID)).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"parent VirtualNetwork '%s' does not exist", virtualNetworkID)
		}
		return ConvertDAOErrorToGRPC(err, "get", refKey(virtualNetworkID))
	}

	virtualNetwork := getResponse.GetObject()
	subnetTenant, err := resolveObjectTenant(ctx, subnet.GetMetadata(), s.tenancyLogic)
	if err != nil {
		return err
	}
	if err := validateTenantMatch(subnetTenant, virtualNetwork, "VirtualNetwork", refKey(virtualNetworkID)); err != nil {
		return err
	}

	// SUB-VAL-05: Check parent VirtualNetwork is READY
	if virtualNetwork.GetStatus().GetState() != privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"parent VirtualNetwork '%s' is not in READY state (current state: %s)",
			virtualNetworkID, virtualNetwork.GetStatus().GetState().String())
	}

	parentSpec := virtualNetwork.GetSpec()

	// SUB-VAL-07: Validate IPv4 CIDR only if parent has IPv4
	if spec.GetIpv4Cidr() != "" {
		if parentSpec.GetIpv4Cidr() == "" {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"subnet has IPv4 CIDR but parent VirtualNetwork does not support IPv4")
		}
		// SUB-VAL-06: Validate IPv4 CIDR is subset of parent
		if err := validateCIDRSubset(spec.GetIpv4Cidr(), parentSpec.GetIpv4Cidr(), cidrIPv4); err != nil {
			return err
		}
	}

	// SUB-VAL-08: Validate IPv6 CIDR only if parent has IPv6
	if spec.GetIpv6Cidr() != "" {
		if parentSpec.GetIpv6Cidr() == "" {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"subnet has IPv6 CIDR but parent VirtualNetwork does not support IPv6")
		}
		// SUB-VAL-06: Validate IPv6 CIDR is subset of parent
		if err := validateCIDRSubset(spec.GetIpv6Cidr(), parentSpec.GetIpv6Cidr(), cidrIPv6); err != nil {
			return err
		}
	}

	// Validate no CIDR overlap with existing subnets in the same VirtualNetwork:
	if err := s.validateNoCIDROverlap(ctx, spec); err != nil {
		return err
	}

	return nil
}

// validateNoCIDROverlap checks that the new subnet's CIDRs don't overlap with any existing
// subnets in the same VirtualNetwork.
// Note: this check is not fully atomic; concurrent subnet creation could bypass overlap
// validation. A locking mechanism would be needed for complete reliability.
func (s *PrivateSubnetsServer) validateNoCIDROverlap(ctx context.Context,
	spec *privatev1.SubnetSpec) error {

	// Fetch all existing subnets for the same VirtualNetwork using pagination:
	vnKey := refKey(spec.GetVirtualNetwork())
	filter := fmt.Sprintf("this.spec.virtual_network.id == %[1]q || this.spec.virtual_network.name == %[1]q", vnKey)
	var allSubnets []*privatev1.Subnet
	var offset int32
	for {
		listRequest := &privatev1.SubnetsListRequest{}
		listRequest.SetFilter(filter)
		listRequest.SetOffset(offset)
		var listResponse *privatev1.SubnetsListResponse
		if err := s.generic.List(ctx, listRequest, &listResponse); err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to list sibling subnets",
				slog.String("virtual_network_id", refKey(spec.GetVirtualNetwork())),
				slog.Any("error", err),
			)
			return grpcstatus.Errorf(grpccodes.Internal, "failed to validate CIDR overlap")
		}
		allSubnets = append(allSubnets, listResponse.GetItems()...)
		if offset+listResponse.GetSize() >= listResponse.GetTotal() {
			break
		}
		offset += listResponse.GetSize()
	}

	for _, existing := range allSubnets {
		existingSpec := existing.GetSpec()

		// Check IPv4 overlap:
		if spec.HasIpv4Cidr() && existingSpec.HasIpv4Cidr() {
			overlap, err := cidrsOverlap(spec.GetIpv4Cidr(), existingSpec.GetIpv4Cidr())
			if err != nil {
				return grpcstatus.Errorf(grpccodes.Internal,
					"failed to parse CIDRs for overlap check: %v", err)
			}
			if overlap {
				return grpcstatus.Errorf(grpccodes.AlreadyExists,
					"subnet IPv4 CIDR '%s' overlaps with existing subnet '%s' (CIDR '%s') "+
						"in VirtualNetwork '%s'",
					spec.GetIpv4Cidr(), existing.GetMetadata().GetName(),
					existingSpec.GetIpv4Cidr(), spec.GetVirtualNetwork())
			}
		}

		// Check IPv6 overlap:
		if spec.HasIpv6Cidr() && existingSpec.HasIpv6Cidr() {
			overlap, err := cidrsOverlap(spec.GetIpv6Cidr(), existingSpec.GetIpv6Cidr())
			if err != nil {
				return grpcstatus.Errorf(grpccodes.Internal,
					"failed to parse CIDRs for overlap check: %v", err)
			}
			if overlap {
				return grpcstatus.Errorf(grpccodes.AlreadyExists,
					"subnet IPv6 CIDR '%s' overlaps with existing subnet '%s' (CIDR '%s') "+
						"in VirtualNetwork '%s'",
					spec.GetIpv6Cidr(), existing.GetMetadata().GetName(),
					existingSpec.GetIpv6Cidr(), spec.GetVirtualNetwork())
			}
		}
	}

	return nil
}

// cidrsOverlap returns true if two CIDRs overlap (one contains any part of the other).
func cidrsOverlap(cidrA, cidrB string) (bool, error) {
	prefixA, errA := netip.ParsePrefix(cidrA)
	prefixB, errB := netip.ParsePrefix(cidrB)
	if errA != nil || errB != nil {
		return false, fmt.Errorf(
			"failed to parse CIDRs: %q: %w, %q: %w",
			cidrA, errA, cidrB, errB,
		)
	}
	return prefixA.Contains(prefixB.Addr()) || prefixB.Contains(prefixA.Addr()), nil
}

// validateImmutableFieldsSubnet validates that immutable fields have not been changed.
func validateImmutableFieldsSubnet(newSubnet *privatev1.Subnet, existingSubnet *privatev1.Subnet) error {
	if existingSubnet == nil {
		return nil // Create operation, no immutability checks
	}

	newSpec := newSubnet.GetSpec()
	existingSpec := existingSubnet.GetSpec()

	// Check immutable virtual_network field
	if refKey(newSpec.GetVirtualNetwork()) != refKey(existingSpec.GetVirtualNetwork()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.virtual_network' is immutable and cannot be changed from '%s' to '%s'",
			refKey(existingSpec.GetVirtualNetwork()), refKey(newSpec.GetVirtualNetwork()))
	}

	// SUB-VAL-14, SUB-VAL-15: Preserve and check immutable CIDR fields.
	for _, field := range []immutableCIDRField{
		{
			fieldName:       "spec.ipv4_cidr",
			ipVersion:       cidrIPv4,
			existingHasCIDR: existingSpec.HasIpv4Cidr,
			existingCIDR:    existingSpec.GetIpv4Cidr,
			newHasCIDR:      newSpec.HasIpv4Cidr,
			newCIDR:         newSpec.GetIpv4Cidr,
			setNewCIDR:      newSpec.SetIpv4Cidr,
		},
		{
			fieldName:       "spec.ipv6_cidr",
			ipVersion:       cidrIPv6,
			existingHasCIDR: existingSpec.HasIpv6Cidr,
			existingCIDR:    existingSpec.GetIpv6Cidr,
			newHasCIDR:      newSpec.HasIpv6Cidr,
			newCIDR:         newSpec.GetIpv6Cidr,
			setNewCIDR:      newSpec.SetIpv6Cidr,
		},
	} {
		if err := field.preserveAndValidate(); err != nil {
			return err
		}
	}

	return nil
}
