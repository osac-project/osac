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
	"testing"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	osacv1alpha1 "github.com/osac-project/bare-metal-fulfillment-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

func TestMain(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Main Suite")
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
		// Test that standard Kubernetes types are available
		pod := &corev1.Pod{}
		gvks, _, err := scheme.ObjectKinds(pod)
		Expect(err).NotTo(HaveOccurred())
		Expect(gvks).NotTo(BeEmpty())
		Expect(gvks[0].Kind).To(Equal("Pod"))
	})
})
