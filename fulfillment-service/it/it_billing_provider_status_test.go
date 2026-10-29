/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingstub"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// The dev deployment configures no billing provider, so these specs exercise disabled mode end to end. They need no
// provider, hence no provider call can happen.
var _ = Describe("Billing provider status", Label("billing"), func() {
	It("is NOT_CONFIGURED over public gRPC when no provider is configured", func(ctx context.Context) {
		client := publicv1.NewBillingProviderStatusesClient(tool.ExternalView().AdminConn())
		response, err := client.Get(ctx, publicv1.BillingProviderStatusesGetRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetState()).To(Equal(
			publicv1.BillingProviderState_BILLING_PROVIDER_STATE_NOT_CONFIGURED))
		Expect(response.GetObject().GetProviderKey()).To(BeEmpty())
	})

	It("is shown by the CLI as NOT_CONFIGURED", func(ctx context.Context) {
		homeDir, err := tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(homeDir)).To(Succeed()) })
		_, _, exitCode := tool.LoginCLI(ctx, homeDir, adminUsername, adminsPassword)
		Expect(exitCode).To(Equal(0), "login should succeed")

		stdout, _, exitCode := tool.RunCLI(ctx, homeDir, "get", "billingproviderstatus", "-o", "json")
		Expect(exitCode).To(Equal(0))
		Expect(stdout).To(MatchJSON(`{
			"state": "BILLING_PROVIDER_STATE_NOT_CONFIGURED",
			"usage_delivery_state": "BILLING_USAGE_DELIVERY_STATE_UNKNOWN"
		}`))
	})
})

// These specs point the deployed grpc-server at a deterministic stub M360 server through the real M360 settings
// (OSAC_BILLING_PROVIDER=m360), so they exercise the real adapter. The stub runs in this test process; the cluster
// must reach it, which depends on the environment, so the specs are skipped unless both variables are set:
//
//	OSAC_IT_BILLING_STUB_LISTEN  address to bind, such as 0.0.0.0:18080
//	OSAC_IT_BILLING_STUB_URL     URL of that listener as seen from the cluster, such as http://172.18.0.1:18080
//
// They restart the shared grpc-server, so they must not overlap other specs.
var _ = Describe("Billing provider status with a stub M360", Label("billing"), Serial, func() {
	const (
		namespace  = "osac"
		deployment = "fulfillment-grpc-server"
		container  = "grpc-server"
		secretName = "billing-stub-credential"
	)

	var stub *billingstub.Stub

	// rollout waits until the deployment's current generation is fully rolled out.
	rollout := func(ctx context.Context) {
		GinkgoHelper()
		Eventually(ctx, func(g Gomega) {
			current := &appsv1.Deployment{}
			g.Expect(tool.KubeClient().Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: deployment},
				current)).To(Succeed())
			g.Expect(current.Status.ObservedGeneration).To(BeNumerically(">=", current.Generation))
			g.Expect(current.Status.UpdatedReplicas).To(Equal(*current.Spec.Replicas))
			g.Expect(current.Status.Replicas).To(Equal(*current.Spec.Replicas))
			g.Expect(current.Status.AvailableReplicas).To(Equal(*current.Spec.Replicas))
		}, 3*time.Minute, time.Second).Should(Succeed())
	}

	statusState := func(ctx context.Context) (publicv1.BillingProviderState, error) {
		client := publicv1.NewBillingProviderStatusesClient(tool.ExternalView().AdminConn())
		response, err := client.Get(ctx, publicv1.BillingProviderStatusesGetRequest_builder{}.Build())
		if err != nil {
			return publicv1.BillingProviderState_BILLING_PROVIDER_STATE_UNSPECIFIED, err
		}
		return response.GetObject().GetState(), nil
	}

	BeforeEach(func(ctx context.Context) {
		listen, stubURL := os.Getenv("OSAC_IT_BILLING_STUB_LISTEN"), os.Getenv("OSAC_IT_BILLING_STUB_URL")
		if listen == "" || stubURL == "" {
			Skip("OSAC_IT_BILLING_STUB_LISTEN and OSAC_IT_BILLING_STUB_URL are not set")
		}
		kube := tool.KubeClient()
		stub = billingstub.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "a1", Name: "stub account", State: billing.AccountActive, RawStatus: "Active"}}})
		Expect(stub.Start(listen)).To(Succeed())
		DeferCleanup(stub.Close)

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: secretName},
			StringData: map[string]string{"token": "stub-token"},
		}
		Expect(kube.Create(ctx, secret)).To(Succeed())
		DeferCleanup(func(ctx context.Context) { Expect(crclient.IgnoreNotFound(kube.Delete(ctx, secret))).To(Succeed()) })

		key := crclient.ObjectKey{Namespace: namespace, Name: deployment}
		current := &appsv1.Deployment{}
		Expect(kube.Get(ctx, key, current)).To(Succeed())
		original := current.Spec.Template.Spec.DeepCopy()
		// Restore the original pod spec so later specs see a deployment without a billing provider.
		DeferCleanup(func(ctx context.Context) {
			latest := &appsv1.Deployment{}
			Expect(kube.Get(ctx, key, latest)).To(Succeed())
			latest.Spec.Template.Spec = *original
			Expect(kube.Update(ctx, latest)).To(Succeed())
			rollout(ctx)
		})

		podSpec := &current.Spec.Template.Spec
		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{Name: "billing-credential",
			VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: secretName}}})
		for i := range podSpec.Containers {
			c := &podSpec.Containers[i]
			if c.Name != container {
				continue
			}
			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: "billing-credential",
				MountPath: "/run/billing", ReadOnly: true})
			c.Env = append(c.Env,
				corev1.EnvVar{Name: "OSAC_BILLING_PROVIDER", Value: "m360"},
				corev1.EnvVar{Name: "OSAC_BILLING_BASE_URL", Value: stubURL},
				corev1.EnvVar{Name: "OSAC_BILLING_API_VERSION", Value: "v2"},
				corev1.EnvVar{Name: "OSAC_BILLING_CREDENTIAL_FILE", Value: "/run/billing/token"},
			)
		}
		Expect(kube.Update(ctx, current)).To(Succeed())
		rollout(ctx)
	})

	It("is AVAILABLE when the stub provider answers, using reads only", func(ctx context.Context) {
		Eventually(ctx, func(g Gomega) {
			state, err := statusState(ctx)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(state).To(Equal(publicv1.BillingProviderState_BILLING_PROVIDER_STATE_AVAILABLE))
		}, time.Minute, time.Second).Should(Succeed())
		Expect(stub.Succeeded("HealthCheck")).To(BeNumerically(">", 0))
		Expect(stub.Writes()).To(BeEmpty())
	})

	It("is UNAVAILABLE when the stub provider fails", func(ctx context.Context) {
		stub.SetScenario(billingfake.Scenario{Failures: map[string]billingfake.Failure{
			"ListAccounts": {Reason: billing.ReasonUnavailable, Status: 503}}})
		Eventually(ctx, func(g Gomega) {
			state, err := statusState(ctx)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(state).To(Equal(publicv1.BillingProviderState_BILLING_PROVIDER_STATE_UNAVAILABLE))
		}, time.Minute, time.Second).Should(Succeed())
		Expect(stub.Writes()).To(BeEmpty())
	})
})
