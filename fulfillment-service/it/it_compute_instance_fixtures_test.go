/*
Copyright (c) 2025 Red Hat Inc.

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

package it

import (
	"context"
	"time"

	. "github.com/onsi/gomega"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type computeInstanceFixtureClients struct {
	subnets                  privatev1.SubnetsClient
	virtualNetworks          privatev1.VirtualNetworksClient
	networkClasses           privatev1.NetworkClassesClient
	computeInstances         publicv1.ComputeInstancesClient
	computeInstanceTemplates privatev1.ComputeInstanceTemplatesClient
	instanceTypes            privatev1.InstanceTypesClient
	storageTiers             privatev1.StorageTiersClient
	storageBackends          privatev1.StorageBackendsClient
	diskImages               privatev1.DiskImagesClient
}

func newComputeInstanceFixtureClients() computeInstanceFixtureClients {
	return computeInstanceFixtureClients{
		subnets:                  privatev1.NewSubnetsClient(tool.InternalView().AdminConn()),
		virtualNetworks:          privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn()),
		networkClasses:           privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn()),
		computeInstances:         publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn()),
		computeInstanceTemplates: privatev1.NewComputeInstanceTemplatesClient(tool.InternalView().AdminConn()),
		instanceTypes:            privatev1.NewInstanceTypesClient(tool.InternalView().AdminConn()),
		storageTiers:             privatev1.NewStorageTiersClient(tool.InternalView().AdminConn()),
		storageBackends:          privatev1.NewStorageBackendsClient(tool.InternalView().AdminConn()),
		diskImages:               privatev1.NewDiskImagesClient(tool.InternalView().AdminConn()),
	}
}

func waitForComputeInstanceFixtureStorageBackend(ctx context.Context, client privatev1.StorageBackendsClient, id string) {
	Eventually(func(g Gomega) {
		_, err := client.Get(ctx, privatev1.StorageBackendsGetRequest_builder{Id: id}.Build())
		g.Expect(err).ToNot(HaveOccurred())
	}, time.Minute, time.Second).Should(Succeed())
}

func cleanupComputeInstanceFixture(
	ctx context.Context,
	clients computeInstanceFixtureClients,
	computeInstanceID, resizeInstanceTypeID, instanceTypeID, subnetID, virtualNetworkID,
	networkClassID, computeInstanceTemplateID, diskImageID, storageTierID, storageBackendID string,
) {
	if computeInstanceID != "" {
		clients.computeInstances.Delete(ctx, publicv1.ComputeInstancesDeleteRequest_builder{Id: computeInstanceID}.Build())
	}
	if resizeInstanceTypeID != "" {
		clients.instanceTypes.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{Id: resizeInstanceTypeID}.Build())
	}
	if instanceTypeID != "" {
		clients.instanceTypes.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{Id: instanceTypeID}.Build())
	}
	if subnetID != "" {
		clients.subnets.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{Id: subnetID}.Build())
	}
	if virtualNetworkID != "" {
		clients.virtualNetworks.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: virtualNetworkID}.Build())
	}
	if networkClassID != "" {
		clients.networkClasses.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{Id: networkClassID}.Build())
	}
	if computeInstanceTemplateID != "" {
		clients.computeInstanceTemplates.Delete(ctx, privatev1.ComputeInstanceTemplatesDeleteRequest_builder{Id: computeInstanceTemplateID}.Build())
	}
	if diskImageID != "" {
		clients.diskImages.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{Id: diskImageID}.Build())
	}
	if storageTierID != "" {
		clients.storageTiers.Delete(ctx, privatev1.StorageTiersDeleteRequest_builder{Id: storageTierID}.Build())
	}
	if storageBackendID != "" {
		clients.storageBackends.Delete(ctx, privatev1.StorageBackendsDeleteRequest_builder{Id: storageBackendID}.Build())
	}
}
