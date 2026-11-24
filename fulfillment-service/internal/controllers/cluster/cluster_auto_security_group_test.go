package cluster

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type securityGroupsListStub struct {
	response *privatev1.SecurityGroupsListResponse
	err      error
}

func (s securityGroupsListStub) List(
	context.Context,
	*privatev1.SecurityGroupsListRequest,
	...grpc.CallOption,
) (*privatev1.SecurityGroupsListResponse, error) {
	return s.response, s.err
}

var _ = It("uses the server-owned automatic security group", func() {
	cluster := privatev1.Cluster_builder{
		Id:       "cluster-123",
		Metadata: privatev1.Metadata_builder{Tenant: "tenant-a"}.Build(),
	}.Build()
	group := privatev1.SecurityGroup_builder{
		Id: "security-group-456",
		Metadata: privatev1.Metadata_builder{
			Name:    "auto-sg-cluster-123",
			Tenant:  "tenant-a",
			Creator: "system",
			Labels: map[string]string{
				"osac.openshift.io/auto-created":      "true",
				"osac.openshift.io/auto-created-for":  "cluster-123",
				"osac.openshift.io/auto-created-kind": "cluster",
			},
		}.Build(),
	}.Build()
	task := &task{
		r:       &function{securityGroupsClient: securityGroupsListStub{response: &privatev1.SecurityGroupsListResponse{Items: []*privatev1.SecurityGroup{group}}}},
		cluster: cluster,
	}

	got, err := task.automaticClusterSecurityGroupID(context.Background())
	Expect(err).NotTo(HaveOccurred())
	Expect(got).To(Equal(group.GetId()))
})
