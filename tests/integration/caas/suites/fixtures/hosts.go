// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package fixtures

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"time"

	metal3v1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	bmfv1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/harness"
	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/host"
)

// RegisterHost waits for real provisioning, then submits the omitted host's inventory.
// OSAC supplies approval; controllers own all outcomes.
func RegisterHost(
	ctx context.Context,
	environment *harness.Environment,
	inventory harness.Inventory,
	instance *bmfv1.BareMetalInstance, infra *assistedv1.InfraEnv,
	tenant string, expectedIgnition []byte,
) *assistedv1.Agent {
	GinkgoHelper()

	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	By("waiting for the real allocation to finish provisioning")

	current := &bmfv1.BareMetalInstance{}

	Eventually(func(g Gomega) {
		g.Expect(environment.Client.Get(ctx, client.ObjectKeyFromObject(instance), current)).To(Succeed())
		g.Expect(current.GetUID()).To(Equal(instance.GetUID()))
		g.Expect(current.GetAnnotations()[harness.TenantAnnotation]).To(Equal(tenant))
		phase := current.Status.Phase
		slog.DebugContext(ctx, "waiting for host provisioning before Agent registration",
			"instance", instance.GetName(),
			"instanceUID", instance.GetUID(),
			"phase", phase,
		)
		g.Expect(phase).To(Equal(bmfv1.BareMetalInstancePhaseReady))
	}).WithContext(ctx).Should(Succeed(), "registration requires real provisioning")

	// Follow the allocated host and require the original identity from this slot.
	external := current.Spec.ExternalHostID

	namespace, name, found := strings.Cut(external, "/")
	Expect(found).To(BeTrue())
	Expect(namespace).To(Equal(inventory.Namespace))

	var machine host.Machine

	for _, original := range inventory.Hosts {
		if original.Name == name {
			machine = original.Machine
		}
	}

	Expect(machine.UID).NotTo(BeEmpty(), "allocation must use this worker's original inventory slot")

	By("checking that provisioning selected the smallest inspected root disk")

	provisioned := &metal3v1.BareMetalHost{}
	Expect(environment.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, provisioned)).To(Succeed())
	Expect(provisioned.UID).To(Equal(machine.UID))
	Expect(provisioned.Spec.RootDeviceHints).NotTo(BeNil())
	rootDevice := provisioned.Spec.RootDeviceHints.DeviceName
	Expect(rootDevice).To(Equal("/dev/sda"))
	Expect(provisioned.GetAnnotations()["osac.openshift.io/fallback-root-device"]).To(Equal(rootDevice))

	VerifyHostUserData(ctx, environment, current, expectedIgnition)

	By("booting the original allocated machine and registering its hardware with Assisted")

	var agent *assistedv1.Agent

	Eventually(func(g Gomega) {
		var err error

		agent, err = machine.Boot(ctx, environment.Client, infra, map[string]string{harness.TenantAnnotation: tenant})
		g.Expect(err).NotTo(HaveOccurred())
	}).WithContext(ctx).Should(Succeed(), "native boot prerequisites must precede registration")

	return agent
}

// VerifyHostUserData follows the BMI's allocated BMH and its exact user-data Secret reference.
func VerifyHostUserData(
	ctx context.Context, environment *harness.Environment, instance *bmfv1.BareMetalInstance, expectedIgnition []byte,
) types.NamespacedName {
	GinkgoHelper()
	Expect(expectedIgnition).NotTo(BeEmpty())

	external := instance.Spec.ExternalHostID
	namespace, name, ok := strings.Cut(external, "/")
	Expect(ok).To(BeTrue())
	Expect(namespace).NotTo(BeEmpty())
	Expect(name).NotTo(BeEmpty())

	host := &metal3v1.BareMetalHost{}
	Expect(environment.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, host)).To(Succeed())
	Expect(host.Spec.ConsumerRef).NotTo(BeNil())
	Expect(host.Spec.ConsumerRef.Name).To(Equal(string(instance.UID)))
	Expect(host.Spec.UserData).NotTo(BeNil())
	secretName, secretNamespace := host.Spec.UserData.Name, host.Spec.UserData.Namespace
	Expect(secretName).NotTo(BeEmpty())
	Expect(secretNamespace).To(Equal(namespace))
	secret, err := environment.Kube.CoreV1().Secrets(secretNamespace).Get(ctx, secretName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	Expect(bytes.Equal(secret.Data["userData"], expectedIgnition)).To(BeTrue(), "BMH Secret must preserve discovery bytes")
	slog.DebugContext(
		ctx,
		"verified allocated host discovery delivery",
		"hostNamespace",
		namespace,
		"host",
		name,
		"hostUID",
		host.GetUID(),
		"instanceUID",
		instance.GetUID(),
	)

	return types.NamespacedName{Namespace: secret.Namespace, Name: secret.Name}
}
