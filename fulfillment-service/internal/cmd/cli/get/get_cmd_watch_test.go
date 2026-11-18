/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package get

import (
	"bytes"
	"context"
	"errors"
	"io"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/packages"
	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// Only the client-stream methods used by Watch are replaced.
type recordingWatchStream struct {
	grpc.ClientStream
	send    func(any) error
	receive func(any) error
}

func (s *recordingWatchStream) SendMsg(message any) error { return s.send(message) }
func (s *recordingWatchStream) CloseSend() error          { return nil }
func (s *recordingWatchStream) RecvMsg(message any) error { return s.receive(message) }

var _ = Describe("Watch command", func() {
	DescribeTable("extractObjectFromEvent",
		func(event proto.Message, expectedType proto.Message, shouldSucceed bool) {
			runner := &runnerContext{}
			object := runner.extractObjectFromEvent(event)

			if shouldSucceed {
				Expect(object).ToNot(BeNil())
				Expect(proto.MessageName(object)).To(Equal(proto.MessageName(expectedType)))
			} else {
				Expect(object).To(BeNil())
			}
		},
		Entry("cluster event",
			&publicv1.Event{
				Payload: &publicv1.Event_Cluster{
					Cluster: &publicv1.Cluster{Id: "test-cluster"},
				},
			},
			(*publicv1.Cluster)(nil),
			true,
		),
		Entry("cluster template event",
			&publicv1.Event{
				Payload: &publicv1.Event_ClusterTemplate{
					ClusterTemplate: &publicv1.ClusterTemplate{Id: "test-template"},
				},
			},
			(*publicv1.ClusterTemplate)(nil),
			true,
		),
		Entry("compute instance event",
			&publicv1.Event{
				Payload: &publicv1.Event_ComputeInstance{
					ComputeInstance: &publicv1.ComputeInstance{Id: "test-instance"},
				},
			},
			(*publicv1.ComputeInstance)(nil),
			true,
		),
		Entry("private hub event",
			&privatev1.Event{
				Payload: &privatev1.Event_Hub{
					Hub: &privatev1.Hub{Id: "test-hub"},
				},
			},
			(*privatev1.Hub)(nil),
			true,
		),
		Entry("public event with no payload",
			&publicv1.Event{},
			nil,
			false,
		),
		Entry("private event with no payload",
			&privatev1.Event{},
			nil,
			false,
		),
	)

	DescribeTable("getEventTypeName",
		func(event proto.Message, expected string) {
			Expect(getEventTypeName(event)).To(Equal(expected))
		},
		Entry("known public event type",
			&publicv1.Event{Type: publicv1.EventType_EVENT_TYPE_OBJECT_CREATED},
			"OBJECT_CREATED",
		),
		Entry("unspecified event type",
			&publicv1.Event{},
			"UNSPECIFIED",
		),
		Entry("unknown event type number",
			func() proto.Message {
				event := &publicv1.Event{}
				msg := event.ProtoReflect()
				typeField := msg.Descriptor().Fields().ByName("type")
				msg.Set(typeField, protoreflect.ValueOfEnum(9999))
				return event
			}(),
			"UNKNOWN(9999)",
		),
	)

	DescribeTable("buildEventFilter",
		func(object proto.Message, keys []string, expectedFilter string) {
			ctrl := gomock.NewController(GinkgoT())
			helper := reflection.NewMockObjectHelper(ctrl)
			helper.EXPECT().FullName().Return(object.ProtoReflect().Descriptor().FullName()).AnyTimes()
			helper.EXPECT().Descriptor().Return(object.ProtoReflect().Descriptor()).AnyTimes()
			runner := &runnerContext{objectHelper: helper}
			filter, err := runner.buildEventFilter(keys)
			Expect(err).NotTo(HaveOccurred())
			Expect(filter).To(Equal(expectedFilter))
		},
		Entry("cluster with no keys", &publicv1.Cluster{}, []string{}, "has(event.cluster)"),
		Entry("cluster with specific ID", &publicv1.Cluster{}, []string{"123"}, `has(event.cluster) && (event.cluster.id == "123" || event.cluster.metadata.name == "123")`),
		Entry("private cluster keeps escaped keys", &privatev1.Cluster{}, []string{`one"two`}, `has(event.cluster) && (event.cluster.id == "one\"two" || event.cluster.metadata.name == "one\"two")`),
	)

	DescribeTable("ordinary Watch public/private routing and termination",
		func(pkg, termination string) {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			calls := 0
			connection, err := grpc.NewClient("passthrough:///p2-watch.invalid",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithStreamInterceptor(func(streamCtx context.Context, _ *grpc.StreamDesc, _ *grpc.ClientConn,
					method string, _ grpc.Streamer, _ ...grpc.CallOption) (grpc.ClientStream, error) {
					Expect(method).To(Equal("/" + pkg + ".Events/Watch"))
					return &recordingWatchStream{
						send: func(request any) error {
							message := request.(proto.Message).ProtoReflect()
							Expect(message.Get(message.Descriptor().Fields().ByName("filter")).String()).To(Equal("has(event.cluster)"))
							return nil
						},
						receive: func(any) error {
							calls++
							switch termination {
							case "EOF":
								return io.EOF
							case "cancel":
								cancel()
								return streamCtx.Err()
							default:
								return io.ErrUnexpectedEOF
							}
						},
					}, nil
				}))
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(connection.Close)
			helper, err := reflection.NewHelper().SetLogger(logger).SetConnection(connection).AddPackage(pkg, 0).Build()
			Expect(err).NotTo(HaveOccurred())
			console, err := terminal.NewConsole().SetLogger(logger).SetStdout(&bytes.Buffer{}).SetStderr(&bytes.Buffer{}).Build()
			Expect(err).NotTo(HaveOccurred())
			runner := &runnerContext{conn: connection, logger: logger, console: console, objectHelper: helper.Lookup("cluster")}
			err = runner.watch(ctx, nil)
			Expect(calls).To(Equal(1))
			switch termination {
			case "EOF":
				Expect(err).NotTo(HaveOccurred())
			case "cancel":
				Expect(errors.Is(err, context.Canceled)).To(BeTrue())
			default:
				Expect(errors.Is(err, io.ErrUnexpectedEOF)).To(BeTrue())
			}
		},
		Entry("public EOF", packages.PublicV1, "EOF"), Entry("private EOF", packages.PrivateV1, "EOF"),
		Entry("public error", packages.PublicV1, "error"), Entry("private error", packages.PrivateV1, "error"),
		Entry("public cancel", packages.PublicV1, "cancel"), Entry("private cancel", packages.PrivateV1, "cancel"),
	)
})
