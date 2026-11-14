// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package metal3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	metal3v1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// metal3Scheme registers inventory resources for the fake API.
func metal3Scheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	Expect(metal3v1.AddToScheme(scheme)).To(Succeed())
	return scheme
}

// inventoryHost supplies persistent hardware with foreign metadata and provisioning inputs.
func inventoryHost() *metal3v1.BareMetalHost {
	return &metal3v1.BareMetalHost{
		ObjectMeta: metav1.ObjectMeta{
			Name: "worker", Namespace: "inventory", UID: "original-hardware",
			Annotations: map[string]string{"retained": "yes"},
			Labels:      map[string]string{"allocation": "owned-by-bmf"},
			Finalizers:  []string{"foreign.example/cleanup"},
		},
		Spec: metal3v1.BareMetalHostSpec{
			ConsumerRef: &corev1.ObjectReference{Name: "consumer"},
			UserData:    &corev1.SecretReference{Name: "input", Namespace: "inventory"},
		},
	}
}

var _ = Describe("Metal3 host responses", func() {
	var host *metal3v1.BareMetalHost

	BeforeEach(func() { host = inventoryHost() })

	It("follows provisioning, image changes, power and release on the same hardware", func(ctx SpecContext) {
		writes := 0
		c := fake.NewClientBuilder().WithScheme(metal3Scheme()).WithObjects(host).
			WithStatusSubresource(host).WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(
				ctx context.Context, c client.Client, name string, object client.Object, opts ...client.SubResourceUpdateOption,
			) error {
				writes++
				data, err := json.Marshal(object.(*metal3v1.BareMetalHost).Status)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(data)).To(ContainSubstring(`"poweredOn":`))
				Expect(string(data)).To(ContainSubstring(`"errorMessage":""`))
				Expect(string(data)).To(ContainSubstring(`"ID":""`))

				return c.SubResource(name).Update(ctx, object, opts...)
			},
		}).Build()
		r := &HostReconciler{Client: c, APIReader: c, Namespace: "inventory"}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(host)}

		for _, step := range []struct {
			description, checksum, deployedChecksum string
			state                                   metal3v1.ProvisioningState
			online                                  bool
		}{
			{description: "making the inventory available", state: metal3v1.StateAvailable},
			{description: "leaving an offline image request pending", checksum: "first", state: metal3v1.StateAvailable},
			{
				description:      "provisioning the first image",
				checksum:         "first",
				deployedChecksum: "first",
				state:            metal3v1.StateProvisioned,
				online:           true,
			},
			{
				description:      "retaining the deployed checksum",
				checksum:         "second",
				deployedChecksum: "first",
				state:            metal3v1.StateProvisioned,
				online:           true,
			},
			{
				description:      "powering off the provisioned host",
				checksum:         "second",
				deployedChecksum: "first",
				state:            metal3v1.StateProvisioned,
			},
			{description: "releasing the host back to available inventory", state: metal3v1.StateAvailable},
		} {
			By(step.description)

			current := &metal3v1.BareMetalHost{}
			Expect(c.Get(ctx, request.NamespacedName, current)).To(Succeed())

			current.Spec.Online = step.online
			image := metal3v1.Image{}
			if step.checksum != "" {
				image = metal3v1.Image{
					URL: "https://example.invalid/image", Checksum: step.checksum, ChecksumType: metal3v1.SHA256,
					DiskFormat: new("raw"), OCIAuthSecretName: new("native-input"),
				}
				current.Spec.Image = image.DeepCopy()
			} else {
				current.Spec.Image = nil
			}

			Expect(c.Update(ctx, current)).To(Succeed())
			_, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			after := &metal3v1.BareMetalHost{}
			Expect(c.Get(ctx, request.NamespacedName, after)).To(Succeed())

			expectedImage := metal3v1.Image{}
			if step.deployedChecksum != "" {
				expectedImage = image
				expectedImage.Checksum = step.deployedChecksum
			}

			Expect(after.Status.Provisioning.Image).To(Equal(expectedImage))
			Expect(after.Status.Provisioning.State).To(Equal(step.state))
			Expect(after.Status.PoweredOn).To(Equal(step.online))
			Expect(after.Status.OperationalStatus).To(Equal(metal3v1.OperationalStatusOK))
			Expect(after.Spec).To(Equal(current.Spec))
			Expect(after.Annotations).To(Equal(host.Annotations))
			Expect(after.Labels).To(Equal(host.Labels))
			Expect(after.Finalizers).To(Equal(host.Finalizers))
			Expect(after.UID).To(Equal(host.UID))

			if step.checksum == "" {
				data, err := json.Marshal(after.Status.Provisioning.Image)
				Expect(err).NotTo(HaveOccurred())
				Expect(data).To(MatchJSON(`{"url":""}`), "release must match BMO's typed empty image")
			}

			before := writes
			_, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(writes).To(Equal(before), "settled status must not be rewritten")
		}

		Expect(writes).To(Equal(4), "availability, provisioning, power-off and release each publish complete status")
	})

	It("observes native detachment without releasing or powering off the original host", func(ctx SpecContext) {
		host.Annotations[metal3v1.DetachedAnnotation] = ""
		host.Status.Provisioning.State = metal3v1.StateProvisioned
		host.Status.Provisioning.Image.URL = "https://example.invalid/deployed"
		host.Status.PoweredOn = true
		host.Status.ErrorType = metal3v1.DetachError
		host.Status.ErrorMessage, host.Status.ErrorCount = "previous detach attempt", 2
		c := fake.NewClientBuilder().WithScheme(metal3Scheme()).WithObjects(host).WithStatusSubresource(host).Build()
		r := &HostReconciler{Client: c, APIReader: c, Namespace: host.Namespace}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(host)}

		By("reporting detachment while image release and power-off are requested")

		_, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		current := &metal3v1.BareMetalHost{}
		Expect(c.Get(ctx, request.NamespacedName, current)).To(Succeed())
		Expect(current.Status.Provisioning).To(Equal(host.Status.Provisioning))
		Expect(current.Status.PoweredOn).To(BeTrue())
		Expect(current.Status.OperationalStatus).To(Equal(metal3v1.OperationalStatusDetached))
		Expect(current.Status.ErrorType).To(BeEmpty())
		Expect(current.Status.ErrorMessage).To(BeEmpty())
		Expect(current.Status.ErrorCount).To(BeZero())
		Expect(current.Spec).To(Equal(host.Spec))
		Expect(current.UID).To(Equal(host.UID))
		Expect(current.Annotations).To(Equal(host.Annotations))
		Expect(current.Finalizers).To(Equal(host.Finalizers))
		before := current.DeepCopy()
		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, request.NamespacedName, current)).To(Succeed())
		Expect(current).To(Equal(before))

		By("resuming normal release after the requesting controller removes detachment")

		delete(current.Annotations, metal3v1.DetachedAnnotation)
		Expect(c.Update(ctx, current)).To(Succeed())

		for range 2 {
			_, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(c.Get(ctx, request.NamespacedName, current)).To(Succeed())
		Expect(current.Status.Provisioning.State).To(Equal(metal3v1.StateAvailable))
		Expect(current.Status.Provisioning.Image).To(Equal(metal3v1.Image{}))
		Expect(current.Status.PoweredOn).To(BeFalse())
		Expect(current.Status.OperationalStatus).To(Equal(metal3v1.OperationalStatusOK))
		Expect(current.UID).To(Equal(host.UID))
	})

	It("restores operational status after a detach failure is cancelled", func(ctx SpecContext) {
		host.Spec.Image = &metal3v1.Image{URL: "https://example.invalid/deployed"}
		host.Spec.Online = true
		host.Status.Provisioning.State = metal3v1.StateProvisioned
		host.Status.Provisioning.Image = *host.Spec.Image.DeepCopy()
		host.Status.PoweredOn = host.Spec.Online
		host.Status.OperationalStatus = metal3v1.OperationalStatusError
		host.Status.ErrorType = metal3v1.DetachError
		host.Status.ErrorMessage, host.Status.ErrorCount = "detach failed", 2
		c := fake.NewClientBuilder().WithScheme(metal3Scheme()).WithObjects(host).WithStatusSubresource(host).Build()

		r := &HostReconciler{Client: c, APIReader: c, Namespace: host.Namespace}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(host)}
		for range 2 {
			_, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			current := &metal3v1.BareMetalHost{}
			Expect(c.Get(ctx, request.NamespacedName, current)).To(Succeed())
			Expect(current.Status.OperationalStatus).To(Equal(metal3v1.OperationalStatusOK))
			Expect(current.Status.ErrorType).To(BeEmpty())
			Expect(current.Status.ErrorMessage).To(BeEmpty())
			Expect(current.Status.ErrorCount).To(BeZero())
			Expect(current.Status.Provisioning).To(Equal(host.Status.Provisioning))
			Expect(current.Status.PoweredOn).To(Equal(host.Status.PoweredOn))
		}
	})

	DescribeTable("allows detachment only in native states unless forced",
		func(state metal3v1.ProvisioningState, value string, detached bool) {
			host.Status.Provisioning.State = state
			host.Annotations[metal3v1.DetachedAnnotation] = value
			next := host.DeepCopy()
			Expect(detachedStatus(host, next)).To(Equal(detached))

			if detached {
				Expect(next.Status.OperationalStatus).To(Equal(metal3v1.OperationalStatusDetached))
			} else {
				Expect(next.Status.OperationalStatus).NotTo(Equal(metal3v1.OperationalStatusDetached))
			}
		},
		Entry("available host", metal3v1.StateAvailable, "", true),
		Entry("active provisioning", metal3v1.StateProvisioning, "", false),
		Entry("forced active provisioning", metal3v1.StateProvisioning, `{"force":true}`, true),
		Entry("malformed arguments during provisioning", metal3v1.StateProvisioning, "{", false),
	)

	DescribeTable("leaves inactive hosts untouched", func(ctx SpecContext, variant string) {
		switch variant {
		case "foreign namespace":
			host.Namespace = "foreign"
		case "paused":
			host.Annotations[metal3v1.PausedAnnotation] = ""
		case "deleting":
			host.DeletionTimestamp = new(metav1.Now())
		}

		reads, writes := 0, 0

		c := fake.NewClientBuilder().WithScheme(metal3Scheme()).WithObjects(host).
			WithStatusSubresource(host).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(
				ctx context.Context, c client.WithWatch, key client.ObjectKey, object client.Object, opts ...client.GetOption,
			) error {
				reads++
				return c.Get(ctx, key, object, opts...)
			},
			SubResourceUpdate: func(
				context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption,
			) error {
				writes++
				return fmt.Errorf("unexpected status write")
			},
		}).Build()
		if variant == "absent" {
			c = fake.NewClientBuilder().WithScheme(metal3Scheme()).Build()
		}

		r := &HostReconciler{Client: c, APIReader: c, Namespace: "inventory"}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(host)})
		Expect(err).NotTo(HaveOccurred())
		Expect(writes).To(BeZero())

		if variant == "foreign namespace" {
			Expect(reads).To(BeZero())
		}
	},
		Entry("does not read another namespace", "foreign namespace"),
		Entry("does not write a deleting host", "deleting"),
		Entry("does not write a paused host", "paused"),
		Entry("accepts a missing host", "absent"),
	)

	It("makes dynamically named hosts available without selection labels", func(ctx SpecContext) {
		host.Name = "load-100-worker-02"
		host.Labels = nil
		c := fake.NewClientBuilder().WithScheme(metal3Scheme()).WithObjects(host).WithStatusSubresource(host).Build()
		r := &HostReconciler{Client: c, APIReader: c, Namespace: "inventory"}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(host)})
		Expect(err).NotTo(HaveOccurred())

		current := &metal3v1.BareMetalHost{}
		Expect(c.Get(ctx, client.ObjectKeyFromObject(host), current)).To(Succeed())
		Expect(current.Status.Provisioning.State).To(Equal(metal3v1.StateAvailable))
		Expect(current.Labels).To(BeEmpty())
	})

	It("acknowledges observed power-off with optimistic locking and preserves unrelated fields", func(ctx SpecContext) {
		host.Annotations[rebootAnnotation] = ""
		host.Spec.Online = true
		host.Spec.Image = &metal3v1.Image{URL: "https://example.invalid/image"}
		host.Status = metal3v1.BareMetalHostStatus{
			PoweredOn:  true,
			ErrorCount: 2, ErrorMessage: "retained observation", OperationalStatus: metal3v1.OperationalStatusOK,
			Provisioning: metal3v1.ProvisionStatus{ID: "native-id", State: metal3v1.StateProvisioned, Image: *host.Spec.Image},
		}
		race := true
		c := fake.NewClientBuilder().WithScheme(metal3Scheme()).WithObjects(host).
			WithStatusSubresource(host).WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(
				ctx context.Context, c client.WithWatch, object client.Object, patch client.Patch, opts ...client.PatchOption,
			) error {
				if race {
					race = false

					current := &metal3v1.BareMetalHost{}
					if err := c.Get(ctx, client.ObjectKeyFromObject(object), current); err != nil {
						return err
					}

					current.Spec.Online = false
					if err := c.Update(ctx, current); err != nil {
						return err
					}
				}

				return c.Patch(ctx, object, patch, opts...)
			},
		}).Build()
		r := &HostReconciler{Client: c, APIReader: c, Namespace: "inventory"}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(host)}

		By("observing power-off before acknowledging the request")

		_, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		By("rejecting acknowledgement when a concurrent writer takes the host offline")

		_, err = r.Reconcile(ctx, request)
		Expect(apierrors.IsConflict(err)).To(BeTrue())
		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		current := &metal3v1.BareMetalHost{}
		Expect(c.Get(ctx, request.NamespacedName, current)).To(Succeed())
		Expect(current.Annotations).NotTo(HaveKey(rebootAnnotation))
		Expect(current.Status.PoweredOn).To(BeFalse())

		By("powering on after the acknowledged reboot")

		current.Spec.Online = true
		Expect(c.Update(ctx, current)).To(Succeed())

		for range 2 {
			_, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
		}

		after := &metal3v1.BareMetalHost{}
		Expect(c.Get(ctx, request.NamespacedName, after)).To(Succeed())
		Expect(after.Annotations).NotTo(HaveKey(rebootAnnotation))
		Expect(after.Annotations).To(HaveKeyWithValue("retained", "yes"))
		Expect(after.Status.ErrorCount).To(Equal(2))
		Expect(after.Status.ErrorMessage).To(Equal("retained observation"))
		Expect(after.Status.Provisioning.ID).To(Equal("native-id"))
		Expect(after.Finalizers).To(Equal(host.Finalizers))
	})

	It("releases an image while preserving client reboot requests", func(ctx SpecContext) {
		host.Annotations[rebootAnnotation] = ""
		host.Annotations[rebootAnnotation+"/client"] = ""
		host.Status.PoweredOn = true
		host.Status.Provisioning.State = metal3v1.StateProvisioned
		host.Status.Provisioning.Image.URL = "https://example.invalid/image"
		c := fake.NewClientBuilder().WithScheme(metal3Scheme()).WithObjects(host).WithStatusSubresource(host).Build()
		r := &HostReconciler{Client: c, APIReader: c, Namespace: "inventory"}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(host)}

		By("clearing the base request and publishing release on the original host")

		for range 2 {
			_, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
		}

		current := &metal3v1.BareMetalHost{}
		Expect(c.Get(ctx, request.NamespacedName, current)).To(Succeed())
		Expect(current.Annotations).To(Equal(map[string]string{"retained": "yes", rebootAnnotation + "/client": ""}))
		Expect(current.Status.Provisioning.State).To(Equal(metal3v1.StateAvailable))
		Expect(current.Status.Provisioning.Image.URL).To(BeEmpty())
		Expect(current.Spec).To(Equal(host.Spec))
		Expect(current.UID).To(Equal(host.UID))
		Expect(current.Labels).To(Equal(host.Labels))
		Expect(current.Finalizers).To(Equal(host.Finalizers))

		By("leaving the client request intact after release settles")

		before := current.DeepCopy()
		_, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, request.NamespacedName, current)).To(Succeed())
		Expect(current).To(Equal(before))
	})

	It("retries stale status and retains another writer's observations", func(ctx SpecContext) {
		race := true
		c := fake.NewClientBuilder().WithScheme(metal3Scheme()).WithObjects(host).
			WithStatusSubresource(host).WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(
				ctx context.Context, c client.Client, name string, object client.Object, opts ...client.SubResourceUpdateOption,
			) error {
				if race {
					race = false

					current := &metal3v1.BareMetalHost{}
					if err := c.Get(ctx, client.ObjectKeyFromObject(object), current); err != nil {
						return err
					}

					current.Status.ErrorMessage = "independent observation"
					if err := c.Status().Update(ctx, current); err != nil {
						return err
					}
				}

				return c.SubResource(name).Update(ctx, object, opts...)
			},
		}).Build()
		r := &HostReconciler{Client: c, APIReader: c, Namespace: "inventory"}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(host)}
		_, err := r.Reconcile(ctx, request)
		Expect(apierrors.IsConflict(err)).To(BeTrue())
		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		current := &metal3v1.BareMetalHost{}
		Expect(c.Get(ctx, request.NamespacedName, current)).To(Succeed())
		Expect(current.Status.Provisioning.State).To(Equal(metal3v1.StateAvailable))
		Expect(current.Status.ErrorMessage).To(Equal("independent observation"))
	})

	DescribeTable("preserves API errors", func(ctx SpecContext, operation string) {
		failure := errors.New("API unavailable")
		c := fake.NewClientBuilder().WithScheme(metal3Scheme()).WithObjects(host).
			WithStatusSubresource(host).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(
				ctx context.Context, c client.WithWatch, key client.ObjectKey, object client.Object, opts ...client.GetOption,
			) error {
				if operation == "read" {
					return failure
				}

				return c.Get(ctx, key, object, opts...)
			},
			SubResourceUpdate: func(
				context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption,
			) error {
				return failure
			},
		}).Build()
		r := &HostReconciler{Client: c, APIReader: c, Namespace: "inventory"}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(host)})
		Expect(errors.Is(err, failure)).To(BeTrue(), "original failure lost: %v", err)
	},
		Entry("read errors", "read"),
		Entry("status errors", "status"),
	)
	DescribeTable(
		"applies native reboot eligibility and power-off policy",
		func(state metal3v1.ProvisioningState, value string, disabled, powered bool) {
			host := &metal3v1.BareMetalHost{Spec: metal3v1.BareMetalHostSpec{Online: true, DisablePowerOff: disabled}}
			host.Annotations = map[string]string{rebootAnnotation + "/client": value}
			host.Status.PoweredOn = true
			host.Status.Provisioning.State = state
			next := host.DeepCopy()
			powerStatus(host, next)
			Expect(next.Status.PoweredOn).To(Equal(powered))
			Expect(next.Annotations).To(Equal(host.Annotations), "the requesting client owns its suffixed request")
		},
		Entry("unprovisioned default request", metal3v1.StateAvailable, "", false, true),
		Entry("unprovisioned forced request", metal3v1.StateAvailable, `{"force":true}`, false, false),
		Entry("malformed arguments use defaults", metal3v1.StateAvailable, "{", false, true),
		Entry("provisioned soft request", metal3v1.StateProvisioned, `{"mode":"soft"}`, false, false),
		Entry("provisioned hard request", metal3v1.StateProvisioned, `{"mode":"hard"}`, false, false),
		Entry("external provisioning", metal3v1.StateExternallyProvisioned, "", false, false),
		Entry("power-off disabled", metal3v1.StateProvisioned, `{"force":true}`, true, true),
	)
})
