/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package grpcserver

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"k8s.io/apimachinery/pkg/runtime"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
	"github.com/osac-project/osac/fulfillment-service/internal/servers"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
)

// ResourceServerDeps bundles the dependencies needed to construct every filterable resource's public/private
// server pair.
type ResourceServerDeps struct {
	Logger                  *slog.Logger
	Notifier                events.Notifier
	PrivateAttributionLogic auth.AttributionLogic
	PublicAttributionLogic  auth.AttributionLogic
	TenancyLogic            auth.TenancyLogic
	MetricsRegisterer       prometheus.Registerer
	HubScheme               *runtime.Scheme
	SecretStore             vault.SecretStore
	TierResolver            servers.TierResolverFunc

	// PrivateUsersServer is already constructed by the caller — the JIT provisioning interceptor needs it
	// before the gRPC server's interceptor chain is built, which happens before RegisterResourceServers is
	// called. It's threaded through here so registration still happens in one place.
	PrivateUsersServer privatev1.UsersServer
}

// ResourceServers exposes the constructed servers that code outside the registration block still needs directly.
type ResourceServers struct {
	PrivateHubsServer             privatev1.HubsServer
	PrivateComputeInstancesServer privatev1.ComputeInstancesServer
}

// RegisterResourceServers constructs and registers every filterable resource's public and private server onto
// registrar. Takes a grpc.ServiceRegistrar, not a concrete *grpc.Server, so production and tests share the exact
// same code path.
func RegisterResourceServers(ctx context.Context, registrar grpc.ServiceRegistrar, //nolint:gocyclo
	deps ResourceServerDeps) (*ResourceServers, error) {
	// ExternalIPPoolsServerBuilder, ProjectsServerBuilder, and PrivateProjectsServerBuilder take the concrete
	// *database.Notifier, unlike every other builder here, which takes the events.Notifier interface.
	dbNotifier, ok := deps.Notifier.(*database.Notifier)
	if !ok {
		return nil, fmt.Errorf("notifier must be a *database.Notifier, got %T", deps.Notifier)
	}

	// Create the cluster templates server:
	deps.Logger.InfoContext(ctx, "Creating cluster templates server")
	clusterTemplatesServer, err := servers.NewClusterTemplatesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster templates server: %w", err)
	}
	publicv1.RegisterClusterTemplatesServer(registrar, clusterTemplatesServer)

	// Create the cluster catalog items server:
	deps.Logger.InfoContext(ctx, "Creating cluster catalog items server")
	clusterCatalogItemsServer, err := servers.NewClusterCatalogItemsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster catalog items server: %w", err)
	}
	publicv1.RegisterClusterCatalogItemsServer(registrar, clusterCatalogItemsServer)

	// Create the compute instance catalog items server:
	deps.Logger.InfoContext(ctx, "Creating compute instance catalog items server")
	computeInstanceCatalogItemsServer, err := servers.NewComputeInstanceCatalogItemsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create compute instance catalog items server: %w", err)
	}
	publicv1.RegisterComputeInstanceCatalogItemsServer(registrar, computeInstanceCatalogItemsServer)

	// Create the private cluster templates server:
	deps.Logger.InfoContext(ctx, "Creating private cluster templates server")
	privateClusterTemplatesServer, err := servers.NewPrivateClusterTemplatesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private cluster templates server: %w", err)
	}
	privatev1.RegisterClusterTemplatesServer(registrar, privateClusterTemplatesServer)

	// Create the private cluster catalog items server:
	deps.Logger.InfoContext(ctx, "Creating private cluster catalog items server")
	privateClusterCatalogItemsServer, err := servers.NewPrivateClusterCatalogItemsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private cluster catalog items server: %w", err)
	}
	privatev1.RegisterClusterCatalogItemsServer(registrar, privateClusterCatalogItemsServer)

	// Create the private compute instance catalog items server:
	deps.Logger.InfoContext(ctx, "Creating private compute instance catalog items server")
	privateComputeInstanceCatalogItemsServer, err := servers.NewPrivateComputeInstanceCatalogItemsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private compute instance catalog items server: %w", err)
	}
	privatev1.RegisterComputeInstanceCatalogItemsServer(registrar, privateComputeInstanceCatalogItemsServer)

	// Create the clusters server:
	deps.Logger.InfoContext(ctx, "Creating clusters server")
	clustersServer, err := servers.NewClustersServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		SetScheme(deps.HubScheme).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create clusters server: %w", err)
	}
	publicv1.RegisterClustersServer(registrar, clustersServer)

	// Create the private clusters server:
	deps.Logger.InfoContext(ctx, "Creating private clusters server")
	privateClustersServer, err := servers.NewPrivateClustersServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private clusters server: %w", err)
	}
	privatev1.RegisterClustersServer(registrar, privateClustersServer)

	// Create the host types server:
	deps.Logger.InfoContext(ctx, "Creating host types server")
	hostTypesServer, err := servers.NewHostTypesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create host types server: %w", err)
	}
	publicv1.RegisterHostTypesServer(registrar, hostTypesServer)

	// Create the private host types server:
	deps.Logger.InfoContext(ctx, "Creating private host types server")
	privateHostTypesServer, err := servers.NewPrivateHostTypesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private host types server: %w", err)
	}
	privatev1.RegisterHostTypesServer(registrar, privateHostTypesServer)

	// Create the compute instance templates server:
	deps.Logger.InfoContext(ctx, "Creating compute instance templates server")
	computeInstanceTemplatesServer, err := servers.NewComputeInstanceTemplatesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create compute instance templates server: %w", err)
	}
	publicv1.RegisterComputeInstanceTemplatesServer(registrar, computeInstanceTemplatesServer)

	// Create the private compute instance templates server:
	deps.Logger.InfoContext(ctx, "Creating private compute instance templates server")
	privateComputeInstanceTemplatesServer, err := servers.NewPrivateComputeInstanceTemplatesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private compute instance templates server: %w", err)
	}
	privatev1.RegisterComputeInstanceTemplatesServer(registrar, privateComputeInstanceTemplatesServer)

	// Create the compute instances server:
	deps.Logger.InfoContext(ctx, "Creating compute instances server")
	computeInstancesServer, err := servers.NewComputeInstancesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create compute instances server: %w", err)
	}
	publicv1.RegisterComputeInstancesServer(registrar, computeInstancesServer)

	// Create the private compute instances server:
	deps.Logger.InfoContext(ctx, "Creating private compute instances server")
	privateComputeInstancesServer, err := servers.NewPrivateComputeInstancesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private compute instances server: %w", err)
	}
	privatev1.RegisterComputeInstancesServer(registrar, privateComputeInstancesServer)

	// Create the disk images server:
	deps.Logger.InfoContext(ctx, "Creating disk images server")
	diskImagesServer, err := servers.NewDiskImagesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create disk images server: %w", err)
	}
	publicv1.RegisterDiskImagesServer(registrar, diskImagesServer)

	// Create the private disk images server:
	deps.Logger.InfoContext(ctx, "Creating private disk images server")
	privateDiskImagesServer, err := servers.NewPrivateDiskImagesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private disk images server: %w", err)
	}
	privatev1.RegisterDiskImagesServer(registrar, privateDiskImagesServer)

	// Create the bare metal instance templates server:
	deps.Logger.InfoContext(ctx, "Creating bare metal instance templates server")
	bareMetalInstanceTemplatesServer, err := servers.NewBareMetalInstanceTemplatesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create bare metal instance templates server: %w", err)
	}
	publicv1.RegisterBareMetalInstanceTemplatesServer(registrar, bareMetalInstanceTemplatesServer)

	// Create the bare metal instance catalog items server:
	deps.Logger.InfoContext(ctx, "Creating bare metal instance catalog items server")
	bareMetalInstanceCatalogItemsServer, err := servers.NewBareMetalInstanceCatalogItemsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create bare metal instance catalog items server: %w", err)
	}
	publicv1.RegisterBareMetalInstanceCatalogItemsServer(registrar, bareMetalInstanceCatalogItemsServer)

	// Create the bare metal instances server:
	deps.Logger.InfoContext(ctx, "Creating bare metal instances server")
	bareMetalInstancesServer, err := servers.NewBareMetalInstancesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create bare metal instances server: %w", err)
	}
	publicv1.RegisterBareMetalInstancesServer(registrar, bareMetalInstancesServer)

	// Create the private bare metal instance templates server:
	deps.Logger.InfoContext(ctx, "Creating private bare metal instance templates server")
	privateBareMetalInstanceTemplatesServer, err := servers.NewPrivateBareMetalInstanceTemplatesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private bare metal instance templates server: %w", err)
	}
	privatev1.RegisterBareMetalInstanceTemplatesServer(registrar, privateBareMetalInstanceTemplatesServer)

	// Create the private bare metal instance catalog items server:
	deps.Logger.InfoContext(ctx, "Creating private bare metal instance catalog items server")
	privateBareMetalInstanceCatalogItemsServer, err := servers.NewPrivateBareMetalInstanceCatalogItemsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private bare metal instance catalog items server: %w", err)
	}
	privatev1.RegisterBareMetalInstanceCatalogItemsServer(registrar, privateBareMetalInstanceCatalogItemsServer)

	// Create the private bare metal instances server:
	deps.Logger.InfoContext(ctx, "Creating private bare metal instances server")
	privateBareMetalInstancesServer, err := servers.NewPrivateBareMetalInstancesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private bare metal instances server: %w", err)
	}
	privatev1.RegisterBareMetalInstancesServer(registrar, privateBareMetalInstancesServer)

	// Create the private hubs server:
	deps.Logger.InfoContext(ctx, "Creating private hubs server")
	privateHubsServer, err := servers.NewPrivateHubsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create hubs server: %w", err)
	}
	privatev1.RegisterHubsServer(registrar, privateHubsServer)

	// Create the virtual networks server:
	deps.Logger.InfoContext(ctx, "Creating virtual networks server")
	virtualNetworksServer, err := servers.NewVirtualNetworksServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create virtual networks server: %w", err)
	}
	publicv1.RegisterVirtualNetworksServer(registrar, virtualNetworksServer)

	// Create the private virtual networks server:
	deps.Logger.InfoContext(ctx, "Creating private virtual networks server")
	privateVirtualNetworksServer, err := servers.NewPrivateVirtualNetworksServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private virtual networks server: %w", err)
	}
	privatev1.RegisterVirtualNetworksServer(registrar, privateVirtualNetworksServer)

	// Create the subnets server:
	deps.Logger.InfoContext(ctx, "Creating subnets server")
	subnetsServer, err := servers.NewSubnetsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create subnets server: %w", err)
	}
	publicv1.RegisterSubnetsServer(registrar, subnetsServer)

	// Create the private subnets server:
	deps.Logger.InfoContext(ctx, "Creating private subnets server")
	privateSubnetsServer, err := servers.NewPrivateSubnetsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private subnets server: %w", err)
	}
	privatev1.RegisterSubnetsServer(registrar, privateSubnetsServer)

	// Create the security groups server:
	deps.Logger.InfoContext(ctx, "Creating security groups server")
	securityGroupsServer, err := servers.NewSecurityGroupsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create security groups server: %w", err)
	}
	publicv1.RegisterSecurityGroupsServer(registrar, securityGroupsServer)

	// Create the private security groups server:
	deps.Logger.InfoContext(ctx, "Creating private security groups server")
	privateSecurityGroupsServer, err := servers.NewPrivateSecurityGroupsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private security groups server: %w", err)
	}
	privatev1.RegisterSecurityGroupsServer(registrar, privateSecurityGroupsServer)

	// Create the private network classes server:
	deps.Logger.InfoContext(ctx, "Creating private network classes server")
	privateNetworkClassesServer, err := servers.NewPrivateNetworkClassesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private network classes server: %w", err)
	}
	privatev1.RegisterNetworkClassesServer(registrar, privateNetworkClassesServer)

	// Create the instance types server:
	deps.Logger.InfoContext(ctx, "Creating instance types server")
	instanceTypesServer, err := servers.NewInstanceTypesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create instance types server: %w", err)
	}
	publicv1.RegisterInstanceTypesServer(registrar, instanceTypesServer)

	// Create the private instance types server:
	deps.Logger.InfoContext(ctx, "Creating private instance types server")
	privateInstanceTypesServer, err := servers.NewPrivateInstanceTypesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private instance types server: %w", err)
	}
	privatev1.RegisterInstanceTypesServer(registrar, privateInstanceTypesServer)

	// Create the bare metal instance types server:
	deps.Logger.InfoContext(ctx, "Creating bare metal instance types server")
	bareMetalInstanceTypesServer, err := servers.NewBareMetalInstanceTypesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create bare metal instance types server: %w", err)
	}
	publicv1.RegisterBareMetalInstanceTypesServer(registrar, bareMetalInstanceTypesServer)

	// Create the private bare metal instance types server:
	deps.Logger.InfoContext(ctx, "Creating private bare metal instance types server")
	privateBareMetalInstanceTypesServer, err := servers.NewPrivateBareMetalInstanceTypesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private bare metal instance types server: %w", err)
	}
	privatev1.RegisterBareMetalInstanceTypesServer(registrar, privateBareMetalInstanceTypesServer)

	// Create the cluster versions server:
	deps.Logger.InfoContext(ctx, "Creating cluster versions server")
	clusterVersionsServer, err := servers.NewClusterVersionsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster versions server: %w", err)
	}
	publicv1.RegisterClusterVersionsServer(registrar, clusterVersionsServer)

	// Create the private cluster versions server:
	deps.Logger.InfoContext(ctx, "Creating private cluster versions server")
	privateClusterVersionsServer, err := servers.NewPrivateClusterVersionsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private cluster versions server: %w", err)
	}
	privatev1.RegisterClusterVersionsServer(registrar, privateClusterVersionsServer)

	// Create the private storage backends server:
	deps.Logger.InfoContext(ctx, "Creating private storage backends server")
	privateStorageBackendsServer, err := servers.NewPrivateStorageBackendsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private storage backends server: %w", err)
	}
	privatev1.RegisterStorageBackendsServer(registrar, privateStorageBackendsServer)

	// Create the private secrets server:
	deps.Logger.InfoContext(ctx, "Creating private secrets server")
	privateSecretsServer, err := servers.NewPrivateSecretsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		SetSecretStore(deps.SecretStore).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private secrets server: %w", err)
	}
	privatev1.RegisterSecretsServer(registrar, privateSecretsServer)

	// Create the public secrets server:
	deps.Logger.InfoContext(ctx, "Creating public secrets server")
	publicSecretsServer, err := servers.NewSecretsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		SetSecretStore(deps.SecretStore).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create public secrets server: %w", err)
	}
	publicv1.RegisterSecretsServer(registrar, publicSecretsServer)

	// Create the storage backends DAO for cross-resource validation in the storage tiers server:
	storageBackendsDAO, err := dao.NewGenericDAO[*privatev1.StorageBackend]().
		SetLogger(deps.Logger).
		SetTenancyLogic(deps.TenancyLogic).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create storage backends DAO: %w", err)
	}

	// Create the private storage tiers server:
	deps.Logger.InfoContext(ctx, "Creating private storage tiers server")
	privateStorageTiersServer, err := servers.NewPrivateStorageTiersServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		SetStorageBackendsDAO(storageBackendsDAO).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private storage tiers server: %w", err)
	}
	privatev1.RegisterStorageTiersServer(registrar, privateStorageTiersServer)

	// Create the roles server:
	deps.Logger.InfoContext(ctx, "Creating roles server")
	rolesServer, err := servers.NewRolesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create roles server: %w", err)
	}
	publicv1.RegisterRolesServer(registrar, rolesServer)

	// Create the private roles server:
	deps.Logger.InfoContext(ctx, "Creating private roles server")
	privateRolesServer, err := servers.NewPrivateRolesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private roles server: %w", err)
	}
	privatev1.RegisterRolesServer(registrar, privateRolesServer)

	// Create the role bindings server:
	deps.Logger.InfoContext(ctx, "Creating role bindings server")
	roleBindingsServer, err := servers.NewRoleBindingsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create role bindings server: %w", err)
	}
	publicv1.RegisterRoleBindingsServer(registrar, roleBindingsServer)

	// Create the private role bindings server:
	deps.Logger.InfoContext(ctx, "Creating private role bindings server")
	privateRoleBindingsServer, err := servers.NewPrivateRoleBindingsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private role bindings server: %w", err)
	}
	privatev1.RegisterRoleBindingsServer(registrar, privateRoleBindingsServer)

	// Create the project memberships server:
	deps.Logger.InfoContext(ctx, "Creating project memberships server")
	projectMembershipsServer, err := servers.NewProjectMembershipsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create project memberships server: %w", err)
	}
	publicv1.RegisterProjectMembershipsServer(registrar, projectMembershipsServer)

	// Create the private project memberships server:
	deps.Logger.InfoContext(ctx, "Creating private project memberships server")
	privateProjectMembershipsServer, err := servers.NewPrivateProjectMembershipsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private project memberships server: %w", err)
	}
	privatev1.RegisterProjectMembershipsServer(registrar, privateProjectMembershipsServer)

	// Create the external IP pools server (read-only: List + Get):
	deps.Logger.InfoContext(ctx, "Creating external IP pools server")
	externalIPPoolsServer, err := servers.NewExternalIPPoolsServer().
		SetLogger(deps.Logger).
		SetNotifier(dbNotifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create external IP pools server: %w", err)
	}
	publicv1.RegisterExternalIPPoolsServer(registrar, externalIPPoolsServer)

	// Create the private external IP pools server:
	deps.Logger.InfoContext(ctx, "Creating private external IP pools server")
	privateExternalIPPoolsServer, err := servers.NewPrivateExternalIPPoolsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private external IP pools server: %w", err)
	}
	privatev1.RegisterExternalIPPoolsServer(registrar, privateExternalIPPoolsServer)

	// Create the external IPs server:
	deps.Logger.InfoContext(ctx, "Creating external IPs server")
	externalIPsServer, err := servers.NewExternalIPsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create external IPs server: %w", err)
	}
	publicv1.RegisterExternalIPsServer(registrar, externalIPsServer)

	// Create the private external IPs server:
	deps.Logger.InfoContext(ctx, "Creating private external IPs server")
	privateExternalIPsServer, err := servers.NewPrivateExternalIPsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private external IPs server: %w", err)
	}
	privatev1.RegisterExternalIPsServer(registrar, privateExternalIPsServer)

	// Create the external IP attachments server:
	deps.Logger.InfoContext(ctx, "Creating external IP attachments server")
	externalIPAttachmentsServer, err := servers.NewExternalIPAttachmentsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create external IP attachments server: %w", err)
	}
	publicv1.RegisterExternalIPAttachmentsServer(registrar, externalIPAttachmentsServer)

	// Create the private external IP attachments server:
	deps.Logger.InfoContext(ctx, "Creating private external IP attachments server")
	privateExternalIPAttachmentsServer, err := servers.NewPrivateExternalIPAttachmentsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private external IP attachments server: %w", err)
	}
	privatev1.RegisterExternalIPAttachmentsServer(registrar, privateExternalIPAttachmentsServer)

	// Create the NAT gateways server:
	deps.Logger.InfoContext(ctx, "Creating NAT gateways server")
	natGatewaysServer, err := servers.NewNATGatewaysServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create NAT gateways server: %w", err)
	}
	publicv1.RegisterNATGatewaysServer(registrar, natGatewaysServer)

	// Create the private NAT gateways server:
	deps.Logger.InfoContext(ctx, "Creating private NAT gateways server")
	privateNATGatewaysServer, err := servers.NewPrivateNATGatewaysServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private NAT gateways server: %w", err)
	}
	privatev1.RegisterNATGatewaysServer(registrar, privateNATGatewaysServer)

	// Create the public tenants server:
	deps.Logger.InfoContext(ctx, "Creating public tenants server")
	publicTenantsServer, err := servers.NewTenantsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create public tenants server: %w", err)
	}
	publicv1.RegisterTenantsServer(registrar, publicTenantsServer)

	// Create the default networking provisioner:
	deps.Logger.InfoContext(ctx, "Creating default networking provisioner")
	defaultNetworkingProvisioner, err := servers.NewDefaultNetworkingProvisioner().
		SetLogger(deps.Logger).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create default networking provisioner: %w", err)
	}

	// Create the private tenants server:
	deps.Logger.InfoContext(ctx, "Creating private tenants server")
	privateTenantsServer, err := servers.NewPrivateTenantsServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		SetDefaultNetworkingProvisioner(defaultNetworkingProvisioner).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private tenants server: %w", err)
	}
	privatev1.RegisterTenantsServer(registrar, privateTenantsServer)

	// Create the public identity providers server:
	deps.Logger.InfoContext(ctx, "Creating public identity providers server")
	publicIdentityProvidersServer, err := servers.NewIdentityProvidersServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create public identity providers server: %w", err)
	}
	publicv1.RegisterIdentityProvidersServer(registrar, publicIdentityProvidersServer)

	// Create the private identity providers server:
	deps.Logger.InfoContext(ctx, "Creating private identity providers server")
	privateIdentityProvidersServer, err := servers.NewPrivateIdentityProvidersServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private identity providers server: %w", err)
	}
	privatev1.RegisterIdentityProvidersServer(registrar, privateIdentityProvidersServer)

	// Create the public projects server:
	deps.Logger.InfoContext(ctx, "Creating public projects server")
	publicProjectsServer, err := servers.NewProjectsServer().
		SetLogger(deps.Logger).
		SetNotifier(dbNotifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create public projects server: %w", err)
	}
	publicv1.RegisterProjectsServer(registrar, publicProjectsServer)

	// Create the private projects server:
	deps.Logger.InfoContext(ctx, "Creating private projects server")
	privateProjectsServer, err := servers.NewPrivateProjectsServer().
		SetLogger(deps.Logger).
		SetNotifier(dbNotifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private projects server: %w", err)
	}
	privatev1.RegisterProjectsServer(registrar, privateProjectsServer)

	// Create the private volumes server:
	deps.Logger.InfoContext(ctx, "Creating private volumes server")
	privateVolumesServer, err := servers.NewPrivateVolumesServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PrivateAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		SetTierResolver(deps.TierResolver).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create private volumes server: %w", err)
	}
	privatev1.RegisterVolumesServer(registrar, privateVolumesServer)

	// Create the public users server:
	deps.Logger.InfoContext(ctx, "Creating public users server")
	publicUsersServer, err := servers.NewUsersServer().
		SetLogger(deps.Logger).
		SetNotifier(deps.Notifier).
		SetAttributionLogic(deps.PublicAttributionLogic).
		SetTenancyLogic(deps.TenancyLogic).
		SetMetricsRegisterer(deps.MetricsRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create public users server: %w", err)
	}
	publicv1.RegisterUsersServer(registrar, publicUsersServer)

	// Register the private users server. It's already constructed — see the PrivateUsersServer doc comment.
	privatev1.RegisterUsersServer(registrar, deps.PrivateUsersServer)

	return &ResourceServers{
		PrivateHubsServer:             privateHubsServer,
		PrivateComputeInstancesServer: privateComputeInstancesServer,
	}, nil
}
