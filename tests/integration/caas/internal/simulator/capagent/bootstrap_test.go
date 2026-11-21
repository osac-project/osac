// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package capagent

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("Agent bootstrap authorization", func() {
	It("publishes an empty native token for an unauthenticated source", func(ctx SpecContext) {
		source := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "bootstrap"},
			Data: map[string][]byte{
				"value": []byte(`{"ignition":{"config":{"merge":[{"source":"https://ignition.invalid/worker"}]}}}`),
			},
		}
		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "worker"},
			Spec:       capiv1.MachineSpec{Bootstrap: capiv1.Bootstrap{DataSecretName: &source.Name}},
		}
		c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(source).Build()
		r := &AgentMachineReconciler{Client: c, APIReader: c}
		pool, token, headers, err := r.bootstrap(ctx, machine)
		Expect(err).NotTo(HaveOccurred())
		Expect(pool).To(Equal("worker"))
		Expect(token).NotTo(BeNil())
		Expect(token.Name).To(Equal("agent-bootstrap"))
		Expect(token.Namespace).To(Equal(machine.Namespace))
		Expect(headers).To(BeEmpty())

		var outputs corev1.SecretList

		Expect(c.List(ctx, &outputs, client.InNamespace("cp"))).To(Succeed())
		Expect(outputs.Items).To(HaveLen(2))

		output := &corev1.Secret{}
		Expect(c.Get(ctx, client.ObjectKey{Namespace: token.Namespace, Name: token.Name}, output)).To(Succeed())
		Expect(output.Data).To(HaveKeyWithValue("ignition-token", []byte{}))
	})

	It("shares an unowned token and refreshes it after the bootstrap input is replaced", func(ctx SpecContext) {
		By("preparing one authenticated bootstrap input for two workers")

		source := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "workers-bootstrap", UID: "source-input"},
			Data: map[string][]byte{"value": []byte(
				`{"ignition":{"config":{"merge":[{"source":"https://ignition.invalid/worker",` +
					`"httpHeaders":[{"name":"Authorization","value":"Bearer test-only"}]}]}}}`,
			)},
		}
		c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(source).Build()
		r := &AgentMachineReconciler{Client: c, APIReader: c}

		for _, name := range []string{"worker-0", "worker-1"} {
			machine := &capiv1.Machine{
				ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: name, UID: types.UID("uid-" + name)},
				Spec:       capiv1.MachineSpec{Bootstrap: capiv1.Bootstrap{DataSecretName: &source.Name}},
			}
			_, ref, _, err := r.bootstrap(ctx, machine)
			Expect(err).NotTo(HaveOccurred())
			Expect(ref).NotTo(BeNil())
			Expect(ref.Namespace).To(Equal(source.Namespace))
			Expect(ref.Name).To(Equal("agent-" + source.Name))
		}

		By("keeping the shared output independent of the source Secret lifecycle")

		token := &corev1.Secret{}
		Expect(c.Get(ctx, client.ObjectKey{Namespace: "cp", Name: "agent-workers-bootstrap"}, token)).To(Succeed())
		// Supply the stored output's API-assigned identity.
		token.UID = "shared-token"
		Expect(c.Update(ctx, token)).To(Succeed())
		Expect(token.OwnerReferences).To(BeEmpty())
		Expect(token.Data).To(HaveKeyWithValue("ignition-token", []byte("test-only")))

		By("refreshing the shared credentials after the source is replaced")

		Expect(c.Delete(ctx, source)).To(Succeed())
		source.ResourceVersion, source.UID = "", "replacement-input"
		source.Data["value"] = []byte(strings.ReplaceAll(string(source.Data["value"]), "test-only", "replacement-token"))
		Expect(c.Create(ctx, source)).To(Succeed())
		replacement := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "replacement"},
			Spec:       capiv1.MachineSpec{Bootstrap: capiv1.Bootstrap{DataSecretName: &source.Name}},
		}
		_, ref, _, err := r.bootstrap(ctx, replacement)
		Expect(err).NotTo(HaveOccurred())
		Expect(ref.Name).To(Equal(token.Name))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(token), token)).To(Succeed())
		Expect(token.OwnerReferences).To(BeEmpty())
		Expect(token.Data).To(HaveKeyWithValue("ignition-token", []byte("replacement-token")))

		By("clearing the shared token when Authorization is removed from the source")

		source.Data["value"] = []byte(`{"ignition":{"config":{"merge":[{"source":"https://ignition.invalid/worker"}]}}}`)
		Expect(c.Update(ctx, source)).To(Succeed())

		_, ref, _, err = r.bootstrap(ctx, replacement)
		Expect(err).NotTo(HaveOccurred())
		Expect(ref.Name).To(Equal(token.Name))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(token), token)).To(Succeed())
		Expect(token.Data).To(HaveKeyWithValue("ignition-token", []byte{}))
		Expect(token.UID).To(Equal(types.UID("shared-token")))
		Expect(token.OwnerReferences).To(BeEmpty())
	})

	It("waits for a terminating token before preparing its replacement", func(ctx SpecContext) {
		source := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "bootstrap"},
			Data: map[string][]byte{"value": []byte(
				`{"ignition":{"config":{"merge":[{"source":"https://ignition.invalid/worker",` +
					`"httpHeaders":[{"name":"Authorization","value":"Bearer test-only"}]}]}}}`,
			)},
		}
		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{Namespace: source.Namespace, Name: "worker"},
			Spec:       capiv1.MachineSpec{Bootstrap: capiv1.Bootstrap{DataSecretName: &source.Name}},
		}
		token := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: source.Namespace, Name: "agent-bootstrap", UID: "terminating-token",
				Finalizers: []string{"example.invalid/cleanup"},
			},
			Data: map[string][]byte{"ignition-token": []byte("old")},
		}
		c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(source, token).Build()
		r := &AgentMachineReconciler{Client: c, APIReader: c}

		By("refusing to publish a reference while token deletion is held")

		Expect(c.Delete(ctx, token)).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(token), token)).To(Succeed())
		before := token.DeepCopy()
		_, ref, _, err := r.bootstrap(ctx, machine)
		Expect(err).To(MatchError("bootstrap token Secret is terminating"))
		Expect(ref).To(BeNil())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(token), token)).To(Succeed())
		Expect(token).To(Equal(before))

		By("preparing credentials after the Secret's cleanup completes")

		token.Finalizers = nil
		Expect(c.Update(ctx, token)).To(Succeed())
		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(token), token))).To(BeTrue())
		_, ref, _, err = r.bootstrap(ctx, machine)
		Expect(err).NotTo(HaveOccurred())
		Expect(ref).NotTo(BeNil())
		Expect(ref.Namespace).To(Equal(source.Namespace))
		Expect(ref.Name).To(Equal("agent-bootstrap"))
		Expect(c.Get(ctx, client.ObjectKeyFromObject(token), token)).To(Succeed())
		Expect(token.DeletionTimestamp).To(BeNil())
		Expect(token.OwnerReferences).To(BeEmpty())
		Expect(token.Data).To(HaveKeyWithValue("ignition-token", []byte("test-only")))
	})

	It("refreshes an existing token while preserving its other data and metadata", func(ctx SpecContext) {
		source := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "bootstrap", UID: "source"}}
		owner := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "other", UID: "other"}}
		token := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "cp",
				Name:      "agent-bootstrap",
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(owner, corev1.SchemeGroupVersion.WithKind("Secret")),
				},
				Annotations: map[string]string{"retained": "yes"},
			},
			Data: map[string][]byte{"ignition-token": []byte("old"), "retained": []byte("value")},
		}
		c := fake.NewClientBuilder().WithScheme(capagentScheme()).WithObjects(token).Build()
		r := &AgentMachineReconciler{Client: c, APIReader: c}
		_, err := r.ensureToken(ctx, source, "new")
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(token), token)).To(Succeed())
		Expect(
			token.OwnerReferences,
		).To(ConsistOf(*metav1.NewControllerRef(owner, corev1.SchemeGroupVersion.WithKind("Secret"))))
		Expect(token.Annotations).To(HaveKeyWithValue("retained", "yes"))
		Expect(token.Data).To(Equal(map[string][]byte{"ignition-token": []byte("new"), "retained": []byte("value")}))
		before := token.DeepCopy()
		_, err = r.ensureToken(ctx, source, "new")
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(token), token)).To(Succeed())
		Expect(token).To(Equal(before))
	})
})
