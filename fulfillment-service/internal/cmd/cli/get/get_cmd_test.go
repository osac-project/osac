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
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
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
})
