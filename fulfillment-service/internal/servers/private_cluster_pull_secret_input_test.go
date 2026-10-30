package servers

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Reference-only cluster credential inputs", func() {
	It("rejects absent API references before any secret lookup", func() {
		server := &PrivateClustersServer{}
		cluster := privatev1.Cluster_builder{Spec: privatev1.ClusterSpec_builder{}.Build()}.Build()
		err := server.validatePullSecretSecret(context.Background(), cluster, false)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	DescribeTable("rejects inline parameters without disclosing their value", func(update bool) {
		fixtureMarker := "OSAC7034_SYNTHETIC_INLINE_INPUT"
		parameter, err := anypb.New(wrapperspb.String(fixtureMarker))
		Expect(err).NotTo(HaveOccurred())
		server := &PrivateClustersServer{}
		cluster := privatev1.Cluster_builder{Spec: privatev1.ClusterSpec_builder{TemplateParameters: map[string]*anypb.Any{"pull_secret": parameter}}.Build()}.Build()
		if update {
			err = server.validateUpdatedClusterSpec(nil, cluster, nil)
		} else {
			_, err = server.applyClusterTemplate(context.Background(), cluster, nil)
		}
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).NotTo(ContainSubstring(fixtureMarker))
	}, Entry("create", false), Entry("merged update candidate", true))
})
