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

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/reflection"
	"github.com/osac-project/fulfillment-service/internal/uuid"
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
				GetMetadata(gomock.Any()).
				Return(templateMetadata).
				AnyTimes()

			// Create the compute instance object:
			computeObj := publicv1.ComputeInstance_builder{
				Id: uuid.New(),
				Metadata: publicv1.Metadata_builder{
					Name: "my-instance",
				}.Build(),
				Spec: publicv1.ComputeInstanceSpec_builder{
					Template: "my-template",
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
