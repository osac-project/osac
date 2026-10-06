/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controllers_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/mocks"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Reconciler builder", func() {
	DescribeTable("Validates the name", func(name, errorMessage string) {
		client, err := grpc.NewClient("passthrough:///unused",
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(client.Close)

		reconciler, err := controllers.NewReconciler[*privatev1.Tenant]().
			SetLogger(logger).
			SetName(name).
			SetClient(client).
			SetFunction(func(context.Context, *privatev1.Tenant) error { return nil }).
			Build()
		if errorMessage == "" {
			Expect(err).ToNot(HaveOccurred())
			Expect(reconciler).ToNot(BeNil())
		} else {
			Expect(err).To(MatchError(ContainSubstring(errorMessage)))
			Expect(reconciler).To(BeNil())
		}
	},
		Entry("Single letter", "a", ""),
		Entry("Single digit", "0", ""),
		Entry("Starts with digit", "1-tenant", ""),
		Entry("Hyphens", "compute-instance", ""),
		Entry("Consecutive hyphens", "a--b", ""),
		Entry("Maximum length with group suffix", strings.Repeat("a", 52), ""),
		Entry("Empty", "", "name is mandatory"),
		Entry("Uppercase", "Tenant", "RFC 1123 DNS label"),
		Entry("Underscore", "compute_instance", "RFC 1123 DNS label"),
		Entry("Dot", "compute.instance", "RFC 1123 DNS label"),
		Entry("Leading hyphen", "-tenant", "RFC 1123 DNS label"),
		Entry("Trailing hyphen", "tenant-", "RFC 1123 DNS label"),
		Entry("Only hyphen", "-", "RFC 1123 DNS label"),
		Entry("Space", "tenant reconciler", "RFC 1123 DNS label"),
		Entry("Newline", "tenant\n", "RFC 1123 DNS label"),
		Entry("Non-ASCII", "ténant", "RFC 1123 DNS label"),
		Entry("Group exceeds DNS label limit", strings.Repeat("a", 53), "at most 52 characters"),
		Entry("DNS label leaves no room for suffix", strings.Repeat("a", 63), "at most 52 characters"),
		Entry("Exceeds DNS label limit", strings.Repeat("a", 64), "RFC 1123 DNS label"),
	)
})

var _ = Describe("Reconciler event groups", func() {
	var ctrl *gomock.Controller

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	DescribeTable(
		"Uses stable, distinct and valid group names",
		func(ctx context.Context, name, group string) {
			// Create a mock stream that captures the watch request:
			requests := make(chan *privatev1.EventsWatchRequest, 1)
			stream := mocks.NewMockClientStream(ctrl)
			stream.EXPECT().SendMsg(gomock.Any()).DoAndReturn(
				func(request *privatev1.EventsWatchRequest) error {
					requests <- request
					return nil
				},
			)
			stream.EXPECT().CloseSend().Return(nil)
			stream.EXPECT().RecvMsg(gomock.Any()).DoAndReturn(
				func(any) error {
					return nil
				},
			)

			// Create a gRPC client with an interceptor that replaces the stream with the mock that
			// captures the watch request:
			interceptor := func(context.Context, *grpc.StreamDesc, *grpc.ClientConn, string, grpc.Streamer,
				...grpc.CallOption) (result grpc.ClientStream, err error) {
				result = stream
				return
			}
			credentials := insecure.NewCredentials()
			client, err := grpc.NewClient(
				"passthrough:///unused",
				grpc.WithTransportCredentials(credentials),
				grpc.WithStreamInterceptor(interceptor),
			)
			Expect(err).ToNot(HaveOccurred())
			defer func() {
				err := client.Close()
				Expect(err).ToNot(HaveOccurred())
			}()

			// Create the reconciler:
			reconciler, err := controllers.NewReconciler[*privatev1.Tenant]().
				SetLogger(logger).
				SetName(name).
				SetClient(client).
				SetFunction(
					func(context.Context, *privatev1.Tenant) error {
						return nil
					},
				).
				SetEventFilter("has(event.tenant)").
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the reconciler so that it will at least send the first watch request, we do not care
			// about any other thing that the reconciler does, or any error that it returns.
			go func() {
				defer GinkgoRecover()
				_ = reconciler.Start(ctx)
			}()

			// Verify that the watch request contains the expected group name:
			var request *privatev1.EventsWatchRequest
			Eventually(ctx, requests).Should(Receive(&request))
			Expect(request.GetGroup()).To(Equal(group))
		},
		Entry(
			"Tenant controller",
			"tenant",
			"tenant-reconciler",
		),
		Entry(
			"Onboarding controller",
			"onboarding",
			"onboarding-reconciler",
		),
		Entry(
			"Compute instance controller",
			"compute-instance",
			"compute-instance-reconciler",
		),
		Entry(
			"Bare metal instance controller",
			"bare-metal-instance",
			"bare-metal-instance-reconciler",
		),
		Entry(
			"Maximum length group",
			strings.Repeat("a", 52),
			strings.Repeat("a", 52)+"-reconciler",
		),
	)
})
