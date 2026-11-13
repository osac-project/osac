package servers

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestValidateExistingWorkloadClusterHub(t *testing.T) {
	tests := []struct {
		name string
		hubs []*privatev1.Hub
		id   string
		code codes.Code
	}{
		{name: "matches one of multiple Hubs", hubs: []*privatev1.Hub{{Id: "hub-a"}, {Id: "hub-b"}}, id: "hub-b", code: codes.OK},
		{name: "rejects a different Hub", hubs: []*privatev1.Hub{{Id: "hub-a"}}, id: "hub-b", code: codes.InvalidArgument},
		{name: "rejects no Hub", id: "hub-a", code: codes.InvalidArgument},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateExistingWorkloadClusterHub(test.hubs, test.id)
			if got := status.Code(err); got != test.code {
				t.Fatalf("expected %s, got %s", test.code, got)
			}
		})
	}
}

func TestValidateActiveWorkloadClusterHub(t *testing.T) {
	activeHubIDs := map[string]struct{}{"hub-a": {}}
	tests := []struct {
		name             string
		workloadClusters []*privatev1.WorkloadCluster
		requestedHubID   string
		code             codes.Code
	}{
		{name: "allows first registration", requestedHubID: "hub-a", code: codes.OK},
		{name: "allows existing registrations on requested Hub", workloadClusters: workloadClustersFromHubs("hub-a"), requestedHubID: "hub-a", code: codes.OK},
		{name: "allows registrations after source Hub deletion", workloadClusters: workloadClustersFromHubs("deleted-hub"), requestedHubID: "hub-a", code: codes.OK},
		{name: "rejects another active Hub", workloadClusters: workloadClustersFromHubs("hub-a"), requestedHubID: "hub-b", code: codes.FailedPrecondition},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateActiveWorkloadClusterHub(test.workloadClusters, activeHubIDs, test.requestedHubID)
			if got := status.Code(err); got != test.code {
				t.Fatalf("expected %s, got %s", test.code, got)
			}
		})
	}
}

func workloadClustersFromHubs(hubIDs ...string) []*privatev1.WorkloadCluster {
	clusters := make([]*privatev1.WorkloadCluster, 0, len(hubIDs))
	for _, hubID := range hubIDs {
		clusters = append(clusters, privatev1.WorkloadCluster_builder{
			Spec: privatev1.WorkloadClusterSpec_builder{SourceHubId: hubID}.Build(),
		}.Build())
	}
	return clusters
}

func TestValidateSupportedWorkloads(t *testing.T) {
	tests := []struct {
		name       string
		values     []string
		code       codes.Code
		messageHas string
	}{
		{name: "accepts MaaS", values: []string{"maas"}, code: codes.OK},
		{name: "rejects empty", code: codes.InvalidArgument, messageHas: "maas"},
		{name: "rejects unknown value", values: []string{"vmaas"}, code: codes.InvalidArgument, messageHas: "maas"},
		{name: "rejects mixed values", values: []string{"maas", "vmaas"}, code: codes.InvalidArgument, messageHas: "maas"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			object := privatev1.WorkloadCluster_builder{
				Spec: privatev1.WorkloadClusterSpec_builder{SupportedWorkloads: test.values}.Build(),
			}.Build()
			err := validateSupportedWorkloads(object)
			if got := status.Code(err); got != test.code {
				t.Fatalf("expected %s, got %s", test.code, got)
			}
			if test.messageHas != "" && !strings.Contains(status.Convert(err).Message(), test.messageHas) {
				t.Fatalf("expected error to contain %q, got %q", test.messageHas, status.Convert(err).Message())
			}
		})
	}
}
