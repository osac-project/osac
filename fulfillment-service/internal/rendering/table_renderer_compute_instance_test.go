/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package rendering

import (
	"bytes"
	"context"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Compute instance table rendering", func() {
	var ctrl *gomock.Controller

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	DescribeTable(
		"Resolves the TEMPLATE column",
		func(ctx context.Context, templateName string, expectedSubstring string) {
			// Create the template object:
			templateMetadata := &publicv1.Metadata{}
			if templateName != "" {
				templateMetadata.SetName(templateName)
			}
			templateObj := &publicv1.ComputeInstanceTemplate{}
			templateObj.SetId("my-template")
			templateObj.SetMetadata(templateMetadata)

			// Create a helper that returns the template object:
			templateDescriptor := templateObj.ProtoReflect().Descriptor()
			templateFullName := templateDescriptor.FullName()
			templateHelper := reflection.NewMockObjectHelper(ctrl)
			templateHelper.EXPECT().FullName().
				Return(templateFullName).
				AnyTimes()
			templateHelper.EXPECT().Descriptor().
				Return(templateDescriptor).
				AnyTimes()
			templateHelper.EXPECT().String().
				Return(string(templateFullName)).
				AnyTimes()
			templateHelper.EXPECT().IsTenantScoped().
				Return(true).
				AnyTimes()
			templateHelper.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(
					reflection.ListResult{
						Items: []proto.Message{
							templateObj,
						},
						Total: 1,
					},
					nil).
				AnyTimes()
			templateHelper.EXPECT().
				GetName(gomock.Any()).
				DoAndReturn(func(obj proto.Message) string {
					return templateName
				}).
				AnyTimes()

			// Create the compute instance object:
			computeObj := publicv1.ComputeInstance_builder{
				Id: uuid.New(),
				Metadata: publicv1.Metadata_builder{
					Name: "my-instance",
				}.Build(),
				Spec: publicv1.ComputeInstanceSpec_builder{
					Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "my-template"}.Build(),
				}.Build(),
			}.Build()

			// Mock helper for the compute instance object:
			computeDescriptor := computeObj.ProtoReflect().Descriptor()
			computeHelper := reflection.NewMockObjectHelper(ctrl)
			computeHelper.EXPECT().FullName().
				Return(computeDescriptor.FullName()).
				AnyTimes()
			computeHelper.EXPECT().Descriptor().
				Return(computeDescriptor).
				AnyTimes()
			computeHelper.EXPECT().String().
				Return(string(computeDescriptor.FullName())).
				AnyTimes()
			computeHelper.EXPECT().IsTenantScoped().
				Return(true).
				AnyTimes()

			// Create a helper that returns compute instances or templates based on the object type:
			helper := reflection.NewMockHelper(ctrl)
			helper.EXPECT().
				Lookup(gomock.Any()).
				DoAndReturn(func(objectType string) reflection.ObjectHelper {
					switch objectType {
					case string(computeDescriptor.FullName()):
						return computeHelper
					case string(templateDescriptor.FullName()):
						return templateHelper
					default:
						return nil
					}
				}).
				AnyTimes()

			// Create the renderer:
			buffer := &bytes.Buffer{}
			renderer, err := NewTableRenderer().
				SetLogger(logger).
				SetHelper(helper).
				SetWriter(buffer).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Render the compute instance object:
			err = renderer.Render(ctx, []proto.Message{computeObj})
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(ContainSubstring(expectedSubstring))
		},
		Entry(
			// Regression for MGMT-23970: TEMPLATE column was blank when 'metadata.name' was empty.
			"Falls back to the key when the looked-up object has no name",
			"",
			"my-template",
		),
		Entry(
			"Shows the template name when the looked-up object has a name",
			"my-name",
			"my-name",
		),
	)
})

var _ = Describe("Compute instance provisioning columns", func() {
	var render func(context.Context, *publicv1.ComputeInstanceStatus) map[string]string

	BeforeEach(func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		descriptor := (&publicv1.ComputeInstance{}).ProtoReflect().Descriptor()
		objectHelper := reflection.NewMockObjectHelper(ctrl)
		objectHelper.EXPECT().FullName().Return(descriptor.FullName()).AnyTimes()
		objectHelper.EXPECT().Descriptor().Return(descriptor).AnyTimes()
		objectHelper.EXPECT().String().Return(string(descriptor.FullName())).AnyTimes()
		objectHelper.EXPECT().IsTenantScoped().Return(true).AnyTimes()
		lookupHelper := reflection.NewMockObjectHelper(ctrl)
		lookupHelper.EXPECT().List(gomock.Any(), gomock.Any()).Return(reflection.ListResult{}, nil).AnyTimes()
		lookupHelper.EXPECT().IsTenantScoped().Return(false).AnyTimes()
		helper := reflection.NewMockHelper(ctrl)
		helper.EXPECT().Lookup(string(descriptor.FullName())).Return(objectHelper).AnyTimes()
		helper.EXPECT().Lookup(gomock.Any()).Return(lookupHelper).AnyTimes()
		buffer := &bytes.Buffer{}
		renderer, err := NewTableRenderer().SetLogger(logger).SetHelper(helper).SetWriter(buffer).Build()
		Expect(err).NotTo(HaveOccurred())
		render = func(ctx context.Context, status *publicv1.ComputeInstanceStatus) map[string]string {
			buffer.Reset()
			instance := publicv1.ComputeInstance_builder{
				Id: "instance-1", Status: status,
				Spec: publicv1.ComputeInstanceSpec_builder{
					Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "template-1"}.Build(),
				}.Build(),
			}.Build()
			Expect(renderer.Render(ctx, []proto.Message{instance})).To(Succeed())
			lines := strings.Split(strings.TrimSpace(buffer.String()), "\n")
			Expect(lines).To(HaveLen(2))
			separator := regexp.MustCompile(`\s{2,}`)
			headers := separator.Split(strings.TrimSpace(lines[0]), -1)
			Expect(headers).To(ContainElements("PROVISIONING", "LAST FAILURE"))
			result := map[string]string{}
			for i, header := range headers {
				start := strings.Index(lines[0], header)
				end := len(lines[1])
				if i+1 < len(headers) {
					end = min(end, strings.Index(lines[0], headers[i+1]))
				}
				if start < end {
					result[header] = strings.TrimSpace(lines[1][start:end])
				}
			}
			return result
		}
	})

	condition := func(kind publicv1.ComputeInstanceConditionType, status publicv1.ConditionStatus, reason string) *publicv1.ComputeInstanceCondition {
		return publicv1.ComputeInstanceCondition_builder{
			Type: kind, Status: status, Reason: proto.String(reason),
			Message: proto.String("retrying after a failure"),
		}.Build()
	}
	provisioned := publicv1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED
	falseStatus := publicv1.ConditionStatus_CONDITION_STATUS_FALSE

	DescribeTable("uses only explicit active retry conditions",
		func(ctx context.Context, conditions []*publicv1.ComputeInstanceCondition, expected string) {
			row := render(ctx, publicv1.ComputeInstanceStatus_builder{
				State:      publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
				Conditions: conditions,
			}.Build())
			Expect(row["STATE"]).To(Equal("RUNNING"))
			Expect(row["PROVISIONING"]).To(Equal(expected))
			Expect(row["LAST FAILURE"]).To(Equal("-"))
		},
		Entry("scheduled legacy retry without failure", []*publicv1.ComputeInstanceCondition{condition(provisioned, falseStatus, "RetryScheduled")}, "Retrying"),
		Entry("running retry without failure", []*publicv1.ComputeInstanceCondition{condition(provisioned, falseStatus, "RetryRunning")}, "Retrying"),
		Entry("true condition is not retrying", []*publicv1.ComputeInstanceCondition{condition(provisioned, publicv1.ConditionStatus_CONDITION_STATUS_TRUE, "RetryScheduled")}, "-"),
		Entry("unknown status is not retrying", []*publicv1.ComputeInstanceCondition{condition(provisioned, publicv1.ConditionStatus(99), "RetryRunning")}, "-"),
		Entry("unspecified status is not retrying", []*publicv1.ComputeInstanceCondition{condition(provisioned, publicv1.ConditionStatus(0), "RetryRunning")}, "-"),
		Entry("different condition type is not retrying", []*publicv1.ComputeInstanceCondition{condition(publicv1.ComputeInstanceConditionType(0), falseStatus, "RetryScheduled")}, "-"),
		Entry("terminal failure is not retrying", []*publicv1.ComputeInstanceCondition{condition(provisioned, falseStatus, "ProvisioningFailed")}, "-"),
		Entry("message alone is not retrying", []*publicv1.ComputeInstanceCondition{condition(provisioned, falseStatus, "WaitingForVM")}, "-"),
		Entry("unknown reason is not retrying", []*publicv1.ComputeInstanceCondition{condition(provisioned, falseStatus, "FutureReason")}, "-"),
		Entry("missing reason is not retrying", []*publicv1.ComputeInstanceCondition{condition(provisioned, falseStatus, "")}, "-"),
		Entry("missing conditions is not retrying", nil, "-"),
	)

	DescribeTable("labels failures independently of active retry",
		func(ctx context.Context, reason publicv1.ComputeInstanceProvisioningFailureReason, expected string) {
			for _, retryReason := range []string{"RetryScheduled", "RetryRunning", "ProvisioningFailed", ""} {
				row := render(ctx, publicv1.ComputeInstanceStatus_builder{
					Conditions:              []*publicv1.ComputeInstanceCondition{condition(provisioned, falseStatus, retryReason)},
					LastProvisioningFailure: publicv1.ComputeInstanceProvisioningFailure_builder{Reason: reason, Attempt: 2}.Build(),
				}.Build())
				Expect(row["LAST FAILURE"]).To(Equal(expected))
				if strings.HasPrefix(retryReason, "Retry") {
					Expect(row["PROVISIONING"]).To(Equal("Retrying"))
				} else {
					Expect(row["PROVISIONING"]).To(Equal("-"))
				}
			}
		},
		Entry("unspecified", publicv1.ComputeInstanceProvisioningFailureReason(0), "UNSPECIFIED"),
		Entry("provider reported", publicv1.ComputeInstanceProvisioningFailureReason(1), "PROVIDER_REPORTED"),
		Entry("result rejected", publicv1.ComputeInstanceProvisioningFailureReason(2), "RESULT_REJECTED"),
		Entry("timed out", publicv1.ComputeInstanceProvisioningFailureReason(3), "TIMED_OUT"),
		Entry("job lost", publicv1.ComputeInstanceProvisioningFailureReason(4), "JOB_LOST"),
		Entry("job not started", publicv1.ComputeInstanceProvisioningFailureReason(5), "JOB_NOT_STARTED"),
		Entry("invalid result", publicv1.ComputeInstanceProvisioningFailureReason(6), "INVALID_RESULT"),
		Entry("provider error", publicv1.ComputeInstanceProvisioningFailureReason(7), "PROVIDER_ERROR"),
		Entry("unknown numeric value", publicv1.ComputeInstanceProvisioningFailureReason(99), "UNKNOWN:99"),
	)

	It("handles an absent status", func(ctx context.Context) {
		row := render(ctx, nil)
		Expect(row["PROVISIONING"]).To(Equal("-"))
		Expect(row["LAST FAILURE"]).To(Equal("-"))
		Expect(row["INTERNAL IP ADDRESS"]).To(Equal("-"))
	})

	It("shows interface IPs and skips entries awaiting an IP", func(ctx context.Context) {
		row := render(ctx, publicv1.ComputeInstanceStatus_builder{
			Interfaces: []*publicv1.ComputeInstanceInterfaceStatus{
				publicv1.ComputeInstanceInterfaceStatus_builder{Name: "first", IpAddress: "192.0.2.10"}.Build(),
				publicv1.ComputeInstanceInterfaceStatus_builder{Name: "second", MacAddress: "02:00:00:00:00:02"}.Build(),
				publicv1.ComputeInstanceInterfaceStatus_builder{Name: "third", IpAddress: "192.0.2.11"}.Build(),
			},
		}.Build())
		Expect(row["INTERNAL IP ADDRESS"]).To(Equal("192.0.2.10, 192.0.2.11"))
	})

	DescribeTable("clears the presentation after success without waiting for readiness",
		func(ctx context.Context, reason string, status publicv1.ConditionStatus) {
			retry := render(ctx, publicv1.ComputeInstanceStatus_builder{
				Conditions:              []*publicv1.ComputeInstanceCondition{condition(provisioned, falseStatus, "RetryRunning")},
				LastProvisioningFailure: publicv1.ComputeInstanceProvisioningFailure_builder{Reason: publicv1.ComputeInstanceProvisioningFailureReason(1)}.Build(),
			}.Build())
			Expect(retry["PROVISIONING"]).To(Equal("Retrying"))
			Expect(retry["LAST FAILURE"]).To(Equal("PROVIDER_REPORTED"))
			cleared := render(ctx, publicv1.ComputeInstanceStatus_builder{
				Conditions: []*publicv1.ComputeInstanceCondition{condition(provisioned, status, reason)},
			}.Build())
			Expect(cleared["PROVISIONING"]).To(Equal("-"))
			Expect(cleared["LAST FAILURE"]).To(Equal("-"))
		},
		Entry("waiting for VM", "WaitingForVM", falseStatus),
		Entry("provisioning storage", "ProvisioningStorage", falseStatus),
		Entry("infrastructure ready", "InfrastructureReady", publicv1.ConditionStatus_CONDITION_STATUS_TRUE),
	)
})
