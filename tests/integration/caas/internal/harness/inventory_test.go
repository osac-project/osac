// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package harness

import (
	"context"
	"errors"
	"fmt"
	"net"

	metal3v1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	physical "github.com/osac-project/osac/tests/integration/caas/internal/simulator/host"
)

// inventoryInputs supplies an original host and independent hardware observation.
func inventoryInputs() (*metal3v1.BareMetalHost, *metal3v1.HardwareData) {
	host := &metal3v1.BareMetalHost{
		ObjectMeta: metav1.ObjectMeta{
			Name: "worker", Namespace: "inventory", UID: "original",
			Labels: map[string]string{"app.kubernetes.io/part-of": "osac-provider-simulator"},
		},
		Spec: metal3v1.BareMetalHostSpec{Online: false},
		Status: metal3v1.BareMetalHostStatus{
			OperationalStatus: metal3v1.OperationalStatusOK, PoweredOn: false,
			Provisioning: metal3v1.ProvisionStatus{State: metal3v1.StateAvailable},
		},
	}
	hardware := &metal3v1.HardwareData{
		ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "inventory", UID: "hardware-original"},
		Spec: metal3v1.HardwareDataSpec{HardwareDetails: &metal3v1.HardwareDetails{
			NIC: []metal3v1.NIC{{Name: "eth0", MAC: "02:00:00:00:00:01"}},
		}},
	}

	return host, hardware
}

var _ = Describe("Suite-owned inventory", func() {
	var environment *Environment

	BeforeEach(func() {
		scheme, err := apiScheme()
		Expect(err).NotTo(HaveOccurred())

		c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, object client.Object, opts ...client.CreateOption) error {
				if object.GetUID() == "" {
					object.SetUID(types.UID(fmt.Sprintf("%T-%s", object, object.GetName())))
				}

				return c.Create(ctx, object, opts...)
			},
		}).Build()
		environment = &Environment{
			Namespace: "hub", Client: c,
			Kube: kubefake.NewClientset(&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: simulatorDeployment, Namespace: "hub"},
				Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "simulator", Env: []corev1.EnvVar{
						{Name: "METAL3_NAMESPACE", Value: "inventory"},
					}}},
				}}},
			}),
		}
	})

	It("accepts empty inventory in the configured namespace", func(ctx SpecContext) {
		namespace, err := InventoryNamespace(ctx, environment)
		Expect(err).NotTo(HaveOccurred())
		Expect(namespace).To(Equal("inventory"))
		Expect(CheckInventoryEmpty(ctx, environment)).To(Succeed())
	})

	DescribeTable("rejects leftover hardware without modifying it",
		func(ctx SpecContext, hardwareOnly, labelled bool) {
			host, hardware := inventoryInputs()

			var object client.Object = host

			if hardwareOnly {
				object = hardware
			}

			if !labelled {
				object.SetLabels(nil)
			}

			Expect(environment.Client.Create(ctx, object)).To(Succeed())
			Expect(CheckInventoryEmpty(ctx, environment)).To(
				MatchError(ContainSubstring("inventory namespace inventory contains 1")),
			)

			current := object.DeepCopyObject().(client.Object)
			Expect(environment.Client.Get(ctx, client.ObjectKeyFromObject(object), current)).To(Succeed())
			Expect(current).To(Equal(object))
		},
		Entry("released labelled host", false, true),
		Entry("unlabelled host", false, false),
		Entry("orphaned HardwareData", true, false),
	)

	It("leaves hardware outside the configured namespace untouched", func(ctx SpecContext) {
		host, hardware := inventoryInputs()
		for _, object := range []client.Object{host, hardware} {
			object.SetNamespace("other")
			Expect(environment.Client.Create(ctx, object)).To(Succeed())
		}

		Expect(CheckInventoryEmpty(ctx, environment)).To(Succeed())

		for _, object := range []client.Object{host, hardware} {
			current := object.DeepCopyObject().(client.Object)
			Expect(environment.Client.Get(ctx, client.ObjectKeyFromObject(object), current)).To(Succeed())
			Expect(current).To(Equal(object))
		}
	})

	DescribeTable("requires a separate configured inventory namespace",
		func(ctx SpecContext, namespace, message string) {
			api := environment.Kube.AppsV1().Deployments(environment.Namespace)
			deployment, err := api.Get(ctx, simulatorDeployment, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			deployment.Spec.Template.Spec.Containers[0].Env[0].Value = namespace
			_, err = api.Update(ctx, deployment, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(CheckInventoryEmpty(ctx, environment)).To(MatchError(ContainSubstring(message)))
		},
		Entry("missing inventory namespace", "", "inventory namespace is missing"),
		Entry("application namespace", "hub", "inventory requires a separate namespace"),
	)

	It("propagates inventory configuration read failures", func(ctx SpecContext) {
		err := environment.Kube.AppsV1().Deployments(environment.Namespace).
			Delete(ctx, simulatorDeployment, metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(apierrors.IsNotFound(CheckInventoryEmpty(ctx, environment))).To(BeTrue())
	})

	DescribeTable("propagates inventory list failures", func(ctx SpecContext, hardwareOnly bool) {
		failure := errors.New("inventory read failed")
		environment.Client = interceptor.NewClient(environment.Client.(client.WithWatch), interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				_, hardware := list.(*metal3v1.HardwareDataList)
				if hardware == hardwareOnly {
					return failure
				}

				return c.List(ctx, list, opts...)
			},
		})
		Expect(CheckInventoryEmpty(ctx, environment)).To(MatchError(failure))
	},
		Entry("BareMetalHost list", false),
		Entry("HardwareData list", true),
	)

	DescribeTable("creates explicit groups with independent names and hardware",
		func(ctx SpecContext, count, hostsPerGroup int) {
			groups := make([]string, count)
			for index := range groups {
				groups[index] = fmt.Sprintf("load-%03d", index+1)
			}

			inventory, err := CreateInventory(ctx, environment, "inventory", groups, hostsPerGroup)
			Expect(err).NotTo(HaveOccurred())
			Expect(inventory.Hosts).To(HaveLen(count * hostsPerGroup))

			names, identities, macs := map[string]bool{}, map[types.UID]bool{}, map[string]bool{}

			for _, group := range groups {
				Expect(inventory.ForSlot(group).Hosts).To(HaveLen(hostsPerGroup))
			}

			for _, host := range inventory.Hosts {
				Expect(names[host.Name]).To(BeFalse())
				Expect(host.UID).NotTo(BeEmpty())
				Expect(host.HardwareUID).NotTo(BeEmpty())
				Expect(identities[host.UID] || identities[host.HardwareUID]).To(BeFalse())
				names[host.Name], identities[host.UID], identities[host.HardwareUID] = true, true, true
				current := &metal3v1.BareMetalHost{}
				Expect(
					environment.Client.Get(ctx, client.ObjectKey{Namespace: host.Namespace, Name: host.Name}, current),
				).To(Succeed())
				Expect(current.UID).To(Equal(host.UID))
				Expect(current.Labels[SlotLabel]).To(Equal(host.Slot))
				Expect(current.Spec.Online).To(BeFalse())

				hardware := &metal3v1.HardwareData{}
				Expect(
					environment.Client.Get(ctx, client.ObjectKey{Namespace: host.Namespace, Name: host.Name}, hardware),
				).To(Succeed())
				Expect(hardware.UID).To(Equal(host.HardwareUID))
				Expect(hardware.Spec.HardwareDetails.Storage).To(Equal([]metal3v1.Storage{
					{Name: "/dev/sdb", SizeBytes: 257698037760}, {Name: "/dev/sda", SizeBytes: 128849018880},
				}))
				mac, err := net.ParseMAC(hardware.Spec.HardwareDetails.NIC[0].MAC)
				Expect(err).NotTo(HaveOccurred())
				Expect(mac[0]&3).To(Equal(byte(2)), "MACs must be locally administered unicast")
				Expect(macs[mac.String()]).To(BeFalse())
				macs[mac.String()] = true
			}

			Expect(inventory.Delete(ctx, environment)).To(Succeed())
			Expect(CheckInventoryEmpty(ctx, environment)).To(Succeed())
		},
		Entry("one group", 1, 2),
		Entry("four groups", 4, 2),
		Entry("one hundred groups", 100, 2),
		Entry("independent host count", 1, 3),
	)

	It("retains partial creation identities without adopting existing hardware", func(ctx SpecContext) {
		foreign := &metal3v1.HardwareData{
			ObjectMeta: metav1.ObjectMeta{Name: "load-001-worker-01", Namespace: "inventory", UID: "foreign"},
		}
		Expect(environment.Client.Create(ctx, foreign)).To(Succeed())
		inventory, err := CreateInventory(ctx, environment, "inventory", []string{"load-001"}, 2)
		Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
		Expect(inventory.Hosts).To(HaveLen(1))
		Expect(inventory.Hosts[0].UID).NotTo(BeEmpty())
		Expect(inventory.Hosts[0].HardwareUID).To(BeEmpty())
		Expect(inventory.Delete(ctx, environment)).To(Succeed())

		host := &metal3v1.BareMetalHost{}
		Expect(apierrors.IsNotFound(environment.Client.Get(ctx, client.ObjectKeyFromObject(foreign), host))).To(BeTrue())
		Expect(inventory.Delete(ctx, environment)).To(Succeed())
		Expect(environment.Client.Get(ctx, client.ObjectKeyFromObject(foreign), foreign)).To(Succeed())
		Expect(foreign.UID).To(Equal(types.UID("foreign")))
	})

	It("resumes deletion after the host is gone and a hardware delete failed", func(ctx SpecContext) {
		inventory, err := CreateInventory(ctx, environment, "inventory", []string{"load-001"}, 1)
		Expect(err).NotTo(HaveOccurred())

		failure := errors.New("hardware deletion unavailable")
		refuse := true
		environment.Client = interceptor.NewClient(environment.Client.(client.WithWatch), interceptor.Funcs{
			Delete: func(ctx context.Context, c client.WithWatch, object client.Object, opts ...client.DeleteOption) error {
				if _, hardware := object.(*metal3v1.HardwareData); hardware && refuse {
					return failure
				}

				return c.Delete(ctx, object, opts...)
			},
		})
		Expect(inventory.Delete(ctx, environment)).To(MatchError(failure))

		refuse = false

		Expect(inventory.Delete(ctx, environment)).To(Succeed())
		Expect(CheckInventoryEmpty(ctx, environment)).To(Succeed())
		Expect(inventory.Delete(ctx, environment)).To(Succeed())
	})

	DescribeTable("uses original UID preconditions and propagates API refusal",
		func(ctx SpecContext, replaceHardware bool) {
			inventory, err := CreateInventory(ctx, environment, "inventory", []string{"load-001"}, 1)
			Expect(err).NotTo(HaveOccurred())

			key := client.ObjectKey{Namespace: "inventory", Name: inventory.Hosts[0].Name}

			var replacement client.Object = &metal3v1.BareMetalHost{}

			if replaceHardware {
				replacement = &metal3v1.HardwareData{}
			}

			Expect(environment.Client.Get(ctx, key, replacement)).To(Succeed())
			replacement.SetUID("replacement")
			Expect(environment.Client.Update(ctx, replacement)).To(Succeed())
			environment.Client = interceptor.NewClient(environment.Client.(client.WithWatch), interceptor.Funcs{
				Delete: func(ctx context.Context, c client.WithWatch, object client.Object, opts ...client.DeleteOption) error {
					options := (&client.DeleteOptions{}).ApplyOptions(opts)

					expected := inventory.Hosts[0].UID
					if _, hardware := object.(*metal3v1.HardwareData); hardware {
						expected = inventory.Hosts[0].HardwareUID
					}

					Expect(options.Preconditions).NotTo(BeNil())
					Expect(options.Preconditions.UID).To(Equal(&expected))

					current := object.DeepCopyObject().(client.Object)
					if err := c.Get(ctx, client.ObjectKeyFromObject(object), current); err != nil {
						return err
					}

					// The fake client omits the API server's UID precondition check.
					if current.GetUID() != *options.Preconditions.UID {
						return apierrors.NewConflict(schema.GroupResource{Group: "metal3.io"}, object.GetName(),
							errors.New("UID precondition failed"))
					}

					return c.Delete(ctx, object, opts...)
				},
			})
			Expect(apierrors.IsConflict(inventory.Delete(ctx, environment))).To(BeTrue())
			Expect(environment.Client.Get(ctx, key, replacement)).To(Succeed())
			Expect(replacement.GetUID()).To(Equal(types.UID("replacement")))
		},
		Entry("replaced host", false),
		Entry("replaced hardware", true),
	)
})

var _ = Describe("Native inventory identity", func() {
	It("retains original host and hardware identities during release checks", func(ctx SpecContext) {
		By("registering a host and its independent HardwareData in a fake client")

		host, hardware := inventoryInputs()
		scheme, err := apiScheme()
		Expect(err).NotTo(HaveOccurred())

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(host, hardware).Build()
		environment := &Environment{Namespace: "hub", Client: c}

		By("retaining the original fixture identities for release checks")

		inventory := Inventory{Namespace: host.Namespace, Hosts: []Host{{Machine: physical.Machine{
			Namespace: host.Namespace, Name: host.Name, UID: host.UID, HardwareUID: hardware.UID,
		}}}}
		Expect(inventory.CheckReleased(ctx, environment)).To(Succeed())

		By("rejecting replacement hardware even while the host remains available")

		Expect(c.Get(ctx, client.ObjectKeyFromObject(hardware), hardware)).To(Succeed())
		hardware.UID = "replacement-hardware"
		Expect(c.Update(ctx, hardware)).To(Succeed())
		Expect(inventory.CheckReleased(ctx, environment)).NotTo(Succeed())

		By("restoring original hardware and rejecting a replacement host")

		hardware.UID = "hardware-original"
		Expect(c.Update(ctx, hardware)).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(host), host)).To(Succeed())
		host.UID = "replacement"
		Expect(c.Update(ctx, host)).To(Succeed())
		Expect(inventory.CheckReleased(ctx, environment)).NotTo(Succeed())
	})
})
