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

package management_test

import (
	"context"
	"errors"

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/bare-metal-fulfillment-operator/internal/management"
)

const (
	metal3TestNamespace = "test-bmaas"
)

func newMetal3TestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = metal3api.AddToScheme(s)
	return s
}

func newMetal3ManagementClient(objects ...client.Object) *management.Metal3Client {
	scheme := newMetal3TestScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&metal3api.BareMetalHost{}).
		Build()
	return management.NewMetal3ClientForTest(fakeClient, metal3TestNamespace)
}

func newBMHForManagement(name string, online bool, poweredOn bool) *metal3api.BareMetalHost {
	return &metal3api.BareMetalHost{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: metal3TestNamespace,
		},
		Spec: metal3api.BareMetalHostSpec{
			Online: online,
		},
		Status: metal3api.BareMetalHostStatus{
			PoweredOn: poweredOn,
		},
	}
}

func newBMHWithAnnotations(name string, online bool, poweredOn bool, annotations map[string]string) *metal3api.BareMetalHost {
	bmh := newBMHForManagement(name, online, poweredOn)
	bmh.Annotations = annotations
	return bmh
}

var _ = Describe("Metal3 Management Backend", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("GetPowerState", func() {
		It("returns PowerOn when host is powered on and stable", func() {
			bmh := newBMHForManagement("host-on", true, true)
			m := newMetal3ManagementClient(bmh)

			status, err := m.GetPowerState(ctx, metal3TestNamespace+"/host-on")
			Expect(err).NotTo(HaveOccurred())
			Expect(status.State).To(Equal(management.PowerOn))
			Expect(status.IsTransitioning).To(BeFalse())
		})

		It("returns PowerOff when host is powered off and stable", func() {
			bmh := newBMHForManagement("host-off", false, false)
			m := newMetal3ManagementClient(bmh)

			status, err := m.GetPowerState(ctx, metal3TestNamespace+"/host-off")
			Expect(err).NotTo(HaveOccurred())
			Expect(status.State).To(Equal(management.PowerOff))
			Expect(status.IsTransitioning).To(BeFalse())
		})

		It("reports transitioning when spec.online != status.poweredOn (powering on)", func() {
			bmh := newBMHForManagement("host-booting", true, false)
			m := newMetal3ManagementClient(bmh)

			status, err := m.GetPowerState(ctx, metal3TestNamespace+"/host-booting")
			Expect(err).NotTo(HaveOccurred())
			Expect(status.State).To(Equal(management.PowerOff))
			Expect(status.IsTransitioning).To(BeTrue())
		})

		It("reports transitioning when spec.online != status.poweredOn (powering off)", func() {
			bmh := newBMHForManagement("host-shutting", false, true)
			m := newMetal3ManagementClient(bmh)

			status, err := m.GetPowerState(ctx, metal3TestNamespace+"/host-shutting")
			Expect(err).NotTo(HaveOccurred())
			Expect(status.State).To(Equal(management.PowerOn))
			Expect(status.IsTransitioning).To(BeTrue())
		})

		It("returns error for missing host", func() {
			m := newMetal3ManagementClient()

			_, err := m.GetPowerState(ctx, metal3TestNamespace+"/nonexistent")
			Expect(err).To(HaveOccurred())
		})

		It("returns error for invalid host ID format", func() {
			m := newMetal3ManagementClient()

			_, err := m.GetPowerState(ctx, "invalid-id")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("SetPowerState", func() {
		It("patches spec.online to true for PowerOn", func() {
			bmh := newBMHForManagement("host-off", false, false)
			m := newMetal3ManagementClient(bmh)

			err := m.SetPowerState(ctx, metal3TestNamespace+"/host-off", management.PowerOn)
			Expect(err).NotTo(HaveOccurred())

			updated := &metal3api.BareMetalHost{}
			Expect(m.TestClient().Get(ctx, client.ObjectKey{
				Namespace: metal3TestNamespace,
				Name:      "host-off",
			}, updated)).To(Succeed())
			Expect(updated.Spec.Online).To(BeTrue())
		})

		It("patches spec.online to false for PowerOff", func() {
			bmh := newBMHForManagement("host-on", true, true)
			m := newMetal3ManagementClient(bmh)

			err := m.SetPowerState(ctx, metal3TestNamespace+"/host-on", management.PowerOff)
			Expect(err).NotTo(HaveOccurred())

			updated := &metal3api.BareMetalHost{}
			Expect(m.TestClient().Get(ctx, client.ObjectKey{
				Namespace: metal3TestNamespace,
				Name:      "host-on",
			}, updated)).To(Succeed())
			Expect(updated.Spec.Online).To(BeFalse())
		})

		It("returns ErrTransitioning if power state is already transitioning", func() {
			bmh := newBMHForManagement("host-booting", true, false)
			m := newMetal3ManagementClient(bmh)

			err := m.SetPowerState(ctx, metal3TestNamespace+"/host-booting", management.PowerOn)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, management.ErrTransitioning)).To(BeTrue())
		})

		It("is a no-op when already in desired state", func() {
			bmh := newBMHForManagement("host-on", true, true)
			m := newMetal3ManagementClient(bmh)

			err := m.SetPowerState(ctx, metal3TestNamespace+"/host-on", management.PowerOn)
			Expect(err).NotTo(HaveOccurred())
		})

		It("returns error for invalid target power state", func() {
			bmh := newBMHForManagement("host-1", false, false)
			m := newMetal3ManagementClient(bmh)

			err := m.SetPowerState(ctx, metal3TestNamespace+"/host-1", "invalid")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid target power state"))
		})

		It("returns error for missing host", func() {
			m := newMetal3ManagementClient()

			err := m.SetPowerState(ctx, metal3TestNamespace+"/nonexistent", management.PowerOn)
			Expect(err).To(HaveOccurred())
		})

		It("returns error for invalid host ID format", func() {
			m := newMetal3ManagementClient()

			err := m.SetPowerState(ctx, "bad-id", management.PowerOn)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("TriggerRestart", func() {
		It("sets reboot annotation on BMH without one", func() {
			bmh := newBMHForManagement("host-1", true, true)
			m := newMetal3ManagementClient(bmh)

			err := m.TriggerRestart(ctx, metal3TestNamespace+"/host-1")
			Expect(err).NotTo(HaveOccurred())

			updated := &metal3api.BareMetalHost{}
			Expect(m.TestClient().Get(ctx, client.ObjectKey{
				Namespace: metal3TestNamespace,
				Name:      "host-1",
			}, updated)).To(Succeed())
			Expect(updated.Annotations).To(HaveKey(metal3api.RebootAnnotationPrefix))
		})

		It("returns ErrTransitioning when reboot annotation already exists", func() {
			annotations := map[string]string{
				metal3api.RebootAnnotationPrefix: "",
			}
			bmh := newBMHWithAnnotations("host-restarting", true, true, annotations)
			m := newMetal3ManagementClient(bmh)

			err := m.TriggerRestart(ctx, metal3TestNamespace+"/host-restarting")
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, management.ErrTransitioning)).To(BeTrue())
		})

		It("returns error for missing host", func() {
			m := newMetal3ManagementClient()

			err := m.TriggerRestart(ctx, metal3TestNamespace+"/nonexistent")
			Expect(err).To(HaveOccurred())
		})

		It("returns error for invalid host ID format", func() {
			m := newMetal3ManagementClient()

			err := m.TriggerRestart(ctx, "bad-id")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("IsRestartComplete", func() {
		It("returns true when no reboot annotation is present", func() {
			bmh := newBMHForManagement("host-1", true, true)
			m := newMetal3ManagementClient(bmh)

			complete, err := m.IsRestartComplete(ctx, metal3TestNamespace+"/host-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(complete).To(BeTrue())
		})

		It("returns false when reboot annotation is present", func() {
			annotations := map[string]string{
				metal3api.RebootAnnotationPrefix: "",
			}
			bmh := newBMHWithAnnotations("host-restarting", true, true, annotations)
			m := newMetal3ManagementClient(bmh)

			complete, err := m.IsRestartComplete(ctx, metal3TestNamespace+"/host-restarting")
			Expect(err).NotTo(HaveOccurred())
			Expect(complete).To(BeFalse())
		})

		It("returns error for missing host", func() {
			m := newMetal3ManagementClient()

			_, err := m.IsRestartComplete(ctx, metal3TestNamespace+"/nonexistent")
			Expect(err).To(HaveOccurred())
		})

		It("returns error for invalid host ID format", func() {
			m := newMetal3ManagementClient()

			_, err := m.IsRestartComplete(ctx, "bad-id")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Registration", func() {
		It("metal3 backend is registered", func() {
			_, err := management.NewClient(context.Background(), &management.Config{
				Type: "metal3",
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring(`unsupported management backend type: "metal3"`))
		})
	})
})
