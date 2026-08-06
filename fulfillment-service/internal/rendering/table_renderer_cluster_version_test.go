/*
Copyright (c) 2026 Red Hat Inc.

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
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
)

var _ = Describe("Cluster VERSION column rendering", func() {
	var ctrl *gomock.Controller

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	// makeVersionHelper creates a helper that returns the given cluster version object for any lookup.
	makeVersionHelper := func(items []proto.Message) *reflection.MockObjectHelper {
		versionDescriptor := (&publicv1.ClusterVersion{}).ProtoReflect().Descriptor()
		helper := reflection.NewMockObjectHelper(ctrl)
		helper.EXPECT().FullName().
			Return(versionDescriptor.FullName()).
			AnyTimes()
		helper.EXPECT().Descriptor().
			Return(versionDescriptor).
			AnyTimes()
		helper.EXPECT().String().
			Return(string(versionDescriptor.FullName())).
			AnyTimes()
		helper.EXPECT().IsTenantScoped().
			Return(true).
			AnyTimes()
		helper.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(reflection.ListResult{Items: items, Total: int32(len(items))}, nil).
			AnyTimes()
		return helper
	}

	renderClusters := func(ctx context.Context, cluster *publicv1.Cluster, versionHelper *reflection.MockObjectHelper) string {
		clusterObj := &publicv1.Cluster{}
		clusterDescriptor := clusterObj.ProtoReflect().Descriptor()
		clusterHelper := reflection.NewMockObjectHelper(ctrl)
		clusterHelper.EXPECT().FullName().
			Return(clusterDescriptor.FullName()).
			AnyTimes()
		clusterHelper.EXPECT().Descriptor().
			Return(clusterDescriptor).
			AnyTimes()
		clusterHelper.EXPECT().String().
			Return(string(clusterDescriptor.FullName())).
			AnyTimes()
		clusterHelper.EXPECT().IsTenantScoped().
			Return(true).
			AnyTimes()

		helper := reflection.NewMockHelper(ctrl)
		helper.EXPECT().
			Lookup(gomock.Any()).
			DoAndReturn(func(objectType string) reflection.ObjectHelper {
				switch objectType {
				case string(clusterDescriptor.FullName()):
					return clusterHelper
				default:
					return versionHelper
				}
			}).
			AnyTimes()

		buffer := &bytes.Buffer{}
		renderer, err := NewTableRenderer().
			SetLogger(logger).
			SetHelper(helper).
			SetWriter(buffer).
			Build()
		Expect(err).ToNot(HaveOccurred())
		err = renderer.Render(ctx, []proto.Message{cluster})
		Expect(err).ToNot(HaveOccurred())
		return buffer.String()
	}

	It("Resolves the version_name to the semver string via lookup_field", func(ctx context.Context) {
		version := publicv1.ClusterVersion_builder{
			Metadata: publicv1.Metadata_builder{Name: "4-17-0"}.Build(),
			Spec: publicv1.ClusterVersionSpec_builder{
				Version: "4.17.0",
			}.Build(),
		}.Build()
		cluster := publicv1.Cluster_builder{
			Id: "cluster-1",
			Spec: publicv1.ClusterSpec_builder{
				VersionName: proto.String("4-17-0"),
			}.Build(),
		}.Build()

		output := renderClusters(ctx, cluster, makeVersionHelper([]proto.Message{version}))
		Expect(output).To(ContainSubstring("4.17.0"))
		Expect(output).ToNot(ContainSubstring("4-17-0\t"))
	})

	It("Falls back to the version_name when the looked-up object has no spec.version", func(ctx context.Context) {
		version := publicv1.ClusterVersion_builder{
			Metadata: publicv1.Metadata_builder{Name: "4-17-0"}.Build(),
			Spec:     publicv1.ClusterVersionSpec_builder{}.Build(),
		}.Build()
		cluster := publicv1.Cluster_builder{
			Id: "cluster-2",
			Spec: publicv1.ClusterSpec_builder{
				VersionName: proto.String("4-17-0"),
			}.Build(),
		}.Build()

		output := renderClusters(ctx, cluster, makeVersionHelper([]proto.Message{version}))
		Expect(output).To(ContainSubstring("4-17-0"))
	})

	It("Shows '-' when the cluster has no version_name", func(ctx context.Context) {
		cluster := publicv1.Cluster_builder{
			Id:   "cluster-3",
			Spec: publicv1.ClusterSpec_builder{}.Build(),
		}.Build()

		output := renderClusters(ctx, cluster, makeVersionHelper(nil))
		Expect(output).To(MatchRegexp(`VERSION.*\n.*-`))
	})
})
