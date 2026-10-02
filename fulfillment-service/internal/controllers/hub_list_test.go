/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package controllers

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("ListAllHubs", func() {
	var (
		ctx    context.Context
		client *MockHubsClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		client = NewMockHubsClient(gomock.NewController(GinkgoT()))
	})

	It("collects every page with a bounded page size", func() {
		client.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, request *privatev1.HubsListRequest, _ ...grpc.CallOption) (*privatev1.HubsListResponse, error) {
				Expect(request.GetOffset()).To(BeZero())
				Expect(request.GetLimit()).To(Equal(int32(100)))
				return &privatev1.HubsListResponse{
					Total: 2, Size: 1, Items: []*privatev1.Hub{privatev1.Hub_builder{Id: "hub-a"}.Build()},
				}, nil
			})
		client.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, request *privatev1.HubsListRequest, _ ...grpc.CallOption) (*privatev1.HubsListResponse, error) {
				Expect(request.GetOffset()).To(Equal(int32(1)))
				Expect(request.GetLimit()).To(Equal(int32(100)))
				return &privatev1.HubsListResponse{
					Total: 2, Size: 1, Items: []*privatev1.Hub{privatev1.Hub_builder{Id: "hub-b"}.Build()},
				}, nil
			})

		hubs, err := ListAllHubs(ctx, client)

		Expect(err).NotTo(HaveOccurred())
		Expect(hubs).To(HaveLen(2))
		Expect(hubs[0].GetId()).To(Equal("hub-a"))
		Expect(hubs[1].GetId()).To(Equal("hub-b"))
	})

	It("rejects an incomplete page rather than returning a partial hub catalog", func() {
		client.EXPECT().List(gomock.Any(), gomock.Any()).Return(&privatev1.HubsListResponse{Total: 2}, nil)

		hubs, err := ListAllHubs(ctx, client)

		Expect(hubs).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("incomplete page")))
	})

	It("rejects a response whose page size disagrees with the returned items", func() {
		client.EXPECT().List(gomock.Any(), gomock.Any()).Return(&privatev1.HubsListResponse{
			Total: 3, Size: 2, Items: []*privatev1.Hub{privatev1.Hub_builder{Id: "hub-a"}.Build()},
		}, nil)

		hubs, err := ListAllHubs(ctx, client)

		Expect(hubs).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("inconsistent page size")))
	})

	It("rejects a catalog total that changes while paging", func() {
		client.EXPECT().List(gomock.Any(), gomock.Any()).Return(&privatev1.HubsListResponse{
			Total: 2, Size: 1, Items: []*privatev1.Hub{privatev1.Hub_builder{Id: "hub-a"}.Build()},
		}, nil)
		client.EXPECT().List(gomock.Any(), gomock.Any()).Return(&privatev1.HubsListResponse{
			Total: 3, Size: 1, Items: []*privatev1.Hub{privatev1.Hub_builder{Id: "hub-b"}.Build()},
		}, nil)

		hubs, err := ListAllHubs(ctx, client)

		Expect(hubs).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("total changed")))
	})
})
