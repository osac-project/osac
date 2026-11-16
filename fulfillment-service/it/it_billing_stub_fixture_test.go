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
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingstub"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// The deployed billing specs point the grpc-server at a TLS stub through the real M360 settings, so they share this
// fixture and serialize on the controller. The mount paths are hand-patched copies of the chart's, not rendered by it.
const (
	billingStubNamespace      = "osac"
	billingStubDeployment     = "fulfillment-grpc-server"
	billingStubContainer      = "grpc-server"
	billingStubCredentialFile = "/etc/fulfillment-grpc-server/billing/credential"
	billingStubToken          = "stub-token"
)

// configureBillingStub starts the TLS stub and points the deployed grpc-server at it through the real M360 settings.
// Cleanup restores the complete original pod spec and waits for NOT_CONFIGURED before removing its fixture Secrets.
func configureBillingStub(ctx context.Context, stub *billingstub.Stub, listen, stubURL string) {
	GinkgoHelper()
	parsed, err := url.Parse(stubURL)
	Expect(err).ToNot(HaveOccurred())
	ca, err := stub.StartTLS(listen, parsed.Hostname())
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(stub.Close)

	kube := tool.KubeClient()
	suffix := uuid.New()
	createSecret := func(name string, data map[string][]byte) {
		GinkgoHelper()
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: billingStubNamespace, Name: name},
			Data:       data,
		}
		Expect(kube.Create(ctx, secret)).To(Succeed())
		DeferCleanup(func(ctx context.Context) {
			Expect(crclient.IgnoreNotFound(kube.Delete(ctx, secret))).To(Succeed())
		})
	}
	secretName, caSecretName := "billing-stub-credential-"+suffix, "billing-stub-ca-"+suffix
	createSecret(secretName, map[string][]byte{"token": []byte(billingStubToken)})
	createSecret(caSecretName, map[string][]byte{"ca.crt": ca})

	key := crclient.ObjectKey{Namespace: billingStubNamespace, Name: billingStubDeployment}
	current := &appsv1.Deployment{}
	Expect(kube.Get(ctx, key, current)).To(Succeed())
	original := current.Spec.Template.Spec.DeepCopy()
	// Restore the original pod spec so later specs see a deployment without a billing provider.
	DeferCleanup(func(ctx context.Context) {
		latest := &appsv1.Deployment{}
		Expect(kube.Get(ctx, key, latest)).To(Succeed())
		latest.Spec.Template.Spec = *original
		Expect(kube.Update(ctx, latest)).To(Succeed())
		rolloutBillingDeployment(ctx)
		// Deployment availability doesn't mean the ingress has refreshed its headless-service DNS endpoints.
		Eventually(ctx, func(g Gomega) {
			object, err := billingStubStatus(ctx)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(object.GetState()).To(Equal(publicv1.BillingProviderState_BILLING_PROVIDER_STATE_NOT_CONFIGURED))
		}, time.Minute, time.Second).Should(Succeed())
	})

	podSpec := &current.Spec.Template.Spec
	podSpec.Volumes = append(podSpec.Volumes,
		corev1.Volume{Name: "billing-credential", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
			SecretName: secretName, Items: []corev1.KeyToPath{{Key: "token", Path: "credential"}}}}},
		corev1.Volume{Name: "billing-ca", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
			SecretName: caSecretName}}})
	for i := range podSpec.Containers {
		container := &podSpec.Containers[i]
		if container.Name != billingStubContainer {
			continue
		}
		container.VolumeMounts = append(container.VolumeMounts,
			corev1.VolumeMount{Name: "billing-credential", MountPath: "/etc/fulfillment-grpc-server/billing",
				ReadOnly: true},
			corev1.VolumeMount{Name: "billing-ca", MountPath: "/etc/fulfillment-grpc-server/billing-ca",
				ReadOnly: true})
		container.Env = append(container.Env,
			corev1.EnvVar{Name: "OSAC_BILLING_PROVIDER", Value: "m360"},
			// The base URL carries the whole path to the provider API; the stub speaks the M360 one.
			corev1.EnvVar{Name: "OSAC_BILLING_BASE_URL", Value: stubURL + "/api/v1/external"},
			corev1.EnvVar{Name: "OSAC_BILLING_CREDENTIAL_FILE", Value: billingStubCredentialFile},
			corev1.EnvVar{Name: "OSAC_BILLING_CA_FILE", Value: "/etc/fulfillment-grpc-server/billing-ca/ca.crt"},
			// The chart's tuning defaults, which it always renders with a provider.
			corev1.EnvVar{Name: "OSAC_BILLING_RETRY_COUNT", Value: "3"},
			corev1.EnvVar{Name: "OSAC_BILLING_PRICING_BREAKER_FAILURE_THRESHOLD", Value: "5"},
			corev1.EnvVar{Name: "OSAC_BILLING_PRICING_BREAKER_FAILURE_WINDOW_SECONDS", Value: "30"},
			corev1.EnvVar{Name: "OSAC_BILLING_PRICING_BREAKER_OPEN_SECONDS", Value: "30"},
		)
	}
	Expect(kube.Update(ctx, current)).To(Succeed())
	rolloutBillingDeployment(ctx)
}

// rolloutBillingDeployment waits until the grpc-server deployment's current generation is fully rolled out.
func rolloutBillingDeployment(ctx context.Context) {
	GinkgoHelper()
	Eventually(ctx, func(g Gomega) {
		current := &appsv1.Deployment{}
		g.Expect(tool.KubeClient().Get(ctx, crclient.ObjectKey{
			Namespace: billingStubNamespace, Name: billingStubDeployment}, current)).To(Succeed())
		g.Expect(current.Spec.Replicas).ToNot(BeNil())
		g.Expect(current.Status.ObservedGeneration).To(BeNumerically(">=", current.Generation))
		g.Expect(current.Status.UpdatedReplicas).To(Equal(*current.Spec.Replicas))
		g.Expect(current.Status.Replicas).To(Equal(*current.Spec.Replicas))
		g.Expect(current.Status.AvailableReplicas).To(Equal(*current.Spec.Replicas))
	}, 3*time.Minute, time.Second).Should(Succeed())
}

// billingStubStatus reads the deployed billing provider status through the public API.
func billingStubStatus(ctx context.Context) (*publicv1.BillingProviderStatus, error) {
	client := publicv1.NewBillingProviderStatusesClient(tool.ExternalView().AdminConn())
	response, err := client.Get(ctx, publicv1.BillingProviderStatusesGetRequest_builder{}.Build())
	return response.GetObject(), err
}

// newBillingTenant creates a tenant for the deployed controller to reconcile and registers its cleanup. The cleanup
// deletes through the API and waits for cleanup to finish, accepting either complete removal or the deletion-marked
// tombstone a billed tenant retains.
func newBillingTenant(ctx context.Context, domains, finalizers []string) *privatev1.Tenant {
	GinkgoHelper()
	client := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
	created, err := client.Create(ctx, privatev1.TenantsCreateRequest_builder{Object: privatev1.Tenant_builder{
		Metadata: privatev1.Metadata_builder{Name: "billing-" + uuid.New(), Finalizers: finalizers}.Build(),
		Spec:     privatev1.TenantSpec_builder{Domains: domains}.Build(),
	}.Build()}.Build())
	Expect(err).ToNot(HaveOccurred())
	tenant := created.GetObject()
	DeferCleanup(func(ctx context.Context) {
		cleanupBillingTenant(ctx, client, tenant.GetId())
	})
	return tenant
}

// cleanupBillingTenant deletes a tenant through the API and waits until it is either gone or a deletion-marked
// tombstone with no finalizers. Billed tenants intentionally retain their identity and history.
func cleanupBillingTenant(ctx context.Context, client privatev1.TenantsClient, id string) {
	GinkgoHelper()
	Eventually(ctx, func(g Gomega) {
		_, err := client.Delete(ctx, privatev1.TenantsDeleteRequest_builder{Id: id}.Build())
		if status.Code(err) != codes.NotFound {
			g.Expect(err).ToNot(HaveOccurred())
		}
	}, time.Minute, time.Second).Should(Succeed())
	Eventually(ctx, func(g Gomega) {
		read, err := client.Get(ctx, privatev1.TenantsGetRequest_builder{Id: id}.Build())
		if status.Code(err) == codes.NotFound {
			return
		}
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(read.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		g.Expect(read.GetObject().GetMetadata().GetFinalizers()).To(BeEmpty())
	}, 3*time.Minute, time.Second).Should(Succeed())
}

// expectBillingReason asserts err is FAILED_PRECONDITION carrying reason in its stable ErrorInfo detail. A provider
// request trace alone cannot prove which precondition rejected the request.
func expectBillingReason(err error, reason string) {
	GinkgoHelper()
	st, ok := status.FromError(err)
	Expect(ok).To(BeTrue(), "error was %v", err)
	Expect(st.Code()).To(Equal(codes.FailedPrecondition), "error was %v", err)
	reasons := make([]string, 0, len(st.Details()))
	for _, detail := range st.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok {
			reasons = append(reasons, info.GetReason())
		}
	}
	Expect(reasons).To(ContainElement(reason), "error was %v", err)
}
