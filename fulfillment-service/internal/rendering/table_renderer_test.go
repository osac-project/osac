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
	"io"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/timestamppb"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/reflection"
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
		helper.EXPECT().IsTenantScoped().
			Return(true).
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

	Describe("Integer columns", func() {
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
							Cores:     4,
							MemoryGib: 16,
							State:     publicv1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
						}.Build(),
					}.Build(),
				},
			)
			Expect(output).To(MatchRegexp(`4\s+16\s+ACTIVE`))
			Expect(output).ToNot(ContainSubstring("%!s"))
		})
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
