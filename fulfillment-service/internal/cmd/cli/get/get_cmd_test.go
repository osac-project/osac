/*
Copyright (c) 2026 Red Hat Inc.

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
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/help"
	"github.com/spf13/cobra"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"sigs.k8s.io/yaml"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

var _ = Describe("Get command", func() {
	var (
		ctx        context.Context
		ctrl       *gomock.Controller
		mockHelper *reflection.MockObjectHelper
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		mockHelper = reflection.NewMockObjectHelper(ctrl)
	})

	Describe("fetchObjects", func() {
		It("should call Get for each object when UseGetForStructuredOutput is true", func() {
			listObject := &publicv1.Cluster{Id: "cluster-1", Metadata: &publicv1.Metadata{Name: "my-cluster"}}
			fullObject := &publicv1.Cluster{Id: "cluster-1", Metadata: &publicv1.Metadata{Name: "my-cluster"}}

			mockHelper.EXPECT().UseGetForStructuredOutput().Return(true)
			mockHelper.EXPECT().List(gomock.Any(), gomock.Any()).
				Return(reflection.ListResult{Items: []proto.Message{listObject}, Total: 1}, nil)
			mockHelper.EXPECT().GetId(listObject).Return("cluster-1")
			mockHelper.EXPECT().Get(gomock.Any(), "cluster-1").Return(fullObject, nil)

			runner := &runnerContext{
				objectHelper: mockHelper,
			}
			runner.args.format = outputFormatYaml

			results, err := runner.fetchObjects(ctx, []string{"my-cluster"})
			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(proto.Equal(results[0], fullObject)).To(BeTrue())
		})

		It("should not call Get when UseGetForStructuredOutput is false", func() {
			listObject := &publicv1.Cluster{Id: "cluster-1", Metadata: &publicv1.Metadata{Name: "my-cluster"}}

			mockHelper.EXPECT().UseGetForStructuredOutput().Return(false)
			mockHelper.EXPECT().List(gomock.Any(), gomock.Any()).
				Return(reflection.ListResult{Items: []proto.Message{listObject}, Total: 1}, nil)

			runner := &runnerContext{
				objectHelper: mockHelper,
			}
			runner.args.format = outputFormatYaml

			results, err := runner.fetchObjects(ctx, []string{"my-cluster"})
			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(proto.Equal(results[0], listObject)).To(BeTrue())
		})

		It("should not check UseGetForStructuredOutput for table format", func() {
			listObject := &publicv1.Cluster{Id: "cluster-1"}

			mockHelper.EXPECT().List(gomock.Any(), gomock.Any()).
				Return(reflection.ListResult{Items: []proto.Message{listObject}, Total: 1}, nil)

			runner := &runnerContext{
				objectHelper: mockHelper,
			}
			runner.args.format = outputFormatTable

			results, err := runner.fetchObjects(ctx, []string{"my-cluster"})
			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(HaveLen(1))
		})

		It("should return error when Get fails", func() {
			listObject := &publicv1.Cluster{Id: "cluster-1"}

			mockHelper.EXPECT().UseGetForStructuredOutput().Return(true)
			mockHelper.EXPECT().List(gomock.Any(), gomock.Any()).
				Return(reflection.ListResult{Items: []proto.Message{listObject}, Total: 1}, nil)
			mockHelper.EXPECT().GetId(listObject).Return("cluster-1")
			mockHelper.EXPECT().Get(gomock.Any(), "cluster-1").Return(nil, fmt.Errorf("not found"))

			runner := &runnerContext{
				objectHelper: mockHelper,
			}
			runner.args.format = outputFormatJson

			_, err := runner.fetchObjects(ctx, []string{"my-cluster"})
			Expect(err).To(HaveOccurred())
		})
	})

	It("offers all networking resources for get completion", func() {
		completed, directive := completeObjectTypes(nil, nil, "")
		Expect(directive).To(Equal(cobra.ShellCompDirectiveNoFileComp))
		Expect(completed).To(ContainElements(
			"virtualnetwork", "virtualnetworks",
			"subnet", "subnets",
			"securitygroup", "securitygroups",
			"externalip", "externalips",
			"externalipattachment", "externalipattachments",
			"natgateway", "natgateways",
		))
	})

	for _, format := range []string{outputFormatJson, outputFormatYaml} {
		It("renders public ComputeInstance disk progress in "+format, func() {
			var stdout bytes.Buffer
			console, err := terminal.NewConsole().SetLogger(logger).
				SetStdout(&stdout).SetStderr(&bytes.Buffer{}).Build()
			Expect(err).ToNot(HaveOccurred())
			instance := publicv1.ComputeInstance_builder{
				Status: publicv1.ComputeInstanceStatus_builder{
					Volumes: []*publicv1.ComputeInstanceVolume{
						publicv1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "volume-boot"}.Build(),
					},
					Conditions: []*publicv1.ComputeInstanceCondition{
						publicv1.ComputeInstanceCondition_builder{
							Type:    publicv1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED,
							Status:  publicv1.ConditionStatus_CONDITION_STATUS_FALSE,
							Reason:  proto.String("VolumeRegistrationPending"),
							Message: proto.String("Disk Volume IDs persisted: 1/2; blocked disks: disk-1; recovery pending."),
						}.Build(),
					},
				}.Build(),
			}.Build()
			runner := &runnerContext{
				console:        console,
				marshalOptions: protojson.MarshalOptions{UseProtoNames: true},
			}
			if format == outputFormatJson {
				Expect(runner.renderJson(ctx, []proto.Message{instance})).To(Succeed())
			} else {
				Expect(runner.renderYaml(ctx, []proto.Message{instance})).To(Succeed())
			}
			Expect(stdout.String()).To(ContainSubstring("volume-boot"))
			Expect(stdout.String()).To(ContainSubstring("VolumeRegistrationPending"))
			Expect(stdout.String()).To(ContainSubstring("disk-1"))
			Expect(stdout.String()).NotTo(ContainSubstring("volume_registration"))
		})
	}
})

var _ = Describe("Compute instance status formats", func() {
	for _, format := range []string{outputFormatJson, outputFormatYaml} {
		It("preserves the provisioning contract in "+format, func() {
			var stdout bytes.Buffer
			console, err := terminal.NewConsole().SetLogger(logger).
				SetStdout(&stdout).SetStderr(&bytes.Buffer{}).Build()
			Expect(err).NotTo(HaveOccurred())
			instance := publicv1.ComputeInstance_builder{
				Status: publicv1.ComputeInstanceStatus_builder{
					Conditions: []*publicv1.ComputeInstanceCondition{
						publicv1.ComputeInstanceCondition_builder{
							Type:   publicv1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED,
							Status: publicv1.ConditionStatus_CONDITION_STATUS_FALSE,
							Reason: proto.String("RetryScheduled"),
						}.Build(),
					},
					LastProvisioningFailure: publicv1.ComputeInstanceProvisioningFailure_builder{
						Reason:  publicv1.ComputeInstanceProvisioningFailureReason_COMPUTE_INSTANCE_PROVISIONING_FAILURE_REASON_PROVIDER_REPORTED,
						Message: "Provisioning failed.", Attempt: 2,
						ObservedAt: timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
					}.Build(),
				}.Build(),
			}.Build()
			original := proto.Clone(instance)
			runner := &runnerContext{console: console, marshalOptions: protojson.MarshalOptions{UseProtoNames: true}}
			if format == outputFormatJson {
				Expect(runner.renderJson(context.Background(), []proto.Message{instance})).To(Succeed())
			} else {
				Expect(runner.renderYaml(context.Background(), []proto.Message{instance})).To(Succeed())
			}
			data := stdout.Bytes()
			if format == outputFormatYaml {
				data, err = yaml.YAMLToJSON(data)
				Expect(err).NotTo(HaveOccurred())
			}
			var actual map[string]any
			Expect(json.Unmarshal(data, &actual)).To(Succeed())
			expectedData, err := runner.marshalOptions.Marshal(instance)
			Expect(err).NotTo(HaveOccurred())
			var expected map[string]any
			Expect(json.Unmarshal(expectedData, &expected)).To(Succeed())
			Expect(actual["status"]).To(Equal(expected["status"]))
			Expect(actual).NotTo(HaveKey("PROVISIONING"))
			Expect(actual).NotTo(HaveKey("LAST FAILURE"))
			Expect(proto.Equal(instance, original)).To(BeTrue())
		})
	}

	It("documents provisioning list columns in rendered help", func() {
		cmd := Cmd()
		help.Setup(cmd)
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		cmd.SetArgs([]string{"--help"})
		Expect(cmd.Execute()).To(Succeed())
		Expect(output.String()).To(ContainSubstring("PROVISIONING"))
		Expect(output.String()).To(ContainSubstring("LAST FAILURE"))
		Expect(output.String()).To(ContainSubstring("Retrying"))
		Expect(output.String()).NotTo(ContainSubstring("{{"))
	})
})
