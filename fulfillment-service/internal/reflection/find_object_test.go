/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package reflection

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/exit"
	"github.com/osac-project/osac/fulfillment-service/internal/packages"
	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// erroringRenderer is a Renderer that fails with a persistent error, to simulate a console that can't write its
// output.
type erroringRenderer struct {
	err error
}

// Render is the implementation of the Renderer interface.
func (r erroringRenderer) Render(context.Context, string, any) error {
	return r.err
}

// countingRenderer is a Renderer that succeeds and counts the calls.
type countingRenderer struct {
	calls int
}

// Render is the implementation of the Renderer interface.
func (r *countingRenderer) Render(context.Context, string, any) error {
	r.calls++
	return nil
}

var _ = Describe("Find object", func() {
	var (
		ctx        context.Context
		server     *testing.Server
		connection *grpc.ClientConn
		helper     Helper
	)

	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = context.Background()

		// Create the server:
		server = testing.NewServer()
		DeferCleanup(server.Stop)

		// Create the client connection:
		connection, err = grpc.NewClient(
			server.Address(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(connection.Close)

		// Create the helper:
		helper, err = NewHelper().
			SetLogger(logger).
			SetConnection(connection).
			AddPackage(packages.PublicV1, 1).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("Returns the object when there is exactly one match", func() {
		// Register a clusters server that responds to the list request with one item:
		publicv1.RegisterClustersServer(server.Registrar(), &testing.ClustersServerFuncs{
			ListFunc: func(ctx context.Context, request *publicv1.ClustersListRequest,
			) (response *publicv1.ClustersListResponse, err error) {
				response = publicv1.ClustersListResponse_builder{
					Size:  1,
					Total: 1,
					Items: []*publicv1.Cluster{
						publicv1.Cluster_builder{
							Id: "cluster-1",
						}.Build(),
					},
				}.Build()
				return
			},
		})
		server.Start()

		renderer := &countingRenderer{}
		objectHelper := helper.Lookup("cluster")
		Expect(objectHelper).ToNot(BeNil())
		object, err := objectHelper.FindObject(ctx, "cluster-1", renderer)
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(object, publicv1.Cluster_builder{Id: "cluster-1"}.Build())).To(BeTrue())
		Expect(renderer.calls).To(BeZero())
	})

	It("Returns the exit error when there are no matches and the template renders", func() {
		// Register a clusters server that responds to the list request with no items:
		publicv1.RegisterClustersServer(server.Registrar(), &testing.ClustersServerFuncs{
			ListFunc: func(ctx context.Context, request *publicv1.ClustersListRequest,
			) (response *publicv1.ClustersListResponse, err error) {
				response = publicv1.ClustersListResponse_builder{
					Size:  0,
					Total: 0,
				}.Build()
				return
			},
		})
		server.Start()

		objectHelper := helper.Lookup("cluster")
		Expect(objectHelper).ToNot(BeNil())
		object, err := objectHelper.FindObject(ctx, "cluster-1", &countingRenderer{})
		Expect(object).To(BeNil())
		Expect(err).To(HaveOccurred())
		var exitErr exit.Error
		Expect(errors.As(err, &exitErr)).To(BeTrue())
		Expect(exitErr.Code()).To(Equal(1))
	})

	It("Returns the render error instead of the exit error when there are no matches", func() {
		// Register a clusters server that responds to the list request with no items:
		publicv1.RegisterClustersServer(server.Registrar(), &testing.ClustersServerFuncs{
			ListFunc: func(ctx context.Context, request *publicv1.ClustersListRequest,
			) (response *publicv1.ClustersListResponse, err error) {
				response = publicv1.ClustersListResponse_builder{
					Size:  0,
					Total: 0,
				}.Build()
				return
			},
		})
		server.Start()

		sentinel := errors.New("broken console")
		objectHelper := helper.Lookup("cluster")
		Expect(objectHelper).ToNot(BeNil())
		object, err := objectHelper.FindObject(ctx, "cluster-1", erroringRenderer{err: sentinel})
		Expect(object).To(BeNil())
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, sentinel)).To(BeTrue())
		var exitErr exit.Error
		Expect(errors.As(err, &exitErr)).To(BeFalse(),
			"the render error should be returned before the exit error is set")
	})

	It("Returns the render error instead of the exit error when there are multiple matches", func() {
		// Register a clusters server that responds to the list request with two items:
		publicv1.RegisterClustersServer(server.Registrar(), &testing.ClustersServerFuncs{
			ListFunc: func(ctx context.Context, request *publicv1.ClustersListRequest,
			) (response *publicv1.ClustersListResponse, err error) {
				response = publicv1.ClustersListResponse_builder{
					Size:  2,
					Total: 2,
					Items: []*publicv1.Cluster{
						publicv1.Cluster_builder{
							Id: "cluster-1",
						}.Build(),
						publicv1.Cluster_builder{
							Id: "cluster-2",
						}.Build(),
					},
				}.Build()
				return
			},
		})
		server.Start()

		sentinel := errors.New("broken console")
		objectHelper := helper.Lookup("cluster")
		Expect(objectHelper).ToNot(BeNil())
		object, err := objectHelper.FindObject(ctx, "cluster", erroringRenderer{err: sentinel})
		Expect(object).To(BeNil())
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, sentinel)).To(BeTrue())
		var exitErr exit.Error
		Expect(errors.As(err, &exitErr)).To(BeFalse(),
			"the render error should be returned before the exit error is set")
	})
})
