// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package cluster verifies Fulfillment API-led lifecycle journeys through the installed services.
package cluster

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	metal3v1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	fulfillmentit "github.com/osac-project/osac/fulfillment-service/it"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/awx"
	"github.com/osac-project/osac/tests/integration/caas/internal/harness"
	"github.com/osac-project/osac/tests/integration/caas/suites/fixtures"
)

var (
	environment        *harness.Environment
	tenantID           string
	connection         *grpc.ClientConn
	connectionClusters publicv1.ClustersClient
	executions         *awx.Installation
	tool               *fulfillmentit.Tool
	defaultGroup       fixtures.InventoryGroup
)

// suiteData shares original fixture identities; each process opens its own clients.
type suiteData struct {
	TenantID string
	Groups   []fixtures.InventoryGroup
}

// TestCluster runs API-led cluster lifecycle and failure/recovery scenarios.
func TestCluster(t *testing.T) {
	RegisterFailHandler(Fail)
	fixtures.ConfigureLogging("cluster")
	SetDefaultEventuallyPollingInterval(time.Second)
	SetDefaultConsistentlyPollingInterval(time.Second)
	RunSpecs(t, "CaaS cluster")
}

// Prepare shared fixtures once, then distribute their original identities to every process.
var _ = SynchronizedBeforeSuite(func(ctx SpecContext) []byte {
	By("connecting to the prepared installation and checking tenant readiness")
	environment = fixtures.OpenKubernetes()
	executions = fixtures.OpenAWX(ctx, environment)
	tool = fixtures.OpenFulfillment(ctx, environment)
	tenantID = fixtures.Tenant(ctx, tool)

	By("requiring a ready simulator and an initially clean installation")
	simulator := &harness.Simulator{Kube: environment.Kube, Namespace: environment.Namespace}
	Expect(simulator.CheckConfiguration(ctx)).To(Succeed())
	Expect(harness.CheckInventoryEmpty(ctx, environment)).To(Succeed())
	fixtures.RequireClean(ctx, environment, harness.Inventory{}, executions, tool.ExternalView().AdminConn(), tenantID)

	var (
		owned             harness.Inventory
		discovery         *corev1.ConfigMap
		inventoryComplete bool
	)

	// This cleanup runs after all processes finish and before the shared clients close.
	DeferCleanup(func(ctx SpecContext) {
		By("requiring global cleanliness before deleting suite fixtures")

		if inventoryComplete {
			fixtures.RequireClean(ctx, environment, owned, executions, tool.ExternalView().AdminConn(), tenantID)
		}

		Eventually(func() error {
			return owned.Delete(ctx, environment)
		}).WithContext(ctx).WithTimeout(time.Minute).Should(Succeed())
		Eventually(func(g Gomega) {
			for _, host := range owned.Hosts {
				key := client.ObjectKey{Namespace: host.Namespace, Name: host.Name}
				g.Expect(apierrors.IsNotFound(environment.Client.Get(ctx, key, &metal3v1.BareMetalHost{}))).To(BeTrue())

				if host.HardwareUID != "" {
					g.Expect(apierrors.IsNotFound(environment.Client.Get(ctx, key, &metal3v1.HardwareData{}))).To(BeTrue())
				}
			}
		}).WithContext(ctx).WithTimeout(time.Minute).Should(Succeed())

		if discovery != nil {
			Expect(environment.Kube.CoreV1().ConfigMaps(discovery.Namespace).Delete(ctx, discovery.Name,
				metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &discovery.UID}})).To(Succeed())
		}
	}, NodeTimeout(4*time.Minute))

	configuration, _ := GinkgoConfiguration()

	names := make([]string, configuration.ParallelTotal)
	for index := range names {
		names[index] = fmt.Sprintf("p%d", index+1)
	}

	By("creating original hardware for the requested inventory groups")
	groups, err := fixtures.CreateInventoryGroups(ctx, environment, names, workersPerCluster)
	inventoryComplete = err == nil

	for _, group := range groups {
		owned.Namespace = group.Inventory.Namespace
		owned.Hosts = append(owned.Hosts, group.Inventory.Hosts...)
	}

	Expect(err).NotTo(HaveOccurred())
	Eventually(func() error {
		return fixtures.CheckInventoryReleased(ctx, environment, owned)
	}).WithContext(ctx).WithTimeout(3 * time.Minute).Should(Succeed())
	AddReportEntry("Suite inventory", owned)

	By("preparing group discovery data and reusable catalog entries")
	discovery, err = harness.CreateIgnition(ctx, environment.Kube, environment.Namespace, names)
	Expect(err).NotTo(HaveOccurred())

	for _, group := range groups {
		ignition := &harness.Ignition{Kube: environment.Kube, Namespace: environment.Namespace, Group: group.Name}
		expected, err := ignition.Stored(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(ignition.WaitForContent(ctx, expected)).To(Succeed())
	}

	Expect(fixtures.EnsureClusterCatalog(ctx, tool.InternalView().AdminConn(),
		tool.ExternalView().UserConn(), tenantID, groups)).To(Succeed())

	data, err := json.Marshal(suiteData{TenantID: tenantID, Groups: groups})
	Expect(err).NotTo(HaveOccurred())

	return data
}, func(ctx SpecContext, data []byte) {
	var shared suiteData

	Expect(json.Unmarshal(data, &shared)).To(Succeed())
	tenantID = shared.TenantID

	if GinkgoParallelProcess() != 1 {
		By("connecting this process to the prepared installation")
		environment = fixtures.OpenKubernetes()
		executions = fixtures.OpenAWX(ctx, environment)
		tool = fixtures.OpenFulfillment(ctx, environment)
	}

	By("selecting this process's default inventory group")
	connection = tool.ExternalView().UserConn()
	connectionClusters = publicv1.NewClustersClient(connection)
	defaultGroup = shared.Groups[GinkgoParallelProcess()-1]
	Expect(defaultGroup.Inventory.Hosts).To(HaveLen(workersPerCluster))
}, NodeTimeout(12*time.Minute))
