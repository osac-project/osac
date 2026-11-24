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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	registryAccessReadyCondition  = "RegistryAccessReady"
	registryProbeLabel            = osacPrefix + "/registry-probe"
	registryProbePullSecretSuffix = "-registry-probe-pull-secret"
	registryProbeWait             = 10 * time.Second
)

// reconcileManagementRegistryProbe runs before the provider starts the core
// HostedCluster install. Kubelet performs the image pull using the Cluster's
// existing pull secret, so this exercises management-cluster registry access
// through the same image-pull path used by HostedControlPlane workloads.
func (r *ClusterOrderReconciler) reconcileManagementRegistryProbe(
	ctx context.Context, order *v1alpha1.ClusterOrder,
) (bool, ctrl.Result, error) {
	if order == nil || !order.Spec.SupportsAutomaticCaaSEndpoints || !r.NetworkingProvisioningEnabled {
		return true, ctrl.Result{}, nil
	}
	if order.Annotations[tenantAnnotation] == "" {
		return false, ctrl.Result{}, fmt.Errorf("ClusterOrder %s/%s has no tenant annotation for registry probe ownership",
			order.Namespace, order.Name)
	}

	image := clusterOrderRegistryProbeImage(order)
	if image == "" {
		message := "Management registry probe is waiting for the ClusterVersion release image"
		order.SetStatusCondition(registryAccessReadyCondition, metav1.ConditionFalse, message, "ImageUnavailable")
		return false, ctrl.Result{RequeueAfter: registryProbeWait}, nil
	}
	message := managementRegistryProbeReadyMessage(image)
	condition := apimeta.FindStatusCondition(order.Status.Conditions, registryAccessReadyCondition)
	if condition != nil && condition.Status == metav1.ConditionTrue && condition.Message == message {
		// Success is persisted by the outer ClusterOrder reconcile before this
		// path runs again. Only then remove the Pod and Secret evidence.
		cleaned, result, err := r.cleanupManagementRegistryProbe(ctx, order)
		if err != nil || !cleaned {
			return false, result, err
		}
		return true, ctrl.Result{}, nil
	}

	probeName := managementRegistryProbePodName(order)
	if err := r.ensureManagementRegistryPullSecret(ctx, order, probeName); err != nil {
		return false, ctrl.Result{}, err
	}

	key := client.ObjectKey{Namespace: order.Namespace, Name: probeName}
	probe := &corev1.Pod{}
	if err := r.Get(ctx, key, probe); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, ctrl.Result{}, fmt.Errorf("get management registry probe Pod: %w", err)
		}
		probe = managementRegistryProbePod(order, probeName, image)
		if err := controllerutil.SetControllerReference(order, probe, r.Scheme); err != nil {
			return false, ctrl.Result{}, fmt.Errorf("set management registry probe owner: %w", err)
		}
		if err := r.Create(ctx, probe); err != nil && !apierrors.IsAlreadyExists(err) {
			return false, ctrl.Result{}, fmt.Errorf("create management registry probe Pod: %w", err)
		}
		order.SetStatusCondition(registryAccessReadyCondition, metav1.ConditionFalse,
			"Management image source is waiting for the release image pull", "ProbePending")
		return false, ctrl.Result{RequeueAfter: registryProbeWait}, nil
	}
	if !managementRegistryProbeOwnedBy(order, probe) {
		return false, ctrl.Result{}, fmt.Errorf("management registry probe Pod %s is not owned by ClusterOrder %s", key, order.Name)
	}
	wantPullSecret := clusterOrderPullSecret(order) != ""
	hasPullSecret := managementRegistryProbeHasPullSecret(order, probe)
	if len(probe.Spec.Containers) != 1 || probe.Spec.Containers[0].Image != image || wantPullSecret != hasPullSecret {
		if err := r.Delete(ctx, probe); err != nil && !apierrors.IsNotFound(err) {
			return false, ctrl.Result{}, fmt.Errorf("delete stale management registry probe Pod: %w", err)
		}
		return false, ctrl.Result{RequeueAfter: time.Second}, nil
	}

	containerStatus := managementRegistryProbeContainerStatus(probe)
	if retry, result, err := r.retryFailedManagementRegistryProbe(ctx, order, probe, containerStatus); retry {
		return false, result, err
	}
	if containerStatus != nil && containerStatus.ImageID != "" {
		order.SetStatusCondition(registryAccessReadyCondition, metav1.ConditionTrue,
			message, "ManagementImagePulled")
		// Persist the evidence before the next pass removes the probe resources.
		return false, ctrl.Result{RequeueAfter: time.Second}, nil
	}

	if failure := managementRegistryProbeFailure(probe, containerStatus); failure != "" {
		order.SetStatusCondition(registryAccessReadyCondition, metav1.ConditionFalse,
			fmt.Sprintf("Management image source could not pull %s: %s", image, failure), "ManagementImagePullFailed")
	} else {
		order.SetStatusCondition(registryAccessReadyCondition, metav1.ConditionFalse,
			fmt.Sprintf("Management image source is still pulling %s", image), "ProbePending")
	}
	return false, ctrl.Result{RequeueAfter: registryProbeWait}, nil
}

func managementRegistryProbeHasPullSecret(order *v1alpha1.ClusterOrder, probe *corev1.Pod) bool {
	if order == nil || probe == nil {
		return false
	}
	wanted := managementRegistryProbePullSecretName(order)
	for _, reference := range probe.Spec.ImagePullSecrets {
		if reference.Name == wanted {
			return true
		}
	}
	return false
}

func (r *ClusterOrderReconciler) retryFailedManagementRegistryProbe(
	ctx context.Context, order *v1alpha1.ClusterOrder, probe *corev1.Pod,
	containerStatus *corev1.ContainerStatus,
) (bool, ctrl.Result, error) {
	if probe.Status.Phase != corev1.PodFailed || (containerStatus != nil && containerStatus.ImageID != "") {
		return false, ctrl.Result{}, nil
	}
	if err := r.Delete(ctx, probe); err != nil && !apierrors.IsNotFound(err) {
		return true, ctrl.Result{}, fmt.Errorf("delete terminal management registry probe Pod: %w", err)
	}
	order.SetStatusCondition(registryAccessReadyCondition, metav1.ConditionFalse,
		"Management image source probe terminated before reporting a pull", "ProbeRetry")
	return true, ctrl.Result{RequeueAfter: time.Second}, nil
}

func (r *ClusterOrderReconciler) cleanupManagementRegistryProbe(
	ctx context.Context, order *v1alpha1.ClusterOrder,
) (bool, ctrl.Result, error) {
	if order == nil || !order.Spec.SupportsAutomaticCaaSEndpoints || !r.NetworkingProvisioningEnabled {
		return true, ctrl.Result{}, nil
	}
	key := client.ObjectKey{Namespace: order.Namespace, Name: managementRegistryProbePodName(order)}
	probe := &corev1.Pod{}
	if err := r.Get(ctx, key, probe); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, ctrl.Result{}, fmt.Errorf("get management registry probe Pod for deletion: %w", err)
		}
	} else {
		if managementRegistryProbeOwnedBy(order, probe) {
			if probe.DeletionTimestamp.IsZero() {
				if err := r.Delete(ctx, probe); err != nil && !apierrors.IsNotFound(err) {
					return false, ctrl.Result{}, fmt.Errorf("delete management registry probe Pod: %w", err)
				}
			}
			return false, ctrl.Result{RequeueAfter: registryProbeWait}, nil
		}
	}

	secret := &corev1.Secret{}
	secretKey := client.ObjectKey{
		Namespace: order.Namespace,
		Name:      managementRegistryProbePullSecretName(order),
	}
	if err := r.Get(ctx, secretKey, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return true, ctrl.Result{}, nil
		}
		return false, ctrl.Result{}, fmt.Errorf("confirm management registry pull Secret deletion: %w", err)
	}
	if !managementRegistryProbeSecretOwnedBy(order, secret) {
		// A same-name Secret owned by another principal is outside this
		// ClusterOrder's cleanup scope; never block teardown or delete it.
		return true, ctrl.Result{}, nil
	}
	if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
		return false, ctrl.Result{}, fmt.Errorf("delete management registry pull Secret: %w", err)
	}
	return false, ctrl.Result{RequeueAfter: time.Second}, nil
}

func managementRegistryProbeSecretOwnedBy(order *v1alpha1.ClusterOrder, secret *corev1.Secret) bool {
	if order == nil || secret == nil || secret.Namespace != order.Namespace {
		return false
	}
	owner := metav1.GetControllerOf(secret)
	return owner != nil && owner.UID == order.UID && owner.Name == order.Name
}

func clusterOrderRegistryProbeImage(order *v1alpha1.ClusterOrder) string {
	if order.Spec.ReleaseImage != "" {
		return order.Spec.ReleaseImage
	}
	for _, request := range order.Spec.NodeRequests {
		if request.ReleaseImage != "" {
			return request.ReleaseImage
		}
	}
	return ""
}

func managementRegistryProbeReadyMessage(image string) string {
	return fmt.Sprintf("Management image source pulled ClusterVersion release image %s", image)
}

func managementRegistryProbePodName(order *v1alpha1.ClusterOrder) string {
	sum := sha256.Sum256([]byte(order.Namespace + "/" + order.Name))
	return "osac-registry-probe-" + hex.EncodeToString(sum[:8])
}

func managementRegistryProbePullSecretName(order *v1alpha1.ClusterOrder) string {
	return managementRegistryProbePodName(order) + registryProbePullSecretSuffix
}

func (r *ClusterOrderReconciler) ensureManagementRegistryPullSecret(
	ctx context.Context, order *v1alpha1.ClusterOrder, probeName string,
) error {
	pullSecret, err := parseClusterOrderPullSecret(order)
	if err != nil {
		return fmt.Errorf("parse ClusterOrder pull secret: %w", err)
	}
	if pullSecret == "" {
		return nil
	}
	name := probeName + registryProbePullSecretSuffix
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: order.Namespace}}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if secret.ResourceVersion != "" && !managementRegistryProbeSecretOwnedBy(order, secret) {
			return fmt.Errorf("management registry pull Secret %s is not owned by ClusterOrder %s",
				client.ObjectKeyFromObject(secret), order.Name)
		}
		if err := controllerutil.SetControllerReference(order, secret, r.Scheme); err != nil {
			return fmt.Errorf("set management registry pull Secret owner: %w", err)
		}
		secret.Type = corev1.SecretTypeDockerConfigJson
		secret.Data = map[string][]byte{corev1.DockerConfigJsonKey: []byte(pullSecret)}
		secret.Annotations = map[string]string{
			tenantAnnotation:         order.Annotations[tenantAnnotation],
			ownerReferenceAnnotation: "ClusterOrder/" + order.Name,
		}
		if order.Labels[osacClusterOrderIDLabel] != "" {
			secret.Labels = map[string]string{osacClusterOrderIDLabel: order.Labels[osacClusterOrderIDLabel]}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("ensure management registry pull Secret: %w", err)
	}
	return nil
}

func clusterOrderPullSecret(order *v1alpha1.ClusterOrder) string {
	pullSecret, _ := parseClusterOrderPullSecret(order)
	return pullSecret
}

func parseClusterOrderPullSecret(order *v1alpha1.ClusterOrder) (string, error) {
	if order.Spec.PullSecret != "" {
		return order.Spec.PullSecret, nil
	}
	if order.Spec.TemplateParameters == "" {
		return "", nil
	}
	parameters := map[string]any{}
	if err := json.Unmarshal([]byte(order.Spec.TemplateParameters), &parameters); err != nil {
		return "", fmt.Errorf("invalid templateParameters JSON")
	}
	pullSecret, present := parameters["pull_secret"]
	if !present {
		return "", nil
	}
	value, ok := pullSecret.(string)
	if !ok {
		return "", fmt.Errorf("templateParameters.pull_secret must be a string")
	}
	return value, nil
}

func managementRegistryProbePod(order *v1alpha1.ClusterOrder, name, image string) *corev1.Pod {
	labels := map[string]string{registryProbeLabel: labelValueTrue, osacClusterOrderNameLabel: order.Name}
	if clusterID := order.Labels[osacClusterOrderIDLabel]; clusterID != "" {
		labels[osacClusterOrderIDLabel] = clusterID
	}
	annotations := map[string]string{
		tenantAnnotation:         order.Annotations[tenantAnnotation],
		ownerReferenceAnnotation: "ClusterOrder/" + order.Name,
	}
	probe := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: order.Namespace, Labels: labels, Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                 corev1.RestartPolicyNever,
			AutomountServiceAccountToken:  ptr.To(false),
			TerminationGracePeriodSeconds: ptr.To[int64](0),
			// Keep Command unset so the pulled release image uses its own entrypoint.
			Containers: []corev1.Container{{
				Name:            "registry-image-pull",
				Image:           image,
				ImagePullPolicy: corev1.PullAlways,
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: ptr.To(false),
					ReadOnlyRootFilesystem:   ptr.To(true),
					RunAsNonRoot:             ptr.To(true),
					RunAsUser:                ptr.To[int64](65532),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
			}},
		},
	}
	if clusterOrderPullSecret(order) != "" {
		probe.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: managementRegistryProbePullSecretName(order)}}
	}
	return probe
}

func managementRegistryProbeOwnedBy(order *v1alpha1.ClusterOrder, probe *corev1.Pod) bool {
	if order == nil || probe == nil || probe.Namespace != order.Namespace ||
		probe.Labels[registryProbeLabel] != labelValueTrue || probe.Labels[osacClusterOrderNameLabel] != order.Name ||
		probe.Annotations[tenantAnnotation] != order.Annotations[tenantAnnotation] ||
		probe.Annotations[ownerReferenceAnnotation] != "ClusterOrder/"+order.Name {
		return false
	}
	owner := metav1.GetControllerOf(probe)
	return owner != nil && owner.UID == order.UID && owner.Name == order.Name
}

func managementRegistryProbeContainerStatus(probe *corev1.Pod) *corev1.ContainerStatus {
	if probe == nil {
		return nil
	}
	for i := range probe.Status.ContainerStatuses {
		if probe.Status.ContainerStatuses[i].Name == "registry-image-pull" {
			return &probe.Status.ContainerStatuses[i]
		}
	}
	return nil
}

func managementRegistryProbeFailure(probe *corev1.Pod, status *corev1.ContainerStatus) string {
	if probe == nil {
		return ""
	}
	if status != nil && status.State.Waiting != nil {
		reason := status.State.Waiting.Reason
		if strings.Contains(reason, "ImagePull") || reason == "ErrImagePull" || reason == "CreateContainerConfigError" {
			message := strings.TrimSpace(status.State.Waiting.Message)
			if message != "" {
				return fmt.Sprintf("%s (%s)", reason, message)
			}
			return reason
		}
	}
	if probe.Status.Phase == corev1.PodFailed {
		if probe.Status.Message != "" {
			return strings.TrimSpace(probe.Status.Message)
		}
		return "probe Pod failed before reporting an image pull"
	}
	return ""
}
