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
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
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

const computeInstanceFixtureProbeTimeout = 10 * time.Second

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
		probeCtx, cancel := context.WithTimeout(ctx, computeInstanceFixtureProbeTimeout)
		defer cancel()
		_, err := client.Get(probeCtx, privatev1.StorageBackendsGetRequest_builder{Id: id}.Build())
		g.Expect(err).ToNot(HaveOccurred())
	}, time.Minute, time.Second).Should(Succeed())
}

func waitForComputeInstanceFixtureResource(ctx context.Context, get func(context.Context) error) {
	Eventually(func(g Gomega) {
		probeCtx, cancel := context.WithTimeout(ctx, computeInstanceFixtureProbeTimeout)
		defer cancel()
		g.Expect(get(probeCtx)).ToNot(HaveOccurred())
	}, time.Minute, time.Second).Should(Succeed())
}

func setComputeInstanceFixtureVirtualNetworkReady(ctx context.Context, client privatev1.VirtualNetworksClient, id string) {
	Eventually(func(g Gomega) {
		probeCtx, cancel := context.WithTimeout(ctx, computeInstanceFixtureProbeTimeout)
		defer cancel()
		resp, err := client.Get(probeCtx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(resp.GetObject().GetStatus().GetState()).To(
			Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
	}, time.Minute, time.Second).Should(Succeed())

	getCtx, cancel := context.WithTimeout(ctx, computeInstanceFixtureProbeTimeout)
	resp, err := client.Get(getCtx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
	cancel()
	Expect(err).ToNot(HaveOccurred())
	virtualNetwork := resp.GetObject()
	virtualNetwork.SetStatus(privatev1.VirtualNetworkStatus_builder{
		State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
	}.Build())
	updateCtx, cancel := context.WithTimeout(ctx, computeInstanceFixtureProbeTimeout)
	_, err = client.Update(updateCtx, privatev1.VirtualNetworksUpdateRequest_builder{
		Object:     virtualNetwork,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
	}.Build())
	cancel()
	Expect(err).ToNot(HaveOccurred())
}

func cleanupComputeInstanceFixture(
	ctx context.Context,
	clients computeInstanceFixtureClients,
	computeInstanceID, resizeInstanceTypeID, instanceTypeID, subnetID, virtualNetworkID,
	networkClassID, computeInstanceTemplateID, diskImageID, storageTierID, storageBackendID string,
) {
	if computeInstanceID != "" {
		deleteAndWaitForComputeInstanceFixtureResource(ctx,
			func(deleteCtx context.Context) error {
				_, err := clients.computeInstances.Delete(deleteCtx, publicv1.ComputeInstancesDeleteRequest_builder{Id: computeInstanceID}.Build())
				return err
			},
			func(getCtx context.Context) error {
				_, err := clients.computeInstances.Get(getCtx, publicv1.ComputeInstancesGetRequest_builder{Id: computeInstanceID}.Build())
				return err
			})
	}
	if resizeInstanceTypeID != "" {
		deleteAndWaitForComputeInstanceFixtureResource(ctx,
			func(deleteCtx context.Context) error {
				_, err := clients.instanceTypes.Delete(deleteCtx, privatev1.InstanceTypesDeleteRequest_builder{Id: resizeInstanceTypeID}.Build())
				return err
			},
			func(getCtx context.Context) error {
				_, err := clients.instanceTypes.Get(getCtx, privatev1.InstanceTypesGetRequest_builder{Id: resizeInstanceTypeID}.Build())
				return err
			})
	}
	if instanceTypeID != "" {
		deleteAndWaitForComputeInstanceFixtureResource(ctx,
			func(deleteCtx context.Context) error {
				_, err := clients.instanceTypes.Delete(deleteCtx, privatev1.InstanceTypesDeleteRequest_builder{Id: instanceTypeID}.Build())
				return err
			},
			func(getCtx context.Context) error {
				_, err := clients.instanceTypes.Get(getCtx, privatev1.InstanceTypesGetRequest_builder{Id: instanceTypeID}.Build())
				return err
			})
	}
	if subnetID != "" {
		deleteComputeInstanceFixtureResource(ctx,
			func(deleteCtx context.Context) error {
				_, err := clients.subnets.Delete(deleteCtx, privatev1.SubnetsDeleteRequest_builder{Id: subnetID}.Build())
				return err
			})
	}
	if virtualNetworkID != "" {
		// VirtualNetworks may have an operator finalizer, so Delete can acknowledge the
		// soft delete while Get continues returning the object until asynchronous cleanup
		// completes. The deletion timestamp is enough to release this fixture's dependency
		// before deleting its NetworkClass.
		deleteComputeInstanceFixtureResource(ctx,
			func(deleteCtx context.Context) error {
				_, err := clients.virtualNetworks.Delete(deleteCtx, privatev1.VirtualNetworksDeleteRequest_builder{Id: virtualNetworkID}.Build())
				return err
			})
	}
	if networkClassID != "" {
		deleteAndWaitForComputeInstanceFixtureResource(ctx,
			func(deleteCtx context.Context) error {
				_, err := clients.networkClasses.Delete(deleteCtx, privatev1.NetworkClassesDeleteRequest_builder{Id: networkClassID}.Build())
				return err
			},
			func(getCtx context.Context) error {
				_, err := clients.networkClasses.Get(getCtx, privatev1.NetworkClassesGetRequest_builder{Id: networkClassID}.Build())
				return err
			})
	}
	if computeInstanceTemplateID != "" {
		deleteAndWaitForComputeInstanceFixtureResource(ctx,
			func(deleteCtx context.Context) error {
				_, err := clients.computeInstanceTemplates.Delete(deleteCtx, privatev1.ComputeInstanceTemplatesDeleteRequest_builder{Id: computeInstanceTemplateID}.Build())
				return err
			},
			func(getCtx context.Context) error {
				_, err := clients.computeInstanceTemplates.Get(getCtx, privatev1.ComputeInstanceTemplatesGetRequest_builder{Id: computeInstanceTemplateID}.Build())
				return err
			})
	}
	if diskImageID != "" {
		deleteAndWaitForComputeInstanceFixtureResource(ctx,
			func(deleteCtx context.Context) error {
				_, err := clients.diskImages.Delete(deleteCtx, privatev1.DiskImagesDeleteRequest_builder{Id: diskImageID}.Build())
				return err
			},
			func(getCtx context.Context) error {
				_, err := clients.diskImages.Get(getCtx, privatev1.DiskImagesGetRequest_builder{Id: diskImageID}.Build())
				return err
			})
	}
	if storageTierID != "" {
		deleteAndWaitForComputeInstanceFixtureResource(ctx,
			func(deleteCtx context.Context) error {
				_, err := clients.storageTiers.Delete(deleteCtx, privatev1.StorageTiersDeleteRequest_builder{Id: storageTierID}.Build())
				return err
			},
			func(getCtx context.Context) error {
				_, err := clients.storageTiers.Get(getCtx, privatev1.StorageTiersGetRequest_builder{Id: storageTierID}.Build())
				return err
			})
	}
	if storageBackendID != "" {
		deleteAndWaitForComputeInstanceFixtureResource(ctx,
			func(deleteCtx context.Context) error {
				_, err := clients.storageBackends.Delete(deleteCtx, privatev1.StorageBackendsDeleteRequest_builder{Id: storageBackendID}.Build())
				return err
			},
			func(getCtx context.Context) error {
				_, err := clients.storageBackends.Get(getCtx, privatev1.StorageBackendsGetRequest_builder{Id: storageBackendID}.Build())
				return err
			})
	}
}

func deleteAndWaitForComputeInstanceFixtureResource(ctx context.Context, delete func(context.Context) error, get func(context.Context) error) {
	deleteCtx, cancel := context.WithTimeout(ctx, computeInstanceFixtureProbeTimeout)
	err := delete(deleteCtx)
	cancel()
	if !expectFixtureDelete(err) {
		return
	}
	if err != nil {
		return
	}
	Eventually(func(g Gomega) {
		probeCtx, cancel := context.WithTimeout(ctx, computeInstanceFixtureProbeTimeout)
		defer cancel()
		g.Expect(grpcstatus.Code(get(probeCtx))).To(Equal(grpccodes.NotFound))
	}, time.Minute, time.Second).Should(Succeed())
}

func deleteComputeInstanceFixtureResource(ctx context.Context, delete func(context.Context) error) {
	deleteCtx, cancel := context.WithTimeout(ctx, computeInstanceFixtureProbeTimeout)
	err := delete(deleteCtx)
	cancel()
	expectFixtureDelete(err)
}

func expectFixtureDelete(err error) bool {
	ok := err == nil || grpcstatus.Code(err) == grpccodes.NotFound
	Expect(ok).To(BeTrue(),
		"fixture cleanup delete failed: %v", err)
	return ok
}
