/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package defaultnetworking owns the asynchronous lifecycle of the networking
// resources that are created from NetworkClass.spec.defaults.
package defaultnetworking

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	defaultLabel             = "osac.openshift.io/default"
	ownerReferenceAnnotation = "osac.openshift.io/owner-reference"
	controllerCreator        = "system"
	defaultResourceName      = "default"
	defaultExternalIPName    = "default-nat"
)

var (
	// ErrResourcesDeleting tells a project reconciler to retry while a child
	// resource controller finishes asynchronous cleanup.
	ErrResourcesDeleting = errors.New("default networking resources are still being deleted")
)

type networkClassesReader interface {
	List(context.Context, *privatev1.NetworkClassesListRequest, ...grpc.CallOption) (*privatev1.NetworkClassesListResponse, error)
}

type virtualNetworksClient interface {
	List(context.Context, *privatev1.VirtualNetworksListRequest, ...grpc.CallOption) (*privatev1.VirtualNetworksListResponse, error)
	Create(context.Context, *privatev1.VirtualNetworksCreateRequest, ...grpc.CallOption) (*privatev1.VirtualNetworksCreateResponse, error)
	Delete(context.Context, *privatev1.VirtualNetworksDeleteRequest, ...grpc.CallOption) (*privatev1.VirtualNetworksDeleteResponse, error)
}

type subnetsClient interface {
	List(context.Context, *privatev1.SubnetsListRequest, ...grpc.CallOption) (*privatev1.SubnetsListResponse, error)
	Create(context.Context, *privatev1.SubnetsCreateRequest, ...grpc.CallOption) (*privatev1.SubnetsCreateResponse, error)
	Delete(context.Context, *privatev1.SubnetsDeleteRequest, ...grpc.CallOption) (*privatev1.SubnetsDeleteResponse, error)
}

type securityGroupsClient interface {
	List(context.Context, *privatev1.SecurityGroupsListRequest, ...grpc.CallOption) (*privatev1.SecurityGroupsListResponse, error)
	Create(context.Context, *privatev1.SecurityGroupsCreateRequest, ...grpc.CallOption) (*privatev1.SecurityGroupsCreateResponse, error)
	Delete(context.Context, *privatev1.SecurityGroupsDeleteRequest, ...grpc.CallOption) (*privatev1.SecurityGroupsDeleteResponse, error)
}

type externalIPPoolsReader interface {
	List(context.Context, *privatev1.ExternalIPPoolsListRequest, ...grpc.CallOption) (*privatev1.ExternalIPPoolsListResponse, error)
}

type externalIPsClient interface {
	List(context.Context, *privatev1.ExternalIPsListRequest, ...grpc.CallOption) (*privatev1.ExternalIPsListResponse, error)
	Create(context.Context, *privatev1.ExternalIPsCreateRequest, ...grpc.CallOption) (*privatev1.ExternalIPsCreateResponse, error)
	Delete(context.Context, *privatev1.ExternalIPsDeleteRequest, ...grpc.CallOption) (*privatev1.ExternalIPsDeleteResponse, error)
}

type natGatewaysClient interface {
	List(context.Context, *privatev1.NATGatewaysListRequest, ...grpc.CallOption) (*privatev1.NATGatewaysListResponse, error)
	Create(context.Context, *privatev1.NATGatewaysCreateRequest, ...grpc.CallOption) (*privatev1.NATGatewaysCreateResponse, error)
	Delete(context.Context, *privatev1.NATGatewaysDeleteRequest, ...grpc.CallOption) (*privatev1.NATGatewaysDeleteResponse, error)
}

// Manager is the controller-owned default-networking lifecycle.
type Manager interface {
	Ensure(context.Context, string) error
	Delete(context.Context, string) error
}

// ManagerBuilder contains the data needed to create a default-networking manager.
type ManagerBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
}

// NewManager creates a builder for the shared default-networking manager.
func NewManager() *ManagerBuilder {
	return &ManagerBuilder{}
}

// SetLogger sets the logger used by the manager.
func (b *ManagerBuilder) SetLogger(value *slog.Logger) *ManagerBuilder {
	b.logger = value
	return b
}

// SetConnection sets the fulfillment-service connection used by the manager.
func (b *ManagerBuilder) SetConnection(value *grpc.ClientConn) *ManagerBuilder {
	b.connection = value
	return b
}

// Build creates a manager backed by the private fulfillment API.
func (b *ManagerBuilder) Build() (Manager, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.connection == nil {
		return nil, errors.New("connection is mandatory")
	}
	return &manager{
		logger:          b.logger,
		networkClasses:  privatev1.NewNetworkClassesClient(b.connection),
		virtualNetworks: privatev1.NewVirtualNetworksClient(b.connection),
		subnets:         privatev1.NewSubnetsClient(b.connection),
		securityGroups:  privatev1.NewSecurityGroupsClient(b.connection),
		externalIPPools: privatev1.NewExternalIPPoolsClient(b.connection),
		externalIPs:     privatev1.NewExternalIPsClient(b.connection),
		natGateways:     privatev1.NewNATGatewaysClient(b.connection),
	}, nil
}

type manager struct {
	logger          *slog.Logger
	networkClasses  networkClassesReader
	virtualNetworks virtualNetworksClient
	subnets         subnetsClient
	securityGroups  securityGroupsClient
	externalIPPools externalIPPoolsReader
	externalIPs     externalIPsClient
	natGateways     natGatewaysClient
}

// Ensure creates the default resources for a tenant when the singleton
// NetworkClass contains defaults. Hub selection is owned by the NetworkClass
// and VirtualNetwork controllers, so default resources are created even while
// the NetworkClass is pending; their controllers keep them pending until a
// canonical Hub is available. Every operation is idempotent; reconciliation
// can safely resume after any API or controller failure.
func (m *manager) Ensure(ctx context.Context, tenantName string) error {
	if tenantName == "system" || tenantName == "shared" {
		return nil
	}

	networkClass, err := m.findNetworkClass(ctx)
	if err != nil {
		return err
	}
	if networkClass == nil || networkClass.GetSpec().GetDefaults() == nil {
		return nil
	}
	defaults := networkClass.GetSpec().GetDefaults()
	vn, err := m.ensureVirtualNetwork(ctx, tenantName, networkClass, defaults)
	if err != nil {
		return fmt.Errorf("failed to ensure default VirtualNetwork: %w", err)
	}

	if defaults.GetSubnetIpv4Cidr() != "" {
		if err := m.ensureSubnet(ctx, tenantName, vn.GetId(), defaults.GetSubnetIpv4Cidr(), "", "default-ipv4"); err != nil {
			return fmt.Errorf("failed to ensure default IPv4 Subnet: %w", err)
		}
	}
	if defaults.GetSubnetIpv6Cidr() != "" {
		if err := m.ensureSubnet(ctx, tenantName, vn.GetId(), "", defaults.GetSubnetIpv6Cidr(), "default-ipv6"); err != nil {
			return fmt.Errorf("failed to ensure default IPv6 Subnet: %w", err)
		}
	}
	if err := m.ensureSecurityGroup(ctx, tenantName, vn.GetId(), defaults); err != nil {
		return fmt.Errorf("failed to ensure default SecurityGroup: %w", err)
	}
	if defaults.GetEnableNatGateway() {
		if err := m.ensureNATGateway(ctx, tenantName, vn.GetId()); err != nil {
			return fmt.Errorf("failed to ensure default NATGateway: %w", err)
		}
	}
	return nil
}

func (m *manager) findNetworkClass(ctx context.Context) (*privatev1.NetworkClass, error) {
	filter := "!has(this.metadata.deletion_timestamp)"
	response, err := m.networkClasses.List(ctx, privatev1.NetworkClassesListRequest_builder{
		Filter: &filter,
		Limit:  new(int32(2)),
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to list active NetworkClasses: %w", err)
	}
	if response.GetTotal() > 1 || len(response.GetItems()) > 1 {
		return nil, errors.New("multiple active NetworkClasses are configured")
	}
	if len(response.GetItems()) == 0 {
		return nil, nil
	}
	return response.GetItems()[0], nil
}

func (m *manager) ensureVirtualNetwork(ctx context.Context, tenantName string, networkClass *privatev1.NetworkClass,
	defaults *privatev1.NetworkDefaults) (*privatev1.VirtualNetwork, error) {
	filter := resourceFilter(tenantName, defaultResourceName)
	response, err := m.virtualNetworks.List(ctx, privatev1.VirtualNetworksListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		return nil, err
	}
	if len(response.GetItems()) > 0 {
		return response.GetItems()[0], nil
	}

	object := privatev1.VirtualNetwork_builder{
		Metadata: privatev1.Metadata_builder{
			Name: defaultResourceName, Tenant: tenantName, Creator: controllerCreator,
			Labels: map[string]string{defaultLabel: "true"},
		}.Build(),
		Spec: privatev1.VirtualNetworkSpec_builder{
			Region:       "default",
			NetworkClass: privatev1.NetworkClassReference_builder{Id: networkClass.GetId()}.Build(),
		}.Build(),
	}.Build()
	if defaults.GetVirtualNetworkIpv4Cidr() != "" {
		object.GetSpec().SetIpv4Cidr(defaults.GetVirtualNetworkIpv4Cidr())
	}
	if defaults.GetVirtualNetworkIpv6Cidr() != "" {
		object.GetSpec().SetIpv6Cidr(defaults.GetVirtualNetworkIpv6Cidr())
	}
	created, err := m.virtualNetworks.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{Object: object}.Build())
	if status.Code(err) == codes.AlreadyExists {
		return m.getVirtualNetwork(ctx, tenantName)
	}
	if err != nil {
		return nil, err
	}
	return created.GetObject(), nil
}

func (m *manager) getVirtualNetwork(ctx context.Context, tenantName string) (*privatev1.VirtualNetwork, error) {
	filter := resourceFilter(tenantName, defaultResourceName)
	response, err := m.virtualNetworks.List(ctx, privatev1.VirtualNetworksListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		return nil, err
	}
	if len(response.GetItems()) == 0 {
		return nil, errors.New("default VirtualNetwork was reported as existing but could not be found")
	}
	return response.GetItems()[0], nil
}

func (m *manager) ensureSubnet(ctx context.Context, tenantName, virtualNetworkID, ipv4CIDR, ipv6CIDR, name string) error {
	filter := resourceFilter(tenantName, name)
	response, err := m.subnets.List(ctx, privatev1.SubnetsListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		return err
	}
	if len(response.GetItems()) > 0 {
		return nil
	}
	object := privatev1.Subnet_builder{
		Metadata: privatev1.Metadata_builder{
			Name: name, Tenant: tenantName, Creator: controllerCreator,
			Labels:      map[string]string{defaultLabel: "true"},
			Annotations: map[string]string{ownerReferenceAnnotation: virtualNetworkID},
		}.Build(),
		Spec: privatev1.SubnetSpec_builder{
			VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
		}.Build(),
	}.Build()
	if ipv4CIDR != "" {
		object.GetSpec().SetIpv4Cidr(ipv4CIDR)
	}
	if ipv6CIDR != "" {
		object.GetSpec().SetIpv6Cidr(ipv6CIDR)
	}
	_, err = m.subnets.Create(ctx, privatev1.SubnetsCreateRequest_builder{Object: object}.Build())
	if status.Code(err) == codes.AlreadyExists {
		return nil
	}
	return err
}

func (m *manager) ensureSecurityGroup(ctx context.Context, tenantName, virtualNetworkID string,
	defaults *privatev1.NetworkDefaults) error {
	filter := resourceFilter(tenantName, defaultResourceName)
	response, err := m.securityGroups.List(ctx, privatev1.SecurityGroupsListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		return err
	}
	if len(response.GetItems()) > 0 {
		return nil
	}
	object := privatev1.SecurityGroup_builder{
		Metadata: privatev1.Metadata_builder{
			Name: defaultResourceName, Tenant: tenantName, Creator: controllerCreator,
			Labels:      map[string]string{defaultLabel: "true"},
			Annotations: map[string]string{ownerReferenceAnnotation: virtualNetworkID},
		}.Build(),
		Spec: privatev1.SecurityGroupSpec_builder{
			VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
			Ingress:        defaults.GetIngressRules(),
			Egress:         defaults.GetEgressRules(),
		}.Build(),
	}.Build()
	_, err = m.securityGroups.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{Object: object}.Build())
	if status.Code(err) == codes.AlreadyExists {
		return nil
	}
	return err
}

func (m *manager) ensureNATGateway(ctx context.Context, tenantName, virtualNetworkID string) error {
	filter := resourceFilter(tenantName, defaultResourceName)
	gateways, err := m.natGateways.List(ctx, privatev1.NATGatewaysListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		return err
	}
	if len(gateways.GetItems()) > 0 {
		return nil
	}

	externalIP, err := m.ensureExternalIP(ctx, tenantName)
	if err != nil {
		return err
	}
	object := privatev1.NATGateway_builder{
		Metadata: privatev1.Metadata_builder{
			Name: defaultResourceName, Tenant: tenantName, Creator: controllerCreator,
			Labels:      map[string]string{defaultLabel: "true"},
			Annotations: map[string]string{ownerReferenceAnnotation: virtualNetworkID},
		}.Build(),
		Spec: privatev1.NATGatewaySpec_builder{
			VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
			ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: externalIP.GetId()}.Build(),
		}.Build(),
	}.Build()
	_, err = m.natGateways.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{Object: object}.Build())
	if status.Code(err) == codes.AlreadyExists {
		return nil
	}
	return err
}

func (m *manager) ensureExternalIP(ctx context.Context, tenantName string) (*privatev1.ExternalIP, error) {
	filter := resourceFilter(tenantName, defaultExternalIPName)
	response, err := m.externalIPs.List(ctx, privatev1.ExternalIPsListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		return nil, err
	}
	if len(response.GetItems()) > 0 {
		return response.GetItems()[0], nil
	}

	pools, err := m.externalIPPools.List(ctx, privatev1.ExternalIPPoolsListRequest_builder{}.Build())
	if err != nil {
		return nil, err
	}
	available := make([]*privatev1.ExternalIPPool, 0, len(pools.GetItems()))
	for _, pool := range pools.GetItems() {
		if pool.GetStatus().GetState() == privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY &&
			pool.GetStatus().GetAvailable() > 0 {
			available = append(available, pool)
		}
	}
	if len(available) == 0 {
		return nil, errors.New("no ready ExternalIPPool has available capacity")
	}
	sort.Slice(available, func(i, j int) bool { return available[i].GetId() < available[j].GetId() })

	object := privatev1.ExternalIP_builder{
		Metadata: privatev1.Metadata_builder{
			Name: defaultExternalIPName, Tenant: tenantName, Creator: controllerCreator,
			Labels: map[string]string{defaultLabel: "true"},
		}.Build(),
		Spec: privatev1.ExternalIPSpec_builder{
			Pool: privatev1.ExternalIPPoolReference_builder{Id: available[0].GetId()}.Build(),
		}.Build(),
	}.Build()
	created, err := m.externalIPs.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{Object: object}.Build())
	if status.Code(err) == codes.AlreadyExists {
		response, findErr := m.externalIPs.List(ctx, privatev1.ExternalIPsListRequest_builder{Filter: &filter}.Build())
		if findErr != nil {
			return nil, fmt.Errorf("ExternalIP already exists but could not be looked up: %w", findErr)
		}
		if len(response.GetItems()) == 0 {
			return nil, errors.New("ExternalIP already exists but could not be found")
		}
		return response.GetItems()[0], nil
	}
	if err != nil {
		return nil, err
	}
	return created.GetObject(), nil
}

// Delete removes the default resources for a tenant through the controller
// service account. Delete requests are intentionally ordered from dependents
// to parents and return ErrResourcesDeleting while finalizers finish.
func (m *manager) Delete(ctx context.Context, tenantName string) error {
	vn, err := m.getVirtualNetworkForDelete(ctx, tenantName)
	if err != nil || vn == nil {
		return err
	}
	vnID := vn.GetId()

	if err := m.deleteNATGateways(ctx, tenantName); err != nil {
		return err
	}
	if err := m.deleteExternalIPs(ctx, tenantName); err != nil {
		return err
	}
	if err := m.deleteSecurityGroups(ctx, tenantName, vnID); err != nil {
		return err
	}
	if err := m.deleteSubnets(ctx, tenantName, vnID); err != nil {
		return err
	}
	if vn.GetMetadata().HasDeletionTimestamp() {
		return ErrResourcesDeleting
	}
	if _, err := m.virtualNetworks.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: vnID}.Build()); err != nil {
		return err
	}
	return ErrResourcesDeleting
}

func (m *manager) getVirtualNetworkForDelete(ctx context.Context, tenantName string) (*privatev1.VirtualNetwork, error) {
	filter := resourceFilter(tenantName, defaultResourceName)
	response, err := m.virtualNetworks.List(ctx, privatev1.VirtualNetworksListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		return nil, err
	}
	if len(response.GetItems()) == 0 {
		return nil, nil
	}
	return response.GetItems()[0], nil
}

func (m *manager) deleteNATGateways(ctx context.Context, tenantName string) error {
	filter := resourceFilter(tenantName, defaultResourceName)
	response, err := m.natGateways.List(ctx, privatev1.NATGatewaysListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		return err
	}
	for _, object := range response.GetItems() {
		if object.GetMetadata().HasDeletionTimestamp() {
			return ErrResourcesDeleting
		}
		if _, err := m.natGateways.Delete(ctx, privatev1.NATGatewaysDeleteRequest_builder{Id: object.GetId()}.Build()); err != nil {
			return err
		}
	}
	if len(response.GetItems()) > 0 {
		return ErrResourcesDeleting
	}
	return nil
}

func (m *manager) deleteExternalIPs(ctx context.Context, tenantName string) error {
	filter := resourceFilter(tenantName, defaultExternalIPName)
	response, err := m.externalIPs.List(ctx, privatev1.ExternalIPsListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		return err
	}
	for _, object := range response.GetItems() {
		if object.GetMetadata().HasDeletionTimestamp() {
			return ErrResourcesDeleting
		}
		if _, err := m.externalIPs.Delete(ctx, privatev1.ExternalIPsDeleteRequest_builder{Id: object.GetId()}.Build()); err != nil {
			return err
		}
	}
	if len(response.GetItems()) > 0 {
		return ErrResourcesDeleting
	}
	return nil
}

func (m *manager) deleteSecurityGroups(ctx context.Context, tenantName, virtualNetworkID string) error {
	filter := childResourceFilter(tenantName, virtualNetworkID)
	response, err := m.securityGroups.List(ctx, privatev1.SecurityGroupsListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		return err
	}
	for _, object := range response.GetItems() {
		if object.GetMetadata().HasDeletionTimestamp() {
			return ErrResourcesDeleting
		}
		if _, err := m.securityGroups.Delete(ctx, privatev1.SecurityGroupsDeleteRequest_builder{Id: object.GetId()}.Build()); err != nil {
			return err
		}
	}
	if len(response.GetItems()) > 0 {
		return ErrResourcesDeleting
	}
	return nil
}

func (m *manager) deleteSubnets(ctx context.Context, tenantName, virtualNetworkID string) error {
	filter := childResourceFilter(tenantName, virtualNetworkID)
	response, err := m.subnets.List(ctx, privatev1.SubnetsListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		return err
	}
	for _, object := range response.GetItems() {
		if object.GetMetadata().HasDeletionTimestamp() {
			return ErrResourcesDeleting
		}
		if _, err := m.subnets.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{Id: object.GetId()}.Build()); err != nil {
			return err
		}
	}
	if len(response.GetItems()) > 0 {
		return ErrResourcesDeleting
	}
	return nil
}

func resourceFilter(tenantName, resourceName string) string {
	return fmt.Sprintf(`this.metadata.tenant == %q && this.metadata.name == %q && this.metadata.labels["%s"] == "true"`,
		tenantName, resourceName, defaultLabel)
}

func childResourceFilter(tenantName, virtualNetworkID string) string {
	return fmt.Sprintf(`this.metadata.tenant == %q && this.spec.virtual_network.id == %q && this.metadata.labels["%s"] == "true"`,
		tenantName, virtualNetworkID, defaultLabel)
}

var _ Manager = (*manager)(nil)
