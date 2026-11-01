// Package pullsecret manages same-hub, owned ClusterOrder credential projections.
package pullsecret

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const TenantAnnotation = "osac.openshift.io/tenant"
const ProjectAnnotation = "osac.openshift.io/project"
const revisionAnnotation = "osac.openshift.io/pull-secret-revision"
const LastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"
const OrderUIDAnnotation = "osac.openshift.io/cluster-order-uid"
const OrderNamespaceAnnotation = "osac.openshift.io/cluster-order-namespace"

var ErrInvalid = errors.New("pull secret unavailable, invalid, or not owned by this ClusterOrder")

// Validate accepts Docker credential objects without including their contents in errors.
func Validate(data []byte) error {
	var config struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}
	if json.Unmarshal(data, &config) != nil || config.Auths == nil {
		return ErrInvalid
	}
	return nil
}

// Parameters parses configuration without logging potentially untrusted input.
func Parameters(order *v1alpha1.ClusterOrder) (map[string]json.RawMessage, error) {
	parameters := map[string]json.RawMessage{}
	if order.Spec.TemplateParameters != "" {
		if json.Unmarshal([]byte(order.Spec.TemplateParameters), &parameters) != nil || parameters == nil {
			return nil, ErrInvalid
		}
	}
	return parameters, nil
}

// ValidateOrder rejects inline inputs; credentials must use an API Secret reference.
func ValidateOrder(order *v1alpha1.ClusterOrder) error {
	parameters, err := Parameters(order)
	if err != nil || order.Spec.PullSecret != "" {
		return ErrInvalid
	}
	if _, exists := parameters["pull_secret"]; exists {
		return ErrInvalid
	}
	return nil
}

// owned requires the precise controller identity and tenant/project provenance.
func owned(order *v1alpha1.ClusterOrder, secret *corev1.Secret) bool {
	owner := metav1.GetControllerOf(secret)
	return secret.DeletionTimestamp.IsZero() && order.UID != "" && owner != nil && owner.UID == order.UID && owner.Name == order.Name &&
		owner.Kind == "ClusterOrder" && owner.APIVersion == v1alpha1.GroupVersion.String() &&
		secret.Annotations[TenantAnnotation] == order.Annotations[TenantAnnotation] && secret.Annotations[ProjectAnnotation] == order.Annotations[ProjectAnnotation]
}

// Read validates the namespace-local source and its published revision, failing closed.
func Read(ctx context.Context, reader client.Reader, order *v1alpha1.ClusterOrder) (*corev1.Secret, error) {
	if ValidateOrder(order) != nil || order.Spec.PullSecretRef == nil || order.Spec.PullSecretRef.Name == "" || order.Spec.PullSecretRevision == "" {
		return nil, ErrInvalid
	}
	secret := &corev1.Secret{}
	if reader.Get(ctx, client.ObjectKey{Namespace: order.Namespace, Name: order.Spec.PullSecretRef.Name}, secret) != nil ||
		!owned(order, secret) || secret.Type != corev1.SecretTypeDockerConfigJson ||
		secret.Annotations[revisionAnnotation] != order.Spec.PullSecretRevision || Validate(secret.Data[corev1.DockerConfigJsonKey]) != nil {
		return nil, ErrInvalid
	}
	return secret, nil
}

// sameCredentials compares fixed-length private digests; revisions remain opaque UUIDs.
func sameCredentials(left, right []byte) bool {
	leftDigest, rightDigest := sha256.Sum256(left), sha256.Sum256(right)
	return subtle.ConstantTimeCompare(leftDigest[:], rightDigest[:]) == 1
}

// Ensure projects authorized API bytes, never adopting a foreign or deleting Secret.
// A failed order write can retry against the owned projection without changing its revision.
func Ensure(ctx context.Context, kube client.Client, order *v1alpha1.ClusterOrder, source *string) error {
	if ValidateOrder(order) != nil || source == nil || Validate([]byte(*source)) != nil || order.UID == "" || !order.DeletionTimestamp.IsZero() {
		return ErrInvalid
	}
	name := order.Name + "-pull-secret"
	if order.Spec.PullSecretRef != nil {
		name = order.Spec.PullSecretRef.Name
	}
	if name == "" {
		return ErrInvalid
	}
	secret := &corev1.Secret{}
	err := kube.Get(ctx, client.ObjectKey{Namespace: order.Namespace, Name: name}, secret)
	if err != nil && !apierrors.IsNotFound(err) {
		return ErrInvalid
	}
	creating := apierrors.IsNotFound(err)
	if creating {
		secret = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: order.Namespace}}
		if controllerutil.SetControllerReference(order, secret, kube.Scheme()) != nil {
			return ErrInvalid
		}
	} else if !owned(order, secret) || secret.Type != corev1.SecretTypeDockerConfigJson {
		return ErrInvalid
	}
	before := secret.DeepCopy()
	if secret.Annotations == nil {
		secret.Annotations = map[string]string{}
	}
	credentialsChanged := !sameCredentials(secret.Data[corev1.DockerConfigJsonKey], []byte(*source))
	if credentialsChanged || secret.Annotations[revisionAnnotation] == "" {
		secret.Annotations[revisionAnnotation] = string(uuid.NewUUID())
	}
	secret.Annotations[TenantAnnotation] = order.Annotations[TenantAnnotation]
	secret.Annotations[ProjectAnnotation] = order.Annotations[ProjectAnnotation]
	delete(secret.Annotations, LastAppliedAnnotation)
	secret.Type = corev1.SecretTypeDockerConfigJson
	secret.Data = map[string][]byte{corev1.DockerConfigJsonKey: []byte(*source)}
	if creating {
		err = kube.Create(ctx, secret)
	} else if credentialsChanged || len(before.Data) != 1 || !reflect.DeepEqual(before.Annotations, secret.Annotations) {
		err = kube.Patch(ctx, secret, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	}
	if err != nil {
		return ErrInvalid
	}
	order.Spec.PullSecretRef = &corev1.LocalObjectReference{Name: name}
	order.Spec.PullSecretRevision = secret.Annotations[revisionAnnotation]
	delete(order.Annotations, LastAppliedAnnotation)
	return nil
}

// BindNamespace stamps only new namespaces and rejects existing foreign provenance.
func BindNamespace(order *v1alpha1.ClusterOrder, namespace *corev1.Namespace) error {
	if order.UID == "" || namespace.Name != order.Namespace+"-"+order.Name || !namespace.DeletionTimestamp.IsZero() {
		return ErrInvalid
	}
	if namespace.ResourceVersion == "" && namespace.UID == "" {
		namespace.Annotations = map[string]string{OrderUIDAnnotation: string(order.UID), OrderNamespaceAnnotation: order.Namespace, TenantAnnotation: order.Annotations[TenantAnnotation], ProjectAnnotation: order.Annotations[ProjectAnnotation]}
		return nil
	}
	return ValidateNamespace(order, namespace)
}

// ValidateNamespace checks provenance before any credential copy or namespace teardown.
func ValidateNamespace(order *v1alpha1.ClusterOrder, namespace *corev1.Namespace) error {
	if order.UID == "" || namespace.Name != order.Namespace+"-"+order.Name ||
		namespace.Annotations[OrderUIDAnnotation] != string(order.UID) || namespace.Annotations[OrderNamespaceAnnotation] != order.Namespace ||
		namespace.Annotations[TenantAnnotation] != order.Annotations[TenantAnnotation] || namespace.Annotations[ProjectAnnotation] != order.Annotations[ProjectAnnotation] {
		return ErrInvalid
	}
	return nil
}

// HostedName is the namespace-local reference consumed by the checked-in CaaS templates.
func HostedName(order *v1alpha1.ClusterOrder) string { return "pullsecret-cluster-" + order.Name }

// EnsureHosted copies credentials only inside the hub, using a valid Namespace owner.
// The namespace UID and order provenance prevent adoption across orders or namespace reuse.
func EnsureHosted(ctx context.Context, kube client.Client, order *v1alpha1.ClusterOrder, namespace *corev1.Namespace) error {
	if ValidateNamespace(order, namespace) != nil || namespace.UID == "" || !namespace.DeletionTimestamp.IsZero() {
		return ErrInvalid
	}
	currentNamespace := &corev1.Namespace{}
	if kube.Get(ctx, client.ObjectKey{Name: namespace.Name}, currentNamespace) != nil || currentNamespace.UID != namespace.UID ||
		ValidateNamespace(order, currentNamespace) != nil || !currentNamespace.DeletionTimestamp.IsZero() {
		return ErrInvalid
	}
	source, err := Read(ctx, kube, order)
	if err != nil {
		return err
	}
	secret := &corev1.Secret{}
	err = kube.Get(ctx, client.ObjectKey{Namespace: namespace.Name, Name: HostedName(order)}, secret)
	if err != nil && !apierrors.IsNotFound(err) {
		return ErrInvalid
	}
	creating := apierrors.IsNotFound(err)
	if creating {
		secret = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: HostedName(order), Namespace: namespace.Name}}
		if controllerutil.SetControllerReference(namespace, secret, kube.Scheme()) != nil {
			return ErrInvalid
		}
	} else {
		owner := metav1.GetControllerOf(secret)
		if owner == nil || owner.UID != namespace.UID || owner.Name != namespace.Name || owner.Kind != "Namespace" || owner.APIVersion != "v1" ||
			!secret.DeletionTimestamp.IsZero() || secret.Type != corev1.SecretTypeDockerConfigJson ||
			secret.Annotations[OrderUIDAnnotation] != string(order.UID) || secret.Annotations[TenantAnnotation] != order.Annotations[TenantAnnotation] || secret.Annotations[ProjectAnnotation] != order.Annotations[ProjectAnnotation] {
			return ErrInvalid
		}
	}
	before := secret.DeepCopy()
	credentialsChanged := !sameCredentials(secret.Data[corev1.DockerConfigJsonKey], source.Data[corev1.DockerConfigJsonKey])
	secret.Type = corev1.SecretTypeDockerConfigJson
	secret.Data = map[string][]byte{corev1.DockerConfigJsonKey: source.Data[corev1.DockerConfigJsonKey]}
	secret.Annotations = map[string]string{OrderUIDAnnotation: string(order.UID), TenantAnnotation: order.Annotations[TenantAnnotation], ProjectAnnotation: order.Annotations[ProjectAnnotation], revisionAnnotation: order.Spec.PullSecretRevision}
	if creating {
		err = kube.Create(ctx, secret)
	} else if credentialsChanged || len(before.Data) != 1 || !reflect.DeepEqual(before.Annotations, secret.Annotations) {
		err = kube.Patch(ctx, secret, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	}
	if err != nil {
		return ErrInvalid
	}
	return nil
}
