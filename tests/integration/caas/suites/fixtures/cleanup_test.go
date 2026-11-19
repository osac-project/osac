// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package fixtures

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	routev1 "github.com/openshift/api/route/v1"
	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	bmfv1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	osacv1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/tests/integration/caas/internal/harness"
)

// requestReuseConnection supplies the two tenant request lists used before reuse.
type requestReuseConnection struct {
	variant string
	methods []string
}

// Invoke checks read-only, bounded request selection and supplies the selected residue or error.
func (c *requestReuseConnection) Invoke(
	ctx context.Context, method string, request, response any, _ ...grpc.CallOption,
) error {
	c.methods = append(c.methods, method)
	deadline, bounded := ctx.Deadline()
	Expect(bounded).To(BeTrue())
	Expect(time.Until(deadline)).To(BeNumerically(">", 0))
	Expect(time.Until(deadline)).To(BeNumerically("<=", 20*time.Second))

	switch request := request.(type) {
	case *publicv1.ClustersListRequest:
		Expect(method).To(Equal(publicv1.Clusters_List_FullMethodName))
		Expect(request.GetFilter()).To(Equal(`this.metadata.tenant == "tenant"`))
		Expect(request.GetLimit()).To(Equal(int32(1)))

		if c.variant == "cluster error" {
			return errors.New("cluster list unavailable")
		}

		if c.variant == "cluster residue" {
			response.(*publicv1.ClustersListResponse).SetItems([]*publicv1.Cluster{
				publicv1.Cluster_builder{Id: "remaining-cluster"}.Build(),
			})
		}
	case *publicv1.BareMetalInstancesListRequest:
		Expect(method).To(Equal(publicv1.BareMetalInstances_List_FullMethodName))
		Expect(request.GetFilter()).To(Equal(`this.metadata.tenant == "tenant"`))
		Expect(request.GetLimit()).To(Equal(int32(1)))

		if c.variant == "allocation error" {
			return errors.New("allocation list unavailable")
		}

		if c.variant == "allocation residue" {
			response.(*publicv1.BareMetalInstancesListResponse).SetItems([]*publicv1.BareMetalInstance{
				publicv1.BareMetalInstance_builder{Id: "remaining-allocation"}.Build(),
			})
		}
	default:
		return fmt.Errorf("unexpected reuse RPC %s", method)
	}

	return nil
}

// NewStream rejects streaming RPCs because reuse observes bounded unary lists.
func (c *requestReuseConnection) NewStream(
	context.Context, *grpc.StreamDesc, string, ...grpc.CallOption,
) (grpc.ClientStream, error) {
	return nil, errors.New("unexpected streaming reuse RPC")
}

var _ = It("rejects native residue written before AWX reports idle", func(ctx SpecContext) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		bmfv1.AddToScheme, osacv1.AddToScheme, hypershiftv1.AddToScheme, capiv1.AddToScheme,
		assistedv1.AddToScheme, installv1.AddToScheme, agentv1.AddToScheme, hivev1.AddToScheme,
		corev1.AddToScheme, routev1.AddToScheme,
	} {
		Expect(add(scheme)).To(Succeed())
	}

	environment := &harness.Environment{
		Namespace: "osac", Kube: kubefake.NewClientset(),
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
	}

	var lateWrite sync.Once

	executions := startObservationInstallation(func() []aap.Job {
		lateWrite.Do(func() {
			Expect(environment.Client.Create(ctx, &hypershiftv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "osac", Name: "late-cluster"},
			})).To(Succeed())
		})

		return nil
	})
	connection := &requestReuseConnection{variant: "empty"}
	err := CheckCleanEnvironment(ctx, environment, harness.Inventory{}, executions, connection, "tenant")
	Expect(err).To(MatchError(ContainSubstring("hostedclusters")))
})

var _ = Describe("Application request reuse checks", func() {
	DescribeTable(
		"rejects remaining requests and failed observations without writes",
		func(ctx SpecContext, variant, failure string, calls int) {
			scheme := runtime.NewScheme()
			Expect(osacv1.AddToScheme(scheme)).To(Succeed())
			Expect(bmfv1.AddToScheme(scheme)).To(Succeed())
			environment := &harness.Environment{
				Namespace: "osac", Kube: kubefake.NewClientset(),
				Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
			}
			connection := &requestReuseConnection{variant: variant}

			err := checkApplicationInputsGone(ctx, environment, connection, "tenant")
			if failure == "" {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(MatchError(ContainSubstring(failure)))
			}

			Expect(connection.methods).To(HaveLen(calls))
			Expect(connection.methods[0]).To(Equal(publicv1.Clusters_List_FullMethodName))

			if calls == 2 {
				Expect(connection.methods[1]).To(Equal(publicv1.BareMetalInstances_List_FullMethodName))
			}
		},
		Entry("cluster residue", "cluster residue", "fulfillment clusters remain", 1),
		Entry("allocation residue", "allocation residue", "fulfillment bare metal allocations remain", 2),
		Entry("cluster read error", "cluster error", "list Fulfillment clusters: cluster list unavailable", 1),
		Entry("allocation read error", "allocation error",
			"list Fulfillment bare metal allocations: allocation list unavailable", 2),
		Entry("empty requests", "empty", "", 2),
	)
})
