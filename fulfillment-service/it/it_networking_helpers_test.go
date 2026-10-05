/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func cleanupNetworkFixtureResource(
	ctx context.Context,
	deleteResource func(context.Context) error,
	getMetadata func(context.Context) (*privatev1.Metadata, error),
	clearFinalizers func(context.Context) error,
) error {
	if err := deleteResource(ctx); err != nil && grpcstatus.Code(err) != grpccodes.NotFound {
		return err
	}
	metadata, err := getMetadata(ctx)
	if grpcstatus.Code(err) == grpccodes.NotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if !metadata.HasDeletionTimestamp() || len(metadata.GetFinalizers()) == 0 {
		return nil
	}
	if err := clearFinalizers(ctx); err != nil {
		return err
	}
	if _, err := getMetadata(ctx); grpcstatus.Code(err) != grpccodes.NotFound {
		if err != nil {
			return err
		}
		return fmt.Errorf("resource remains after clearing fixture finalizers")
	}
	return nil
}

func cleanupNetworkClassFixture(ctx context.Context, client privatev1.NetworkClassesClient, id string) error {
	return cleanupNetworkFixtureResource(ctx,
		func(ctx context.Context) error {
			_, err := client.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{Id: id}.Build())
			return err
		},
		func(ctx context.Context) (*privatev1.Metadata, error) {
			response, err := client.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			return response.GetObject().GetMetadata(), nil
		},
		func(ctx context.Context) error {
			response, err := client.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: id}.Build())
			if err != nil {
				return err
			}
			object := response.GetObject()
			object.GetMetadata().SetFinalizers(nil)
			_, err = client.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
				Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}}, Lock: true,
			}.Build())
			return err
		})
}

func cleanupVirtualNetworkFixture(ctx context.Context, client privatev1.VirtualNetworksClient, id string) error {
	return cleanupNetworkFixtureResource(ctx,
		func(ctx context.Context) error {
			_, err := client.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: id}.Build())
			return err
		},
		func(ctx context.Context) (*privatev1.Metadata, error) {
			response, err := client.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			return response.GetObject().GetMetadata(), nil
		},
		func(ctx context.Context) error {
			response, err := client.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
			if err != nil {
				return err
			}
			object := response.GetObject()
			object.GetMetadata().SetFinalizers(nil)
			_, err = client.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
				Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}}, Lock: true,
			}.Build())
			return err
		})
}

func waitForNetworkClassReady(
	ctx context.Context,
	client privatev1.NetworkClassesClient,
	id string,
) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		response, err := client.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: id}.Build())
		g.Expect(err).NotTo(HaveOccurred())
		object := response.GetObject()
		g.Expect(object.GetStatus().GetState()).To(
			Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY),
			"NetworkClass %q did not become ready: %s",
			id,
			object.GetStatus().GetMessage(),
		)
	}, time.Minute, time.Second).Should(Succeed())
}
