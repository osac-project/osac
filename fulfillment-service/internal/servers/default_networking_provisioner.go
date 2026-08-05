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

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

const defaultLabel = "osac.openshift.io/default"
const ownerReferenceAnnotation = "osac.openshift.io/owner-reference"

func validateNotDefault(labels map[string]string, resourceType string) error {
	if labels[defaultLabel] == "true" {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"cannot delete default %s: default networking resources are system-managed", resourceType)
	}
	return nil
}

type DefaultNetworkingProvisionerBuilder struct {
	logger            *slog.Logger
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

type DefaultNetworkingProvisioner struct {
	logger            *slog.Logger
	networkClassDao   *dao.GenericDAO[*privatev1.NetworkClass]
	virtualNetworkDao *dao.GenericDAO[*privatev1.VirtualNetwork]
	subnetDao         *dao.GenericDAO[*privatev1.Subnet]
	securityGroupDao  *dao.GenericDAO[*privatev1.SecurityGroup]
	externalIPDao     *dao.GenericDAO[*privatev1.ExternalIP]
	externalIPPoolDao *dao.GenericDAO[*privatev1.ExternalIPPool]
	natGatewayDao     *dao.GenericDAO[*privatev1.NATGateway]
	tenantDao         *dao.GenericDAO[*privatev1.Tenant]
}

func NewDefaultNetworkingProvisioner() *DefaultNetworkingProvisionerBuilder {
	return &DefaultNetworkingProvisionerBuilder{}
}

func (b *DefaultNetworkingProvisionerBuilder) SetLogger(value *slog.Logger) *DefaultNetworkingProvisionerBuilder {
	b.logger = value
	return b
}

func (b *DefaultNetworkingProvisionerBuilder) SetTenancyLogic(value auth.TenancyLogic) *DefaultNetworkingProvisionerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *DefaultNetworkingProvisionerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *DefaultNetworkingProvisionerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *DefaultNetworkingProvisionerBuilder) Build() (result *DefaultNetworkingProvisioner, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
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

	virtualNetworkDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	subnetDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	securityGroupDao, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	externalIPDao, err := dao.NewGenericDAO[*privatev1.ExternalIP]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	externalIPPoolDao, err := dao.NewGenericDAO[*privatev1.ExternalIPPool]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	natGatewayDao, err := dao.NewGenericDAO[*privatev1.NATGateway]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	tenantDao, err := dao.NewGenericDAO[*privatev1.Tenant]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &DefaultNetworkingProvisioner{
		logger:            b.logger,
		networkClassDao:   networkClassDao,
		virtualNetworkDao: virtualNetworkDao,
		subnetDao:         subnetDao,
		securityGroupDao:  securityGroupDao,
		externalIPDao:     externalIPDao,
		externalIPPoolDao: externalIPPoolDao,
		natGatewayDao:     natGatewayDao,
		tenantDao:         tenantDao,
	}
	return
}

// Provision creates default networking resources for the given tenant. Returns nil if no default
// NetworkClass exists or if the NetworkClass has no defaults configured.
//
// Callers must ensure ctx carries a database transaction — all resources are created within that
// transaction so a failure rolls back the entire set. The gRPC interceptor provides this when
// called from an RPC handler.
func (p *DefaultNetworkingProvisioner) Provision(ctx context.Context, tenantName string) error {
	nc, err := findDefaultNetworkClass(ctx, p.logger, p.networkClassDao)
	if err != nil {
		return fmt.Errorf("failed to find default NetworkClass: %w", err)
	}
	if nc == nil {
		p.logger.InfoContext(ctx, "No default NetworkClass configured, skipping default networking",
			slog.String("tenant", tenantName))
		return nil
	}

	defaults := nc.GetSpec().GetDefaults()
	if defaults == nil {
		p.logger.InfoContext(ctx, "Default NetworkClass has no defaults configured, skipping default networking",
			slog.String("tenant", tenantName),
			slog.String("network_class", nc.GetId()))
		return nil
	}

	p.logger.InfoContext(ctx, "Creating default networking resources",
		slog.String("tenant", tenantName),
		slog.String("network_class", nc.GetId()))

	vnID, err := p.createDefaultVirtualNetwork(ctx, tenantName, defaults, nc)
	if err != nil {
		return fmt.Errorf("failed to create default VirtualNetwork: %w", err)
	}

	if defaults.GetSubnetIpv4Cidr() != "" {
		_, err = p.createDefaultSubnet(ctx, tenantName, vnID, defaults.GetSubnetIpv4Cidr(), "", "default-ipv4")
		if err != nil {
			return fmt.Errorf("failed to create default IPv4 Subnet: %w", err)
		}
	}

	if defaults.GetSubnetIpv6Cidr() != "" {
		_, err = p.createDefaultSubnet(ctx, tenantName, vnID, "", defaults.GetSubnetIpv6Cidr(), "default-ipv6")
		if err != nil {
			return fmt.Errorf("failed to create default IPv6 Subnet: %w", err)
		}
	}

	_, err = p.createDefaultSecurityGroup(ctx, tenantName, vnID, defaults)
	if err != nil {
		return fmt.Errorf("failed to create default SecurityGroup: %w", err)
	}

	if defaults.GetEnableNatGateway() {
		err = p.provisionNATGateway(ctx, tenantName, vnID)
		if err != nil {
			return fmt.Errorf("failed to create default NATGateway: %w", err)
		}
	}

	p.logger.InfoContext(ctx, "Default networking resources created successfully",
		slog.String("tenant", tenantName))
	return nil
}

func (p *DefaultNetworkingProvisioner) createDefaultVirtualNetwork(
	ctx context.Context,
	tenantName string,
	defaults *privatev1.NetworkDefaults,
	nc *privatev1.NetworkClass,
) (string, error) {
	vn := privatev1.VirtualNetwork_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   "default",
			Tenant: tenantName,
			Labels: map[string]string{
				defaultLabel: "true",
			},
			Creator: "system",
		}.Build(),
		Spec: privatev1.VirtualNetworkSpec_builder{
			Region:                 "default",
			NetworkClass:           nc.GetId(),
			ImplementationStrategy: nc.GetImplementationStrategy(),
		}.Build(),
		Status: privatev1.VirtualNetworkStatus_builder{
			State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING,
		}.Build(),
	}.Build()

	if ipv4 := defaults.GetVirtualNetworkIpv4Cidr(); ipv4 != "" {
		vn.GetSpec().SetIpv4Cidr(ipv4)
	}
	if ipv6 := defaults.GetVirtualNetworkIpv6Cidr(); ipv6 != "" {
		vn.GetSpec().SetIpv6Cidr(ipv6)
	}

	resp, err := p.virtualNetworkDao.Create().SetObject(vn).Do(ctx)
	if err != nil {
		return "", err
	}
	return resp.GetObject().GetId(), nil
}

func (p *DefaultNetworkingProvisioner) createDefaultSubnet(
	ctx context.Context,
	tenantName, vnID, ipv4CIDR, ipv6CIDR, name string,
) (string, error) {
	subnet := privatev1.Subnet_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   name,
			Tenant: tenantName,
			Labels: map[string]string{
				defaultLabel: "true",
			},
			Annotations: map[string]string{
				ownerReferenceAnnotation: vnID,
			},
			Creator: "system",
		}.Build(),
		Spec: privatev1.SubnetSpec_builder{
			VirtualNetwork: vnID,
		}.Build(),
		Status: privatev1.SubnetStatus_builder{
			State: privatev1.SubnetState_SUBNET_STATE_PENDING,
		}.Build(),
	}.Build()

	if ipv4CIDR != "" {
		subnet.GetSpec().SetIpv4Cidr(ipv4CIDR)
	}
	if ipv6CIDR != "" {
		subnet.GetSpec().SetIpv6Cidr(ipv6CIDR)
	}

	resp, err := p.subnetDao.Create().SetObject(subnet).Do(ctx)
	if err != nil {
		return "", err
	}
	return resp.GetObject().GetId(), nil
}

func (p *DefaultNetworkingProvisioner) createDefaultSecurityGroup(
	ctx context.Context,
	tenantName, vnID string,
	defaults *privatev1.NetworkDefaults,
) (string, error) {
	sg := privatev1.SecurityGroup_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   "default",
			Tenant: tenantName,
			Labels: map[string]string{
				defaultLabel: "true",
			},
			Annotations: map[string]string{
				ownerReferenceAnnotation: vnID,
			},
			Creator: "system",
		}.Build(),
		Spec: privatev1.SecurityGroupSpec_builder{
			VirtualNetwork:         vnID,
			Ingress:                defaults.GetIngressRules(),
			Egress:                 defaults.GetEgressRules(),
			ImplementationStrategy: "network_policy",
		}.Build(),
		Status: privatev1.SecurityGroupStatus_builder{
			State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING,
		}.Build(),
	}.Build()

	resp, err := p.securityGroupDao.Create().SetObject(sg).Do(ctx)
	if err != nil {
		return "", err
	}
	return resp.GetObject().GetId(), nil
}

func (p *DefaultNetworkingProvisioner) provisionNATGateway(
	ctx context.Context,
	tenantName, vnID string,
) error {
	pool, err := p.findExternalIPPool(ctx)
	if err != nil {
		return err
	}

	externalIPID, err := p.createDefaultExternalIP(ctx, tenantName, pool.GetId())
	if err != nil {
		return err
	}

	err = p.updatePoolCapacity(ctx, pool.GetId(), int64(1))
	if err != nil {
		return err
	}

	_, err = p.createDefaultNATGateway(ctx, tenantName, vnID, externalIPID)
	if err != nil {
		return err
	}

	err = p.updateExternalIPAttachedFlag(ctx, externalIPID, true)
	if err != nil {
		return err
	}

	return nil
}

func (p *DefaultNetworkingProvisioner) findExternalIPPool(ctx context.Context) (*privatev1.ExternalIPPool, error) {
	listResponse, err := p.externalIPPoolDao.List().Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query ExternalIP pools: %w", err)
	}
	var best *privatev1.ExternalIPPool
	for _, pool := range listResponse.GetItems() {
		if pool.GetStatus().GetState() != privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY {
			continue
		}
		if pool.GetStatus().GetAvailable() <= 0 {
			continue
		}
		if best == nil || pool.GetStatus().GetAvailable() > best.GetStatus().GetAvailable() {
			best = pool
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no ExternalIP pool with available capacity found for default NATGateway")
	}
	return best, nil
}

func (p *DefaultNetworkingProvisioner) createDefaultExternalIP(
	ctx context.Context,
	tenantName, poolID string,
) (string, error) {
	eip := privatev1.ExternalIP_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   "default-nat",
			Tenant: tenantName,
			Labels: map[string]string{
				defaultLabel: "true",
			},
			Creator: "system",
		}.Build(),
		Spec: privatev1.ExternalIPSpec_builder{
			Pool: poolID,
		}.Build(),
		Status: privatev1.ExternalIPStatus_builder{
			State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING,
		}.Build(),
	}.Build()

	resp, err := p.externalIPDao.Create().SetObject(eip).Do(ctx)
	if err != nil {
		return "", err
	}
	return resp.GetObject().GetId(), nil
}

func (p *DefaultNetworkingProvisioner) createDefaultNATGateway(
	ctx context.Context,
	tenantName, vnID, externalIPID string,
) (string, error) {
	ng := privatev1.NATGateway_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   "default",
			Tenant: tenantName,
			Labels: map[string]string{
				defaultLabel: "true",
			},
			Annotations: map[string]string{
				ownerReferenceAnnotation: vnID,
			},
			Creator: "system",
		}.Build(),
		Spec: privatev1.NATGatewaySpec_builder{
			VirtualNetwork: vnID,
			ExternalIp:     externalIPID,
		}.Build(),
		Status: privatev1.NATGatewayStatus_builder{
			State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING,
		}.Build(),
	}.Build()

	resp, err := p.natGatewayDao.Create().SetObject(ng).Do(ctx)
	if err != nil {
		return "", err
	}
	return resp.GetObject().GetId(), nil
}

func (p *DefaultNetworkingProvisioner) updatePoolCapacity(ctx context.Context, poolID string, delta int64) error {
	getResponse, err := p.externalIPPoolDao.Get().
		SetId(poolID).
		SetLock(true).
		Do(ctx)
	if err != nil {
		return fmt.Errorf("failed to get ExternalIPPool for capacity update: %w", err)
	}

	pool := getResponse.GetObject()
	newAllocated := pool.GetStatus().GetAllocated() + delta
	newAvailable := pool.GetStatus().GetAvailable() - delta
	if newAvailable < 0 {
		return fmt.Errorf("ExternalIP pool '%s' has no available capacity", poolID)
	}
	pool.GetStatus().SetAllocated(newAllocated)
	pool.GetStatus().SetAvailable(newAvailable)

	_, err = p.externalIPPoolDao.Update().SetObject(pool).Do(ctx)
	if err != nil {
		return fmt.Errorf("failed to update ExternalIPPool capacity: %w", err)
	}
	return nil
}

func (p *DefaultNetworkingProvisioner) updateExternalIPAttachedFlag(ctx context.Context, externalIPID string, attached bool) error {
	getResponse, err := p.externalIPDao.Get().
		SetId(externalIPID).
		SetLock(true).
		Do(ctx)
	if err != nil {
		return fmt.Errorf("failed to get ExternalIP for attached flag update: %w", err)
	}

	eip := getResponse.GetObject()
	eip.GetStatus().SetAttached(attached)

	_, err = p.externalIPDao.Update().SetObject(eip).Do(ctx)
	if err != nil {
		return fmt.Errorf("failed to update ExternalIP attached flag: %w", err)
	}
	return nil
}
