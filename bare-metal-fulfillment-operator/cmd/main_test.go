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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/management"
)

func TestMain(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Main Suite")
}

const (
	testMetal3Backend = "metal3"
	testNetBoxBackend = "netbox"

	netboxDeviceObjectType    = "dcim.device"
	netboxCustomFieldLabelKey = "label"
	netboxTextType            = "text"
	netboxTextTypeLabel       = "Text"
)

func customFieldFixture(
	id int, name, label, fieldType, typeLabel string,
	defaultValue any,
	exactFilter bool,
) map[string]any {
	field := map[string]any{
		"id":                      id,
		"url":                     "/api/extras/custom-fields/" + strconv.Itoa(id) + "/",
		"display":                 label,
		"object_types":            []string{netboxDeviceObjectType},
		"type":                    map[string]string{"value": fieldType, netboxCustomFieldLabelKey: typeLabel},
		"data_type":               fieldType,
		"name":                    name,
		netboxCustomFieldLabelKey: label,
		"required":                false,
		"default":                 defaultValue,
	}
	if exactFilter {
		field["filter_logic"] = map[string]string{"value": "exact", netboxCustomFieldLabelKey: "Exact"}
	}
	return field
}

type startupTestManager struct {
	ctrl.Manager
	config *rest.Config
	scheme *runtime.Scheme
	mapper meta.RESTMapper
}

func (m *startupTestManager) GetClient() client.Client {
	return nil
}

func (m *startupTestManager) GetConfig() *rest.Config {
	return m.config
}

func (m *startupTestManager) GetScheme() *runtime.Scheme {
	return m.scheme
}

func (m *startupTestManager) GetRESTMapper() meta.RESTMapper {
	return m.mapper
}

var _ = Describe("Scheme Initialization", func() {
	It("should register all expected schemes", func() {
		// Verify the global scheme variable is initialized
		Expect(scheme).NotTo(BeNil())

		// Verify client-go scheme types are registered
		Expect(scheme.IsGroupRegistered(corev1.SchemeGroupVersion.Group)).To(BeTrue(),
			"client-go core types should be registered")

		// Verify OSAC BareMetalPool types are registered
		Expect(scheme.IsGroupRegistered(osacv1alpha1.GroupVersion.Group)).To(BeTrue(),
			"OSAC bare-metal-fulfillment-operator types should be registered")
	})

	It("should recognize BareMetalPool type", func() {
		gvks, _, err := scheme.ObjectKinds(&osacv1alpha1.BareMetalPool{})
		Expect(err).NotTo(HaveOccurred())
		Expect(gvks).To(HaveLen(1))
		Expect(gvks[0].Kind).To(Equal("BareMetalPool"))
		Expect(gvks[0].Group).To(Equal(osacv1alpha1.GroupVersion.Group))
		Expect(gvks[0].Version).To(Equal(osacv1alpha1.GroupVersion.Version))
	})

	It("should recognize BareMetalPoolList type", func() {
		gvks, _, err := scheme.ObjectKinds(&osacv1alpha1.BareMetalPoolList{})
		Expect(err).NotTo(HaveOccurred())
		Expect(gvks).To(HaveLen(1))
		Expect(gvks[0].Kind).To(Equal("BareMetalPoolList"))
		Expect(gvks[0].Group).To(Equal(osacv1alpha1.GroupVersion.Group))
		Expect(gvks[0].Version).To(Equal(osacv1alpha1.GroupVersion.Version))
	})

	It("should support creating new schemes with the same registrations", func() {
		testScheme := runtime.NewScheme()
		Expect(clientgoscheme.AddToScheme(testScheme)).To(Succeed())
		Expect(osacv1alpha1.AddToScheme(testScheme)).To(Succeed())

		// Verify the test scheme has the same registrations as the global scheme
		Expect(testScheme.IsGroupRegistered(corev1.SchemeGroupVersion.Group)).To(BeTrue())
		Expect(testScheme.IsGroupRegistered(osacv1alpha1.GroupVersion.Group)).To(BeTrue())
	})

	It("should handle core Kubernetes types", func() {
		pod := &corev1.Pod{}
		gvks, _, err := scheme.ObjectKinds(pod)
		Expect(err).NotTo(HaveOccurred())
		Expect(gvks).NotTo(BeEmpty())
		Expect(gvks[0].Kind).To(Equal("Pod"))
	})

	It("should register metal3 BareMetalHost types", func() {
		Expect(scheme.IsGroupRegistered("metal3.io")).To(BeTrue())

		bmh := &metal3api.BareMetalHost{}
		gvks, _, err := scheme.ObjectKinds(bmh)
		Expect(err).NotTo(HaveOccurred())
		Expect(gvks).NotTo(BeEmpty())
		Expect(gvks[0].Kind).To(Equal("BareMetalHost"))
	})
})

var _ = Describe("createInventoryClient", func() {
	newStartupTestManager := func() *startupTestManager {
		testScheme := runtime.NewScheme()
		Expect(clientgoscheme.AddToScheme(testScheme)).To(Succeed())
		Expect(metal3api.AddToScheme(testScheme)).To(Succeed())
		mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{
			corev1.SchemeGroupVersion,
			metal3api.GroupVersion,
		})
		return &startupTestManager{
			config: &rest.Config{Host: "https://kubernetes.example.test"},
			scheme: testScheme,
			mapper: mapper,
		}
	}

	It("should return error for unsupported inventory type", func() {
		inventoryCfg := &inventory.Config{
			Type: "unknown",
		}
		managementCfg := &management.Config{
			Type: "metal3",
		}

		client, err := createInventoryClient(context.Background(), inventoryCfg, managementCfg, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unsupported inventory type"))
		Expect(client).To(BeNil())
	})

	It("requires Metal3 management when NetBox is selected", func() {
		inventoryCfg := &inventory.Config{
			Type:      testNetBoxBackend,
			HostClass: testMetal3Backend,
		}
		managementCfg := &management.Config{Type: "openstack"}

		inventoryClient, err := createInventoryClient(context.Background(), inventoryCfg, managementCfg, nil)
		Expect(err).To(MatchError(ContainSubstring("NetBox backend requires management.type=metal3")))
		Expect(inventoryClient).To(BeNil())
	})

	It("requires the Metal3 host class when NetBox is selected", func() {
		mgr := newStartupTestManager()
		inventoryCfg := &inventory.Config{
			Type:      testNetBoxBackend,
			HostClass: testNetBoxBackend,
		}
		managementCfg := &management.Config{
			Type: testMetal3Backend,
			Options: map[string]any{
				testMetal3Backend: map[string]any{"namespace": "metal3-system"},
			},
		}

		inventoryClient, err := createInventoryClient(context.Background(), inventoryCfg, managementCfg, mgr)
		Expect(err).To(MatchError(ContainSubstring("NetBox backend requires inventory.hostClass=metal3")))
		Expect(inventoryClient).To(BeNil())
	})

	It("rejects NetBox configuration without the Metal3 namespace", func() {
		inventoryCfg := &inventory.Config{
			Type:      testNetBoxBackend,
			HostClass: testMetal3Backend,
		}
		managementCfg := &management.Config{
			Type:    testMetal3Backend,
			Options: map[string]any{testMetal3Backend: map[string]any{}},
		}

		inventoryClient, err := createInventoryClient(context.Background(), inventoryCfg, managementCfg, nil)
		Expect(err).To(MatchError(ContainSubstring("metal3 namespace is required in management config")))
		Expect(inventoryClient).To(BeNil())
	})

	It("validates the fixed NetBox schema at startup without listing devices", func() {
		tokenFile := filepath.Join(GinkgoT().TempDir(), "netbox-token")
		Expect(os.WriteFile(tokenFile, []byte("test-token\n"), 0o600)).To(Succeed())

		metadataRequests := 0
		deviceRequests := 0
		objectType := ""
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/extras/custom-fields/" {
				if r.URL.Path == "/api/dcim/devices/" {
					deviceRequests++
				}
				http.NotFound(w, r)
				return
			}
			metadataRequests++
			objectType = r.URL.Query().Get("object_type")
			fields := []any{
				customFieldFixture(1, "osac_managed", "OSAC managed", "boolean", "Boolean (true/false)", false, true),
				customFieldFixture(
					2, "osac_instance_id", "OSAC instance ID",
					netboxTextType, netboxTextTypeLabel, "", true,
				),
				customFieldFixture(3, "osac_bmc_address", "OSAC BMC address", netboxTextType, netboxTextTypeLabel, "", false),
				customFieldFixture(4, "osac_boot_mac", "OSAC boot MAC", netboxTextType, netboxTextTypeLabel, "", false),
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count": len(fields), "next": nil, "previous": nil, "results": fields,
			})
		}))
		defer server.Close()

		mgr := newStartupTestManager()
		inventoryCfg := &inventory.Config{
			Type:      testNetBoxBackend,
			HostClass: testMetal3Backend,
			Options: map[string]any{
				testNetBoxBackend: map[string]any{
					"url":               server.URL,
					"tokenFile":         tokenFile,
					"allowInsecureHTTP": true,
				},
			},
		}
		managementCfg := &management.Config{
			Type: testMetal3Backend,
			Options: map[string]any{
				testMetal3Backend: map[string]any{"namespace": "metal3-system"},
			},
		}

		inventoryClient, err := createInventoryClient(context.Background(), inventoryCfg, managementCfg, mgr)
		Expect(err).NotTo(HaveOccurred())
		Expect(inventoryClient).To(BeAssignableToTypeOf(&inventory.NetBoxClient{}))
		Expect(metadataRequests).To(Equal(1))
		Expect(objectType).To(Equal(netboxDeviceObjectType))
		Expect(deviceRequests).To(BeZero())
	})
})
