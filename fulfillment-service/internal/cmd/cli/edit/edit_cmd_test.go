/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package edit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/updatable"
	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Edit command", func() {
	var (
		ctx              context.Context
		logger           *slog.Logger
		server           *testing.Server
		conn             *grpc.ClientConn
		console          *terminal.Console
		output           *bytes.Buffer
		stderr           *bytes.Buffer
		helper           reflection.ObjectHelper
		reflectionHelper reflection.Helper
	)

	BeforeEach(func() {
		var err error

		ctx = context.Background()

		logger = slog.New(slog.NewTextHandler(GinkgoWriter, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))

		output = &bytes.Buffer{}
		stderr = &bytes.Buffer{}

		console, err = terminal.NewConsole().
			SetLogger(logger).
			SetStdout(output).
			SetStderr(stderr).
			Build()
		Expect(err).ToNot(HaveOccurred())

		err = console.AddTemplates(templatesFS, "templates")
		Expect(err).ToNot(HaveOccurred())

		server = testing.NewServer()
		DeferCleanup(server.Stop)

		conn, err = grpc.NewClient(
			server.Address(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(conn.Close)

		reflectionHelper, err = reflection.NewHelper().
			SetLogger(logger).
			SetConnection(conn).
			AddPackage("osac.public.v1", 0).
			Build()
		Expect(err).ToNot(HaveOccurred())

		helper = reflectionHelper.Lookup("cluster")
		Expect(helper).ToNot(BeNil())
	})

	JustBeforeEach(func() {
		server.Start()
	})

	Describe("update", func() {
		var (
			warnings    []string
			updateErr   error
			updateCalls atomic.Int32
		)

		BeforeEach(func() {
			warnings = nil
			updateErr = nil
			updateCalls.Store(0)
			publicv1.RegisterComputeInstancesServer(server.Registrar(), &testing.ComputeInstancesServerFuncs{
				UpdateFunc: func(ctx context.Context, request *publicv1.ComputeInstancesUpdateRequest,
				) (*publicv1.ComputeInstancesUpdateResponse, error) {
					updateCalls.Add(1)
					if updateErr != nil {
						return nil, updateErr
					}
					return publicv1.ComputeInstancesUpdateResponse_builder{
						Object:   request.Object,
						Warnings: warnings,
					}.Build(), nil
				},
			})
			helper = reflectionHelper.Lookup("computeinstance")
			Expect(helper).ToNot(BeNil())
		})

		DescribeTable("prints server warnings to stderr after a successful update",
			func(serverWarnings []string, expectedOutput string) {
				warnings = serverWarnings
				object := &publicv1.ComputeInstance{Id: "test-vm"}
				runner := &runnerContext{helper: helper, console: console}

				// Pass the same object as both original and edited since we're testing warning handling
				updated, err := runner.update(ctx, object, object)

				Expect(err).ToNot(HaveOccurred())
				Expect(proto.Equal(updated, object)).To(BeTrue())
				Expect(stderr.String()).To(Equal(expectedOutput))
				Expect(output.String()).To(BeEmpty())
			},
			Entry("no warnings", nil, ""),
			Entry("deprecated instance type",
				[]string{"Instance type is deprecated; use replacement-type before 2030-01-01."},
				"Warning: Instance type is deprecated; use replacement-type before 2030-01-01.\n",
			),
			Entry("multiple warnings", []string{"First warning", "Second warning"},
				"Warning: First warning\nWarning: Second warning\n",
			),
		)

		It("reports warning output failures after a successful update without retrying", func() {
			reader, writer := io.Pipe()
			Expect(reader.Close()).To(Succeed())
			DeferCleanup(writer.Close)
			console, err := terminal.NewConsole().
				SetLogger(logger).
				SetStdout(output).
				SetStderr(writer).
				Build()
			Expect(err).ToNot(HaveOccurred())

			warnings = []string{"Instance type is deprecated."}
			object := &publicv1.ComputeInstance{Id: "test-vm"}
			runner := &runnerContext{helper: helper, console: console}

			updated, err := runner.update(ctx, object, object)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("update succeeded, but failed to write warning to stderr"))
			Expect(errors.Is(err, io.ErrClosedPipe)).To(BeTrue())
			Expect(proto.Equal(updated, object)).To(BeTrue())
			Expect(updateCalls.Load()).To(Equal(int32(1)))
			Expect(output.String()).To(BeEmpty())
		})

		It("preserves update failures without printing warnings", func() {
			updateErr = grpcstatus.Error(codes.FailedPrecondition, "instance type is obsolete")
			runner := &runnerContext{helper: helper, console: console}
			object := &publicv1.ComputeInstance{Id: "test-vm"}

			_, err := runner.update(ctx, object, object)

			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("instance type is obsolete"))
			Expect(stderr.String()).To(BeEmpty())
			Expect(output.String()).To(BeEmpty())
		})
	})

	DescribeTable("isWatchable",
		func(objectType string, packages map[string]int, expected bool) {
			builder := reflection.NewHelper().
				SetLogger(logger).
				SetConnection(conn)
			for pkg, order := range packages {
				builder = builder.AddPackage(pkg, order)
			}
			reflectionHelper, err := builder.Build()
			Expect(err).ToNot(HaveOccurred())

			h := reflectionHelper.Lookup(objectType)
			Expect(h).ToNot(BeNil(), "failed to look up object type %q", objectType)

			runner := &runnerContext{helper: h}
			Expect(runner.isWatchable()).To(Equal(expected))
		},
		Entry("public cluster is watchable", "cluster", map[string]int{"osac.public.v1": 0}, true),
		Entry("public compute instance is watchable", "computeinstance", map[string]int{"osac.public.v1": 0}, true),
		Entry("public identity provider is not watchable", "identityprovider", map[string]int{"osac.public.v1": 0}, false),
		Entry("private hub is watchable", "hub", map[string]int{"osac.private.v1": 0}, true),
	)

	It("does not offer immutable networking resources for edit completion", func() {
		completed, directive := completeObjectTypes(nil, nil, "")
		Expect(directive).To(Equal(cobra.ShellCompDirectiveNoFileComp))
		Expect(completed).ToNot(ContainElements(
			"virtualnetwork", "virtualnetworks",
			"subnet", "subnets",
			"securitygroup", "securitygroups",
			"externalip", "externalips",
			"externalipattachment", "externalipattachments",
			"natgateway", "natgateways",
		))
	})

	It("rejects direct edits of immutable objects before opening an editor", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		mockHelper := reflection.NewMockObjectHelper(ctrl)
		mockHelper.EXPECT().IsUpdatable().Return(false)
		mockHelper.EXPECT().FullName().Return(protoreflect.FullName("osac.public.v1.VirtualNetwork"))

		err := updatable.Ensure(mockHelper)
		Expect(err).To(MatchError(`object type "osac.public.v1.VirtualNetwork" is immutable; updates are not supported`))
	})

	It("allows edits of update-capable objects", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		mockHelper := reflection.NewMockObjectHelper(ctrl)
		mockHelper.EXPECT().IsUpdatable().Return(true)

		Expect(updatable.Ensure(mockHelper)).ToNot(HaveOccurred())
	})

	Describe("fetchObject", func() {
		var (
			ctrl       *gomock.Controller
			mockHelper *reflection.MockObjectHelper
		)

		BeforeEach(func() {
			ctrl = gomock.NewController(GinkgoT())
			DeferCleanup(ctrl.Finish)
			mockHelper = reflection.NewMockObjectHelper(ctrl)
		})

		It("should call Get after FindObject when UseGetForStructuredOutput is true", func() {
			listObject := &publicv1.Cluster{Id: "cluster-1", Metadata: &publicv1.Metadata{Name: "my-cluster"}}
			fullObject := &publicv1.Cluster{Id: "cluster-1", Metadata: &publicv1.Metadata{Name: "my-cluster"}}

			mockHelper.EXPECT().FindObject(gomock.Any(), "my-cluster", gomock.Any()).Return(listObject, nil)
			mockHelper.EXPECT().UseGetForStructuredOutput().Return(true)
			mockHelper.EXPECT().GetId(listObject).Return("cluster-1")
			mockHelper.EXPECT().Get(gomock.Any(), "cluster-1").Return(fullObject, nil)

			runner := &runnerContext{helper: mockHelper, console: console}
			result, err := runner.fetchObject(ctx, "my-cluster")
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(result, fullObject)).To(BeTrue())
		})

		It("should return error when Get fails with UseGetForStructuredOutput enabled", func() {
			listObject := &publicv1.Cluster{Id: "cluster-1"}

			mockHelper.EXPECT().FindObject(gomock.Any(), "my-cluster", gomock.Any()).Return(listObject, nil)
			mockHelper.EXPECT().UseGetForStructuredOutput().Return(true)
			mockHelper.EXPECT().GetId(listObject).Return("cluster-1")
			mockHelper.EXPECT().Get(gomock.Any(), "cluster-1").Return(nil, fmt.Errorf("not found"))

			runner := &runnerContext{helper: mockHelper, console: console}
			_, err := runner.fetchObject(ctx, "my-cluster")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to get full object"))
		})

		It("should not call Get when UseGetForStructuredOutput is false", func() {
			listObject := &publicv1.Cluster{Id: "cluster-1", Metadata: &publicv1.Metadata{Name: "my-cluster"}}

			mockHelper.EXPECT().FindObject(gomock.Any(), "my-cluster", gomock.Any()).Return(listObject, nil)
			mockHelper.EXPECT().UseGetForStructuredOutput().Return(false)

			runner := &runnerContext{helper: mockHelper, console: console}
			result, err := runner.fetchObject(ctx, "my-cluster")
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(result, listObject)).To(BeTrue())
		})
	})

	Describe("showWatchSuggestion", func() {
		It("should render watch suggestion with object type and ID", func() {
			runner := &runnerContext{
				console: console,
				helper:  helper,
			}

			cluster := &publicv1.Cluster{
				Id: "test-cluster-123",
				Metadata: &publicv1.Metadata{
					Name: "my-test-cluster",
				},
			}

			runner.showWatchSuggestion(ctx, cluster)

			outputStr := output.String()
			Expect(outputStr).To(ContainSubstring("get cluster test-cluster-123 --watch"))
			Expect(outputStr).To(ContainSubstring("watch for changes"))
		})
	})
})
