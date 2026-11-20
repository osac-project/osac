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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type stubNotificationResourceAccess struct {
	allow map[string]bool
	err   error
}

func (s *stubNotificationResourceAccess) MayAccess(_ context.Context,
	resource *privatev1.NotificationResourceReference) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	if resource == nil {
		return true, nil
	}
	id := resource.GetCluster().GetId()
	if id == "" {
		id = resource.GetComputeInstance().GetId()
	}
	if allowed, ok := s.allow[id]; ok {
		return allowed, nil
	}
	return false, nil
}

func (s *stubNotificationResourceAccess) MayAccessMany(ctx context.Context,
	resources []*privatev1.NotificationResourceReference) ([]bool, error) {
	out := make([]bool, len(resources))
	for i, resource := range resources {
		ok, err := s.MayAccess(ctx, resource)
		if err != nil {
			return nil, err
		}
		out[i] = ok
	}
	return out, nil
}

var _ = Describe("Notifications server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewNotificationsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewNotificationsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewNotificationsServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewNotificationsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *NotificationsServer
		var privateServer *PrivateNotificationsServer
		const callerUser = "user-1"

		BeforeEach(func() {
			ctx = auth.ContextWithSubject(ctx, &auth.Subject{
				User:    callerUser,
				Tenants: collections.NewSet(testTenant),
			})

			var err error
			server, err = NewNotificationsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			privateServer, err = NewPrivateNotificationsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		inboxObject := func(recipient, eventID string) *privatev1.Notification {
			return privatev1.Notification_builder{
				Metadata: privatev1.Metadata_builder{Name: "inbox-" + recipient + "-" + eventID}.Build(),
				Spec: privatev1.NotificationSpec_builder{
					Kind:     privatev1.NotificationKind_NOTIFICATION_KIND_EVENT,
					Severity: privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
					Type:     "osac.notification.event.resource_created",
					Event: privatev1.NotificationEventReference_builder{
						Id:     eventID,
						Source: "osac/fulfillment-service",
					}.Build(),
					RecipientId: recipient,
					Title:       "Resource ready",
				}.Build(),
			}.Build()
		}

		It("Creates a tenant announcement", func() {
			response, err := server.Create(ctx, publicv1.NotificationsCreateRequest_builder{
				Object: publicv1.Notification_builder{
					Metadata: publicv1.Metadata_builder{Name: "maint"}.Build(),
					Spec: publicv1.NotificationSpec_builder{
						Kind:     publicv1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT,
						Severity: publicv1.NotificationSeverity_NOTIFICATION_SEVERITY_WARNING,
						Type:     "osac.notification.announcement",
						Scope:    publicv1.NotificationScope_NOTIFICATION_SCOPE_TENANT,
						Title:    "Maintenance",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			created := response.GetObject()
			Expect(created.GetId()).ToNot(BeEmpty())
			Expect(created.GetSpec().GetKind()).To(Equal(
				publicv1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT))
			Expect(created.GetSpec().GetScope()).To(Equal(
				publicv1.NotificationScope_NOTIFICATION_SCOPE_TENANT))
			Expect(created.GetMetadata().GetTenant()).To(Equal(testTenant))
		})

		It("Rejects non-announcement public Create", func() {
			_, err := server.Create(ctx, publicv1.NotificationsCreateRequest_builder{
				Object: publicv1.Notification_builder{
					Metadata: publicv1.Metadata_builder{Name: "inbox"}.Build(),
					Spec: publicv1.NotificationSpec_builder{
						Kind:     publicv1.NotificationKind_NOTIFICATION_KIND_EVENT,
						Severity: publicv1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
						Title:    "Resource ready",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("Rejects announcement Create without scope", func() {
			_, err := server.Create(ctx, publicv1.NotificationsCreateRequest_builder{
				Object: publicv1.Notification_builder{
					Metadata: publicv1.Metadata_builder{Name: "noscope"}.Build(),
					Spec: publicv1.NotificationSpec_builder{
						Kind:     publicv1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT,
						Severity: publicv1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
						Title:    "x",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("Omits List items and hides Get when resource access is denied", func() {
			access := &stubNotificationResourceAccess{allow: map[string]bool{"cluster-denied": false}}
			server, err := NewNotificationsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetResourceAccess(access).
				Build()
			Expect(err).ToNot(HaveOccurred())

			created, err := privateServer.Create(ctx, privatev1.NotificationsCreateRequest_builder{
				Object: privatev1.Notification_builder{
					Metadata: privatev1.Metadata_builder{Name: "gated-inbox"}.Build(),
					Spec: privatev1.NotificationSpec_builder{
						Kind:     privatev1.NotificationKind_NOTIFICATION_KIND_EVENT,
						Severity: privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
						Type:     "osac.notification.event.resource_created",
						Event: privatev1.NotificationEventReference_builder{
							Id:     "evt-gated",
							Source: "osac/fulfillment-service",
						}.Build(),
						RecipientId: callerUser,
						Title:       "secret title",
						Resource: privatev1.NotificationResourceReference_builder{
							Cluster: privatev1.ClusterLocalReference_builder{Id: "cluster-denied"}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			list, err := server.List(ctx, &publicv1.NotificationsListRequest{})
			Expect(err).ToNot(HaveOccurred())
			for _, item := range list.GetItems() {
				Expect(item.GetId()).ToNot(Equal(created.GetObject().GetId()))
				Expect(item.GetSpec().GetTitle()).ToNot(Equal("secret title"))
			}

			_, err = server.Get(ctx, publicv1.NotificationsGetRequest_builder{
				Id: created.GetObject().GetId(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
		})

		It("Fails closed on Get when resource access check errors", func() {
			access := &stubNotificationResourceAccess{err: errors.New("checker unavailable")}
			server, err := NewNotificationsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetResourceAccess(access).
				Build()
			Expect(err).ToNot(HaveOccurred())

			created, err := privateServer.Create(ctx, privatev1.NotificationsCreateRequest_builder{
				Object: privatev1.Notification_builder{
					Metadata: privatev1.Metadata_builder{Name: "err-gated"}.Build(),
					Spec: privatev1.NotificationSpec_builder{
						Kind:     privatev1.NotificationKind_NOTIFICATION_KIND_EVENT,
						Severity: privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
						Type:     "osac.notification.event.resource_created",
						Event: privatev1.NotificationEventReference_builder{
							Id:     "evt-err",
							Source: "osac/fulfillment-service",
						}.Build(),
						RecipientId: callerUser,
						Title:       "hidden",
						Resource: privatev1.NotificationResourceReference_builder{
							Cluster: privatev1.ClusterLocalReference_builder{Id: "cluster-x"}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Get(ctx, publicv1.NotificationsGetRequest_builder{
				Id: created.GetObject().GetId(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
		})

		It("Hides other users' inbox rows from List/Get/Update/Delete", func() {
			other, err := privateServer.Create(ctx, privatev1.NotificationsCreateRequest_builder{
				Object: inboxObject("other-user", "evt-other"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			otherID := other.GetObject().GetId()

			own, err := privateServer.Create(ctx, privatev1.NotificationsCreateRequest_builder{
				Object: inboxObject(callerUser, "evt-own"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			list, err := server.List(ctx, &publicv1.NotificationsListRequest{})
			Expect(err).ToNot(HaveOccurred())
			ids := make([]string, 0, len(list.GetItems()))
			for _, item := range list.GetItems() {
				ids = append(ids, item.GetId())
			}
			Expect(ids).To(ContainElement(own.GetObject().GetId()))
			Expect(ids).ToNot(ContainElement(otherID))

			_, err = server.Get(ctx, publicv1.NotificationsGetRequest_builder{Id: otherID}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))

			_, err = server.Update(ctx, publicv1.NotificationsUpdateRequest_builder{
				Object: publicv1.Notification_builder{
					Id: otherID,
					Metadata: publicv1.Metadata_builder{
						Version: other.GetObject().GetMetadata().GetVersion(),
					}.Build(),
					Spec: publicv1.NotificationSpec_builder{
						State: publicv1.NotificationState_NOTIFICATION_STATE_READ,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))

			_, err = server.Delete(ctx, publicv1.NotificationsDeleteRequest_builder{Id: otherID}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
		})

		It("Rejects announcement Delete for tenant users", func() {
			created, err := server.Create(ctx, publicv1.NotificationsCreateRequest_builder{
				Object: publicv1.Notification_builder{
					Metadata: publicv1.Metadata_builder{Name: "no-user-del"}.Build(),
					Spec: publicv1.NotificationSpec_builder{
						Kind:     publicv1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT,
						Severity: publicv1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
						Type:     "osac.notification.announcement",
						Scope:    publicv1.NotificationScope_NOTIFICATION_SCOPE_TENANT,
						Title:    "Admin only delete",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Delete(ctx, publicv1.NotificationsDeleteRequest_builder{
				Id: created.GetObject().GetId(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
		})

		It("Hides announcement Delete when resource access is denied", func() {
			access := &stubNotificationResourceAccess{allow: map[string]bool{"cluster-denied": false}}
			server, err := NewNotificationsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetResourceAccess(access).
				Build()
			Expect(err).ToNot(HaveOccurred())

			created, err := privateServer.Create(ctx, privatev1.NotificationsCreateRequest_builder{
				Object: privatev1.Notification_builder{
					Metadata: privatev1.Metadata_builder{Name: "ann-gated-del"}.Build(),
					Spec: privatev1.NotificationSpec_builder{
						Kind:     privatev1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT,
						Severity: privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
						Type:     "osac.notification.announcement",
						Scope:    privatev1.NotificationScope_NOTIFICATION_SCOPE_TENANT,
						Title:    "gated announcement",
						Resource: privatev1.NotificationResourceReference_builder{
							Cluster: privatev1.ClusterLocalReference_builder{Id: "cluster-denied"}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			adminCtx := auth.ContextWithSubject(ctx, &auth.Subject{
				User:    "platform-admin",
				Tenants: auth.AllTenants,
			})
			_, err = server.Delete(adminCtx, publicv1.NotificationsDeleteRequest_builder{
				Id: created.GetObject().GetId(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
		})
	})
})
