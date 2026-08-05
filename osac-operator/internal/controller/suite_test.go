/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	bmfov1alpha1 "github.com/osac-project/bare-metal-fulfillment-operator/api/v1alpha1"
	ovnv1 "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/userdefinednetwork/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"

	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac-operator/pkg/provisioning"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	cfg           *rest.Config
	k8sClient     client.Client
	testMcManager mcmanager.Manager
	testEnv       *envtest.Environment
	ctx           context.Context
	cancel        context.CancelFunc
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "config", "crd", "fakes"),
		},
		ErrorIfCRDPathMissing: true,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
			fmt.Sprintf("1.31.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = osacv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = ovnv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kubevirtv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = bmfov1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = hypershiftv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	By("creating multicluster manager for tenant controller tests")
	localMgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).NotTo(HaveOccurred())
	testMcManager, err = mcmanager.WithMultiCluster(localMgr, nil)
	Expect(err).NotTo(HaveOccurred())
	go func() {
		_ = localMgr.Start(ctx)
	}()
	ok := localMgr.GetCache().WaitForCacheSync(ctx)
	Expect(ok).To(BeTrue())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

// noopProvisioningProvider is a no-op provisioning provider for tests that need a provider
// but don't test provisioning behavior.
type noopProvisioningProvider struct{}

func (noopProvisioningProvider) TriggerProvision(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
	return &provisioning.ProvisionResult{
		JobID:        "noop-job",
		InitialState: osacv1alpha1.JobStatePending,
	}, nil
}

func (noopProvisioningProvider) GetProvisionStatus(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	return provisioning.ProvisionStatus{JobID: jobID, State: osacv1alpha1.JobStateUnknown}, nil
}

func (noopProvisioningProvider) TriggerDeprovision(_ context.Context, _ client.Object, _ []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
	return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionSkipped}, nil
}

func (noopProvisioningProvider) GetDeprovisionStatus(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	return provisioning.ProvisionStatus{JobID: jobID, State: osacv1alpha1.JobStateUnknown}, nil
}

func (noopProvisioningProvider) Name() string { return "noop" }

// newTestComputeInstanceSpec creates a valid ComputeInstanceSpec for testing
func newTestComputeInstanceSpec(templateID string) osacv1alpha1.ComputeInstanceSpec {
	return osacv1alpha1.ComputeInstanceSpec{
		TemplateID: templateID,
		Image: osacv1alpha1.ImageSpec{
			SourceType: osacv1alpha1.ImageSourceTypeRegistry,
			SourceRef:  "quay.io/fedora/fedora-coreos:stable",
		},
		Cores:     4,
		MemoryGiB: 8,
		BootDisk: osacv1alpha1.DiskSpec{
			SizeGiB: 30,
		},
		RunStrategy: osacv1alpha1.RunStrategyAlways,
	}
}
