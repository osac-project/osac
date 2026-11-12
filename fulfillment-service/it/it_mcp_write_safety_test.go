/*
Copyright (c) 2026 Red Hat Inc.

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

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type mcpComputeInstancesFaultClient struct {
	publicv1.ComputeInstancesClient
	createCalls        int
	getCalls           int
	dropCreateResponse bool
	failGet            bool
	lastCreateID       string
}

func (c *mcpComputeInstancesFaultClient) Create(
	ctx context.Context,
	request *publicv1.ComputeInstancesCreateRequest,
	options ...grpc.CallOption,
) (*publicv1.ComputeInstancesCreateResponse, error) {
	c.createCalls++
	c.lastCreateID = request.GetObject().GetId()
	response, err := c.ComputeInstancesClient.Create(ctx, request, options...)
	if err != nil {
		return nil, err
	}
	if c.dropCreateResponse {
		return nil, status.Error(codes.Unavailable, "test injected Create response loss")
	}
	return response, nil
}

func (c *mcpComputeInstancesFaultClient) Get(
	ctx context.Context,
	request *publicv1.ComputeInstancesGetRequest,
	options ...grpc.CallOption,
) (*publicv1.ComputeInstancesGetResponse, error) {
	c.getCalls++
	if c.failGet {
		return nil, status.Error(codes.Unavailable, "test injected Get failure")
	}
	return c.ComputeInstancesClient.Get(ctx, request, options...)
}

func deferMCPComputeInstanceCleanup(ctx context.Context, id string) {
	GinkgoHelper()
	DeferCleanup(func() {
		client := privatev1.NewComputeInstancesClient(tool.InternalView().AdminConn())
		_, err := client.Delete(ctx, privatev1.ComputeInstancesDeleteRequest_builder{Id: id}.Build())
		if status.Code(err) != codes.NotFound {
			Expect(err).ToNot(HaveOccurred())
		}
		waitForMCPComputeInstanceDeletion(ctx, id)
	})
}
