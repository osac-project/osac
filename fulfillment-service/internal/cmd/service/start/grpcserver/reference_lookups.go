/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package grpcserver

import (
	"fmt"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/references"
)

func registerReferenceLookups(
	validator *references.ReferenceValidator,
	logger *slog.Logger,
	tenancyLogic auth.TenancyLogic,
	metricsRegisterer prometheus.Registerer,
) error {
	// Networking references
	virtualNetworksDAO, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create VirtualNetwork DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.VirtualNetworkLocalReference", virtualNetworksDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.VirtualNetworkLocalReference", virtualNetworksDAO)

	networkClassesDAO, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create NetworkClass DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.NetworkClassReference", networkClassesDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.NetworkClassReference", networkClassesDAO)

	subnetsDAO, err := dao.NewGenericDAO[*privatev1.Subnet]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create Subnet DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.SubnetLocalReference", subnetsDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.SubnetLocalReference", subnetsDAO)

	securityGroupsDAO, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create SecurityGroup DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.SecurityGroupLocalReference", securityGroupsDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.SecurityGroupLocalReference", securityGroupsDAO)

	externalIPsDAO, err := dao.NewGenericDAO[*privatev1.ExternalIP]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create ExternalIP DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.ExternalIPLocalReference", externalIPsDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.ExternalIPLocalReference", externalIPsDAO)

	externalIPPoolsDAO, err := dao.NewGenericDAO[*privatev1.ExternalIPPool]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create ExternalIPPool DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.ExternalIPPoolReference", externalIPPoolsDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.ExternalIPPoolReference", externalIPPoolsDAO)

	// Compute references
	computeInstancesDAO, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create ComputeInstance DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.ComputeInstanceLocalReference", computeInstancesDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.ComputeInstanceLocalReference", computeInstancesDAO)

	computeInstanceTemplatesDAO, err := dao.NewGenericDAO[*privatev1.ComputeInstanceTemplate]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create ComputeInstanceTemplate DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.ComputeInstanceTemplateReference", computeInstanceTemplatesDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.ComputeInstanceTemplateReference", computeInstanceTemplatesDAO)

	computeInstanceCatalogItemsDAO, err := dao.NewGenericDAO[*privatev1.ComputeInstanceCatalogItem]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create ComputeInstanceCatalogItem DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.ComputeInstanceCatalogItemReference", computeInstanceCatalogItemsDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.ComputeInstanceCatalogItemReference", computeInstanceCatalogItemsDAO)

	instanceTypesDAO, err := dao.NewGenericDAO[*privatev1.InstanceType]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create InstanceType DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.InstanceTypeReference", instanceTypesDAO)
	references.RegisterDAOLookup(validator, "osac.private.v1.InstanceTypeLocalReference", instanceTypesDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.InstanceTypeReference", instanceTypesDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.InstanceTypeLocalReference", instanceTypesDAO)

	// Cluster and bare metal references
	clustersDAO, err := dao.NewGenericDAO[*privatev1.Cluster]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create Cluster DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.ClusterLocalReference", clustersDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.ClusterLocalReference", clustersDAO)

	clusterTemplatesDAO, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create ClusterTemplate DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.ClusterTemplateReference", clusterTemplatesDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.ClusterTemplateReference", clusterTemplatesDAO)

	clusterCatalogItemsDAO, err := dao.NewGenericDAO[*privatev1.ClusterCatalogItem]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create ClusterCatalogItem DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.ClusterCatalogItemReference", clusterCatalogItemsDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.ClusterCatalogItemReference", clusterCatalogItemsDAO)

	hostTypesDAO, err := dao.NewGenericDAO[*privatev1.HostType]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create HostType DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.HostTypeReference", hostTypesDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.HostTypeReference", hostTypesDAO)

	bareMetalInstancesDAO, err := dao.NewGenericDAO[*privatev1.BareMetalInstance]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create BareMetalInstance DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.BareMetalInstanceLocalReference", bareMetalInstancesDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.BareMetalInstanceLocalReference", bareMetalInstancesDAO)

	bareMetalInstanceCatalogItemsDAO, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceCatalogItem]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create BareMetalInstanceCatalogItem DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.BareMetalInstanceCatalogItemReference", bareMetalInstanceCatalogItemsDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.BareMetalInstanceCatalogItemReference", bareMetalInstanceCatalogItemsDAO)

	bareMetalInstanceTemplatesDAO, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceTemplate]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create BareMetalInstanceTemplate DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.BareMetalInstanceTemplateReference", bareMetalInstanceTemplatesDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.BareMetalInstanceTemplateReference", bareMetalInstanceTemplatesDAO)

	// IAM references
	rolesDAO, err := dao.NewGenericDAO[*privatev1.Role]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create Role DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.RoleReference", rolesDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.RoleReference", rolesDAO)

	refUsersDAO, err := dao.NewGenericDAO[*privatev1.User]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create User DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.UserReference", refUsersDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.UserReference", refUsersDAO)

	// Cluster version references
	clusterVersionsDAO, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create ClusterVersion DAO for reference lookups: %w", err)
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.ClusterVersionReference", clusterVersionsDAO)
	references.RegisterDAOLookup(validator, "osac.public.v1.ClusterVersionReference", clusterVersionsDAO)

	return nil
}
