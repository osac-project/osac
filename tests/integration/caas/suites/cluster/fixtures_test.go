// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	bmfv1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	osacv1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"k8s.io/apimachinery/pkg/types"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/awx"
	"github.com/osac-project/osac/tests/integration/caas/internal/harness"
	scenario "github.com/osac-project/osac/tests/integration/caas/internal/simulator/scenario"
	"github.com/osac-project/osac/tests/integration/caas/suites/fixtures"
)

const clusterJobStartTimeout = 10 * time.Minute

// clusterWorker retains one allocation, the registered host and Agent, and the native consumer claim.
// Worker names are the keys in clusterFixture.workers.
type clusterWorker struct {
	instance       *bmfv1.BareMetalInstance
	externalHostID string
	agent          *assistedv1.Agent
	machineClaim   client.ObjectKey
}

// clusterFixture retains the identities and execution evidence of one cluster request.
//
// The test fills in the cluster ID, ClusterOrder details and worker resources
// as they become available. Keeping this information lets cleanup
// find the resources even if the test fails before provisioning finishes.
//
// The deployed providers keep responding independently of this scenario. The
// AWX Pod observer stays active through deletion to retain worker-image evidence.
type clusterFixture struct {
	requestedWorkers int32
	clusters         publicv1.ClustersClient
	group            fixtures.InventoryGroup
	ignition         *harness.Ignition
	jobPods          *awx.JobPodObserver
	settings         *scenario.Scenario
	namespaces       map[string]types.UID
	instances        map[types.UID]*bmfv1.BareMetalInstance
	agents           map[types.UID]*assistedv1.Agent
	runID            string
	clusterID        string
	createAttempted  bool
	capiCluster      *capiv1.Cluster
	order            *osacv1.ClusterOrder
	workers          map[string]*clusterWorker
	hostedCluster    *hypershiftv1.HostedCluster
	nodePool         *hypershiftv1.NodePool
	infraEnv         *assistedv1.InfraEnv
	userDataSecrets  map[types.NamespacedName]struct{}
	cleanupFinished  bool
}

// newClusterFixture retains explicit worker demand and execution evidence through cleanup.
func newClusterFixture(
	ctx context.Context, profile scenario.Profile, group fixtures.InventoryGroup, workers int32,
) *clusterFixture {
	GinkgoHelper()

	if err := fixtures.CheckInventoryReleased(ctx, environment, group.Inventory); err != nil {
		AbortSuite("inventory group is not reusable: " + err.Error())
	}

	bytes := make([]byte, 6)
	_, err := rand.Read(bytes)
	Expect(err).NotTo(HaveOccurred())
	s := &clusterFixture{
		requestedWorkers: workers,
		clusters:         publicv1.NewClustersClient(connection),
		group:            group,
		ignition:         &harness.Ignition{Kube: environment.Kube, Namespace: environment.Namespace, Group: group.Name},
		runID:            "caas-" + hex.EncodeToString(bytes),
		workers:          map[string]*clusterWorker{},
		instances:        map[types.UID]*bmfv1.BareMetalInstance{},
		agents:           map[types.UID]*assistedv1.Agent{},
		namespaces:       map[string]types.UID{},
		userDataSecrets:  map[types.NamespacedName]struct{}{},
	}

	By("configuring provider responses for this cluster request")
	s.settings = &scenario.Scenario{Client: environment.Client, Namespace: environment.Namespace, Name: s.runID}
	Expect(s.settings.Create(ctx, profile, s.ignition.URL())).To(Succeed())
	AddReportEntry("Provider profile", profile)
	AddReportEntry("Inventory group", group.Name)
	DeferCleanup(func(ctx context.Context) {
		if s.jobPods != nil {
			defer s.jobPods.Close()
		}

		complete := false
		defer func() {
			if !complete {
				AbortSuite("case cleanup failed; stopping inventory group reuse")
			}
		}()

		if s.createAttempted && s.clusterID == "" {
			By("recovering the submitted request before allowing cleanup or reuse")
			Eventually(func() error {
				return s.recoverClusterID(ctx)
			}).WithContext(ctx).WithTimeout(time.Minute).Should(Succeed())
		}

		if s.clusterID != "" && !s.cleanupFinished {
			s.cleanupAfterFailure(ctx)
		}

		By("retiring response settings after case cleanup")
		Expect(s.settings.Delete(ctx)).To(Succeed())
		complete = true
	}, NodeTimeout(15*time.Minute))
	s.jobPods, err = awx.ObserveJobPods(ctx, environment)
	Expect(err).NotTo(HaveOccurred())
	slog.DebugContext(ctx, "opening cluster scenario", "run", s.runID, "group", group.Name)

	return s
}
