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
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/yaml.v3"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Table renderer", func() {
	var ctrl *gomock.Controller

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	// makeObjectHelper creates a object helper that returns the descriptor for the given type.
	makeObjectHelper := func(object proto.Message) *reflection.MockObjectHelper {
		descriptor := object.ProtoReflect().Descriptor()
		fullName := descriptor.FullName()
		helper := reflection.NewMockObjectHelper(ctrl)
		helper.EXPECT().FullName().
			Return(fullName).
			AnyTimes()
		helper.EXPECT().Descriptor().
			Return(descriptor).
			AnyTimes()
		helper.EXPECT().String().
			Return(string(fullName)).
			AnyTimes()
		// Billing accounts are platform-scoped despite having metadata. Assignments
		// remain tenant-scoped; ordinary resource fixtures retain tenant injection.
		tenantScoped := descriptor.Fields().ByName("metadata") != nil && descriptor.Name() != "BillingAccount"
		helper.EXPECT().IsTenantScoped().
			Return(tenantScoped).
			AnyTimes()
		return helper
	}

	// makeLookupHelper creates a lookup helper that returns an empty list for any lookup column.
	makeLookupHelper := func() *reflection.MockObjectHelper {
		helper := reflection.NewMockObjectHelper(ctrl)
		helper.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(reflection.ListResult{}, nil).
			AnyTimes()
		helper.EXPECT().IsTenantScoped().
			Return(false).
			AnyTimes()
		return helper
	}

	renderDiskImage := func(ctx context.Context, item proto.Message) string {
		objectHelper := makeObjectHelper(item)
		lookupHelper := makeLookupHelper()

		helper := reflection.NewMockHelper(ctrl)
		helper.EXPECT().
			Lookup(objectHelper.String()).
			Return(objectHelper).
			AnyTimes()
		helper.EXPECT().
			Lookup(gomock.Any()).
			Return(lookupHelper).
			AnyTimes()

		buffer := &bytes.Buffer{}
		renderer, err := NewTableRenderer().
			SetLogger(logger).
			SetHelper(helper).
			SetWriter(buffer).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(renderer.Render(ctx, []proto.Message{item})).To(Succeed())
		return buffer.String()
	}

	It("Renders DiskImage metadata.name and ID in separate columns", func(ctx context.Context) {
		items := []proto.Message{
			publicv1.DiskImage_builder{
				Id:       "di-123",
				Metadata: publicv1.Metadata_builder{Name: "fedora"}.Build(),
			}.Build(),
			privatev1.DiskImage_builder{
				Id:       "di-123",
				Metadata: privatev1.Metadata_builder{Name: "fedora"}.Build(),
			}.Build(),
		}

		for _, item := range items {
			output := renderDiskImage(ctx, item)
			Expect(output).To(MatchRegexp(`NAME.*\bID\b`))
			Expect(output).To(MatchRegexp(`(?m)^fedora.*di-123(?:\s|$)`))
		}
	})

	Describe("DELETING column", func() {
		renderSubnets := func(ctx context.Context, items []*publicv1.Subnet) string {
			// Create the object helpers for the type of the table and for lookups:
			objectHelper := makeObjectHelper(&publicv1.Subnet{})
			lookupHelper := makeLookupHelper()

			// Create the helper:
			helper := reflection.NewMockHelper(ctrl)
			helper.EXPECT().
				Lookup(objectHelper.String()).
				Return(objectHelper).
				AnyTimes()
			helper.EXPECT().
				Lookup(gomock.Any()).
				Return(lookupHelper).
				AnyTimes()

			// Try to render the table:
			buffer := &bytes.Buffer{}
			renderer, err := NewTableRenderer().
				SetLogger(logger).
				SetHelper(helper).
				SetWriter(buffer).
				Build()
			Expect(err).ToNot(HaveOccurred())
			err = renderer.Render(ctx, items)
			Expect(err).ToNot(HaveOccurred())
			return buffer.String()
		}

		It("Always includes the DELETING header", func(ctx context.Context) {
			output := renderSubnets(
				ctx,
				[]*publicv1.Subnet{
					publicv1.Subnet_builder{
						Id: "subnet-1",
						Metadata: publicv1.Metadata_builder{
							Name: "active-subnet",
						}.Build(),
					}.Build(),
				},
			)
			Expect(output).To(ContainSubstring("DELETING"))
		})

		It("Shows dash for non-deleting objects", func(ctx context.Context) {
			output := renderSubnets(
				ctx,
				[]*publicv1.Subnet{
					publicv1.Subnet_builder{
						Id: "subnet-1",
						Metadata: publicv1.Metadata_builder{
							Name: "active-subnet",
						}.Build(),
					}.Build(),
				},
			)
			Expect(output).To(MatchRegexp(`DELETING.*\n.*-`))
		})

		It("Shows 'Yes' for deleting objects", func(ctx context.Context) {
			output := renderSubnets(
				ctx,
				[]*publicv1.Subnet{
					publicv1.Subnet_builder{
						Id: "subnet-2",
						Metadata: publicv1.Metadata_builder{
							Name:              "deleting-subnet",
							DeletionTimestamp: timestamppb.Now(),
						}.Build(),
					}.Build(),
				})
			Expect(output).To(ContainSubstring("DELETING"))
			Expect(output).To(MatchRegexp(`Yes\s+.*subnet-2\s`))
		})
	})

	DescribeTable("DELETING requires a singular Timestamp descriptor",
		func(shape string) {
			metadataFields := []*descriptorpb.FieldDescriptorProto{{
				Name: proto.String("name"), Number: proto.Int32(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			}}
			if shape != "absent" {
				deletion := &descriptorpb.FieldDescriptorProto{
					Name: proto.String("deletion_timestamp"), Number: proto.Int32(2),
					Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".google.protobuf.Timestamp"),
				}
				switch shape {
				case "scalar":
					deletion.Type, deletion.TypeName = descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), nil
				case "repeated":
					deletion.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
				case "other message":
					deletion.TypeName = proto.String(".p2.render.Empty")
				}
				metadataFields = append(metadataFields, deletion)
			}
			file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
				Name: proto.String("p2_renderer.proto"), Package: proto.String("p2.render"), Syntax: proto.String("proto3"),
				Dependency: []string{"google/protobuf/timestamp.proto"},
				MessageType: []*descriptorpb.DescriptorProto{
					{Name: proto.String("Resource"), Field: []*descriptorpb.FieldDescriptorProto{
						{Name: proto.String("id"), Number: proto.Int32(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
						{Name: proto.String("metadata"), Number: proto.Int32(2), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".p2.render.Metadata")},
					}},
					{Name: proto.String("Metadata"), Field: metadataFields},
					{Name: proto.String("Empty")},
				},
			}, protoregistry.GlobalFiles)
			Expect(err).NotTo(HaveOccurred())
			object := dynamicpb.NewMessage(file.Messages().ByName("Resource"))
			object.Set(object.Descriptor().Fields().ByName("id"), protoreflect.ValueOfString("synthetic-resource"))
			objectHelper := reflection.NewMockObjectHelper(ctrl)
			objectHelper.EXPECT().FullName().Return(object.Descriptor().FullName()).AnyTimes()
			objectHelper.EXPECT().String().Return(string(object.Descriptor().FullName())).AnyTimes()
			objectHelper.EXPECT().Descriptor().Return(object.Descriptor()).AnyTimes()
			objectHelper.EXPECT().IsTenantScoped().Return(false).AnyTimes()
			helper := reflection.NewMockHelper(ctrl)
			helper.EXPECT().Lookup(string(object.Descriptor().FullName())).Return(objectHelper).AnyTimes()
			var output bytes.Buffer
			renderer, err := NewTableRenderer().SetLogger(logger).SetHelper(helper).SetWriter(&output).Build()
			Expect(err).NotTo(HaveOccurred())
			Expect(renderer.Render(context.Background(), []proto.Message{object})).To(Succeed())
			Expect(output.String()).To(ContainSubstring("synthetic-resource"))
			if shape == "timestamp" {
				Expect(output.String()).To(ContainSubstring("DELETING"))
			} else {
				Expect(output.String()).NotTo(ContainSubstring("DELETING"))
			}
		},
		Entry("no deletion field", "absent"), Entry("scalar is not a lifecycle timestamp", "scalar"),
		Entry("repeated Timestamp is not a lifecycle timestamp", "repeated"),
		Entry("arbitrary message is not a lifecycle timestamp", "other message"),
		Entry("ordinary singular Timestamp remains supported", "timestamp"),
	)

	renderInstanceTypes := func(ctx context.Context, items []*publicv1.InstanceType) string {
		objectHelper := makeObjectHelper(&publicv1.InstanceType{})
		lookupHelper := makeLookupHelper()

		helper := reflection.NewMockHelper(ctrl)
		helper.EXPECT().
			Lookup(objectHelper.String()).
			Return(objectHelper).
			AnyTimes()
		helper.EXPECT().
			Lookup(gomock.Any()).
			Return(lookupHelper).
			AnyTimes()

		buffer := &bytes.Buffer{}
		renderer, err := NewTableRenderer().
			SetLogger(logger).
			SetHelper(helper).
			SetWriter(buffer).
			Build()
		Expect(err).ToNot(HaveOccurred())
		err = renderer.Render(ctx, items)
		Expect(err).ToNot(HaveOccurred())
		return buffer.String()
	}

	Describe("Integer columns", func() {
		It("Renders integer fields as plain numbers", func(ctx context.Context) {
			output := renderInstanceTypes(
				ctx,
				[]*publicv1.InstanceType{
					publicv1.InstanceType_builder{
						Id: "standard-4-16",
						Metadata: publicv1.Metadata_builder{
							Name: "standard-4-16",
						}.Build(),
						Spec: publicv1.InstanceTypeSpec_builder{
							Vcpus:     4,
							MemoryGib: 16,
							State:     publicv1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
						}.Build(),
					}.Build(),
				},
			)
			Expect(output).To(MatchRegexp(`4\s+16\s+.*ACTIVE`))
			Expect(output).ToNot(ContainSubstring("%!s"))
		})
	})

	Describe("Optional GPU columns", func() {
		It("Renders GPU fields for GPU-enabled InstanceType", func(ctx context.Context) {
			output := renderInstanceTypes(
				ctx,
				[]*publicv1.InstanceType{
					publicv1.InstanceType_builder{
						Id: "gpu-a100-8core",
						Metadata: publicv1.Metadata_builder{
							Name: "gpu-a100-8core",
						}.Build(),
						Spec: publicv1.InstanceTypeSpec_builder{
							Vcpus:     8,
							MemoryGib: 64,
							State:     publicv1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
							Gpu: publicv1.GpuSpec_builder{
								PciDeviceSelector: "10DE:20B0",
								ResourceName:      "nvidia.com/A100",
								Count:             1,
							}.Build(),
						}.Build(),
					}.Build(),
				},
			)
			Expect(output).To(ContainSubstring("nvidia.com/A100"))
			Expect(output).To(MatchRegexp(`1\s+nvidia\.com/A100\s+ACTIVE`))
		})

		It("Renders blank GPU columns for non-GPU InstanceType", func(ctx context.Context) {
			output := renderInstanceTypes(
				ctx,
				[]*publicv1.InstanceType{
					publicv1.InstanceType_builder{
						Id: "standard-4-16",
						Metadata: publicv1.Metadata_builder{
							Name: "standard-4-16",
						}.Build(),
						Spec: publicv1.InstanceTypeSpec_builder{
							Vcpus:     4,
							MemoryGib: 16,
							State:     publicv1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
						}.Build(),
					}.Build(),
				},
			)
			Expect(output).To(ContainSubstring("GPU NAME"))
			Expect(output).To(MatchRegexp(`16\s+0\s+-\s+ACTIVE`))
		})
	})

	Describe("Billing layouts", func() {
		accountHeaders := []string{
			"ID", "NAME", "PROVIDER", "DISPLAY-NAME", "CURRENCY", "STATE",
			"EXTERNAL-BILLING-ACCOUNT-ID", "STATUS-OBSERVED",
		}
		assignmentHeaders := []string{
			"ID", "NAME", "TENANT", "ACCOUNT", "STATE", "EFFECTIVE-FROM", "EFFECTIVE-TO", "MESSAGE",
		}
		statusHeaders := []string{
			"STATE", "PROVIDER", "MESSAGE", "CAPABILITIES", "LAST-SUCCESSFUL-CHECK", "USAGE-DELIVERY",
		}

		// An inventory of existing files cannot detect a missing domain layout.
		DescribeTable("Requires all six explicit billing layouts and their domain headers",
			func(filename string, headers []string) {
				data, err := tablesFS.ReadFile("tables/" + filename)
				Expect(err).NotTo(HaveOccurred())
				var layout tableLayout
				Expect(yaml.Unmarshal(data, &layout)).To(Succeed())
				actual := make([]string, len(layout.Columns))
				for i, col := range layout.Columns {
					actual[i] = col.Header
				}
				Expect(actual).To(Equal(headers))
			},
			Entry("public account", "osac.public.v1.BillingAccount.yaml", accountHeaders),
			Entry("private account", "osac.private.v1.BillingAccount.yaml", accountHeaders),
			Entry("public assignment", "osac.public.v1.TenantBillingAssignment.yaml", assignmentHeaders),
			Entry("private assignment", "osac.private.v1.TenantBillingAssignment.yaml", assignmentHeaders),
			Entry("public status", "osac.public.v1.BillingProviderStatus.yaml", statusHeaders),
			Entry("private status", "osac.private.v1.BillingProviderStatus.yaml", statusHeaders),
		)

		// Decode fixtures into the actual generated types, not synthetic descriptors.
		billingObject := func(prototype proto.Message, fields map[string]any) proto.Message {
			data, err := json.Marshal(fields)
			Expect(err).NotTo(HaveOccurred())
			object := prototype.ProtoReflect().New().Interface()
			Expect(protojson.Unmarshal(data, object)).To(Succeed())
			return object
		}
		renderBilling := func(ctx context.Context, objects ...proto.Message) string {
			before := make([]proto.Message, len(objects))
			beforeJSON := make([]map[string]any, len(objects))
			for i, object := range objects {
				before[i] = proto.Clone(object)
				data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(object)
				Expect(err).NotTo(HaveOccurred())
				Expect(json.Unmarshal(data, &beforeJSON[i])).To(Succeed())
			}
			objectHelper := makeObjectHelper(objects[0])
			helper := reflection.NewMockHelper(ctrl)
			// No catch-all lookup or RPC expectations: references must render stored
			// identity directly, without attempting account Get or name resolution.
			helper.EXPECT().Lookup(objectHelper.String()).Return(objectHelper).AnyTimes()
			var output bytes.Buffer
			renderer, err := NewTableRenderer().SetLogger(logger).SetHelper(helper).SetWriter(&output).Build()
			Expect(err).NotTo(HaveOccurred())
			Expect(renderer.Render(ctx, objects)).To(Succeed())
			for i, object := range objects {
				Expect(proto.Equal(object, before[i])).To(BeTrue(), "table formatting must not mutate protobufs")
				data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(object)
				Expect(err).NotTo(HaveOccurred())
				var afterJSON map[string]any
				Expect(json.Unmarshal(data, &afterJSON)).To(Succeed())
				Expect(afterJSON).To(Equal(beforeJSON[i]), "raw structured values must remain unchanged")
			}
			return output.String()
		}
		expectBillingRows := func(output string, headers []string, rows ...[]string) {
			// Compare complete rendered cells, including whitespace tokens. Splitting
			// on spaces would accidentally equate such tokens with missing values.
			var expected bytes.Buffer
			writer := tabwriter.NewWriter(&expected, 0, 0, 2, ' ', 0)
			fmt.Fprintln(writer, strings.Join(headers, "\t"))
			for _, row := range rows {
				Expect(row).To(HaveLen(len(headers)))
				fmt.Fprintln(writer, strings.Join(row, "\t"))
			}
			Expect(writer.Flush()).To(Succeed())
			Expect(output).To(Equal(expected.String()))
		}
		withDeleting := func(cells []string) []string {
			return append([]string{cells[0], "-"}, cells[1:]...)
		}
		domainHeaders := func(headers []string) []string {
			return append([]string{headers[0], "DELETING"}, headers[1:]...)
		}
		var controls strings.Builder
		for c := rune(0); c <= '\u009f'; c++ {
			if c < ' ' || c >= '\u007f' {
				controls.WriteRune(c)
			}
		}
		controlText := controls.String()
		controlSpaces := strings.Repeat(" ", 65)

		for _, prototype := range []proto.Message{&publicv1.BillingAccount{}, &privatev1.BillingAccount{}} {
			Describe(string(prototype.ProtoReflect().Descriptor().FullName()), func() {
				It("Renders empty and partial nested objects with unknown state and dash placeholders", func(ctx context.Context) {
					for _, fields := range []map[string]any{
						{}, {"metadata": map[string]any{}}, {"status": map[string]any{}},
						{"status": map[string]any{"base_currency": "", "external_billing_account_id": ""}},
					} {
						object := billingObject(prototype, fields)
						output := renderBilling(ctx, object)
						expectBillingRows(output, domainHeaders(accountHeaders),
							withDeleting([]string{"-", "-", "-", "-", "-", "UNKNOWN", "-", "-"}))
					}
				})

				It("Retains public domain columns and observation start with selected tenant and project", func(ctx context.Context) {
					status := map[string]any{
						"provider_key": "m360", "display_name": "Provider Label", "base_currency": "EUR", "state": 1,
						"external_billing_account_id": "local-attribution", "state_observation_timestamp": "2026-11-17T01:02:03.123456789Z",
					}
					if prototype.ProtoReflect().Descriptor().FullName().Parent() == "osac.private.v1" {
						status["provider_account_reference"] = "native-identity-must-not-be-a-column"
					}
					object := billingObject(prototype, map[string]any{
						"id": "opaque-id", "metadata": map[string]any{
							"name": "ba-stable-name", "tenant": "system", "creation_timestamp": "2001-01-01T00:00:00Z",
							"deletion_timestamp": "2026-11-17T02:00:00Z",
						}, "status": status,
					})
					for _, tenant := range []string{"", "selected-tenant"} {
						scoped := config.ProjectIntoContext(config.TenantIntoContext(ctx, tenant), "selected-project")
						expectBillingRows(renderBilling(scoped, object), domainHeaders(accountHeaders), []string{
							"opaque-id", "Yes", "ba-stable-name", "m360", "Provider Label", "EUR", "ACTIVE",
							"local-attribution", "2026-11-17T01:02:03.123456789Z",
						})
					}
				})

				It("Shortens every declared account state and retains future enum numbers", func(ctx context.Context) {
					states := prototype.ProtoReflect().Descriptor().Fields().ByName("status").Message().Fields().ByName("state").Enum().Values()
					expected := map[protoreflect.EnumNumber]string{0: "UNKNOWN", 1: "ACTIVE", 2: "INACTIVE", 3: "UNKNOWN"}
					for i := range states.Len() {
						number := states.Get(i).Number()
						Expect(expected).To(HaveKey(number))
					}
					expected[991], expected[-7] = "UNKNOWN:991", "UNKNOWN:-7"
					for number, text := range expected {
						object := billingObject(prototype, map[string]any{"status": map[string]any{"state": int32(number)}})
						expectBillingRows(renderBilling(ctx, object), domainHeaders(accountHeaders),
							withDeleting([]string{"-", "-", "-", "-", "-", text, "-", "-"}))
					}
				})

				It("Distinguishes missing observation from real epoch and fractional timestamps", func(ctx context.Context) {
					for _, timestamp := range []string{"", "1970-01-01T00:00:00Z", "1970-01-01T00:00:00.000000001Z", "2026-11-17T01:02:03.1Z"} {
						status := map[string]any{}
						cell := "-"
						if timestamp != "" {
							status["state_observation_timestamp"], cell = timestamp, timestamp
						}
						object := billingObject(prototype, map[string]any{"status": status})
						expectBillingRows(renderBilling(ctx, object), domainHeaders(accountHeaders),
							withDeleting([]string{"-", "-", "-", "-", "-", "UNKNOWN", "-", cell}))
					}
				})

				It("Sanitizes all C0/C1 controls only in table cells without truncating Unicode labels or raw tokens", func(ctx context.Context) {
					label := strings.Repeat("Étiquette 雲 ☁ — ", 100)
					for _, token := range []string{"", "   ", "  exact-token  ", "before" + controlText + "after"} {
						object := billingObject(prototype, map[string]any{"status": map[string]any{
							"provider_key": "m" + controlText + "360", "display_name": label + controlText + "end",
							"base_currency": "E" + controlText + "UR", "external_billing_account_id": token,
						}})
						cell := token
						if token == "" {
							cell = "-"
						} else if strings.Contains(token, controlText) {
							cell = "before" + controlSpaces + "after"
						}
						expectBillingRows(renderBilling(ctx, object), domainHeaders(accountHeaders), withDeleting([]string{
							"-", "-", "m" + controlSpaces + "360", label + controlSpaces + "end", "E" + controlSpaces + "UR", "UNKNOWN", cell, "-",
						}))
						actual, ok := reflection.ResolveFieldPath[string](object, "status.external_billing_account_id")
						Expect(ok).To(BeTrue())
						Expect(actual).To(Equal(token), "equality/uniqueness uses the original token bytes")
					}
				})
			})
		}

		for _, prototype := range []proto.Message{&publicv1.TenantBillingAssignment{}, &privatev1.TenantBillingAssignment{}} {
			Describe(string(prototype.ProtoReflect().Descriptor().FullName()), func() {
				It("Renders empty and partial nested objects without account lookups", func(ctx context.Context) {
					for _, fields := range []map[string]any{
						{}, {"metadata": map[string]any{}}, {"status": map[string]any{}}, {"spec": map[string]any{}},
						{"spec": map[string]any{"tenant": map[string]any{}, "billing_account": map[string]any{}}},
					} {
						object := billingObject(prototype, fields)
						expectBillingRows(renderBilling(ctx, object), domainHeaders(assignmentHeaders),
							withDeleting([]string{"-", "-", "-", "-", "UNKNOWN", "-", "-", "-"}))
					}
				})

				It("Renders canonical stored reference ids and exactly one explicit tenant with either selection", func(ctx context.Context) {
					object := billingObject(prototype, map[string]any{
						"id": "interval-id", "metadata": map[string]any{"name": "assignment-name", "tenant": "wrong-source-tenant"},
						"spec": map[string]any{
							"tenant":          map[string]any{"id": "tenant-id", "name": "tenant-name"},
							"billing_account": map[string]any{"id": "account-id", "name": "ba-name"},
						}, "status": map[string]any{"connection_state": 2},
					})
					for _, tenant := range []string{"", "selected-tenant"} {
						scoped := config.ProjectIntoContext(config.TenantIntoContext(ctx, tenant), "selected-project")
						expectBillingRows(renderBilling(scoped, object), domainHeaders(assignmentHeaders),
							withDeleting([]string{"interval-id", "assignment-name", "tenant-id", "account-id", "CONNECTED", "-", "-", "-"}))
					}
				})

				It("Retains one present reference when the other nested reference is missing", func(ctx context.Context) {
					for _, field := range []string{"tenant", "billing_account"} {
						object := billingObject(prototype, map[string]any{"spec": map[string]any{
							field: map[string]any{"id": "stored-reference-id"},
						}})
						cells := []string{"-", "-", "-", "-", "UNKNOWN", "-", "-", "-"}
						if field == "tenant" {
							cells[2] = "stored-reference-id"
						} else {
							cells[3] = "stored-reference-id"
						}
						expectBillingRows(renderBilling(ctx, object), domainHeaders(assignmentHeaders), withDeleting(cells))
					}
				})

				It("Shortens every declared connection state without inventing a successful default", func(ctx context.Context) {
					states := prototype.ProtoReflect().Descriptor().Fields().ByName("status").Message().Fields().ByName("connection_state").Enum().Values()
					expected := map[protoreflect.EnumNumber]string{0: "UNKNOWN", 1: "PENDING", 2: "CONNECTED", 3: "FAILED"}
					for i := range states.Len() {
						Expect(expected).To(HaveKey(states.Get(i).Number()))
					}
					expected[991], expected[-7] = "UNKNOWN:991", "UNKNOWN:-7"
					for number, text := range expected {
						object := billingObject(prototype, map[string]any{"status": map[string]any{"connection_state": int32(number)}})
						expectBillingRows(renderBilling(ctx, object), domainHeaders(assignmentHeaders),
							withDeleting([]string{"-", "-", "-", "-", text, "-", "-", "-"}))
					}
				})

				It("Preserves absent/current, epoch, fractional, contiguous and zero-length interval boundaries", func(ctx context.Context) {
					boundary := "2026-11-17T01:02:03.123456789Z"
					intervals := [][2]string{
						{"", ""}, {"1970-01-01T00:00:00Z", ""},
						{"1970-01-01T00:00:00.000000001Z", boundary}, {boundary, ""}, {boundary, boundary},
					}
					var objects []proto.Message
					var rows [][]string
					for _, interval := range intervals {
						status := map[string]any{}
						cells := []string{"-", "-"}
						for i, field := range []string{"effective_from_timestamp", "effective_to_timestamp"} {
							if interval[i] != "" {
								status[field], cells[i] = interval[i], interval[i]
							}
						}
						objects = append(objects, billingObject(prototype, map[string]any{
							"metadata": map[string]any{"creation_timestamp": "2001-01-01T00:00:00Z"}, "status": status,
						}))
						rows = append(rows, withDeleting([]string{"-", "-", "-", "-", "UNKNOWN", cells[0], cells[1], "-"}))
					}
					// The old end and replacement start are the same stored instant;
					// rendering makes no claim about elapsed time or connection state.
					expectBillingRows(renderBilling(ctx, objects...), domainHeaders(assignmentHeaders), rows...)
				})

				It("Retains full remediation and tombstone text while removing only table controls", func(ctx context.Context) {
					for _, message := range []string{
						"BILLING_PROVIDER_CORRELATION_WRITE_UNSUPPORTED " + strings.Repeat("Réparer 雲; ", 100),
						"Tenant deleted; retained billing history — 終了",
					} {
						object := billingObject(prototype, map[string]any{"status": map[string]any{
							"message": message + controlText + "end", "connection_state": 1,
						}})
						expectBillingRows(renderBilling(ctx, object), domainHeaders(assignmentHeaders),
							withDeleting([]string{"-", "-", "-", "-", "PENDING", "-", "-", message + controlSpaces + "end"}))
					}
				})
			})
		}

		for _, prototype := range []proto.Message{&publicv1.BillingProviderStatus{}, &privatev1.BillingProviderStatus{}} {
			Describe(string(prototype.ProtoReflect().Descriptor().FullName()), func() {
				It("Keeps the six status columns free of identity, tenant and lifecycle", func(ctx context.Context) {
					for _, tenant := range []string{"", "selected-tenant"} {
						scoped := config.ProjectIntoContext(config.TenantIntoContext(ctx, tenant), "selected-project")
						expectBillingRows(renderBilling(scoped, prototype), statusHeaders,
							[]string{"UNSPECIFIED", "-", "-", "-", "-", "UNSPECIFIED"})
					}
				})

				It("Preserves full provider text, controls and real timestamps without mutating raw output", func(ctx context.Context) {
					message := strings.Repeat("Remediation 雲 — ", 100)
					for _, timestamp := range []string{"1970-01-01T00:00:00Z", "2026-11-17T01:02:03.123456789Z"} {
						object := billingObject(prototype, map[string]any{
							"state": 2, "provider_key": "m" + controlText + "360", "message": message + controlText + "end",
							"supported_capabilities": []int{1, 991}, "last_successful_check": timestamp, "usage_delivery_state": 1,
						})
						expectBillingRows(renderBilling(ctx, object), statusHeaders, []string{
							"AVAILABLE", "m" + controlSpaces + "360", message + controlSpaces + "end", "PRICING,UNKNOWN:991", timestamp, "UNKNOWN",
						})
					}
				})

				It("Shortens every declared singleton enum and preserves future scalar and repeated values", func(ctx context.Context) {
					for _, field := range []string{"state", "supported_capabilities", "usage_delivery_state"} {
						enum := prototype.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(field)).Enum()
						expected := map[string][]string{
							"state":                  {"UNSPECIFIED", "NOT_CONFIGURED", "AVAILABLE", "UNAVAILABLE"},
							"supported_capabilities": {"UNSPECIFIED", "PRICING"},
							"usage_delivery_state":   {"UNSPECIFIED", "UNKNOWN", "AVAILABLE", "UNAVAILABLE"},
						}[field]
						Expect(enum.Values().Len()).To(Equal(len(expected)))
						for number, text := range append(expected, "UNKNOWN:991") {
							value := number
							if number == len(expected) {
								value = 991
							}
							var fixture any = value
							cells := []string{"UNSPECIFIED", "-", "-", "-", "-", "UNSPECIFIED"}
							switch field {
							case "state":
								cells[0] = text
							case "supported_capabilities":
								fixture, cells[3] = []int{value}, text
							case "usage_delivery_state":
								cells[5] = text
							}
							object := billingObject(prototype, map[string]any{field: fixture})
							expectBillingRows(renderBilling(ctx, object), statusHeaders, cells)
						}
					}
				})
			})
		}
	})

	It("Compiles CEL expressions successfully for all table definitions", func(ctx context.Context) {
		// Collect all table definition files:
		tableFiles, err := filepath.Glob("tables/*.yaml")
		Expect(err).ToNot(HaveOccurred())
		for i, tableFile := range tableFiles {
			tableFiles[i] = filepath.Base(tableFile)
		}
		Expect(tableFiles).ToNot(
			BeEmpty(),
			"Expected at least one '.yaml' file in the 'tables' directory, but found none",
		)

		// Iterate over all table definition files and compile the CEL expressions for each table.
		for _, tableFile := range tableFiles {
			// Find the object type:
			objectName := strings.TrimSuffix(tableFile, ".yaml")
			objectFullName := protoreflect.FullName(objectName)
			objectType, err := protoregistry.GlobalTypes.FindMessageByName(objectFullName)
			Expect(err).ToNot(
				HaveOccurred(),
				"Type '%s' for table file '%s' not found",
				objectFullName, tableFile,
			)

			// Create the object helper for the type of the table. This needs to return the real full name
			// and descriptor.
			objectHelper := makeObjectHelper(objectType.New().Interface())

			// The table will probably use lookup columns, and that requires an object helper for the looked
			// up type. But we don't know that in advance, and we don't want to poke into the internals of
			// the format of the table. Instead of that we create a helper that always responds to the
			// 'List' method with an empty list of objects. It doesn't need to respond to any other methods.
			lookupHelper := makeLookupHelper()

			// Configure the helper to return the object helper for the type of the table, and the lookup
			// helper for any other type.
			helper := reflection.NewMockHelper(ctrl)
			helper.EXPECT().
				Lookup(objectName).
				Return(objectHelper).
				AnyTimes()
			helper.EXPECT().
				Lookup(gomock.Any()).
				Return(lookupHelper).
				AnyTimes()

			// Build the renderer:
			renderer, err := NewTableRenderer().
				SetLogger(logger).
				SetHelper(helper).
				SetWriter(io.Discard).
				Build()
			Expect(err).ToNot(
				HaveOccurred(),
				"Failed to build renderer for table file '%s'",
				tableFile,
			)

			// Render a slice with a single empty instance, to force compilation and evaluation of all CEL
			// program expressions in the table definition.
			object := objectType.New().Interface()
			objects := []proto.Message{
				object,
			}
			err = renderer.Render(ctx, objects)
			Expect(err).ToNot(
				HaveOccurred(),
				"Failed to render table file '%s'",
				tableFile,
			)
		}
	})
})
