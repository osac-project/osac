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

package controller

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/url"
	"sync"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/trustadmission"
)

const (
	trustObserverServiceAccount = "osac-fulfillment-trust-observer"
	trustKubeAPITimeout         = 15 * time.Second
	trustTokenLifetime          = 10 * time.Minute
	trustTokenRefreshBefore     = time.Minute
	trustCSINameLabel           = "app.kubernetes.io/name"
	trustCSIComponentLabel      = "app.kubernetes.io/component"
	trustCSIControllerComponent = "controller"
)

type TrustServiceAccountToken struct {
	Token     string
	ExpiresAt time.Time
}

// TrustTokenIssuer uses the hosted-cluster kubeconfig only to validate the
// preinstalled trust identities and request short-lived service-account tokens.
type TrustTokenIssuer interface {
	Issue(context.Context, []byte, string, string, string, string) (TrustServiceAccountToken, error)
}

type HostedClusterKubeconfigReader interface {
	Read(context.Context, *v1alpha1.ClusterOrder) ([]byte, error)
}

// HostedClusterKubeconfigResolver follows the ClusterOrder's control-plane
// reference to the HyperShift kubeconfig Secret. Kubeconfig bytes stay in this
// process and are never copied to a Secret, status field, log, or AAP job.
type HostedClusterKubeconfigResolver struct {
	Management client.Reader
}

func (r *HostedClusterKubeconfigResolver) Read(ctx context.Context, order *v1alpha1.ClusterOrder) ([]byte, error) {
	if r == nil || r.Management == nil || order == nil || order.Status.ClusterReference == nil {
		return nil, fmt.Errorf("hosted-cluster kubeconfig is not available")
	}
	ref := order.Status.ClusterReference
	if ref.Namespace == "" || ref.HostedClusterName == "" {
		return nil, fmt.Errorf("hosted-cluster reference is incomplete")
	}
	hcpNamespace := ref.Namespace + "-" + ref.HostedClusterName
	hcp := &hypershiftv1beta1.HostedControlPlane{}
	if err := r.Management.Get(ctx, types.NamespacedName{Namespace: hcpNamespace, Name: ref.HostedClusterName}, hcp); err != nil {
		return nil, fmt.Errorf("read HostedControlPlane kubeconfig reference: %w", err)
	}
	if hcp.Status.KubeConfig == nil || hcp.Status.KubeConfig.Name == "" || hcp.Status.KubeConfig.Key == "" {
		return nil, fmt.Errorf("HostedControlPlane kubeconfig reference is incomplete")
	}
	secret := &corev1.Secret{}
	if err := r.Management.Get(ctx, types.NamespacedName{Namespace: hcpNamespace, Name: hcp.Status.KubeConfig.Name}, secret); err != nil {
		return nil, fmt.Errorf("read HostedControlPlane kubeconfig Secret: %w", err)
	}
	data := secret.Data[hcp.Status.KubeConfig.Key]
	if len(data) == 0 {
		return nil, fmt.Errorf("HostedControlPlane kubeconfig data is empty")
	}
	return append([]byte(nil), data...), nil
}

// KubernetesTrustTokenIssuer validates the chart-installed service-account
// ownership and requests bounded TokenRequest credentials from the tenant API.
type KubernetesTrustTokenIssuer struct {
	Expiration time.Duration
	Now        func() time.Time
}

func (i *KubernetesTrustTokenIssuer) Issue(ctx context.Context, kubeconfig []byte, namespace, serviceAccount, tenant, owner string) (TrustServiceAccountToken, error) {
	if namespace == "" || serviceAccount == "" || tenant == "" || owner == "" {
		return TrustServiceAccountToken{}, fmt.Errorf("trust token identity is incomplete")
	}
	adminConfig, err := verifiedHostedClusterConfig(kubeconfig)
	if err != nil {
		return TrustServiceAccountToken{}, err
	}
	kube, err := kubernetes.NewForConfig(adminConfig)
	if err != nil {
		return TrustServiceAccountToken{}, fmt.Errorf("create hosted-cluster bootstrap client: %w", err)
	}
	return i.issueWithClient(ctx, kube, namespace, serviceAccount, tenant, owner)
}

func (i *KubernetesTrustTokenIssuer) issueWithClient(ctx context.Context, kube kubernetes.Interface, namespace, serviceAccount, tenant, owner string) (TrustServiceAccountToken, error) {
	if kube == nil || namespace == "" || serviceAccount == "" || tenant == "" || owner == "" {
		return TrustServiceAccountToken{}, fmt.Errorf("trust token identity is incomplete")
	}
	account, err := kube.CoreV1().ServiceAccounts(namespace).Get(ctx, serviceAccount, metav1.GetOptions{})
	if err != nil {
		return TrustServiceAccountToken{}, fmt.Errorf("read tenant trust service account: %w", err)
	}
	if account.UID == "" || account.Annotations[trustadmission.TenantAnnotation] != tenant ||
		account.Annotations[trustadmission.OwnerReferenceAnnotation] != owner {
		return TrustServiceAccountToken{}, fmt.Errorf("tenant trust service account identity does not match ClusterOrder")
	}
	expiration := i.Expiration
	if expiration <= 0 || expiration > trustTokenLifetime {
		expiration = trustTokenLifetime
	}
	seconds := int64(expiration / time.Second)
	request := &authenticationv1.TokenRequest{Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: &seconds}}
	result, err := kube.CoreV1().ServiceAccounts(namespace).CreateToken(ctx, serviceAccount, request, metav1.CreateOptions{})
	if err != nil {
		return TrustServiceAccountToken{}, fmt.Errorf("request short-lived tenant trust token: %w", err)
	}
	current, err := kube.CoreV1().ServiceAccounts(namespace).Get(ctx, serviceAccount, metav1.GetOptions{})
	if err != nil || current.UID != account.UID || current.Annotations[trustadmission.TenantAnnotation] != tenant ||
		current.Annotations[trustadmission.OwnerReferenceAnnotation] != owner {
		return TrustServiceAccountToken{}, fmt.Errorf("tenant trust service account changed during token request")
	}
	now := time.Now()
	if i.Now != nil {
		now = i.Now()
	}
	if result.Status.Token == "" || !result.Status.ExpirationTimestamp.After(now) || result.Status.ExpirationTimestamp.After(now.Add(trustTokenLifetime+time.Minute)) {
		return TrustServiceAccountToken{}, fmt.Errorf("tenant trust token lifetime is invalid")
	}
	return TrustServiceAccountToken{Token: result.Status.Token, ExpiresAt: result.Status.ExpirationTimestamp.Time}, nil
}

// HostedClusterTrustTargetResolver retains scoped tokens only in process memory
// and renews them before expiry. No kubeconfig or bearer token is persisted.
type HostedClusterTrustTargetResolver struct {
	Kubeconfigs HostedClusterKubeconfigReader
	Issuer      TrustTokenIssuer
	Namespace   string
	Now         func() time.Time
	mu          sync.Mutex
	cache       map[string]cachedTrustTarget
}

type cachedTrustTarget struct {
	target    FulfillmentTrustTarget
	expiresAt time.Time
	identity  trustTargetIdentity
}

type trustTargetIdentity struct {
	orderUID          types.UID
	tenant            string
	tenantNamespace   string
	hcpNamespace      string
	hostedClusterName string
}

func (r *HostedClusterTrustTargetResolver) Resolve(ctx context.Context, order *v1alpha1.ClusterOrder) (FulfillmentTrustTarget, error) {
	if r == nil || r.Kubeconfigs == nil || order == nil || order.UID == "" || r.Namespace == "" ||
		order.Annotations[trustadmission.TenantAnnotation] == "" {
		return nil, fmt.Errorf("hosted-cluster trust target is not configured")
	}
	identity := trustTargetIdentityFor(order, r.Namespace)
	key := client.ObjectKeyFromObject(order).String()
	if target, ok := r.cachedTarget(key, identity); ok {
		return target, nil
	}
	target, expiresAt, err := r.buildTarget(ctx, order)
	if err != nil {
		return nil, err
	}
	r.cacheTarget(key, identity, target, expiresAt)
	return target, nil
}

func (r *HostedClusterTrustTargetResolver) cachedTarget(key string, identity trustTargetIdentity) (FulfillmentTrustTarget, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cached, ok := r.cache[key]
	if !ok || cached.identity != identity || !cached.expiresAt.After(r.now().Add(trustTokenRefreshBefore)) {
		return nil, false
	}
	return cached.target, true
}

func (r *HostedClusterTrustTargetResolver) cacheTarget(key string, identity trustTargetIdentity, target FulfillmentTrustTarget, expiresAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = make(map[string]cachedTrustTarget)
	}
	r.cache[key] = cachedTrustTarget{target: target, expiresAt: expiresAt, identity: identity}
}

func trustTargetIdentityFor(order *v1alpha1.ClusterOrder, tenantNamespace string) trustTargetIdentity {
	identity := trustTargetIdentity{orderUID: order.UID, tenant: order.Annotations[trustadmission.TenantAnnotation], tenantNamespace: tenantNamespace}
	if reference := order.Status.ClusterReference; reference != nil {
		identity.hcpNamespace = reference.Namespace
		identity.hostedClusterName = reference.HostedClusterName
	}
	return identity
}

func (r *HostedClusterTrustTargetResolver) buildTarget(ctx context.Context, order *v1alpha1.ClusterOrder) (FulfillmentTrustTarget, time.Time, error) {
	kubeconfig, err := r.Kubeconfigs.Read(ctx, order)
	if err != nil {
		return nil, time.Time{}, err
	}
	issuer := r.Issuer
	if issuer == nil {
		issuer = &KubernetesTrustTokenIssuer{}
	}
	tenant := order.Annotations[trustadmission.TenantAnnotation]
	identities := make([]string, 0, 3)
	identities = append(identities, trustObserverServiceAccount, trustadmission.TrustSyncServiceAccount)
	publisherName, err := trustadmission.PublisherServiceAccountName(string(order.UID))
	if err != nil {
		return nil, time.Time{}, err
	}
	identities = append(identities, publisherName)
	tokens := make([]TrustServiceAccountToken, len(identities))
	for index, name := range identities {
		tokens[index], err = issuer.Issue(ctx, kubeconfig, r.Namespace, name, tenant, string(order.UID))
		if err != nil {
			return nil, time.Time{}, err
		}
	}
	adminConfig, err := verifiedHostedClusterConfig(kubeconfig)
	if err != nil {
		return nil, time.Time{}, err
	}
	target, expiresAt, err := newKubernetesTrustTarget(adminConfig, tokens, r.Namespace)
	if err != nil {
		return nil, time.Time{}, err
	}
	return target, expiresAt, nil
}

func newKubernetesTrustTarget(adminConfig *rest.Config, tokens []TrustServiceAccountToken, namespace string) (FulfillmentTrustTarget, time.Time, error) {
	if len(tokens) != 3 {
		return nil, time.Time{}, fmt.Errorf("trust target requires observer, sync, and publisher tokens")
	}
	observerConfig, err := trustServiceAccountConfig(adminConfig, tokens[0].Token)
	if err != nil {
		return nil, time.Time{}, err
	}
	writerConfig, err := trustServiceAccountConfig(adminConfig, tokens[1].Token)
	if err != nil {
		return nil, time.Time{}, err
	}
	publisherConfig, err := trustServiceAccountConfig(adminConfig, tokens[2].Token)
	if err != nil {
		return nil, time.Time{}, err
	}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, time.Time{}, err
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		return nil, time.Time{}, err
	}
	observer, err := client.New(observerConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("create tenant trust observer: %w", err)
	}
	writer, err := client.New(writerConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("create tenant trust writer: %w", err)
	}
	publisher, err := trustadmission.NewControlClient(publisherConfig, namespace)
	if err != nil {
		return nil, time.Time{}, err
	}
	expiresAt := tokens[0].ExpiresAt
	for _, token := range tokens[1:] {
		if token.ExpiresAt.Before(expiresAt) {
			expiresAt = token.ExpiresAt
		}
	}
	target := &KubernetesTrustTarget{Observer: observer, Writer: writer, Publisher: publisher, Namespace: namespace}
	return target, expiresAt, nil
}

func (r *HostedClusterTrustTargetResolver) ForgetByOrder(key client.ObjectKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cache, key.String())
}

func (r *HostedClusterTrustTargetResolver) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func verifiedHostedClusterConfig(kubeconfig []byte) (*rest.Config, error) {
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("parse hosted-cluster kubeconfig: %w", err)
	}
	endpoint, err := url.Parse(config.Host)
	hasClientCertificate := len(config.CertData) > 0 && len(config.KeyData) > 0
	hasBearerToken := config.BearerToken != ""
	caPool := x509.NewCertPool()
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" ||
		endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") || config.Insecure || config.CAFile != "" ||
		len(config.CAData) == 0 || !caPool.AppendCertsFromPEM(config.CAData) || config.ExecProvider != nil || config.AuthProvider != nil ||
		config.BearerTokenFile != "" || config.CertFile != "" || config.KeyFile != "" || config.Username != "" || config.Password != "" ||
		config.Proxy != nil || config.Impersonate.UserName != "" || len(config.Impersonate.Groups) != 0 || config.Impersonate.UID != "" ||
		len(config.Impersonate.Extra) != 0 || ((len(config.CertData) == 0) != (len(config.KeyData) == 0)) ||
		hasBearerToken == hasClientCertificate {
		return nil, fmt.Errorf("hosted-cluster kubeconfig must use verified TLS and embedded credentials")
	}
	config.Timeout = trustKubeAPITimeout
	return config, nil
}

func trustServiceAccountConfig(base *rest.Config, token string) (*rest.Config, error) {
	if base == nil || token == "" {
		return nil, fmt.Errorf("short-lived trust service-account token is unavailable")
	}
	config := rest.CopyConfig(base)
	config.Timeout = trustKubeAPITimeout
	config.BearerToken = token
	config.BearerTokenFile = ""
	config.Username = ""
	config.Password = ""
	config.CertData = nil
	config.KeyData = nil
	config.CertFile = ""
	config.KeyFile = ""
	config.ExecProvider = nil
	config.AuthProvider = nil
	config.Impersonate = rest.ImpersonationConfig{}
	return config, nil
}

// trustCSIControllerDeployments recognizes the standalone and umbrella chart names.
func trustCSIControllerDeployments(items []appsv1.Deployment) []appsv1.Deployment {
	controllers := make([]appsv1.Deployment, 0, len(items))
	for i := range items {
		deployment := items[i]
		name := deployment.Labels[trustCSINameLabel]
		if deployment.Labels[trustCSIComponentLabel] != trustCSIControllerComponent || (name != "csi-driver" && name != "csiDriver") {
			continue
		}
		controllers = append(controllers, deployment)
	}
	return controllers
}

// KubernetesTrustTarget reads tenant state with the observer identity and
// performs only admission-validated trust mutations with the scoped sync token.
type KubernetesTrustTarget struct {
	Observer  client.Reader
	Writer    client.Client
	Publisher interface {
		Publish(context.Context, trustadmission.ExpectedBundle) error
		Revoke(context.Context, trustadmission.RecordKey) error
	}
	Namespace string
}

func (t *KubernetesTrustTarget) Publish(ctx context.Context, record trustadmission.ExpectedBundle) error {
	return t.Publisher.Publish(ctx, record)
}

func (t *KubernetesTrustTarget) Revoke(ctx context.Context, key trustadmission.RecordKey) error {
	return t.Publisher.Revoke(ctx, key)
}

// Apply idempotently writes the expected bundle and changes only the CSI
// Deployment pod-template hash, which the tenant admission webhook enforces.
func (t *KubernetesTrustTarget) Apply(ctx context.Context, expected trustadmission.ExpectedBundle) error {
	if t.Writer == nil {
		return fmt.Errorf("tenant trust writer is not configured")
	}
	key := types.NamespacedName{Namespace: t.Namespace, Name: trustadmission.ConfigMapName}
	configMap := &corev1.ConfigMap{}
	if err := t.Writer.Get(ctx, key, configMap); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		if err := t.Writer.Create(ctx, trustConfigMap(expected, t.Namespace)); err != nil {
			return err
		}
	} else {
		base := configMap.DeepCopy()
		configMap.Data = map[string]string{trustadmission.BundleDataKey: string(expected.BundlePEM)}
		configMap.BinaryData = nil
		configMap.OwnerReferences = nil
		configMap.Finalizers = nil
		configMap.GenerateName = ""
		configMap.Annotations = trustAnnotations(expected)
		if err := t.Writer.Patch(ctx, configMap, client.MergeFrom(base)); err != nil {
			return err
		}
	}
	deployments := &appsv1.DeploymentList{}
	if err := t.Writer.List(ctx, deployments, client.InNamespace(t.Namespace), client.MatchingLabels{
		trustCSIComponentLabel: trustCSIControllerComponent,
	}); err != nil {
		return err
	}
	deployments.Items = trustCSIControllerDeployments(deployments.Items)
	if len(deployments.Items) == 0 {
		return errTrustClientUnavailable
	}
	for i := range deployments.Items {
		if deployments.Items[i].Labels[trustadmission.TrustClientLabel] != labelValueTrue {
			return errTrustClientUnavailable
		}
	}
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		if deployment.Spec.Template.Annotations[trustadmission.BundleHashAnnotation] == expected.Key.BundleSHA256 {
			continue
		}
		base := deployment.DeepCopy()
		if deployment.Spec.Template.Annotations == nil {
			deployment.Spec.Template.Annotations = make(map[string]string)
		}
		deployment.Spec.Template.Annotations[trustadmission.BundleHashAnnotation] = expected.Key.BundleSHA256
		if err := t.Writer.Patch(ctx, deployment, client.MergeFrom(base)); err != nil {
			return err
		}
	}
	return nil
}

func trustConfigMap(expected trustadmission.ExpectedBundle, namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: trustadmission.ConfigMapName, Namespace: namespace,
		Annotations: trustAnnotations(expected)}, Data: map[string]string{trustadmission.BundleDataKey: string(expected.BundlePEM)}}
}

func trustAnnotations(expected trustadmission.ExpectedBundle) map[string]string {
	return map[string]string{
		trustadmission.TenantAnnotation:         expected.Tenant,
		trustadmission.OwnerReferenceAnnotation: expected.OwnerReference,
		trustadmission.BundleHashAnnotation:     expected.Key.BundleSHA256,
	}
}

// Observe verifies exact bundle metadata and the ready rollout for all CSI
// controller Deployments. Missing or legacy controllers are never reported ready.
func (t *KubernetesTrustTarget) Observe(ctx context.Context, expected trustadmission.ExpectedBundle) (bool, bool, error) {
	if t.Observer == nil {
		return false, false, fmt.Errorf("tenant trust observer is not configured")
	}
	configMap := &corev1.ConfigMap{}
	err := t.Observer.Get(ctx, types.NamespacedName{Namespace: t.Namespace, Name: trustadmission.ConfigMapName}, configMap)
	if err != nil && !apierrors.IsNotFound(err) {
		return false, false, err
	}
	configMapCurrent := err == nil && len(configMap.Data) == 1 && len(configMap.BinaryData) == 0 &&
		configMap.Data[trustadmission.BundleDataKey] == string(expected.BundlePEM) &&
		mapsEqual(configMap.Annotations, trustAnnotations(expected))
	deployments := &appsv1.DeploymentList{}
	if err := t.Observer.List(ctx, deployments, client.InNamespace(t.Namespace), client.MatchingLabels{
		trustCSIComponentLabel: trustCSIControllerComponent,
	}); err != nil {
		return false, false, err
	}
	deployments.Items = trustCSIControllerDeployments(deployments.Items)
	if len(deployments.Items) == 0 {
		return false, false, nil
	}
	current := configMapCurrent
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		if deployment.Labels[trustadmission.TrustClientLabel] != labelValueTrue {
			return false, true, nil
		}
		if deployment.Spec.Template.Annotations[trustadmission.BundleHashAnnotation] != expected.Key.BundleSHA256 ||
			!trustDeploymentReady(deployment) {
			current = false
		}
		oldReady, err := t.oldReadyPods(ctx, deployment, expected.Key.BundleSHA256)
		if err != nil {
			return false, false, err
		}
		if oldReady {
			current = false
		}
	}
	return current, false, nil
}

func (t *KubernetesTrustTarget) oldReadyPods(ctx context.Context, deployment *appsv1.Deployment, hash string) (bool, error) {
	replicaSets := &appsv1.ReplicaSetList{}
	if err := t.Observer.List(ctx, replicaSets, client.InNamespace(t.Namespace)); err != nil {
		return false, err
	}
	oldUIDs := make(map[types.UID]struct{})
	for i := range replicaSets.Items {
		replicaSet := &replicaSets.Items[i]
		if ownedByUID(replicaSet.OwnerReferences, deployment.UID) &&
			replicaSet.Spec.Template.Annotations[trustadmission.BundleHashAnnotation] != hash {
			if replicaSet.Status.ReadyReplicas > 0 {
				return true, nil
			}
			oldUIDs[replicaSet.UID] = struct{}{}
		}
	}
	if len(oldUIDs) == 0 {
		return false, nil
	}
	pods := &corev1.PodList{}
	if err := t.Observer.List(ctx, pods, client.InNamespace(t.Namespace)); err != nil {
		return false, err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		for uid := range oldUIDs {
			if ownedByUID(pod.OwnerReferences, uid) && podReady(pod) {
				return true, nil
			}
		}
	}
	return false, nil
}

func trustDeploymentReady(deployment *appsv1.Deployment) bool {
	desired := int32(1)
	if deployment.Spec.Replicas != nil {
		desired = *deployment.Spec.Replicas
	}
	return deployment.Status.ObservedGeneration == deployment.Generation &&
		deployment.Status.UpdatedReplicas == desired && deployment.Status.ReadyReplicas == desired &&
		deployment.Status.AvailableReplicas == desired && deployment.Status.UnavailableReplicas == 0
}

func mapsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range right {
		if left[key] != value {
			return false
		}
	}
	return true
}

func ownedByUID(refs []metav1.OwnerReference, uid types.UID) bool {
	for _, ref := range refs {
		if ref.UID == uid {
			return true
		}
	}
	return false
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
