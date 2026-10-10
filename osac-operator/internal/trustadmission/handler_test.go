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

package trustadmission_test

import (
	"context"
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/osac-project/osac/osac-operator/internal/trustadmission"
)

const syncServiceAccount = "system:serviceaccount:osac-csi:osac-fulfillment-trust-sync"

var _ = Describe("Handler", func() {
	var (
		now     time.Time
		record  trustadmission.ExpectedBundle
		store   trustadmission.Store
		handler *trustadmission.Handler
	)

	BeforeEach(func() {
		now = time.Date(2026, time.September, 27, 12, 0, 0, 0, time.UTC)
		record = expectedBundle(certificatePEM(), now.Add(time.Hour))
		store = trustadmission.NewStore()
		Expect(store.Publish(context.Background(), record)).To(Succeed())
		var err error
		handler, err = trustadmission.NewHandler(store, trustadmission.Config{
			TenantNamespace: tenantNamespace,
			Now:             func() time.Time { return now },
		})
		Expect(err).NotTo(HaveOccurred())
	})

	DescribeTable("allows the exact ConfigMap for supported operations",
		func(operation admissionv1.Operation) {
			response := handler.Handle(context.Background(), configMapRequest(operation, configMapFor(record), syncServiceAccount))

			Expect(response.Allowed).To(BeTrue())
		},
		Entry("create", admissionv1.Create),
		Entry("update", admissionv1.Update),
	)

	DescribeTable("denies invalid ConfigMap requests",
		func(username string, operation admissionv1.Operation, mutate func(*corev1.ConfigMap)) {
			candidate := configMapFor(record)
			mutate(candidate)

			response := handler.Handle(context.Background(), configMapRequest(operation, candidate, username))
			Expect(response.Allowed).To(BeFalse())
		},
		Entry("from another identity", "system:serviceaccount:osac-csi:other", admissionv1.Create, func(*corev1.ConfigMap) {}),
		Entry("with binary data", syncServiceAccount, admissionv1.Create, func(candidate *corev1.ConfigMap) {
			candidate.BinaryData = map[string][]byte{"extra": {1}}
		}),
		Entry("with owner references", syncServiceAccount, admissionv1.Create, func(candidate *corev1.ConfigMap) {
			candidate.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "owner", UID: "owner-uid"}}
		}),
		Entry("with finalizers", syncServiceAccount, admissionv1.Create, func(candidate *corev1.ConfigMap) {
			candidate.Finalizers = []string{"example.com/finalizer"}
		}),
		Entry("with an immutable field", syncServiceAccount, admissionv1.Create, func(candidate *corev1.ConfigMap) {
			immutable := true
			candidate.Immutable = &immutable
		}),
		Entry("for an unsupported operation", syncServiceAccount, admissionv1.Delete, func(*corev1.ConfigMap) {}),
	)

	It("denies deletion of the protected trust ConfigMap", func() {
		candidate := configMapFor(record)
		oldObject, err := json.Marshal(candidate)
		Expect(err).NotTo(HaveOccurred())
		request := configMapRequest(admissionv1.Delete, candidate, syncServiceAccount)
		request.Object.Raw = nil
		request.OldObject.Raw = oldObject

		Expect(handler.Handle(context.Background(), request).Allowed).To(BeFalse())
	})

	It("allows the namespace controller to delete the protected trust ConfigMap", func() {
		candidate := configMapFor(record)
		oldObject, err := json.Marshal(candidate)
		Expect(err).NotTo(HaveOccurred())
		request := configMapRequest(admissionv1.Delete, candidate, "system:serviceaccount:kube-system:namespace-controller")
		request.Object.Raw = nil
		request.OldObject.Raw = oldObject

		Expect(handler.Handle(context.Background(), request).Allowed).To(BeTrue())
	})

	It("allows managed labels on the expected ConfigMap", func() {
		candidate := configMapFor(record)
		candidate.Labels = map[string]string{"app.kubernetes.io/managed-by": "Helm"}
		Expect(handler.Handle(context.Background(), configMapRequest(admissionv1.Create, candidate, syncServiceAccount)).Allowed).To(BeTrue())
	})

	It("allows unrelated ConfigMaps from other identities without granting sync broader writes", func() {
		candidate := configMapFor(record)
		candidate.Name = "unrelated-settings"
		Expect(handler.Handle(context.Background(), configMapRequest(admissionv1.Create, candidate,
			"system:serviceaccount:osac-csi:other")).Allowed).To(BeTrue())
		Expect(handler.Handle(context.Background(), configMapRequest(admissionv1.Create, candidate,
			syncServiceAccount)).Allowed).To(BeFalse())
	})

	It("allows only the expected pod-template hash change on a labelled Deployment", func() {
		oldDeployment := deploymentWithTrustClientLabel()
		newDeployment := oldDeployment.DeepCopy()
		newDeployment.Spec.Template.Annotations = map[string]string{trustadmission.BundleHashAnnotation: record.Key.BundleSHA256}

		response := handler.Handle(context.Background(), deploymentRequest(oldDeployment, newDeployment, syncServiceAccount))
		Expect(response.Allowed).To(BeTrue())
	})

	DescribeTable("denies Deployment updates outside the trust boundary",
		func(mutate func(oldDeployment, newDeployment *appsv1.Deployment)) {
			oldDeployment := deploymentWithTrustClientLabel()
			newDeployment := oldDeployment.DeepCopy()
			newDeployment.Spec.Template.Annotations = map[string]string{trustadmission.BundleHashAnnotation: record.Key.BundleSHA256}
			mutate(oldDeployment, newDeployment)

			response := handler.Handle(context.Background(), deploymentRequest(oldDeployment, newDeployment, syncServiceAccount))
			Expect(response.Allowed).To(BeFalse())
		},
		Entry("when the trust label is added by the request", func(oldDeployment, newDeployment *appsv1.Deployment) {
			delete(oldDeployment.Labels, trustadmission.TrustClientLabel)
		}),
		Entry("when another pod-template field changes", func(_ *appsv1.Deployment, newDeployment *appsv1.Deployment) {
			newDeployment.Spec.Template.Spec.Containers[0].Image = "different-image"
		}),
		Entry("when deployment metadata changes", func(_ *appsv1.Deployment, newDeployment *appsv1.Deployment) {
			newDeployment.Labels["extra"] = "value"
		}),
	)

	It("allows unrelated Deployment updates by other identities", func() {
		oldDeployment := deploymentWithTrustClientLabel()
		delete(oldDeployment.Labels, trustadmission.TrustClientLabel)
		newDeployment := oldDeployment.DeepCopy()
		newDeployment.Spec.Template.Spec.Containers[0].Image = "example.invalid/updated"
		response := handler.Handle(context.Background(), deploymentRequest(oldDeployment, newDeployment,
			"system:serviceaccount:osac-csi:other"))
		Expect(response.Allowed).To(BeTrue())
	})

	It("allows installer changes to a trust client without changing its trust boundary", func() {
		oldDeployment := deploymentWithTrustClientLabel()
		newDeployment := oldDeployment.DeepCopy()
		newDeployment.Spec.Template.Spec.Containers[0].Image = "example.invalid/updated"
		response := handler.Handle(context.Background(), deploymentRequest(oldDeployment, newDeployment,
			"system:serviceaccount:osac-csi:installer"))
		Expect(response.Allowed).To(BeTrue())
	})

	DescribeTable("denies trust boundary changes by other identities",
		func(mutate func(oldDeployment, newDeployment *appsv1.Deployment)) {
			oldDeployment := deploymentWithTrustClientLabel()
			newDeployment := oldDeployment.DeepCopy()
			mutate(oldDeployment, newDeployment)
			response := handler.Handle(context.Background(), deploymentRequest(oldDeployment, newDeployment,
				"system:serviceaccount:osac-csi:other"))
			Expect(response.Allowed).To(BeFalse())
		},
		Entry("removing the trust-client label", func(_, newDeployment *appsv1.Deployment) {
			delete(newDeployment.Labels, trustadmission.TrustClientLabel)
		}),
		Entry("altering the bundle hash", func(_, newDeployment *appsv1.Deployment) {
			newDeployment.Spec.Template.Annotations = map[string]string{trustadmission.BundleHashAnnotation: "unapproved"}
		}),
	)

	It("does not disclose request payloads in a denial", func() {
		const rawKubeconfig = "apiVersion: v1\nclusters:\n- name: confidential"
		candidate := configMapFor(record)
		candidate.Data[trustadmission.BundleDataKey] = rawKubeconfig

		response := handler.Handle(context.Background(), configMapRequest(admissionv1.Create, candidate, syncServiceAccount))
		Expect(response.Allowed).To(BeFalse())
		Expect(response.Result.Message).NotTo(ContainSubstring(rawKubeconfig))
		Expect(response.Result.Message).NotTo(ContainSubstring("confidential"))
	})

	It("ignores requests outside the configured tenant namespace", func() {
		candidate := configMapFor(record)
		candidate.Namespace = "another-tenant"

		request := configMapRequest(admissionv1.Create, candidate, "system:serviceaccount:another-tenant:unrelated")
		request.Namespace = candidate.Namespace
		response := handler.Handle(context.Background(), request)
		Expect(response.Allowed).To(BeTrue())
	})
})

func configMapRequest(operation admissionv1.Operation, configMap *corev1.ConfigMap, username string) admission.Request {
	request := admissionRequest(operation, "", "v1", "configmaps", "ConfigMap", configMap, nil, username)
	request.Namespace = configMap.Namespace
	request.Name = configMap.Name
	return request
}

func deploymentRequest(oldDeployment, newDeployment *appsv1.Deployment, username string) admission.Request {
	request := admissionRequest(admissionv1.Update, "apps", "v1", "deployments", "Deployment", newDeployment, oldDeployment, username)
	request.Namespace = newDeployment.Namespace
	request.Name = newDeployment.Name
	return request
}

func admissionRequest(operation admissionv1.Operation, group, version, resource, kind string, object, oldObject any, username string) admission.Request {
	objectRaw, err := json.Marshal(object)
	Expect(err).NotTo(HaveOccurred())

	var oldObjectRaw []byte
	if oldObject != nil {
		oldObjectRaw, err = json.Marshal(oldObject)
		Expect(err).NotTo(HaveOccurred())
	}

	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		UID:       types.UID("request-id"),
		Kind:      metav1.GroupVersionKind{Group: group, Version: version, Kind: kind},
		Resource:  metav1.GroupVersionResource{Group: group, Version: version, Resource: resource},
		Operation: operation,
		UserInfo:  authenticationv1.UserInfo{Username: username},
		Object:    runtime.RawExtension{Raw: objectRaw},
		OldObject: runtime.RawExtension{Raw: oldObjectRaw},
	}}
}

func deploymentWithTrustClientLabel() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "csi-driver-controller",
			Namespace: tenantNamespace,
			Labels:    map[string]string{trustadmission.TrustClientLabel: "true"},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "csi-driver"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "csi-driver"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "driver", Image: "example.invalid/driver"}}},
			},
		},
	}
}
