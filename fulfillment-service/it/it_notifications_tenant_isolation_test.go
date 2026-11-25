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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Notifications tenant isolation", Label("notifications"), func() {
	It("keeps tenant-scoped inbox and announcements invisible across tenants", func(ctx context.Context) {
		tenantsClient := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
		privateClient := privatev1.NewNotificationsClient(tool.InternalView().AdminConn())

		nameA := fmt.Sprintf("test-%s", uuid.New())
		idA := createTenant(ctx, tenantsClient, nameA)
		nameB := fmt.Sprintf("test-%s", uuid.New())
		idB := createTenant(ctx, tenantsClient, nameB)

		_, tokenSourceA := loginAsBreakGlass(ctx, tenantsClient, nameA, idA)
		extConnA, err := tool.makeGrpcConn(externalServiceAddr, tokenSourceA)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = extConnA.Close() })

		_, tokenSourceB := loginAsBreakGlass(ctx, tenantsClient, nameB, idB)
		extConnB, err := tool.makeGrpcConn(externalServiceAddr, tokenSourceB)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = extConnB.Close() })

		publicA := publicv1.NewNotificationsClient(extConnA)
		publicB := publicv1.NewNotificationsClient(extConnB)

		// Break-glass alone is not tenant-admin; seed via private API. Public Create
		// authz is covered by the Notifications API suite.
		announcement, err := privateClient.Create(ctx, privatev1.NotificationsCreateRequest_builder{
			Object: privatev1.Notification_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("ann-a-%s", uuid.New()[:8]),
					Tenant: nameA,
				}.Build(),
				Spec: privatev1.NotificationSpec_builder{
					Kind:     privatev1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT,
					Severity: privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
					Type:     "osac.notification.announcement.maintenance",
					Scope:    privatev1.NotificationScope_NOTIFICATION_SCOPE_TENANT,
					Title:    "Tenant A only",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		annID := announcement.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, _ = privateClient.Delete(ctx, privatev1.NotificationsDeleteRequest_builder{Id: annID}.Build())
		})

		inbox, err := privateClient.Create(ctx, privatev1.NotificationsCreateRequest_builder{
			Object: privatev1.Notification_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("inbox-a-%s", uuid.New()[:8]),
					Tenant: nameA,
				}.Build(),
				Spec: privatev1.NotificationSpec_builder{
					Kind:        privatev1.NotificationKind_NOTIFICATION_KIND_EVENT,
					Severity:    privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
					Type:        "osac.notification.event.resource_created",
					Source:      "osac-fulfillment",
					RecipientId: fmt.Sprintf("%s-osac-break-glass", nameA),
					Title:       "Tenant A inbox",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		inboxID := inbox.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, _ = privateClient.Delete(ctx, privatev1.NotificationsDeleteRequest_builder{Id: inboxID}.Build())
		})

		listA, err := publicA.List(ctx, &publicv1.NotificationsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(containsId(listA.GetItems(), annID)).To(BeTrue())
		Expect(containsId(listA.GetItems(), inboxID)).To(BeTrue())

		listB, err := publicB.List(ctx, &publicv1.NotificationsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(containsId(listB.GetItems(), annID)).To(BeFalse())
		Expect(containsId(listB.GetItems(), inboxID)).To(BeFalse())

		_, err = publicB.Get(ctx, publicv1.NotificationsGetRequest_builder{Id: annID}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
		_, err = publicB.Get(ctx, publicv1.NotificationsGetRequest_builder{Id: inboxID}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
	})
})
