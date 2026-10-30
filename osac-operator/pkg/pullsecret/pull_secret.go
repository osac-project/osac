// Package pullsecret manages owned ClusterOrder credential projections and migration.
package pullsecret

import (
	"bytes"
	"context"
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
const revisionAnnotation = "osac.openshift.io/pull-secret-revision"
const LastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

var ErrInvalid = errors.New("pull secret unavailable, invalid, or not owned by this ClusterOrder")

func Validate(data []byte) error {
	var config struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}
	if json.Unmarshal(data, &config) != nil || config.Auths == nil {
		return ErrInvalid
	}
	return nil
}

func Parameters(order *v1alpha1.ClusterOrder) (map[string]json.RawMessage, error) {
	parameters := map[string]json.RawMessage{}
	if order.Spec.TemplateParameters != "" {
		if json.Unmarshal([]byte(order.Spec.TemplateParameters), &parameters) != nil || parameters == nil {
			return nil, ErrInvalid
		}
	}
	return parameters, nil
}

func owned(order *v1alpha1.ClusterOrder, secret *corev1.Secret) bool {
	owner := metav1.GetControllerOf(secret)
	if !secret.DeletionTimestamp.IsZero() || order.UID == "" || owner == nil || owner.UID != order.UID || owner.Name != order.Name ||
		owner.Kind != "ClusterOrder" || owner.APIVersion != v1alpha1.GroupVersion.String() {
		return false
	}
	tenant := secret.Annotations[TenantAnnotation]
	return tenant == "" || tenant == order.Annotations[TenantAnnotation]
}

func Read(ctx context.Context, reader client.Reader, order *v1alpha1.ClusterOrder) (*corev1.Secret, error) {
	if order.Spec.PullSecretRef == nil || order.Spec.PullSecretRef.Name == "" {
		return nil, ErrInvalid
	}
	secret := &corev1.Secret{}
	if reader.Get(ctx, client.ObjectKey{Namespace: order.Namespace, Name: order.Spec.PullSecretRef.Name}, secret) != nil ||
		!owned(order, secret) || secret.Type != corev1.SecretTypeDockerConfigJson ||
		Validate(secret.Data[corev1.DockerConfigJsonKey]) != nil {
		return nil, ErrInvalid
	}
	return secret, nil
}

func Ensure(ctx context.Context, kube client.Client, order *v1alpha1.ClusterOrder, source *string) error {
	parameters, err := Parameters(order)
	if err != nil {
		return err
	}
	payload := ""
	if source != nil {
		payload = *source
	} else if order.Spec.PullSecretRef == nil {
		payload = order.Spec.PullSecret
		if payload == "" {
			if raw, exists := parameters["pull_secret"]; exists {
				if json.Unmarshal(raw, &payload) != nil || payload == "" {
					return ErrInvalid
				}
			}
		}
	}
	if payload != "" {
		if Validate([]byte(payload)) != nil || order.UID == "" {
			return ErrInvalid
		}
		name := order.Name + "-pull-secret"
		if order.Spec.PullSecretRef != nil {
			name = order.Spec.PullSecretRef.Name
		}
		secret := &corev1.Secret{}
		err = kube.Get(ctx, client.ObjectKey{Namespace: order.Namespace, Name: name}, secret)
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
		if !bytes.Equal(secret.Data[corev1.DockerConfigJsonKey], []byte(payload)) || secret.Annotations[revisionAnnotation] == "" {
			secret.Annotations[revisionAnnotation] = string(uuid.NewUUID())
		}
		secret.Annotations[TenantAnnotation] = order.Annotations[TenantAnnotation]
		delete(secret.Annotations, LastAppliedAnnotation)
		secret.Type = corev1.SecretTypeDockerConfigJson
		secret.Data = map[string][]byte{corev1.DockerConfigJsonKey: []byte(payload)}
		if creating {
			err = kube.Create(ctx, secret)
		} else if !reflect.DeepEqual(before, secret) {
			err = kube.Patch(ctx, secret, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
		}
		if err != nil {
			return ErrInvalid
		}
		order.Spec.PullSecretRef = &corev1.LocalObjectReference{Name: name}
		order.Spec.PullSecretRevision = secret.Annotations[revisionAnnotation]
	} else if order.Spec.PullSecretRef != nil {
		secret, readErr := Read(ctx, kube, order)
		if readErr != nil {
			return readErr
		}
		if secret.Annotations[revisionAnnotation] == "" {
			current := string(secret.Data[corev1.DockerConfigJsonKey])
			return Ensure(ctx, kube, order, &current)
		}
		order.Spec.PullSecretRevision = secret.Annotations[revisionAnnotation]
	}
	order.Spec.PullSecret = ""
	if _, exists := parameters["pull_secret"]; exists {
		delete(parameters, "pull_secret")
		encoded, marshalErr := json.Marshal(parameters)
		if marshalErr != nil {
			return ErrInvalid
		}
		order.Spec.TemplateParameters = string(encoded)
	}
	delete(order.Annotations, LastAppliedAnnotation)
	return nil
}

func Migrate(ctx context.Context, kube client.Client, order *v1alpha1.ClusterOrder) (bool, error) {
	before := order.DeepCopy()
	if err := Ensure(ctx, kube, order, nil); err != nil {
		return false, err
	}
	if reflect.DeepEqual(before, order) {
		return false, nil
	}
	if err := kube.Patch(ctx, order, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return false, ErrInvalid
	}
	return true, nil
}
