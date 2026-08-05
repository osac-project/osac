/*
Copyright (c) 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	. "github.com/osac-project/fulfillment-service/internal/testing"
)

var _ = Describe("GrpcExternalAuthInterceptor project metadata authorization", func() {
	Describe("Projects Get operation", func() {
		It("Should fetch metadata from database and include in context extensions", func(ctx context.Context) {
			interceptor, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				SetMetadataFetcher(func(ctx context.Context, id string) *ObjectMetadata {
					return &ObjectMetadata{Tenant: "authoritative-tenant", Name: "authoritative-project"}
				}).
				SetInputCallback(func(ctx context.Context, input map[string]any) error {
					Expect(input).To(HaveKey("context"))
					context, ok := input["context"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(context).To(HaveKey("context_extensions"))
					extensions, ok := context["context_extensions"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(extensions).To(HaveKeyWithValue("id", "project-123"))
					Expect(extensions).To(HaveKeyWithValue("tenant", "authoritative-tenant"))
					Expect(extensions).To(HaveKeyWithValue("name", "authoritative-project"))
					return nil
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			token := MakeTokenObject(nil, nil)
			ctx = ContextWithToken(ctx, token)
			_, _ = interceptor.UnaryServer(
				ctx,
				publicv1.ProjectsGetRequest_builder{
					Id: "project-123",
				}.Build(),
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Projects/Get",
				},
				func(ctx context.Context, req any) (any, error) {
					return nil, nil
				},
			)
		})
	})

	Describe("Projects Delete operation", func() {
		It("Should fetch metadata from database and include in context extensions", func(ctx context.Context) {
			interceptor, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				SetMetadataFetcher(func(ctx context.Context, id string) *ObjectMetadata {
					return &ObjectMetadata{Tenant: "authoritative-tenant", Name: "authoritative-project"}
				}).
				SetInputCallback(func(ctx context.Context, input map[string]any) error {
					Expect(input).To(HaveKey("context"))
					context, ok := input["context"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(context).To(HaveKey("context_extensions"))
					extensions, ok := context["context_extensions"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(extensions).To(HaveKeyWithValue("id", "project-456"))
					Expect(extensions).To(HaveKeyWithValue("tenant", "authoritative-tenant"))
					Expect(extensions).To(HaveKeyWithValue("name", "authoritative-project"))
					return nil
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			token := MakeTokenObject(nil, nil)
			ctx = ContextWithToken(ctx, token)
			_, _ = interceptor.UnaryServer(
				ctx,
				publicv1.ProjectsDeleteRequest_builder{
					Id: "project-456",
				}.Build(),
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Projects/Delete",
				},
				func(ctx context.Context, req any) (any, error) {
					return nil, nil
				},
			)
		})
	})

	Describe("Projects Update operation", func() {
		It("Should fetch metadata from database and include in context extensions", func(ctx context.Context) {
			interceptor, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				SetMetadataFetcher(func(ctx context.Context, id string) *ObjectMetadata {
					return &ObjectMetadata{Tenant: "authoritative-tenant", Name: "authoritative-project"}
				}).
				SetInputCallback(func(ctx context.Context, input map[string]any) error {
					Expect(input).To(HaveKey("context"))
					context, ok := input["context"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(context).To(HaveKey("context_extensions"))
					extensions, ok := context["context_extensions"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(extensions).To(HaveKeyWithValue("id", "project-789"))
					Expect(extensions).To(HaveKeyWithValue("tenant", "authoritative-tenant"))
					Expect(extensions).To(HaveKeyWithValue("name", "authoritative-project"))
					return nil
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			token := MakeTokenObject(nil, nil)
			ctx = ContextWithToken(ctx, token)
			_, _ = interceptor.UnaryServer(
				ctx,
				publicv1.ProjectsUpdateRequest_builder{
					Object: publicv1.Project_builder{
						Id: "project-789",
						Metadata: publicv1.Metadata_builder{
							Tenant: "client-spoofed-tenant",
							Name:   "client-spoofed-name",
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{},
				}.Build(),
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Projects/Update",
				},
				func(ctx context.Context, req any) (any, error) {
					return nil, nil
				},
			)
		})

		It("Should ignore client-provided metadata values and use authoritative database values", func(ctx context.Context) {
			interceptor, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				SetMetadataFetcher(func(ctx context.Context, id string) *ObjectMetadata {
					return &ObjectMetadata{Tenant: "authoritative-tenant", Name: "authoritative-project"}
				}).
				SetInputCallback(func(ctx context.Context, input map[string]any) error {
					Expect(input).To(HaveKey("context"))
					context, ok := input["context"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(context).To(HaveKey("context_extensions"))
					extensions, ok := context["context_extensions"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(extensions["tenant"]).To(Equal("authoritative-tenant"))
					Expect(extensions["name"]).To(Equal("authoritative-project"))
					Expect(extensions["tenant"]).ToNot(Equal("malicious-tenant"))
					Expect(extensions["name"]).ToNot(Equal("malicious-project"))
					return nil
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			token := MakeTokenObject(nil, nil)
			ctx = ContextWithToken(ctx, token)
			_, _ = interceptor.UnaryServer(
				ctx,
				publicv1.ProjectsUpdateRequest_builder{
					Object: publicv1.Project_builder{
						Id: "project-999",
						Metadata: publicv1.Metadata_builder{
							Tenant: "malicious-tenant",
							Name:   "malicious-project",
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{},
				}.Build(),
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Projects/Update",
				},
				func(ctx context.Context, req any) (any, error) {
					return nil, nil
				},
			)
		})
	})

	Describe("Projects List operation", func() {
		It("Should not fetch metadata for list operations", func(ctx context.Context) {
			interceptor, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				SetMetadataFetcher(func(ctx context.Context, id string) *ObjectMetadata {
					Fail("metadata fetcher should not be called for list operations")
					return nil
				}).
				SetInputCallback(func(ctx context.Context, input map[string]any) error {
					Expect(input).To(HaveKey("context"))
					context, ok := input["context"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(context).To(HaveKey("context_extensions"))
					extensions, ok := context["context_extensions"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(extensions).ToNot(HaveKey("tenant"))
					Expect(extensions).ToNot(HaveKey("name"))
					return nil
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			token := MakeTokenObject(nil, nil)
			ctx = ContextWithToken(ctx, token)
			_, _ = interceptor.UnaryServer(
				ctx,
				publicv1.ProjectsListRequest_builder{}.Build(),
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Projects/List",
				},
				func(ctx context.Context, req any) (any, error) {
					return nil, nil
				},
			)
		})
	})

	Describe("ProjectMemberships Get operation", func() {
		It("Should fetch project membership metadata and include in context extensions", func(ctx context.Context) {
			interceptor, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				SetProjectMembershipMetadataFetcher(func(ctx context.Context, id string) *ObjectMetadata {
					Expect(id).To(Equal("pm-123"))
					return &ObjectMetadata{Tenant: "authoritative-tenant", Project: "authoritative-project"}
				}).
				SetInputCallback(func(ctx context.Context, input map[string]any) error {
					Expect(input).To(HaveKey("context"))
					context, ok := input["context"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(context).To(HaveKey("context_extensions"))
					extensions, ok := context["context_extensions"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(extensions).To(HaveKeyWithValue("id", "pm-123"))
					Expect(extensions).To(HaveKeyWithValue("tenant", "authoritative-tenant"))
					Expect(extensions).To(HaveKeyWithValue("project", "authoritative-project"))
					return nil
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			token := MakeTokenObject(nil, nil)
			ctx = ContextWithToken(ctx, token)
			_, _ = interceptor.UnaryServer(
				ctx,
				publicv1.ProjectMembershipsGetRequest_builder{
					Id: "pm-123",
				}.Build(),
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.ProjectMemberships/Get",
				},
				func(ctx context.Context, req any) (any, error) {
					return nil, nil
				},
			)
		})
	})

	Describe("ProjectMemberships Delete operation", func() {
		It("Should fetch project membership metadata and include in context extensions", func(ctx context.Context) {
			interceptor, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				SetProjectMembershipMetadataFetcher(func(ctx context.Context, id string) *ObjectMetadata {
					return &ObjectMetadata{Tenant: "authoritative-tenant", Project: "authoritative-project"}
				}).
				SetInputCallback(func(ctx context.Context, input map[string]any) error {
					Expect(input).To(HaveKey("context"))
					context, ok := input["context"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(context).To(HaveKey("context_extensions"))
					extensions, ok := context["context_extensions"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(extensions).To(HaveKeyWithValue("id", "pm-456"))
					Expect(extensions).To(HaveKeyWithValue("tenant", "authoritative-tenant"))
					Expect(extensions).To(HaveKeyWithValue("project", "authoritative-project"))
					return nil
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			token := MakeTokenObject(nil, nil)
			ctx = ContextWithToken(ctx, token)
			_, _ = interceptor.UnaryServer(
				ctx,
				publicv1.ProjectMembershipsDeleteRequest_builder{
					Id: "pm-456",
				}.Build(),
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.ProjectMemberships/Delete",
				},
				func(ctx context.Context, req any) (any, error) {
					return nil, nil
				},
			)
		})
	})

	Describe("ProjectMemberships Create operation", func() {
		It("Should extract project name from request body and include in context extensions", func(ctx context.Context) {
			interceptor, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				SetProjectMembershipMetadataFetcher(func(ctx context.Context, id string) *ObjectMetadata {
					Fail("project membership metadata fetcher should not be called for Create")
					return nil
				}).
				SetInputCallback(func(ctx context.Context, input map[string]any) error {
					Expect(input).To(HaveKey("context"))
					context, ok := input["context"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(context).To(HaveKey("context_extensions"))
					extensions, ok := context["context_extensions"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(extensions).To(HaveKeyWithValue("project", "my-project"))
					Expect(extensions).ToNot(HaveKey("tenant"))
					return nil
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			token := MakeTokenObject(nil, nil)
			ctx = ContextWithToken(ctx, token)
			_, _ = interceptor.UnaryServer(
				ctx,
				publicv1.ProjectMembershipsCreateRequest_builder{
					Object: publicv1.ProjectMembership_builder{
						Metadata: publicv1.Metadata_builder{
							Name:    "test-membership",
							Project: "my-project",
						}.Build(),
					}.Build(),
				}.Build(),
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.ProjectMemberships/Create",
				},
				func(ctx context.Context, req any) (any, error) {
					return nil, nil
				},
			)
		})
	})

	Describe("ProjectMemberships List operation", func() {
		It("Should not fetch metadata for list operations", func(ctx context.Context) {
			interceptor, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				SetProjectMembershipMetadataFetcher(func(ctx context.Context, id string) *ObjectMetadata {
					Fail("project membership metadata fetcher should not be called for List")
					return nil
				}).
				SetInputCallback(func(ctx context.Context, input map[string]any) error {
					Expect(input).To(HaveKey("context"))
					context, ok := input["context"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(context).To(HaveKey("context_extensions"))
					extensions, ok := context["context_extensions"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(extensions).ToNot(HaveKey("tenant"))
					Expect(extensions).ToNot(HaveKey("project"))
					return nil
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			token := MakeTokenObject(nil, nil)
			ctx = ContextWithToken(ctx, token)
			_, _ = interceptor.UnaryServer(
				ctx,
				publicv1.ProjectMembershipsListRequest_builder{}.Build(),
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.ProjectMemberships/List",
				},
				func(ctx context.Context, req any) (any, error) {
					return nil, nil
				},
			)
		})
	})

	Describe("Other resource operations", func() {
		It("Should not fetch project metadata for Clusters operations", func(ctx context.Context) {
			interceptor, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				SetMetadataFetcher(func(ctx context.Context, id string) *ObjectMetadata {
					Fail("metadata fetcher should not be called for non-Projects resources")
					return nil
				}).
				SetInputCallback(func(ctx context.Context, input map[string]any) error {
					Expect(input).To(HaveKey("context"))
					context, ok := input["context"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(context).To(HaveKey("context_extensions"))
					extensions, ok := context["context_extensions"].(map[string]any)
					Expect(ok).To(BeTrue())
					Expect(extensions).To(HaveKeyWithValue("id", "cluster-123"))
					Expect(extensions).ToNot(HaveKey("tenant"))
					Expect(extensions).ToNot(HaveKey("name"))
					return nil
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			token := MakeTokenObject(nil, nil)
			ctx = ContextWithToken(ctx, token)
			_, _ = interceptor.UnaryServer(
				ctx,
				publicv1.ClustersGetRequest_builder{
					Id: "cluster-123",
				}.Build(),
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Clusters/Get",
				},
				func(ctx context.Context, req any) (any, error) {
					return nil, nil
				},
			)
		})
	})
})
