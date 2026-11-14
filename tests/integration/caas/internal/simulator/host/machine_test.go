// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package host

import (
	"context"
	"encoding/json"
	"errors"

	metal3v1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

type bootFixture struct {
	machine  Machine
	host     *metal3v1.BareMetalHost
	hardware *metal3v1.HardwareData
	userData *corev1.Secret
	infra    *assistedv1.InfraEnv
}

// hostScheme registers original inventory and registration records for fake clients.
func hostScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	Expect(metal3v1.AddToScheme(scheme)).To(Succeed())
	Expect(assistedv1.AddToScheme(scheme)).To(Succeed())
	Expect(corev1.AddToScheme(scheme)).To(Succeed())

	return scheme
}

// bootInputs supplies independent inventory, provisioning and discovery inputs.
func bootInputs() bootFixture {
	m := Machine{Namespace: "inventory", Name: "worker", UID: "physical-host", HardwareUID: "hardware"}
	bmh := &metal3v1.BareMetalHost{
		ObjectMeta: metav1.ObjectMeta{Namespace: m.Namespace, Name: m.Name, UID: m.UID},
		Spec: metal3v1.BareMetalHostSpec{
			Online: true, UserData: &corev1.SecretReference{Name: "discovery-data", Namespace: "inventory"},
		},
		Status: metal3v1.BareMetalHostStatus{
			PoweredOn: true, Provisioning: metal3v1.ProvisionStatus{State: metal3v1.StateProvisioned},
		},
	}
	hardware := &metal3v1.HardwareData{
		ObjectMeta: metav1.ObjectMeta{Namespace: m.Namespace, Name: m.Name, UID: m.HardwareUID},
		Spec: metal3v1.HardwareDataSpec{HardwareDetails: &metal3v1.HardwareDetails{
			NIC: []metal3v1.NIC{{Name: "eth0", MAC: "02:00:00:00:00:01"}},
		}},
	}
	data := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: m.Namespace, Name: "discovery-data"},
		Data:       map[string][]byte{"userData": []byte(`{"ignition":{"version":"3.2.0"}}`)},
	}
	infra := &assistedv1.InfraEnv{ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "discovery", UID: "infra"}}
	infra.Status.BootArtifacts.DiscoveryIgnitionURL = "http://ignition.release.svc/p1/ignition.json"

	return bootFixture{machine: m, host: bmh, hardware: hardware, userData: data, infra: infra}
}

var _ = Describe("Physical host boot", func() {
	var inputs bootFixture

	BeforeEach(func() { inputs = bootInputs() })

	DescribeTable(
		"rejects unsafe inputs before registering an Agent",
		func(ctx SpecContext, change func(*bootFixture)) {
			original := inputs.infra.DeepCopy()
			change(&inputs)
			c := fake.NewClientBuilder().WithScheme(hostScheme()).
				WithObjects(inputs.host, inputs.hardware, inputs.userData, inputs.infra).Build()

			By("rejecting boot without disclosing the supplied user-data")

			_, err := inputs.machine.Boot(ctx, c, original, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring("private-invalid-document"))
			err = c.Get(ctx, client.ObjectKey{Namespace: inputs.infra.Namespace, Name: "host-worker"}, &assistedv1.Agent{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "unsafe inputs must not register an Agent")
		},
		Entry(
			"provisioning has not finished",
			func(input *bootFixture) { input.host.Status.Provisioning.State = metal3v1.StateProvisioning },
		),
		Entry("the host is offline", func(input *bootFixture) { input.host.Spec.Online = false }),
		Entry("power has not been observed", func(input *bootFixture) { input.host.Status.PoweredOn = false }),
		Entry("the host was replaced", func(input *bootFixture) { input.host.UID = "replacement" }),
		Entry("the hardware record was replaced", func(input *bootFixture) { input.hardware.UID = "replacement" }),
		Entry("the InfraEnv was replaced", func(input *bootFixture) { input.infra.UID = "replacement" }),
		Entry(
			"discovery artifacts are missing",
			func(input *bootFixture) { input.infra.Status.BootArtifacts.DiscoveryIgnitionURL = "" },
		),
		Entry("user-data is in a foreign namespace", func(input *bootFixture) {
			input.host.Spec.UserData.Namespace = "foreign"
			input.userData.Namespace = "foreign"
		}),
		Entry(
			"user-data is invalid",
			func(input *bootFixture) { input.userData.Data["userData"] = []byte("private-invalid-document") },
		),
	)

	DescribeTable("boots with user-data in the host namespace", func(ctx SpecContext, namespace string) {
		inputs.host.Spec.UserData.Namespace = namespace
		c := fake.NewClientBuilder().WithScheme(hostScheme()).
			WithObjects(inputs.host, inputs.hardware, inputs.userData, inputs.infra).
			WithStatusSubresource(&assistedv1.Agent{}).Build()
		_, err := inputs.machine.Boot(ctx, c, inputs.infra, nil)
		Expect(err).NotTo(HaveOccurred())
	},
		Entry("explicit namespace", "inventory"),
		Entry("omitted namespace", ""),
	)

	It("recovers interrupted registration while preserving approval and consumer inputs", func(ctx SpecContext) {
		machine, infra := inputs.machine, inputs.infra
		interrupted := true
		failure := errors.New("inventory submission interrupted")
		c := fake.NewClientBuilder().
			WithScheme(hostScheme()).
			WithObjects(inputs.host, inputs.hardware, inputs.userData, infra).
			WithStatusSubresource(&assistedv1.Agent{}).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, object client.Object, opts ...client.CreateOption) error {
					if agent, ok := object.(*assistedv1.Agent); ok {
						agent.UID = "registered-host"
					}

					return c.Create(ctx, object, opts...)
				},
				SubResourcePatch: func(
					ctx context.Context, c client.Client, subresource string, object client.Object,
					patch client.Patch, opts ...client.SubResourcePatchOption,
				) error {
					current := &assistedv1.Agent{}
					Expect(c.Get(ctx, client.ObjectKeyFromObject(object), current)).To(Succeed())
					data, err := patch.Data(object)
					Expect(err).NotTo(HaveOccurred())

					var body struct {
						Metadata metav1.ObjectMeta `json:"metadata"`
					}

					Expect(json.Unmarshal(data, &body)).To(Succeed())
					Expect(body.Metadata.UID).To(Equal(current.UID), "inventory writes must retain observed identity")
					Expect(
						body.Metadata.ResourceVersion,
					).To(Equal(current.ResourceVersion), "inventory writes must retain observed version")

					if interrupted {
						interrupted = false
						// Approval changes independently while the inventory write fails.
						Expect(current.Spec.Approved).To(BeFalse(), "hosts must not approve themselves")
						current.Spec.Approved = true
						Expect(c.Update(ctx, current)).To(Succeed())

						return failure
					}

					return c.SubResource(subresource).Patch(ctx, object, patch, opts...)
				},
			}).
			Build()

		By("interrupting inventory submission after an independent approver acts")

		_, err := machine.Boot(ctx, c, infra, map[string]string{"opaque": "retained"})
		Expect(errors.Is(err, failure)).To(BeTrue(), "original failure lost: %v", err)

		By("recovering the original registration without publishing controller outcomes")

		agent, err := machine.Boot(ctx, c, infra, map[string]string{"opaque": "retained"})
		Expect(err).NotTo(HaveOccurred())

		owner := kube.OwnerReference(agent, "InfraEnv")
		Expect(owner).NotTo(BeNil())
		Expect(owner.UID).To(Equal(infra.UID))
		Expect(owner.Controller).To(BeNil())
		Expect(agent.Spec.Approved).To(BeTrue())
		Expect(agent.Spec.ClusterDeploymentName).To(BeNil())
		Expect(agent.Status.Conditions).To(BeNil())
		Expect(agent.Status.Inventory.Interfaces).To(HaveLen(1))
		Expect(agent.Status.Inventory.Interfaces[0].MacAddress).To(Equal("02:00:00:00:00:01"))

		By("leaving consumer inputs and registration unchanged on repeated boot")

		agent.Spec.Approved = true
		agent.Spec.ClusterDeploymentName = &assistedv1.ClusterReference{Name: "deployment"}
		Expect(c.Update(ctx, agent)).To(Succeed())
		before := agent.DeepCopy()
		recovered, err := machine.Boot(ctx, c, infra, map[string]string{"opaque": "retained"})
		Expect(err).NotTo(HaveOccurred())
		Expect(recovered).To(Equal(before))

		By("rejecting a registration from another InfraEnv incarnation without changing it")

		agent.OwnerReferences[0].UID = "previous-infra"
		Expect(c.Update(ctx, agent)).To(Succeed())
		before = agent.DeepCopy()
		_, err = machine.Boot(ctx, c, infra, map[string]string{"opaque": "retained"})
		Expect(err).To(HaveOccurred())

		after := &assistedv1.Agent{}
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), after)).To(Succeed())
		Expect(after).To(Equal(before))
	})
})

var _ = Describe("Physical host release", func() {
	var inputs bootFixture

	BeforeEach(func() {
		inputs = bootInputs()
		inputs.host.Spec = metal3v1.BareMetalHostSpec{}
		inputs.host.Status = metal3v1.BareMetalHostStatus{
			OperationalStatus: metal3v1.OperationalStatusOK,
			Provisioning:      metal3v1.ProvisionStatus{State: metal3v1.StateAvailable, ID: "native-id"},
		}
	})

	DescribeTable(
		"accepts BMO's empty image with observed power matching the request",
		func(ctx SpecContext, online bool) {
			inputs.host.Spec.Online = online
			inputs.host.Status.PoweredOn = online
			c := fake.NewClientBuilder().WithScheme(hostScheme()).WithObjects(inputs.host, inputs.hardware).Build()
			Expect(inputs.machine.CheckReleased(ctx, c)).To(Succeed())
		},
		Entry("offline inventory", false),
		Entry("online inventory", true),
	)

	DescribeTable(
		"rejects unreleased or replaced inventory",
		func(ctx SpecContext, change func(*bootFixture)) {
			change(&inputs)
			c := fake.NewClientBuilder().WithScheme(hostScheme()).WithObjects(inputs.host, inputs.hardware).Build()
			Expect(inputs.machine.CheckReleased(ctx, c)).NotTo(Succeed())
		},
		Entry("a replacement host", func(input *bootFixture) { input.host.UID = "replacement" }),
		Entry("a replacement hardware record", func(input *bootFixture) { input.hardware.UID = "replacement" }),
		Entry("a deleting host", func(input *bootFixture) {
			input.host.Finalizers = []string{"native.example/cleanup"}
			input.host.DeletionTimestamp = new(metav1.Now())
		}),
		Entry("deleting hardware", func(input *bootFixture) {
			input.hardware.Finalizers = []string{"native.example/cleanup"}
			input.hardware.DeletionTimestamp = new(metav1.Now())
		}),
		Entry("provisioning is still active", func(input *bootFixture) {
			input.host.Status.Provisioning.State = metal3v1.StateProvisioned
		}),
		Entry("power has not caught up", func(input *bootFixture) { input.host.Status.PoweredOn = true }),
		Entry(
			"an unhealthy host",
			func(input *bootFixture) { input.host.Status.OperationalStatus = metal3v1.OperationalStatusError },
		),
		Entry(
			"a consumer reference",
			func(input *bootFixture) { input.host.Spec.ConsumerRef = &corev1.ObjectReference{Name: "consumer"} },
		),
		Entry(
			"a requested image",
			func(input *bootFixture) { input.host.Spec.Image = &metal3v1.Image{URL: "https://image.invalid"} },
		),
		Entry("user-data", func(input *bootFixture) { input.host.Spec.UserData = &corev1.SecretReference{Name: "input"} }),
		Entry("meta-data", func(input *bootFixture) { input.host.Spec.MetaData = &corev1.SecretReference{Name: "input"} }),
		Entry(
			"network-data",
			func(input *bootFixture) { input.host.Spec.NetworkData = &corev1.SecretReference{Name: "input"} },
		),
		Entry(
			"a status image URL",
			func(input *bootFixture) { input.host.Status.Provisioning.Image.URL = "https://image.invalid" },
		),
		Entry(
			"a residual checksum with an empty URL",
			func(input *bootFixture) { input.host.Status.Provisioning.Image.Checksum = "stale" },
		),
		Entry(
			"a residual checksum type",
			func(input *bootFixture) { input.host.Status.Provisioning.Image.ChecksumType = metal3v1.SHA256 },
		),
		Entry("a residual format", func(input *bootFixture) { input.host.Status.Provisioning.Image.DiskFormat = new("raw") }),
		Entry(
			"a residual credential reference",
			func(input *bootFixture) { input.host.Status.Provisioning.Image.OCIAuthSecretName = new("input") },
		),
		Entry(
			"a reboot request",
			func(input *bootFixture) { input.host.Annotations = map[string]string{"reboot.metal3.io": ""} },
		),
		Entry("a paused host", func(input *bootFixture) {
			input.host.Annotations = map[string]string{metal3v1.PausedAnnotation: ""}
		}),
		Entry("a client-suffixed reboot request", func(input *bootFixture) {
			input.host.Annotations = map[string]string{metal3v1.RebootAnnotationPrefix + "/client": ""}
		}),
	)
})
