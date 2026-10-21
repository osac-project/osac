/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package baremetalworker

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const sshFlowKeyMaterial = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG8K1ZuSC7tmzxD5LJJXwkCfStVEjzXWYCFhJaLBxWAn flow@example.com"

// stubWorkerSecretsClient resolves any requested ID to fixed material or an error.
type stubWorkerSecretsClient struct {
	key      string
	err      error
	requests []string
}

func (s *stubWorkerSecretsClient) Get(_ context.Context, in *privatev1.SecretsGetRequest, _ ...grpc.CallOption) (*privatev1.SecretsGetResponse, error) {
	s.requests = append(s.requests, in.GetId())
	if s.err != nil {
		return nil, status.Error(codes.NotFound, s.err.Error())
	}
	return privatev1.SecretsGetResponse_builder{
		Object: privatev1.Secret_builder{
			Id:       in.GetId(),
			Metadata: privatev1.Metadata_builder{Name: "flow-key", Tenant: "tenant"}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data:     map[string][]byte{"public_key": []byte(s.key)},
		}.Build(),
	}.Build(), nil
}

func sshFlowHarness(clusterSSHKey *privatev1.SecretReference, orderSSHKey *v1alpha1.FulfillmentSecretReference) (*Reconciler, *nodeSetClient, *v1alpha1.ClusterOrder, *privatev1.Cluster) {
	GinkgoHelper()
	scheme := runtime.NewScheme()
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
		Name:      "ssh-flow",
		Namespace: "test",
		Labels:    map[string]string{clusterOrderIDLabel: "ssh-flow-cluster"},
		Annotations: map[string]string{
			"osac.openshift.io/tenant": "tenant",
		},
	}, Spec: v1alpha1.ClusterOrderSpec{
		NodeRequests: []v1alpha1.NodeRequest{nodeRequest("standard", 1)},
		SSHKey:       orderSSHKey,
	}}
	kube := clientfake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(co).WithObjects(co).Build()
	fc := &nodeSetClient{kube: kube, order: client.ObjectKeyFromObject(co)}
	cluster := privatev1.Cluster_builder{
		Id:       "ssh-flow-cluster",
		Metadata: privatev1.Metadata_builder{Tenant: "tenant", Project: "proj"}.Build(),
		Spec:     privatev1.ClusterSpec_builder{SshKey: clusterSSHKey}.Build(),
	}.Build()
	fc.cluster = cluster
	r := &Reconciler{Client: kube, apiReader: kube, scheme: scheme, fulfillment: fc, recorder: events.NewFakeRecorder(100)}
	return r, fc, co, cluster
}

var _ = Describe("Cluster SSH key reference flow", func() {
	Describe("resolveClusterSSHAuthorizedKey", func() {
		It("resolves the registered Secret material for the order tenant", func() {
			r, _, co, cluster := sshFlowHarness(
				privatev1.SecretReference_builder{Id: "key-id"}.Build(),
				&v1alpha1.FulfillmentSecretReference{ID: "key-id"})
			secrets := &stubWorkerSecretsClient{key: sshFlowKeyMaterial}
			r.SetSecretsClient(secrets)

			key, err := r.resolveClusterSSHAuthorizedKey(context.Background(), co, cluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(key).To(Equal(sshFlowKeyMaterial))
			Expect(secrets.requests).To(ConsistOf("key-id"))
		})

		It("returns an empty key when the Cluster has no reference", func() {
			r, _, co, cluster := sshFlowHarness(nil, nil)
			r.SetSecretsClient(&stubWorkerSecretsClient{})

			key, err := r.resolveClusterSSHAuthorizedKey(context.Background(), co, cluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(key).To(BeEmpty())
		})

		It("fails when the Secret cannot be resolved", func() {
			r, _, co, cluster := sshFlowHarness(
				privatev1.SecretReference_builder{Id: "missing-key"}.Build(),
				&v1alpha1.FulfillmentSecretReference{ID: "missing-key"})
			r.SetSecretsClient(&stubWorkerSecretsClient{err: errors.New("ssh key secret 'missing-key' not found")})

			_, err := r.resolveClusterSSHAuthorizedKey(context.Background(), co, cluster)
			Expect(err).To(MatchError(ContainSubstring("missing-key")))
			Expect(err.Error()).NotTo(ContainSubstring(sshFlowKeyMaterial))
		})

		It("fails when the resolved Secret has no public key", func() {
			r, _, co, cluster := sshFlowHarness(
				privatev1.SecretReference_builder{Id: "empty-key"}.Build(),
				&v1alpha1.FulfillmentSecretReference{ID: "empty-key"})
			r.SetSecretsClient(&stubWorkerSecretsClient{})

			_, err := r.resolveClusterSSHAuthorizedKey(context.Background(), co, cluster)
			Expect(err).To(MatchError(ContainSubstring("empty-key")))
		})

		It("fails when no Secrets client is configured", func() {
			r, _, co, cluster := sshFlowHarness(
				privatev1.SecretReference_builder{Id: "key-id"}.Build(),
				&v1alpha1.FulfillmentSecretReference{ID: "key-id"})

			_, err := r.resolveClusterSSHAuthorizedKey(context.Background(), co, cluster)
			Expect(err).To(MatchError(ContainSubstring("not configured")))
		})
	})

	Describe("buildInfraEnv", func() {
		It("materializes the resolved key into sshAuthorizedKey", func() {
			r, _, co, _ := sshFlowHarness(nil, nil)

			infraEnv, err := r.buildInfraEnv(co, co.Name+infraEnvNameSuffix, sshFlowKeyMaterial)
			Expect(err).NotTo(HaveOccurred())
			key, found, err := unstructuredString(infraEnv.Object, "spec", "sshAuthorizedKey")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(key).To(Equal(sshFlowKeyMaterial))
		})

		It("omits sshAuthorizedKey when no key was resolved", func() {
			r, _, co, _ := sshFlowHarness(nil, nil)

			infraEnv, err := r.buildInfraEnv(co, co.Name+infraEnvNameSuffix, "")
			Expect(err).NotTo(HaveOccurred())
			_, found, err := unstructuredString(infraEnv.Object, "spec", "sshAuthorizedKey")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeFalse())
		})
	})

	Describe("buildBMICreateRequest", func() {
		It("carries the canonical reference with its project and no inline key", func() {
			r, _, co, cluster := sshFlowHarness(
				privatev1.SecretReference_builder{Id: "key-id", Name: "key-name", Project: "proj"}.Build(),
				&v1alpha1.FulfillmentSecretReference{ID: "key-id"})

			request, err := r.buildBMICreateRequest(co, "tenant", cluster,
				co.Spec.NodeRequests[0], "worker-slot-1", nil, "{}")
			Expect(err).NotTo(HaveOccurred())
			Expect(request.GetSpec().GetSshKey()).NotTo(BeNil())
			Expect(request.GetSpec().GetSshKey().GetId()).To(Equal("key-id"))
			Expect(request.GetSpec().GetSshKey().GetName()).To(Equal("key-name"))
			Expect(request.GetSpec().GetSshKey().GetProject()).To(Equal("proj"))
			Expect(request.GetSpec().GetSshKey()).NotTo(BeIdenticalTo(cluster.GetSpec().GetSshKey()),
				"the reference must be cloned, not aliased")
		})

		It("omits the reference when the Cluster has none", func() {
			r, _, co, cluster := sshFlowHarness(nil, nil)

			request, err := r.buildBMICreateRequest(co, "tenant", cluster,
				co.Spec.NodeRequests[0], "worker-slot-1", nil, "{}")
			Expect(err).NotTo(HaveOccurred())
			Expect(request.GetSpec().GetSshKey()).To(BeNil())
		})

		It("uses the Cluster reference without depending on the order projection", func() {
			r, _, co, cluster := sshFlowHarness(
				privatev1.SecretReference_builder{Id: "rotated-key"}.Build(),
				&v1alpha1.FulfillmentSecretReference{ID: "stale-key"})

			request, err := r.buildBMICreateRequest(co, "tenant", cluster,
				co.Spec.NodeRequests[0], "worker-slot-1", nil, "{}")
			Expect(err).NotTo(HaveOccurred())
			Expect(request.GetSpec().GetSshKey().GetId()).To(Equal("rotated-key"))
		})
	})

	Describe("createMissingInfraEnv", func() {
		It("holds InfraEnv creation with a recoverable condition when the Secret is missing", func() {
			r, _, co, cluster := sshFlowHarness(
				privatev1.SecretReference_builder{Id: "missing-key"}.Build(),
				&v1alpha1.FulfillmentSecretReference{ID: "missing-key"})
			r.SetSecretsClient(&stubWorkerSecretsClient{err: errors.New("ssh key secret 'missing-key' not found")})
			// The pull secret must resolve so the SSH key is the only blocker.
			co.Spec.PullSecret = `{"auths":{}}`

			evidence, result, _, err := r.createMissingInfraEnv(context.Background(), co, cluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(evidence.err).To(MatchError(ContainSubstring("missing-key")))
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))

			// The condition is persisted on the stored order; read it back.
			stored := &v1alpha1.ClusterOrder{}
			Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), stored)).To(Succeed())
			cond := conditionOfType(stored, v1alpha1.ConditionInfraEnvReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReasonSSHKeyResolutionPending))
			Expect(cond.Message).To(ContainSubstring("missing-key"))
			Expect(cond.Message).NotTo(ContainSubstring(sshFlowKeyMaterial))
		})

		It("creates the InfraEnv with the resolved key when the Secret resolves", func() {
			r, _, co, cluster := sshFlowHarness(
				privatev1.SecretReference_builder{Id: "key-id"}.Build(),
				&v1alpha1.FulfillmentSecretReference{ID: "key-id"})
			r.SetSecretsClient(&stubWorkerSecretsClient{key: sshFlowKeyMaterial})
			co.Spec.PullSecret = `{"auths":{}}`

			_, result, created, err := r.createMissingInfraEnv(context.Background(), co, cluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(created).To(BeTrue())
			Expect(result.RequeueAfter).To(Equal(infraEnvRequeueInterval))
		})
	})

})

func unstructuredString(obj map[string]interface{}, fields ...string) (string, bool, error) {
	current := obj
	for i, field := range fields {
		if i == len(fields)-1 {
			value, ok := current[field]
			if !ok {
				return "", false, nil
			}
			str, ok := value.(string)
			if !ok {
				return "", false, errors.New("field is not a string")
			}
			return str, true, nil
		}
		next, ok := current[field].(map[string]interface{})
		if !ok {
			return "", false, nil
		}
		current = next
	}
	return "", false, nil
}

func conditionOfType(co *v1alpha1.ClusterOrder, conditionType string) *metav1.Condition {
	for i := range co.Status.Conditions {
		if co.Status.Conditions[i].Type == conditionType {
			return &co.Status.Conditions[i]
		}
	}
	return nil
}

var _ = Describe("SSH-key client compatibility", func() {
	It("satisfies the worker SecretsClient seam", func() {
		var _ SecretsClient = &stubWorkerSecretsClient{}
	})
})
