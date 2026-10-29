/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package servers

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

var _ = Describe("Machine-only role bindings", func() {
	var private *PrivateRoleBindingsServer
	var public *RoleBindingsServer
	var roleID func(string) string
	BeforeEach(func() {
		var err error
		private, err = NewPrivateRoleBindingsServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		public, err = NewRoleBindingsServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		roles, err := NewPrivateRolesServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		roleID = func(name string) string {
			response, err := roles.List(ctx, privatev1.RolesListRequest_builder{Filter: new(fmt.Sprintf("this.metadata.name == %q", name))}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(1))
			return response.GetItems()[0].GetId()
		}
	})
	DescribeTable("rejects public human bindings by catalog ID", func(name string) {
		response, err := public.Create(ctx, publicv1.RoleBindingsCreateRequest_builder{Object: publicv1.RoleBinding_builder{
			Metadata: publicv1.Metadata_builder{Name: "human"}.Build(),
			Spec: publicv1.RoleBindingSpec_builder{
				Role:  publicv1.RoleReference_builder{Id: roleID(name)}.Build(),
				Users: []*publicv1.UserReference{publicv1.UserReference_builder{Id: "human-user"}.Build()},
			}.Build(),
		}.Build()}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(response).To(BeNil())
		list, err := public.List(ctx, &publicv1.RoleBindingsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(list.GetTotal()).To(BeZero())
	}, Entry("automation", "tenant-automation"), Entry("CSI", "osac-csi-driver"))
	DescribeTable("rejects private human bindings by name", func(name string) {
		response, err := private.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{Object: privatev1.RoleBinding_builder{
			Metadata: privatev1.Metadata_builder{Name: "human"}.Build(),
			Spec: privatev1.RoleBindingSpec_builder{
				Role:  privatev1.RoleReference_builder{Name: name}.Build(),
				Users: []*privatev1.UserReference{privatev1.UserReference_builder{Id: "human-user"}.Build()},
			}.Build(),
		}.Build()}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(response).To(BeNil())
	}, Entry("automation", "tenant-automation"), Entry("CSI", "osac-csi-driver"))
	DescribeTable("rejects interchangeable role selectors on create and merged update", func(name string, nameInID bool) {
		publicRef := &publicv1.RoleReference{}
		privateRef := &privatev1.RoleReference{}
		if nameInID {
			publicRef.SetId(name)
			privateRef.SetId(name)
		} else {
			publicRef.SetName(roleID(name))
			privateRef.SetName(roleID(name))
		}
		_, err := public.Create(ctx, publicv1.RoleBindingsCreateRequest_builder{Object: publicv1.RoleBinding_builder{
			Metadata: publicv1.Metadata_builder{Name: "rejected-public"}.Build(),
			Spec: publicv1.RoleBindingSpec_builder{
				Role:  publicRef,
				Users: []*publicv1.UserReference{publicv1.UserReference_builder{Id: "human-user"}.Build()},
			}.Build(),
		}.Build()}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		_, err = private.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{Object: privatev1.RoleBinding_builder{
			Metadata: privatev1.Metadata_builder{Name: "rejected-private"}.Build(),
			Spec: privatev1.RoleBindingSpec_builder{
				Role:  privateRef,
				Users: []*privatev1.UserReference{privatev1.UserReference_builder{Id: "human-user"}.Build()},
			}.Build(),
		}.Build()}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		created, err := public.Create(ctx, publicv1.RoleBindingsCreateRequest_builder{Object: publicv1.RoleBinding_builder{
			Metadata: publicv1.Metadata_builder{Name: "human"}.Build(),
			Spec: publicv1.RoleBindingSpec_builder{
				Role:  publicv1.RoleReference_builder{Id: roleID("tenant-admin")}.Build(),
				Users: []*publicv1.UserReference{publicv1.UserReference_builder{Id: "human-user"}.Build()},
			}.Build(),
		}.Build()}.Build())
		Expect(err).ToNot(HaveOccurred())
		_, err = public.Update(ctx, publicv1.RoleBindingsUpdateRequest_builder{Object: publicv1.RoleBinding_builder{
			Id: created.GetObject().GetId(), Spec: publicv1.RoleBindingSpec_builder{Role: publicRef}.Build(),
		}.Build(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.role"}}}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		_, err = private.Update(ctx, privatev1.RoleBindingsUpdateRequest_builder{Object: privatev1.RoleBinding_builder{
			Id: created.GetObject().GetId(), Spec: privatev1.RoleBindingSpec_builder{Role: privateRef}.Build(),
		}.Build(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.role"}}}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	}, Entry("automation name in ID", "tenant-automation", true), Entry("CSI name in ID", "osac-csi-driver", true),
		Entry("automation UUID in name", "tenant-automation", false), Entry("CSI UUID in name", "osac-csi-driver", false))
	It("validates the merged update and ignores an out-of-mask machine role", func() {
		humanRole := roleID("tenant-admin")
		machineRole := roleID("tenant-automation")
		created, err := public.Create(ctx, publicv1.RoleBindingsCreateRequest_builder{Object: publicv1.RoleBinding_builder{
			Metadata: publicv1.Metadata_builder{Name: "human"}.Build(),
			Spec: publicv1.RoleBindingSpec_builder{
				Role:  publicv1.RoleReference_builder{Id: humanRole}.Build(),
				Users: []*publicv1.UserReference{publicv1.UserReference_builder{Id: "human-user"}.Build()},
			}.Build(),
		}.Build()}.Build())
		Expect(err).ToNot(HaveOccurred())
		request := publicv1.RoleBindingsUpdateRequest_builder{Object: publicv1.RoleBinding_builder{
			Id:       created.GetObject().GetId(),
			Metadata: publicv1.Metadata_builder{DisplayName: "Changed"}.Build(),
			Spec:     publicv1.RoleBindingSpec_builder{Role: publicv1.RoleReference_builder{Id: machineRole}.Build()}.Build(),
		}.Build(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.display_name"}}}.Build()
		updated, err := public.Update(ctx, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.GetObject().GetSpec().GetRole().GetId()).To(Equal(humanRole))
		request.SetUpdateMask(&fieldmaskpb.FieldMask{Paths: []string{"spec.role"}})
		rejected, err := public.Update(ctx, request)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(rejected).To(BeNil())
		stored, err := public.Get(ctx, publicv1.RoleBindingsGetRequest_builder{Id: created.GetObject().GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(stored.GetObject().GetSpec().GetRole().GetId()).To(Equal(humanRole))
	})
})
