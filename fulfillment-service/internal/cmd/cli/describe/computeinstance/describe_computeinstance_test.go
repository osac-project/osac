/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package computeinstance

import (
	"bytes"
	"fmt"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/help"
	"io"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func formatComputeInstance(ci *publicv1.ComputeInstance) string {
	var buf bytes.Buffer
	GinkgoHelper()
	Expect(renderComputeInstance(&buf, ci)).To(Succeed())
	return buf.String()
}

var _ = Describe("Describe Compute Instance", func() {
	DescribeTable("returns output errors",
		func(id string) {
			outputErr := fmt.Errorf("output unavailable")
			reader, writer := io.Pipe()
			Expect(reader.CloseWithError(outputErr)).To(Succeed())
			DeferCleanup(writer.Close)

			Expect(renderComputeInstance(writer, &publicv1.ComputeInstance{Id: id})).To(MatchError(outputErr))
		},
		Entry("when the final flush fails", "ci-001"),
		Entry("when a formfeed flushes during a write", "ci-001\f"),
	)

	It("should display all fields when set", func() {
		ci := &publicv1.ComputeInstance{
			Id: "ci-001",
			Spec: &publicv1.ComputeInstanceSpec{
				Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "tpl-small-001"}.Build(),
			},
			Status: &publicv1.ComputeInstanceStatus{
				State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
			},
		}
		output := formatComputeInstance(ci)
		Expect(output).To(ContainSubstring("ci-001"))
		Expect(output).To(MatchRegexp(`Catalog Item:\s+-`))
		Expect(output).NotTo(ContainSubstring("Template:"))
		Expect(output).To(ContainSubstring("RUNNING"))
	})

	It("should strip COMPUTE_INSTANCE_STATE_ prefix from state", func() {
		ci := &publicv1.ComputeInstance{
			Id: "ci-002",
			Status: &publicv1.ComputeInstanceStatus{
				State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
			},
		}
		output := formatComputeInstance(ci)
		Expect(output).To(ContainSubstring("RUNNING"))
		Expect(output).NotTo(ContainSubstring("COMPUTE_INSTANCE_STATE_"))
	})

	It("should show '-' for catalog item when spec is nil", func() {
		ci := &publicv1.ComputeInstance{
			Id: "ci-003",
		}
		output := formatComputeInstance(ci)
		Expect(output).To(MatchRegexp(`Catalog Item:\s+-`))
	})

	It("should display last_restarted_at when set", func() {
		restartTime := time.Date(2026, 3, 15, 10, 30, 0, 0, time.UTC)
		ci := &publicv1.ComputeInstance{
			Id: "ci-test-001",
			Spec: &publicv1.ComputeInstanceSpec{
				Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "tpl-small-001"}.Build(),
			},
			Status: &publicv1.ComputeInstanceStatus{
				State:           publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
				LastRestartedAt: timestamppb.New(restartTime),
			},
		}

		output := formatComputeInstance(ci)
		Expect(output).NotTo(ContainSubstring("Template:"))
		Expect(output).To(ContainSubstring("Catalog Item:"))
		Expect(output).To(ContainSubstring("Last Restarted At:"))
		Expect(output).To(ContainSubstring("2026-03-15T10:30:00Z"))
	})

	It("should omit last_restarted_at when not set", func() {
		ci := &publicv1.ComputeInstance{
			Id: "ci-test-002",
			Spec: &publicv1.ComputeInstanceSpec{
				Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "tpl-small-001"}.Build(),
			},
			Status: &publicv1.ComputeInstanceStatus{
				State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
			},
		}

		output := formatComputeInstance(ci)
		Expect(output).NotTo(ContainSubstring("Template:"))
		Expect(output).To(ContainSubstring("Catalog Item:"))
		Expect(output).NotTo(ContainSubstring("Last Restarted At:"))
	})

	It("should omit last_restarted_at when status is nil", func() {
		ci := &publicv1.ComputeInstance{
			Id: "ci-test-003",
			Spec: &publicv1.ComputeInstanceSpec{
				Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "tpl-small-001"}.Build(),
			},
		}

		output := formatComputeInstance(ci)
		Expect(output).NotTo(ContainSubstring("Template:"))
		Expect(output).To(ContainSubstring("Catalog Item:"))
		Expect(output).To(MatchRegexp(`State:\s+-`))
		Expect(output).NotTo(ContainSubstring("Last Restarted At:"))
	})

	It("should NOT show Template: row even when template is set on spec", func() {
		ci := &publicv1.ComputeInstance{
			Id: "ci-010",
			Spec: &publicv1.ComputeInstanceSpec{
				Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "tpl-small-001"}.Build(),
			},
		}
		output := formatComputeInstance(ci)
		Expect(output).NotTo(ContainSubstring("Template:"))
		Expect(output).To(ContainSubstring("Catalog Item:"))
	})

	It("should show catalog item ID when set", func() {
		ci := &publicv1.ComputeInstance{
			Spec: publicv1.ComputeInstanceSpec_builder{
				CatalogItem: publicv1.ComputeInstanceCatalogItemReference_builder{Id: "my-ci-catalog-item"}.Build(),
			}.Build(),
		}
		output := formatComputeInstance(ci)
		Expect(output).To(MatchRegexp(`Catalog Item:\s+my-ci-catalog-item`))
	})
})

var _ = Describe("Compute instance provisioning details", func() {
	condition := func(kind publicv1.ComputeInstanceConditionType, status publicv1.ConditionStatus, reason string) *publicv1.ComputeInstanceCondition {
		return &publicv1.ComputeInstanceCondition{
			Type: kind, Status: status, Reason: proto.String(reason), Message: proto.String("retrying after a failure"),
		}
	}
	provisioned := publicv1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED
	falseStatus := publicv1.ConditionStatus_CONDITION_STATUS_FALSE
	trueStatus := publicv1.ConditionStatus_CONDITION_STATUS_TRUE
	unspecifiedStatus := publicv1.ConditionStatus_CONDITION_STATUS_UNSPECIFIED
	unknownStatus := publicv1.ConditionStatus(99)

	DescribeTable("uses only explicit active retry conditions",
		func(conditions []*publicv1.ComputeInstanceCondition, retry bool) {
			output := formatComputeInstance(&publicv1.ComputeInstance{Status: &publicv1.ComputeInstanceStatus{
				State:      publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
				Conditions: conditions,
			}})
			Expect(output).To(MatchRegexp(`State:\s+RUNNING`))
			if retry {
				Expect(output).To(MatchRegexp(`Provisioning:\s+Retrying`))
			} else {
				Expect(output).NotTo(ContainSubstring("Provisioning:"))
			}
			Expect(output).NotTo(ContainSubstring("Last Provisioning Failure"))
		},
		Entry("legacy scheduled retry", []*publicv1.ComputeInstanceCondition{condition(provisioned, falseStatus, "RetryScheduled")}, true),
		Entry("running retry", []*publicv1.ComputeInstanceCondition{condition(provisioned, falseStatus, "RetryRunning")}, true),
		Entry("true condition", []*publicv1.ComputeInstanceCondition{condition(provisioned, trueStatus, "RetryScheduled")}, false),
		Entry("unknown status", []*publicv1.ComputeInstanceCondition{condition(provisioned, unknownStatus, "RetryRunning")}, false),
		Entry("unspecified status", []*publicv1.ComputeInstanceCondition{condition(provisioned, unspecifiedStatus, "RetryRunning")}, false),
		Entry("different condition type", []*publicv1.ComputeInstanceCondition{condition(publicv1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_UNSPECIFIED, falseStatus, "RetryScheduled")}, false),
		Entry("terminal failure", []*publicv1.ComputeInstanceCondition{condition(provisioned, falseStatus, "ProvisioningFailed")}, false),
		Entry("free text alone", []*publicv1.ComputeInstanceCondition{condition(provisioned, falseStatus, "WaitingForVM")}, false),
		Entry("unknown reason", []*publicv1.ComputeInstanceCondition{condition(provisioned, falseStatus, "FutureReason")}, false),
		Entry("absent reason", []*publicv1.ComputeInstanceCondition{condition(provisioned, falseStatus, "")}, false),
		Entry("absent conditions", nil, false),
	)

	DescribeTable("shows the latest failure independently of retry",
		func(reason publicv1.ComputeInstanceProvisioningFailureReason, label, message string) {
			for _, retryReason := range []string{"RetryScheduled", "RetryRunning", "ProvisioningFailed", ""} {
				output := formatComputeInstance(&publicv1.ComputeInstance{Status: &publicv1.ComputeInstanceStatus{
					Conditions: []*publicv1.ComputeInstanceCondition{condition(provisioned, falseStatus, retryReason)},
					LastProvisioningFailure: &publicv1.ComputeInstanceProvisioningFailure{
						Reason: reason, Message: message, Attempt: 2,
						ObservedAt: timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("offset", 7200))),
					},
				}})
				Expect(output).To(MatchRegexp(`Last Provisioning Failure Reason:\s+` + label + `\n`))
				Expect(output).To(MatchRegexp(`Last Provisioning Failure Message:\s+` + message + `\n`))
				Expect(output).To(MatchRegexp(`Last Provisioning Failure Attempt:\s+2\n`))
				Expect(output).To(MatchRegexp(`Last Provisioning Failure Observed At:\s+2026-01-02T01:04:05Z\n`))
				if retryReason == "RetryScheduled" || retryReason == "RetryRunning" {
					Expect(output).To(MatchRegexp(`Provisioning:\s+Retrying`))
				} else {
					Expect(output).NotTo(ContainSubstring("Provisioning:"))
				}
			}
		},
		Entry("unspecified", publicv1.ComputeInstanceProvisioningFailureReason_COMPUTE_INSTANCE_PROVISIONING_FAILURE_REASON_UNSPECIFIED, "UNSPECIFIED", "Internal error"),
		Entry("provider reported", publicv1.ComputeInstanceProvisioningFailureReason_COMPUTE_INSTANCE_PROVISIONING_FAILURE_REASON_PROVIDER_REPORTED, "PROVIDER_REPORTED", "Provisioning failed."),
		Entry("result rejected", publicv1.ComputeInstanceProvisioningFailureReason_COMPUTE_INSTANCE_PROVISIONING_FAILURE_REASON_RESULT_REJECTED, "RESULT_REJECTED", "Internal error"),
		Entry("timed out", publicv1.ComputeInstanceProvisioningFailureReason_COMPUTE_INSTANCE_PROVISIONING_FAILURE_REASON_TIMED_OUT, "TIMED_OUT", "The provisioning attempt timed out."),
		Entry("job lost", publicv1.ComputeInstanceProvisioningFailureReason_COMPUTE_INSTANCE_PROVISIONING_FAILURE_REASON_JOB_LOST, "JOB_LOST", "Internal error"),
		Entry("job not started", publicv1.ComputeInstanceProvisioningFailureReason_COMPUTE_INSTANCE_PROVISIONING_FAILURE_REASON_JOB_NOT_STARTED, "JOB_NOT_STARTED", "Internal error"),
		Entry("invalid result", publicv1.ComputeInstanceProvisioningFailureReason_COMPUTE_INSTANCE_PROVISIONING_FAILURE_REASON_INVALID_RESULT, "INVALID_RESULT", "Internal error"),
		Entry("provider error", publicv1.ComputeInstanceProvisioningFailureReason_COMPUTE_INSTANCE_PROVISIONING_FAILURE_REASON_PROVIDER_ERROR, "PROVIDER_ERROR", "Internal error"),
		Entry("unknown numeric value", publicv1.ComputeInstanceProvisioningFailureReason(99), "UNKNOWN:99", "Internal error"),
	)

	DescribeTable("omits missing or invalid optional failure details",
		func(failure *publicv1.ComputeInstanceProvisioningFailure) {
			output := formatComputeInstance(&publicv1.ComputeInstance{Status: &publicv1.ComputeInstanceStatus{LastProvisioningFailure: failure}})
			Expect(output).To(MatchRegexp(`Last Provisioning Failure Reason:\s+UNSPECIFIED`))
			for _, field := range []string{"Message", "Attempt", "Observed At"} {
				Expect(output).NotTo(ContainSubstring("Last Provisioning Failure " + field + ":"))
			}
			Expect(output).NotTo(ContainSubstring("Provisioning:"))
		},
		Entry("empty failure", &publicv1.ComputeInstanceProvisioningFailure{}),
		Entry("invalid timestamp", &publicv1.ComputeInstanceProvisioningFailure{ObservedAt: &timestamppb.Timestamp{Seconds: 253402300800}}),
		Entry("negative attempt", &publicv1.ComputeInstanceProvisioningFailure{Attempt: -1}),
	)

	DescribeTable("clears retry and failure after success",
		func(reason string, status publicv1.ConditionStatus) {
			instance := &publicv1.ComputeInstance{Status: &publicv1.ComputeInstanceStatus{
				Conditions:              []*publicv1.ComputeInstanceCondition{condition(provisioned, falseStatus, "RetryRunning")},
				LastProvisioningFailure: &publicv1.ComputeInstanceProvisioningFailure{Message: "Provisioning failed."},
			}}
			Expect(formatComputeInstance(instance)).To(ContainSubstring("Last Provisioning Failure"))
			instance.Status.Conditions = []*publicv1.ComputeInstanceCondition{condition(provisioned, status, reason)}
			instance.Status.LastProvisioningFailure = nil
			output := formatComputeInstance(instance)
			Expect(output).NotTo(ContainSubstring("Last Provisioning Failure"))
			Expect(output).NotTo(ContainSubstring("Provisioning:"))
		},
		Entry("waiting for VM", "WaitingForVM", falseStatus),
		Entry("provisioning storage", "ProvisioningStorage", falseStatus),
		Entry("infrastructure ready", "InfrastructureReady", publicv1.ConditionStatus_CONDITION_STATUS_TRUE),
	)

	It("retains the exact fixed message", func() {
		for _, message := range []string{"Provisioning failed.", "Internal error", "The provisioning attempt timed out."} {
			output := formatComputeInstance(&publicv1.ComputeInstance{Status: &publicv1.ComputeInstanceStatus{
				LastProvisioningFailure: &publicv1.ComputeInstanceProvisioningFailure{Message: message},
			}})
			Expect(output).To(ContainSubstring(fmt.Sprintf("%s\n", message)))
		}
	})
})

var _ = Describe("Compute instance describe help", func() {
	It("renders retry and failure examples", func() {
		cmd := Cmd()
		help.Setup(cmd)
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		cmd.SetArgs([]string{"--help"})
		Expect(cmd.Execute()).To(Succeed())
		for _, text := range []string{"RetryScheduled", "RetryRunning", "ProvisioningFailed", "WaitingForVM", "ProvisioningStorage", "Last Provisioning Failure Attempt", "2026-01-02T03:04:05Z"} {
			Expect(output.String()).To(ContainSubstring(text))
		}
		Expect(output.String()).NotTo(ContainSubstring("{{"))
	})
})
