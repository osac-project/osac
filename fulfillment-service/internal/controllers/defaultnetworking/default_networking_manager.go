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
	// ErrResourcesDeleting tells the owning controller to retry while a child
	// resource controller finishes asynchronous cleanup.
	ErrResourcesDeleting = errors.New("default networking resources are still being deleted")

	// ErrNoPoolCapacity tells the owning controller that every READY IPv4
	// ExternalIPPool is full, so the default ExternalIP can't be created. It is
	// a final failure: the controller reports it and doesn't retry.
	ErrNoPoolCapacity = errors.New("no READY IPv4 ExternalIPPool has capacity")

	// ErrNoReadyPool tells the owning controller that the default ExternalIP
	// waits for a READY IPv4 ExternalIPPool. The other default resources are
	// still ensured; the controller retries later.
	ErrNoReadyPool = errors.New("no READY IPv4 ExternalIPPool")
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
	Ensure(context.Context, string) (*privatev1.NetworkDefaults, error)
	Delete(context.Context, string) error
}

// ManagerBuilder contains the data needed to create a default-networking manager.
type ManagerBuilder struct {
	logger              *slog.Logger
	connection          *grpc.ClientConn
	provisioningEnabled *bool
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

// SetProvisioningEnabled sets the value of global.networking.provisioningEnabled. With provider networking disabled
// the manager creates no ExternalIP and no NATGateway. The default is true.
func (b *ManagerBuilder) SetProvisioningEnabled(value bool) *ManagerBuilder {
	b.provisioningEnabled = &value
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
	provisioningEnabled := true
	if b.provisioningEnabled != nil {
		provisioningEnabled = *b.provisioningEnabled
	}
	return &manager{
		provisioningEnabled: provisioningEnabled,
		logger:              b.logger,
		networkClasses:      privatev1.NewNetworkClassesClient(b.connection),
		virtualNetworks:     privatev1.NewVirtualNetworksClient(b.connection),
		subnets:             privatev1.NewSubnetsClient(b.connection),
		securityGroups:      privatev1.NewSecurityGroupsClient(b.connection),
		externalIPPools:     privatev1.NewExternalIPPoolsClient(b.connection),
		externalIPs:         privatev1.NewExternalIPsClient(b.connection),
		natGateways:         privatev1.NewNATGatewaysClient(b.connection),
	}, nil
}

type manager struct {
	provisioningEnabled bool
	logger              *slog.Logger
	networkClasses      networkClassesReader
	virtualNetworks     virtualNetworksClient
	subnets             subnetsClient
	securityGroups      securityGroupsClient
	externalIPPools     externalIPPoolsReader
	externalIPs         externalIPsClient
	natGateways         natGatewaysClient
}

// Ensure creates the default resources of a tenant from the defaults of the
// NetworkClass and returns those defaults. With provider networking enabled it
// creates the VirtualNetwork and the ExternalIP default-nat first, then the
// Subnet default-ipv4 and the SecurityGroup default once the VirtualNetwork is
// READY, then the NATGateway default once the VirtualNetwork is READY and the
// ExternalIP ALLOCATED. With provider networking disabled it creates no
// ExternalIP and no NATGateway. Every step is idempotent, so reconciliation can
// resume after any failure. The creates lock the Tenant record and fail while
// the tenant is being deleted.
func (m *manager) Ensure(ctx context.Context, tenantName string) (*privatev1.NetworkDefaults, error) {
	if tenantName == "system" || tenantName == "shared" {
		return nil, nil
	}

	networkClass, err := m.findNetworkClass(ctx)
	if err != nil {
		return nil, err
	}
	if networkClass == nil || networkClass.GetSpec().GetDefaults() == nil {
		return nil, nil
	}
	defaults := networkClass.GetSpec().GetDefaults()
	if networkClass.GetStatus().GetState() != privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY ||
		networkClass.GetMetadata().HasDeletionTimestamp() {
		return defaults, nil
	}
	vn, err := m.ensureVirtualNetwork(ctx, tenantName, defaults)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure default VirtualNetwork: %w", err)
	}
	var externalIP *privatev1.ExternalIP
	var waitErr error
	if m.provisioningEnabled {
		externalIP, err = m.ensureExternalIP(ctx, tenantName)
		switch {
		case errors.Is(err, ErrNoPoolCapacity):
			return defaults, err
		case errors.Is(err, ErrNoReadyPool):
			waitErr = err
		case err != nil:
			return nil, fmt.Errorf("failed to ensure default ExternalIP: %w", err)
		}
	}
	if vn.GetStatus().GetState() != privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY {
		return defaults, waitErr
	}

	if _, err := m.ensureSubnet(ctx, tenantName, vn.GetId(), defaults.GetSubnetIpv4Cidr(), "default-ipv4"); err != nil {
		return nil, fmt.Errorf("failed to ensure default IPv4 Subnet: %w", err)
	}
	if _, err := m.ensureSecurityGroup(ctx, tenantName, vn.GetId(), defaults); err != nil {
		return nil, fmt.Errorf("failed to ensure default SecurityGroup: %w", err)
	}
	if externalIP != nil &&
		externalIP.GetStatus().GetState() == privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED {
		if err := m.ensureNATGateway(ctx, tenantName, vn.GetId(), externalIP.GetId()); err != nil {
			return nil, fmt.Errorf("failed to ensure default NATGateway: %w", err)
		}
	}
	return defaults, waitErr
}

func listExistingAfterAlreadyExists[T any](resourceName string, list func() ([]*T, error)) (*T, error) {
	items, err := list()
	if err != nil {
		return nil, fmt.Errorf("%s already exists but could not be looked up: %w", resourceName, err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%s already exists but could not be found", resourceName)
	}
	return items[0], nil
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

func (m *manager) ensureVirtualNetwork(ctx context.Context, tenantName string,
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
			Ipv4Cidr: new(defaults.GetVirtualNetworkIpv4Cidr()),
		}.Build(),
	}.Build()
	m.logger.InfoContext(ctx, "CreatingDefaultNetworking",
		slog.String("tenant", tenantName), slog.String("kind", "VirtualNetwork"))
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

func (m *manager) ensureSubnet(ctx context.Context, tenantName, virtualNetworkID, ipv4CIDR, name string) (*privatev1.Subnet, error) {
	filter := resourceFilter(tenantName, name)
	response, err := m.subnets.List(ctx, privatev1.SubnetsListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		return nil, err
	}
	if len(response.GetItems()) > 0 {
		return response.GetItems()[0], nil
	}
	object := privatev1.Subnet_builder{
		Metadata: privatev1.Metadata_builder{
			Name: name, Tenant: tenantName, Creator: controllerCreator,
			Labels:      map[string]string{defaultLabel: "true"},
			Annotations: map[string]string{ownerReferenceAnnotation: virtualNetworkID},
		}.Build(),
		Spec: privatev1.SubnetSpec_builder{
			VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
			Ipv4Cidr:       new(ipv4CIDR),
		}.Build(),
	}.Build()
	created, err := m.subnets.Create(ctx, privatev1.SubnetsCreateRequest_builder{Object: object}.Build())
	if status.Code(err) == codes.AlreadyExists {
		return listExistingAfterAlreadyExists("subnet", func() ([]*privatev1.Subnet, error) {
			response, err := m.subnets.List(ctx, privatev1.SubnetsListRequest_builder{Filter: &filter}.Build())
			if err != nil {
				return nil, err
			}
			return response.GetItems(), nil
		})
	}
	if err != nil {
		return nil, err
	}
	return created.GetObject(), nil
}

func (m *manager) ensureSecurityGroup(ctx context.Context, tenantName, virtualNetworkID string,
	defaults *privatev1.NetworkDefaults) (*privatev1.SecurityGroup, error) {
	filter := resourceFilter(tenantName, defaultResourceName)
	response, err := m.securityGroups.List(ctx, privatev1.SecurityGroupsListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		return nil, err
	}
	if len(response.GetItems()) > 0 {
		return response.GetItems()[0], nil
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
	created, err := m.securityGroups.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{Object: object}.Build())
	if status.Code(err) == codes.AlreadyExists {
		return listExistingAfterAlreadyExists("security group", func() ([]*privatev1.SecurityGroup, error) {
			response, err := m.securityGroups.List(ctx, privatev1.SecurityGroupsListRequest_builder{Filter: &filter}.Build())
			if err != nil {
				return nil, err
			}
			return response.GetItems(), nil
		})
	}
	if err != nil {
		return nil, err
	}
	return created.GetObject(), nil
}

func (m *manager) ensureNATGateway(ctx context.Context, tenantName, virtualNetworkID, externalIPID string) error {
	filter := resourceFilter(tenantName, defaultResourceName)
	gateways, err := m.natGateways.List(ctx, privatev1.NATGatewaysListRequest_builder{Filter: &filter}.Build())
	if err != nil {
		return err
	}
	if len(gateways.GetItems()) > 0 {
		return nil
	}
	m.logger.InfoContext(ctx, "CreatingDefaultNetworking",
		slog.String("tenant", tenantName), slog.String("kind", "NATGateway"))
	object := privatev1.NATGateway_builder{
		Metadata: privatev1.Metadata_builder{
			Name: defaultResourceName, Tenant: tenantName, Creator: controllerCreator,
			Labels:      map[string]string{defaultLabel: "true"},
			Annotations: map[string]string{ownerReferenceAnnotation: virtualNetworkID},
		}.Build(),
		Spec: privatev1.NATGatewaySpec_builder{
			VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
			ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: externalIPID}.Build(),
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
	ready := make([]*privatev1.ExternalIPPool, 0, len(pools.GetItems()))
	for _, pool := range pools.GetItems() {
		if pool.GetSpec().GetIpFamily() == privatev1.IPFamily_IP_FAMILY_IPV4 &&
			pool.GetStatus().GetState() == privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY &&
			!pool.GetMetadata().HasDeletionTimestamp() {
			ready = append(ready, pool)
		}
	}
	// Without a READY pool, wait: the pool may still be provisioning.
	if len(ready) == 0 {
		return nil, ErrNoReadyPool
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].GetId() < ready[j].GetId() })

	m.logger.InfoContext(ctx, "CreatingDefaultNetworking",
		slog.String("tenant", tenantName), slog.String("kind", "ExternalIP"))
	var created *privatev1.ExternalIPsCreateResponse
	for _, pool := range ready {
		if pool.GetStatus().GetAvailable() <= 0 {
			continue
		}
		object := privatev1.ExternalIP_builder{
			Metadata: privatev1.Metadata_builder{
				Name: defaultExternalIPName, Tenant: tenantName, Creator: controllerCreator,
				Labels: map[string]string{defaultLabel: "true"},
			}.Build(),
			Spec: privatev1.ExternalIPSpec_builder{
				Pool: privatev1.ExternalIPPoolReference_builder{Id: pool.GetId()}.Build(),
			}.Build(),
		}.Build()
		created, err = m.externalIPs.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{Object: object}.Build())
		// The create rechecks the capacity under the pool lock; when the pool filled meanwhile, try the next one.
		if status.Code(err) == codes.FailedPrecondition {
			continue
		}
		break
	}
	if created == nil && err == nil || status.Code(err) == codes.FailedPrecondition {
		return nil, ErrNoPoolCapacity
	}
	if status.Code(err) == codes.AlreadyExists {
		return listExistingAfterAlreadyExists("ExternalIP", func() ([]*privatev1.ExternalIP, error) {
			response, err := m.externalIPs.List(ctx, privatev1.ExternalIPsListRequest_builder{Filter: &filter}.Build())
			if err != nil {
				return nil, err
			}
			return response.GetItems(), nil
		})
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
