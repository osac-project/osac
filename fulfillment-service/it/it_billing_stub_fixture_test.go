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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
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
	billingStubOwner          = "osac.openshift.io/it-billing-stub"
	billingStubRequestTimeout = 10 * time.Second
)

// configureBillingStub starts the TLS stub and points the deployed grpc-server at it through the real M360 settings.
func configureBillingStub(ctx context.Context, stub *billingstub.Stub, listen, stubURL string) {
	GinkgoHelper()
	parsed, err := url.Parse(stubURL)
	Expect(err).ToNot(HaveOccurred())
	ca, err := stub.StartTLS(listen, parsed.Hostname())
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(stub.Close)

	kube := tool.KubeClient()
	suffix := uuid.New()
	secretName, caSecretName := "billing-stub-credential-"+suffix, "billing-stub-ca-"+suffix
	defaultMode := int32(corev1.SecretVolumeSourceDefaultMode)
	patch := billingStubPatch{
		owner: suffix,
		volumes: []corev1.Volume{
			{Name: "billing-credential", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
				SecretName: secretName, DefaultMode: &defaultMode,
				Items: []corev1.KeyToPath{{Key: "token", Path: "credential"}}}}},
			{Name: "billing-ca", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
				SecretName: caSecretName, DefaultMode: &defaultMode}}},
		},
		mounts: []corev1.VolumeMount{
			{Name: "billing-credential", MountPath: "/etc/fulfillment-grpc-server/billing", ReadOnly: true},
			{Name: "billing-ca", MountPath: "/etc/fulfillment-grpc-server/billing-ca", ReadOnly: true},
		},
		env: []corev1.EnvVar{
			{Name: "OSAC_BILLING_PROVIDER", Value: "m360"},
			// The base URL carries the whole path to the provider API; the stub speaks the M360 one.
			{Name: "OSAC_BILLING_BASE_URL", Value: stubURL + "/api/v1/external"},
			{Name: "OSAC_BILLING_CREDENTIAL_FILE", Value: billingStubCredentialFile},
			{Name: "OSAC_BILLING_CA_FILE", Value: "/etc/fulfillment-grpc-server/billing-ca/ca.crt"},
			// The chart's tuning defaults, which it always renders with a provider.
			{Name: "OSAC_BILLING_RETRY_COUNT", Value: "3"},
			{Name: "OSAC_BILLING_PRICING_BREAKER_FAILURE_THRESHOLD", Value: "5"},
			{Name: "OSAC_BILLING_PRICING_BREAKER_FAILURE_WINDOW_SECONDS", Value: "30"},
			{Name: "OSAC_BILLING_PRICING_BREAKER_OPEN_SECONDS", Value: "30"},
		},
	}
	// Register before the first write: a failed request may have been accepted by the API server.
	var attemptedSecrets []*corev1.Secret
	DeferCleanup(func(ctx context.Context) error {
		if err := patch.cleanup(ctx, kube, attemptedSecrets); err != nil {
			return err
		}
		if patch.deploymentUID == "" {
			return nil
		}
		// Deployment availability doesn't guarantee the ingress has refreshed the headless-service endpoints.
		return wait.PollUntilContextTimeout(ctx, time.Second, time.Minute, true, func(ctx context.Context) (bool, error) {
			ctx, cancel := context.WithTimeout(ctx, billingStubRequestTimeout)
			defer cancel()
			object, err := billingStubStatus(ctx)
			return err == nil && object.GetState() == publicv1.BillingProviderState_BILLING_PROVIDER_STATE_NOT_CONFIGURED, nil
		})
	}, NodeTimeout(6*time.Minute))

	current := &appsv1.Deployment{}
	Expect(kube.Get(ctx, billingDeploymentKey(), current)).To(Succeed())
	Expect(patch.apply(current)).To(Succeed())
	for _, secret := range []*corev1.Secret{
		{ObjectMeta: metav1.ObjectMeta{Name: secretName}, Data: map[string][]byte{"token": []byte(billingStubToken)}},
		{ObjectMeta: metav1.ObjectMeta{Name: caSecretName}, Data: map[string][]byte{"ca.crt": ca}},
	} {
		secret.Namespace = billingStubNamespace
		secret.Annotations = map[string]string{billingStubOwner: suffix}
		attemptedSecrets = append(attemptedSecrets, secret)
		Expect(kube.Create(ctx, secret)).To(Succeed())
	}
	Expect(patch.updateDeployment(ctx, kube, current)).To(Succeed())
	Expect(rolloutBillingDeployment(ctx, kube)).To(Succeed())
	waitBillingPublicAPIReady(ctx)
}

type billingStubPatch struct {
	owner                        string
	deploymentUID                types.UID
	deploymentUpdateAcknowledged bool
	volumes                      []corev1.Volume
	mounts                       []corev1.VolumeMount
	env                          []corev1.EnvVar
}

func billingDeploymentKey() crclient.ObjectKey {
	return crclient.ObjectKey{Namespace: billingStubNamespace, Name: billingStubDeployment}
}

func (p *billingStubPatch) updateDeployment(ctx context.Context, kube crclient.Client, deployment *appsv1.Deployment) error {
	p.deploymentUID = deployment.UID
	err := kube.Update(ctx, deployment)
	p.deploymentUpdateAcknowledged = err == nil
	return err
}

func (p billingStubPatch) apply(deployment *appsv1.Deployment) error {
	template := &deployment.Spec.Template
	if template.Annotations[billingStubOwner] != "" {
		return errors.New("billing stub fixture already owns the Deployment template")
	}
	return p.edit(template, true)
}

func (p billingStubPatch) edit(template *corev1.PodTemplateSpec, add bool) error {
	pod := &template.Spec
	if err := editBillingStubEntries(&pod.Volumes, p.volumes, func(v corev1.Volume) string { return v.Name }, add); err != nil {
		return fmt.Errorf("billing stub volumes: %w", err)
	}
	index := slices.IndexFunc(pod.Containers, func(c corev1.Container) bool { return c.Name == billingStubContainer })
	if index < 0 && add {
		return fmt.Errorf("Deployment is missing container %q", billingStubContainer)
	}
	if index >= 0 {
		container := &pod.Containers[index]
		if err := editBillingStubEntries(&container.VolumeMounts, p.mounts, func(m corev1.VolumeMount) string { return m.MountPath }, add); err != nil {
			return fmt.Errorf("billing stub volume mounts: %w", err)
		}
		if err := editBillingStubEntries(&container.Env, p.env, func(e corev1.EnvVar) string { return e.Name }, add); err != nil {
			return fmt.Errorf("billing stub environment: %w", err)
		}
	}
	if add {
		if template.Annotations == nil {
			template.Annotations = make(map[string]string)
		}
		template.Annotations[billingStubOwner] = p.owner
	} else {
		delete(template.Annotations, billingStubOwner)
	}
	return nil
}

// Add only absent entries, and remove only exact fixture entries. A concurrent edit to a fixture entry
// is ambiguous: report it instead of undoing it. The caller discards the read on any error.
func editBillingStubEntries[T any](entries *[]T, fixture []T, key func(T) string, add bool) error {
	for _, entry := range fixture {
		matches := func(other T) bool { return key(other) == key(entry) }
		i := slices.IndexFunc(*entries, matches)
		if add {
			if i >= 0 {
				return fmt.Errorf("entry %q already exists", key(entry))
			}
			*entries = append(*entries, entry)
		} else if i >= 0 {
			if !equality.Semantic.DeepEqual((*entries)[i], entry) || slices.ContainsFunc((*entries)[i+1:], matches) {
				return fmt.Errorf("entry %q changed concurrently", key(entry))
			}
			*entries = slices.Delete(*entries, i, i+1)
		}
	}
	return nil
}

func (p billingStubPatch) cleanup(ctx context.Context, kube crclient.Client, secrets []*corev1.Secret) error {
	if p.deploymentUID != "" {
		if err := p.restore(ctx, kube); err != nil {
			return fmt.Errorf("restore billing stub Deployment (retaining fixture Secrets): %w", err)
		}
		if err := rolloutBillingDeployment(ctx, kube); err != nil {
			return fmt.Errorf("wait for restored billing stub Deployment (retaining fixture Secrets): %w", err)
		}
	}
	var errs []error
	for _, attempted := range secrets {
		if err := p.deleteSecret(ctx, kube, attempted); err != nil {
			errs = append(errs, fmt.Errorf("clean up billing stub Secret %q: %w", attempted.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (p billingStubPatch) restore(ctx context.Context, kube crclient.Client) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deployment, err := readBillingDeployment(ctx, kube)
		if err != nil {
			return err
		}
		if deployment.UID != p.deploymentUID || deployment.Spec.Template.Annotations[billingStubOwner] != p.owner {
			if !p.deploymentUpdateAcknowledged {
				// Reading a template without our marker does not prove that an unacknowledged
				// setup update won't commit later and start referencing the Secrets.
				return errors.New("setup Deployment update was not acknowledged and fixture changes could not be removed")
			}
			return nil
		}
		if err := p.edit(&deployment.Spec.Template, false); err != nil {
			return err
		}
		// Update uses the fresh resourceVersion. On conflict strip our changes from a new read, not from the old snapshot.
		ctx, cancel := context.WithTimeout(ctx, billingStubRequestTimeout)
		defer cancel()
		return kube.Update(ctx, deployment)
	})
}

func (p billingStubPatch) deleteSecret(ctx context.Context, kube crclient.Client, attempted *corev1.Secret) error {
	secret := &corev1.Secret{}
	readCtx, cancel := context.WithTimeout(ctx, billingStubRequestTimeout)
	err := kube.Get(readCtx, crclient.ObjectKeyFromObject(attempted), secret)
	cancel()
	if err != nil {
		return crclient.IgnoreNotFound(err)
	}
	// An ambiguous create response may not return a UID; the unique marker still identifies our create.
	if secret.Annotations[billingStubOwner] != p.owner || (attempted.UID != "" && attempted.UID != secret.UID) {
		return errors.New("Secret is no longer owned by this fixture run")
	}

	// Recheck the live template right before each delete; Kubernetes provides no atomic cross-object precondition.
	deployment, err := readBillingDeployment(ctx, kube)
	if err != nil {
		return fmt.Errorf("cannot verify that the Deployment no longer references the Secret: %w", err)
	}
	// Kubernetes Secret references use the exact name as a string. Look throughout the pod template so
	// alternate reference paths (projected volumes, envFrom, init containers, etc.) also fail closed.
	template, err := json.Marshal(&deployment.Spec.Template)
	if err != nil {
		return err
	}
	if bytes.Contains(template, []byte(strconv.Quote(secret.Name))) {
		return errors.New("Deployment template still references the Secret")
	}
	deleteCtx, cancel := context.WithTimeout(ctx, billingStubRequestTimeout)
	defer cancel()
	return crclient.IgnoreNotFound(kube.Delete(deleteCtx, secret, crclient.Preconditions{
		UID: &secret.UID, ResourceVersion: &secret.ResourceVersion,
	}))
}

func readBillingDeployment(ctx context.Context, kube crclient.Client) (*appsv1.Deployment, error) {
	ctx, cancel := context.WithTimeout(ctx, billingStubRequestTimeout)
	defer cancel()
	current := &appsv1.Deployment{}
	return current, kube.Get(ctx, billingDeploymentKey(), current)
}

// waitBillingPublicAPIReady waits for the public proxy path after rollout without warming billing discovery or
// touching the provider request trace. Capabilities.Get is also the integration tool's gRPC readiness probe.
func waitBillingPublicAPIReady(ctx context.Context) {
	GinkgoHelper()
	readyCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	client := publicv1.NewCapabilitiesClient(tool.ExternalView().AdminConn())
	Eventually(readyCtx, func() error {
		_, err := client.Get(readyCtx, publicv1.CapabilitiesGetRequest_builder{}.Build())
		return err
	}, time.Minute, time.Second).Should(Succeed())
}

// rolloutBillingDeployment waits until the grpc-server deployment's current generation is fully rolled out.
func rolloutBillingDeployment(ctx context.Context, kube crclient.Client) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
		current, err := readBillingDeployment(ctx, kube)
		if err != nil {
			return false, err
		}
		replicas := current.Spec.Replicas
		return replicas != nil &&
			current.Status.ObservedGeneration >= current.Generation &&
			current.Status.UpdatedReplicas == *replicas &&
			current.Status.Replicas == *replicas &&
			current.Status.AvailableReplicas == *replicas, nil
	})
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
