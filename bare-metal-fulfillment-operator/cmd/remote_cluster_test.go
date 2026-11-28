/*
Copyright 2026.

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

package main

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/management"
)

// fixedClientManager is a manager stub whose GetClient returns one fixed
// client, so client routing can be asserted by identity.
type fixedClientManager struct {
	ctrl.Manager
	client client.Client
	config *rest.Config
	scheme *runtime.Scheme
	mapper meta.RESTMapper
}

func (m *fixedClientManager) GetClient() client.Client {
	return m.client
}

func (m *fixedClientManager) GetConfig() *rest.Config {
	return m.config
}

func (m *fixedClientManager) GetScheme() *runtime.Scheme {
	return m.scheme
}

func (m *fixedClientManager) GetRESTMapper() meta.RESTMapper {
	return m.mapper
}

// writeRemoteKubeconfig writes a kubeconfig pointing at server. It carries
// no credentials: building the remote access only parses the file and never
// dials, so none are needed and none can leak.
func writeRemoteKubeconfig(server string) string {
	const contextName = "outer"
	path := filepath.Join(GinkgoT().TempDir(), "remote.kubeconfig")
	apiConfig := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			contextName: {Server: server},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			contextName + "-user": {},
		},
		Contexts: map[string]*clientcmdapi.Context{
			contextName: {Cluster: contextName, AuthInfo: contextName + "-user"},
		},
		CurrentContext: contextName,
	}
	Expect(clientcmd.WriteToFile(apiConfig, path)).To(Succeed())
	return path
}

func newRemoteAccess(server string) *remoteClusterAccess {
	GinkgoT().Setenv(envRemoteClusterKubeconfig, writeRemoteKubeconfig(server))
	access, err := loadRemoteClusterAccess()
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, access).NotTo(BeNil())
	return access
}

func newLocalManager() *fixedClientManager {
	testScheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(testScheme)).To(Succeed())
	Expect(metal3api.AddToScheme(testScheme)).To(Succeed())
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		corev1.SchemeGroupVersion,
		metal3api.GroupVersion,
	})
	return &fixedClientManager{
		client: fake.NewClientBuilder().WithScheme(testScheme).Build(),
		config: &rest.Config{Host: "https://kubernetes.example.test"},
		scheme: testScheme,
		mapper: mapper,
	}
}

var _ = Describe("loadRemoteClusterAccess", func() {
	It("returns nil access when the variable is unset", func() {
		GinkgoT().Setenv(envRemoteClusterKubeconfig, "")

		access, err := loadRemoteClusterAccess()

		Expect(err).NotTo(HaveOccurred())
		Expect(access).To(BeNil())
	})

	It("rejects a blank path", func() {
		GinkgoT().Setenv(envRemoteClusterKubeconfig, " \t ")

		access, err := loadRemoteClusterAccess()

		Expect(access).To(BeNil())
		Expect(err).To(MatchError("OSAC_REMOTE_CLUSTER_KUBECONFIG is set to a blank path"))
	})

	It("rejects a kubeconfig that cannot be loaded", func() {
		GinkgoT().Setenv(envRemoteClusterKubeconfig,
			filepath.Join(GinkgoT().TempDir(), "missing.kubeconfig"))

		access, err := loadRemoteClusterAccess()

		Expect(access).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("build config from kubeconfig")))
	})

	It("builds access that resolves to the kubeconfig's server", func() {
		access := newRemoteAccess("https://outer.example.test:6443")

		Expect(access.cluster).NotTo(BeNil())
		Expect(access.cluster.GetConfig().Host).To(Equal("https://outer.example.test:6443"))
		Expect(access.direct).NotTo(BeNil())
	})
})

var _ = Describe("resolveMetal3Clients", func() {
	It("routes through the manager in local mode", func() {
		mgr := newLocalManager()

		cached, direct, err := resolveMetal3Clients(mgr, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(cached).To(BeIdenticalTo(mgr.client))
		Expect(direct).NotTo(BeNil())
	})

	It("requires a manager in local mode", func() {
		cached, direct, err := resolveMetal3Clients(nil, nil)

		Expect(cached).To(BeNil())
		Expect(direct).To(BeNil())
		Expect(err).To(MatchError("kubernetes manager is required to create Metal3 dependencies"))
	})

	It("routes through the outer cluster in remote mode without a manager", func() {
		access := newRemoteAccess("https://outer.example.test:6443")

		cached, direct, err := resolveMetal3Clients(nil, access)

		Expect(err).NotTo(HaveOccurred())
		Expect(cached).To(BeIdenticalTo(access.cluster.GetClient()))
		Expect(direct).To(BeIdenticalTo(access.direct))
	})
})

var _ = Describe("applyRemoteRestConfig", func() {
	It("leaves the backend configs untouched in local mode", func() {
		inventoryCfg := &inventory.Config{Type: testMetal3Backend}
		managementCfg := &management.Config{Type: testMetal3Backend}

		applyRemoteRestConfig(inventoryCfg, managementCfg, nil)

		Expect(inventoryCfg.RESTConfig).To(BeNil())
		Expect(managementCfg.RESTConfig).To(BeNil())
	})

	It("points the backend configs at the outer cluster", func() {
		access := newRemoteAccess("https://outer.example.test:6443")
		restConfig := access.cluster.GetConfig()
		inventoryCfg := &inventory.Config{Type: testMetal3Backend}
		managementCfg := &management.Config{Type: testMetal3Backend}

		applyRemoteRestConfig(inventoryCfg, managementCfg, access)

		Expect(inventoryCfg.RESTConfig).To(BeIdenticalTo(restConfig))
		Expect(managementCfg.RESTConfig).To(BeIdenticalTo(restConfig))
	})
})
