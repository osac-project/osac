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
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/help"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/yaml.v3"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/packages"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// Run the actual Cmd/runner.run/console path against the native local gRPC test
// server. Dynamic wire messages avoid generated server doubles for singleton
// services. Removing child registrations selects generic dispatch only;
// unrelated child commands remain registered in production.
func executeGenericGet(invoke func(string, proto.Message, proto.Message) error, args ...string) (string, error) {
	server := testing.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		defer GinkgoRecover()
		path, ok := grpc.MethodFromServerStream(stream)
		Expect(ok).To(BeTrue())
		name := strings.ReplaceAll(strings.TrimPrefix(path, "/"), "/", ".")
		descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(name))
		Expect(err).NotTo(HaveOccurred())
		method := descriptor.(protoreflect.MethodDescriptor)
		request, response := dynamicpb.NewMessage(method.Input()), dynamicpb.NewMessage(method.Output())
		if err := stream.RecvMsg(request); err != nil {
			return err
		}
		if err := invoke(path, request, response); err != nil {
			return err
		}
		return stream.SendMsg(response)
	}))
	DeferCleanup(server.Stop)
	server.Start()
	var stdout bytes.Buffer
	console, err := terminal.NewConsole().SetLogger(logger).SetStdout(&stdout).SetStderr(&bytes.Buffer{}).Build()
	Expect(err).NotTo(HaveOccurred())
	settings, err := config.NewSettings().SetLogger(logger).SetDir("/tmp/opencode").Build()
	Expect(err).NotTo(HaveOccurred())
	settings.SetAddress(server.Address())
	settings.SetPlaintext(true)
	settings.SetPrivate(true)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx = config.ProjectIntoContext(config.TenantIntoContext(ctx, "p2-tenant"), "p2-project")
	ctx = logging.LoggerIntoContext(terminal.ConsoleIntoContext(config.SettingsIntoContext(ctx, settings), console), logger)
	command := Cmd()
	root := &cobra.Command{Use: "osac", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(command)
	root.SetOut(&stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(append([]string{"get"}, args...))
	err = root.ExecuteContext(ctx)
	return stdout.String(), err
}

// Synthetic provider text is deliberately not normalized by generic structured
// output. Terminal table sanitization has separate renderer coverage.
const structuredAttributionToken = "  p6-token\t\n\x1b\"quoted\"  "

func genericBillingItem(kind string, index int) map[string]any {
	metadata := map[string]any{
		"name": fmt.Sprintf("billing-name-%d", index), "tenant": "p2-tenant",
		"labels":     map[string]any{"env": "test"},
		"finalizers": []any{"p6-test-finalizer"},
	}
	item := map[string]any{"id": fmt.Sprintf("billing-id-%d", index), "metadata": metadata}
	if kind == "billingaccount" {
		metadata["tenant"] = "system"
		item["spec"] = map[string]any{}
		status := map[string]any{
			"provider_key": "m360", "display_name": "Provider display label", "base_currency": "USD",
			"state": "BILLING_ACCOUNT_STATE_ACTIVE", "state_observation_timestamp": "2026-01-01T00:00:00Z",
			"external_billing_account_id": structuredAttributionToken,
			"provider_account_reference":  "provider-native-reference",
		}
		item["status"] = status
	} else {
		item["spec"] = map[string]any{
			"billing_account": map[string]any{"id": "account-id", "name": "ba-stable-name"},
		}
		status := map[string]any{
			"connection_state":         "TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING",
			"effective_from_timestamp": "2026-01-01T00:00:00Z",
			"message":                  "BILLING_PROVIDER_CORRELATION_WRITE_UNSUPPORTED: manual remediation required",
		}
		if index > 0 {
			status["effective_to_timestamp"] = "2026-02-01T00:00:00Z"
		}
		item["status"] = status
	}
	return item
}

func populateGenericBillingResponse(response proto.Message, kind string, start, count, total int) error {
	items := make([]any, count)
	for i := range items {
		items[i] = genericBillingItem(kind, start+i)
	}
	data, err := json.Marshal(map[string]any{"items": items, "size": count, "total": total})
	if err != nil {
		return err
	}
	return protojson.Unmarshal(data, response)
}

var _ = Describe("Get command", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("generic billing command", func() {
		pkg := packages.PrivateV1
		for _, kind := range []string{"billingaccount", "tenantbillingassignment"} {
			service, objectName := "BillingAccounts", "BillingAccount"
			if kind == "tenantbillingassignment" {
				service, objectName = "TenantBillingAssignments", "TenantBillingAssignment"
			}
			for _, format := range []string{outputFormatJson, outputFormatYaml} {
				DescribeTable(pkg+" "+kind+" "+format+" uses List-only Any item cardinality and descriptor visibility",
					func(count int) {
						var calls atomic.Int32
						stdout, err := executeGenericGet(func(method string, request, response proto.Message) error {
							calls.Add(1)
							Expect(method).To(Equal("/"+pkg+"."+service+"/List"), "keyed billing must not invoke the account Get scaffold")
							message := request.ProtoReflect()
							Expect(message.Get(message.Descriptor().Fields().ByName("offset")).Int()).To(BeZero())
							return populateGenericBillingResponse(response, kind, 0, count, count)
						}, kind, "billing-id-0", `second"key`, "--output="+format)
						Expect(err).NotTo(HaveOccurred())
						Expect(calls.Load()).To(Equal(int32(1)))
						var value any
						if format == outputFormatJson {
							Expect(json.Unmarshal([]byte(stdout), &value)).To(Succeed())
						} else {
							Expect(yaml.Unmarshal([]byte(stdout), &value)).To(Succeed())
						}
						var values []any
						if count == 1 {
							values = []any{value}
						} else {
							var ok bool
							values, ok = value.([]any)
							Expect(ok).To(BeTrue(), "zero/many must be sequences, never size/total/items envelopes")
						}
						Expect(values).To(HaveLen(count))
						for i, value := range values {
							expected := genericBillingItem(kind, i)
							expected["@type"] = "type.googleapis.com/" + pkg + "." + objectName
							Expect(value).To(Equal(expected), "private descriptors retain their fields; public descriptors omit native identity and finalizers")
						}
					},
					Entry("zero", 0), Entry("one", 1), Entry("many", 2),
				)
			}

			DescribeTable(pkg+" "+kind+" reaches domain tables and the generic empty message",
				func(count int) {
					var calls atomic.Int32
					stdout, err := executeGenericGet(func(method string, _, response proto.Message) error {
						calls.Add(1)
						Expect(method).To(Equal("/" + pkg + "." + service + "/List"))
						return populateGenericBillingResponse(response, kind, 0, count, count)
					}, kind)
					Expect(err).NotTo(HaveOccurred())
					Expect(calls.Load()).To(Equal(int32(1)))
					if count == 0 {
						Expect(stdout).To(ContainSubstring("There are no objects matching the given criteria."))
						return
					}
					Expect(stdout).To(ContainSubstring("billing-id-0"))
					Expect(stdout).To(ContainSubstring("billing-name-0"))
					Expect(stdout).To(MatchRegexp(`\bNAME\b`))
					Expect(stdout).NotTo(ContainSubstring("provider-native-reference"))
					if kind == "billingaccount" {
						Expect(stdout).NotTo(MatchRegexp(`\bTENANT\b`))
						Expect(stdout).To(ContainSubstring("ACTIVE"))
					} else {
						Expect(stdout).To(MatchRegexp(`\bTENANT\b`))
						Expect(stdout).To(ContainSubstring("PENDING"))
						Expect(stdout).To(ContainSubstring("manual remediation required"))
					}
				},
				Entry("empty", 0), Entry("one", 1),
			)

			DescribeTable(pkg+" "+kind+" carries keys and domain CEL unchanged on the first page",
				func(history bool) {
					filter := `this.status.state == 1 && this.status.state_observation_timestamp >= timestamp("2026-01-01T00:00:00Z") && this.metadata.labels["env"] == "test"`
					if kind == "tenantbillingassignment" {
						filter = `this.status.connection_state == 1 && this.status.effective_from_timestamp >= timestamp("2026-01-01T00:00:00Z") && this.metadata.labels["env"] == "test" && `
						if history {
							filter += `has(this.status.effective_to_timestamp)`
						} else {
							filter += `!has(this.status.effective_to_timestamp)`
						}
					}
					filter = "(" + filter + `) || this.metadata.tenant == "other-tenant"`
					expected := `(this.id in ["first", "two\"quoted"] || this.metadata.name in ["first", "two\"quoted"]) && (` + filter + `)`
					if kind == "tenantbillingassignment" {
						expected = `this.metadata.tenant == "p2-tenant" && (` + expected + `)`
					}
					var calls atomic.Int32
					stdout, err := executeGenericGet(func(method string, request, response proto.Message) error {
						calls.Add(1)
						Expect(method).To(Equal("/" + pkg + "." + service + "/List"))
						message := request.ProtoReflect()
						fields := message.Descriptor().Fields()
						Expect(message.Get(fields.ByName("filter")).String()).To(Equal(expected))
						return populateGenericBillingResponse(response, kind, 0, 1, 2)
					}, kind, "first", `two"quoted`, "--output=json", "--filter="+filter)
					Expect(err).NotTo(HaveOccurred())
					Expect(calls.Load()).To(Equal(int32(1)))
					var value any
					Expect(json.Unmarshal([]byte(stdout), &value)).To(Succeed())
					var values []any
					if list, ok := value.([]any); ok {
						values = list
					} else {
						values = []any{value}
					}
					Expect(values).To(HaveLen(1))
				},
				Entry("current", false), Entry("history", true),
			)

			for _, format := range []string{outputFormatTable, outputFormatJson, outputFormatYaml} {
				It(pkg+" "+kind+" "+format+" buffers actual command stdout when the first List request fails", func() {
					var calls atomic.Int32
					stdout, err := executeGenericGet(func(method string, _, _ proto.Message) error {
						calls.Add(1)
						Expect(method).To(Equal("/" + pkg + "." + service + "/List"))
						return grpcstatus.Error(codes.Internal, "p6 billing List failed")
					}, kind, "--output="+format)
					Expect(calls.Load()).To(Equal(int32(1)))
					Expect(stdout).To(BeEmpty(), "no billing inventory may escape the real console path")
					Expect(err).To(MatchError(ContainSubstring("p6 billing List failed")))
				})
			}
		}

		It("has no public billing descriptors and preserves private field visibility", func() {
			for _, name := range []protoreflect.FullName{
				"osac.public.v1.BillingAccount",
				"osac.public.v1.BillingAccountSignal",
				"osac.public.v1.BillingProviderStatus",
				"osac.public.v1.TenantBillingAssignment",
			} {
				_, err := protoregistry.GlobalFiles.FindDescriptorByName(name)
				Expect(err).To(HaveOccurred(), string(name))
			}
			descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName("osac.private.v1.BillingAccount")
			Expect(err).NotTo(HaveOccurred())
			fields := descriptor.(protoreflect.MessageDescriptor).Fields()
			status := fields.ByName("status").Message().Fields()
			metadata := fields.ByName("metadata").Message().Fields()
			Expect(status.ByName("provider_account_reference")).NotTo(BeNil())
			Expect(metadata.ByName("finalizers")).NotTo(BeNil())
		})
	})

	It("registers billing status alongside the existing get subcommands", func() {
		var names []string
		for _, child := range Cmd().Commands() {
			names = append(names, child.Name())
		}
		Expect(names).To(ConsistOf("billingproviderstatus", "externalippool", "storagetier", "token"))
	})

	Describe("get command completion", func() {

		It("completes billing status through Cobra subcommand registration", func() {
			var output bytes.Buffer
			command := &cobra.Command{Use: "osac"}
			command.AddCommand(Cmd())
			command.SetOut(&output)
			command.SetErr(&bytes.Buffer{})
			command.SetArgs([]string{"__completeNoDesc", "get", "billingprovider"})
			Expect(command.ExecuteContext(context.Background())).To(Succeed())
			Expect(strings.Fields(output.String())).To(ContainElement("billingproviderstatus"))
		})

	})

	Describe("billing status subcommand uses one empty Get", func() {
		pkg := packages.PrivateV1
		for _, format := range []string{outputFormatTable, outputFormatJson, outputFormatYaml} {
			DescribeTable(pkg+" "+format+" preserves singleton payload presence without identity/metadata",
				func(shape string) {
					var calls atomic.Int32
					stdout, err := executeGenericGet(func(method string, request, response proto.Message) error {
						calls.Add(1)
						Expect(method).To(Equal("/"+pkg+".BillingProviderStatuses/Get"), "never List, Watch, or a keyed fallback")
						Expect(request.ProtoReflect().Descriptor().Fields().Len()).To(BeZero())
						Expect(proto.Size(request)).To(BeZero())
						if shape != "absent" {
							res := response.ProtoReflect()
							object := res.Mutable(res.Descriptor().Fields().ByName("object")).Message()
							Expect(object.Descriptor().Fields().ByName("id")).To(BeNil())
							Expect(object.Descriptor().Fields().ByName("metadata")).To(BeNil())
							if shape == "payload" {
								fields := object.Descriptor().Fields()
								state := fields.ByName("state")
								object.Set(state, protoreflect.ValueOfEnum(state.Enum().Values().ByName("BILLING_PROVIDER_STATE_NOT_CONFIGURED").Number()))
								object.Set(fields.ByName("message"), protoreflect.ValueOfString("p2 singleton payload"))
							}
						}
						return nil
					}, "billingproviderstatus", "--output="+format)
					Expect(calls.Load()).To(Equal(int32(1)), "actual generic command must invoke Get, not merely offer a discovery capability")
					if shape == "absent" {
						Expect(err).To(HaveOccurred())
						Expect(stdout).To(BeEmpty())
						return
					}
					Expect(err).NotTo(HaveOccurred())
					Expect(stdout).NotTo(BeEmpty())
					if format == outputFormatTable {
						Expect(stdout).NotTo(MatchRegexp(`\b(ID|NAME|TENANT|DELETING)\b`))
						Expect(strings.Fields(strings.Split(strings.TrimSpace(stdout), "\n")[0])).To(Equal([]string{
							"STATE", "PROVIDER", "MESSAGE", "CAPABILITIES", "LAST-SUCCESSFUL-CHECK", "USAGE-DELIVERY",
						}))
						if shape == "payload" {
							Expect(stdout).To(ContainSubstring("p2 singleton payload"))
						}
					} else {
						var value map[string]any
						if format == outputFormatJson {
							Expect(json.Unmarshal([]byte(stdout), &value)).To(Succeed())
						} else {
							Expect(yaml.Unmarshal([]byte(stdout), &value)).To(Succeed())
						}
						expected := map[string]any{"@type": "type.googleapis.com/" + pkg + ".BillingProviderStatus"}
						if shape == "payload" {
							expected["state"] = "BILLING_PROVIDER_STATE_NOT_CONFIGURED"
							expected["message"] = "p2 singleton payload"
						}
						Expect(value).To(Equal(expected))
					}
				},
				Entry("present NOT_CONFIGURED payload", "payload"), Entry("present empty object", "empty"), Entry("absent object", "absent"),
			)
		}

		DescribeTable(pkg+" rejects singleton keys and explicitly supplied flags before RPC",
			func(extra []string) {
				var calls atomic.Int32
				args := append([]string{"billingproviderstatus"}, extra...)
				stdout, err := executeGenericGet(func(string, proto.Message, proto.Message) error {
					calls.Add(1)
					return grpcstatus.Error(codes.Internal, "singleton validation reached RPC")
				}, args...)
				Expect(err).To(HaveOccurred())
				Expect(calls.Load()).To(BeZero())
				Expect(stdout).To(BeEmpty())
			},
			Entry("key", []string{"invented-id"}), Entry("explicit empty filter", []string{"--filter="}),
			Entry("explicit filter", []string{"--filter=true"}), Entry("explicit false watch", []string{"--watch=false"}),
			Entry("watch", []string{"--watch"}), Entry("invalid format", []string{"--output=invalid"}),
		)
	})

	DescribeTable("singleton structured output has truthful shape",
		func(format string, count int) {
			var stdout bytes.Buffer
			console, err := terminal.NewConsole().SetLogger(logger).SetStdout(&stdout).SetStderr(&bytes.Buffer{}).Build()
			Expect(err).NotTo(HaveOccurred())
			runner := &runnerContext{console: console, marshalOptions: protojson.MarshalOptions{UseProtoNames: true}}
			objects := make([]proto.Message, count)
			for i := range objects {
				objects[i] = &privatev1.BillingProviderStatus{State: privatev1.BillingProviderState_BILLING_PROVIDER_STATE_NOT_CONFIGURED}
			}
			var value any
			if format == outputFormatJson {
				Expect(runner.renderJson(ctx, objects)).To(Succeed())
				Expect(json.Unmarshal(stdout.Bytes(), &value)).To(Succeed())
			} else {
				Expect(runner.renderYaml(ctx, objects)).To(Succeed())
				Expect(yaml.Unmarshal(stdout.Bytes(), &value)).To(Succeed())
			}
			var values []any
			if count == 1 {
				values = []any{value}
			} else {
				var ok bool
				values, ok = value.([]any)
				Expect(ok).To(BeTrue(), "zero and many items must be sequences")
			}
			Expect(values).To(HaveLen(count))
			for _, item := range values {
				Expect(item).To(Equal(map[string]any{
					"@type": "type.googleapis.com/osac.private.v1.BillingProviderStatus",
					"state": "BILLING_PROVIDER_STATE_NOT_CONFIGURED",
				}))
			}
		},
		Entry("JSON zero", outputFormatJson, 0), Entry("JSON one", outputFormatJson, 1), Entry("JSON many", outputFormatJson, 2),
		Entry("YAML zero", outputFormatYaml, 0), Entry("YAML one", outputFormatYaml, 1), Entry("YAML many", outputFormatYaml, 2),
	)

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
				data, err = sigsyaml.YAMLToJSON(data)
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
