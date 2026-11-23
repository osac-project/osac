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
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/metricswrite"
)

// MetricsWriteConfig carries global.observability.clusterRemoteWrite plus the
// scrape interval. The zero value disables tenant remote write.
type MetricsWriteConfig struct {
	Enabled             bool
	Namespace           string
	WriteURL            string
	Issuer              string
	RevocationConfigMap string
	CertificateDuration string
	RenewBefore         string
	ScrapeInterval      string
	KeepKubePrefix      string
	DropPrefixes        []string
}

// Validate reports whether an enabled configuration carries every value the
// tenant remote-write configuration is built from.
func (c MetricsWriteConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	missing := []string{}
	for name, value := range map[string]string{
		"observability namespace": c.Namespace,
		"write URL":               c.WriteURL,
		"write CA issuer":         c.Issuer,
		"revocation ConfigMap":    c.RevocationConfigMap,
		"certificate duration":    c.CertificateDuration,
		"certificate renewBefore": c.RenewBefore,
		"scrape interval":         c.ScrapeInterval,
		"keep kube prefix":        c.KeepKubePrefix,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("tenant remote write is enabled but incomplete: missing %s", strings.Join(missing, ", "))
	}
	return nil
}

// reconcileClusterMetricsWrite issues and installs the tenant cluster's client
// identity, then merges the OSAC remote-write entry into the cluster's
// monitoring configuration. It requeues while the write CA, cert-manager or the
// tenant cluster are not ready, and keeps polling after success so a renewed
// certificate reaches the tenant cluster.
func (r *ClusterOrderReconciler) reconcileClusterMetricsWrite(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
	hc *hypershiftv1beta1.HostedCluster,
) (ctrl.Result, error) {
	if !r.MetricsWrite.Enabled {
		return ctrl.Result{}, nil
	}
	if hc == nil || !hostedClusterControlPlaneIsAvailable(hc) {
		return ctrl.Result{}, nil
	}
	log := ctrllog.FromContext(ctx).WithValues("clusterorder", instance.GetName())
	requeue := ctrl.Result{RequeueAfter: r.StatusPollInterval}

	issuer := &unstructured.Unstructured{}
	issuer.SetAPIVersion(metricswrite.CertificateAPIVersion)
	issuer.SetKind(metricswrite.IssuerKind)
	issuerKey := client.ObjectKey{Namespace: r.MetricsWrite.Namespace, Name: r.MetricsWrite.Issuer}
	if err := r.apiReader.Get(ctx, issuerKey, issuer); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("write CA issuer is not installed yet; waiting")
			return requeue, nil
		}
		return ctrl.Result{}, fmt.Errorf("get write CA issuer %s: %w", issuerKey, err)
	}
	if !metricswrite.IssuerReady(issuer) {
		log.Info("write CA issuer is not ready; waiting")
		return requeue, nil
	}

	guest, ready, reachable, err := r.guestClusterClient(ctx, hc)
	if err != nil {
		return ctrl.Result{}, err
	}
	if guest == nil || !reachable || !ready {
		// Cluster monitoring only runs once a node is Ready, and the write
		// entry would point at an endpoint that does not exist yet.
		return requeue, nil
	}

	certificateName := metricswrite.CertificateName(instance.GetName())
	if err := r.ensureMetricsWriteCertificate(ctx, instance, hc, certificateName); err != nil {
		return ctrl.Result{}, err
	}

	issued := &corev1.Secret{}
	if err := r.apiReader.Get(ctx, client.ObjectKey{
		Namespace: r.MetricsWrite.Namespace,
		Name:      certificateName,
	}, issued); err != nil {
		if apierrors.IsNotFound(err) {
			// cert-manager has not written the issued Secret yet.
			return requeue, nil
		}
		return ctrl.Result{}, fmt.Errorf("get issued client Secret %s: %w", certificateName, err)
	}
	caPEM, certPEM, keyPEM := issued.Data[metricswrite.CAKey], issued.Data[metricswrite.CertKey], issued.Data[metricswrite.KeyKey]
	if len(caPEM) == 0 || len(certPEM) == 0 || len(keyPEM) == 0 {
		return requeue, nil
	}

	if err := r.applyTenantMetricsSecrets(ctx, guest, instance.GetName(), caPEM, certPEM, keyPEM); err != nil {
		if isTransientTenantError(err) {
			log.Info("tenant cluster is not accepting writes yet", "error", err.Error())
			return requeue, nil
		}
		return ctrl.Result{}, err
	}
	if err := r.applyTenantMonitoringConfig(ctx, guest, hc); err != nil {
		if isTransientTenantError(err) {
			log.Info("tenant cluster is not accepting writes yet", "error", err.Error())
			return requeue, nil
		}
		return ctrl.Result{}, err
	}
	return requeue, nil
}

// ensureMetricsWriteCertificate creates the cert-manager Certificate, or
// updates it when a reused order name would otherwise keep an identity for a
// different hosted cluster.
func (r *ClusterOrderReconciler) ensureMetricsWriteCertificate(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
	hc *hypershiftv1beta1.HostedCluster,
	certificateName string,
) error {
	desired, err := metricswrite.ClientCertificate(metricswrite.CertificateOptions{
		Namespace:      r.MetricsWrite.Namespace,
		OrderName:      instance.GetName(),
		OrderNamespace: instance.GetNamespace(),
		OrderUID:       string(instance.GetUID()),
		CommonName:     hc.GetName(),
		Issuer:         r.MetricsWrite.Issuer,
		Duration:       r.MetricsWrite.CertificateDuration,
		RenewBefore:    r.MetricsWrite.RenewBefore,
	})
	if err != nil {
		return err
	}

	existing := &unstructured.Unstructured{}
	existing.SetAPIVersion(metricswrite.CertificateAPIVersion)
	existing.SetKind(metricswrite.CertificateKind)
	key := client.ObjectKey{Namespace: r.MetricsWrite.Namespace, Name: certificateName}
	if err := r.apiReader.Get(ctx, key, existing); err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.Client.Create(ctx, desired); err != nil {
				return fmt.Errorf("create client Certificate %s: %w", key, err)
			}
			return nil
		}
		return fmt.Errorf("get client Certificate %s: %w", key, err)
	}

	commonName, _, _ := unstructured.NestedString(existing.Object, "spec", "commonName")
	if commonName == hc.GetName() {
		return nil
	}
	// The order name outlived the hosted cluster it named. Replace the
	// identity rather than silently writing metrics under the old name.
	desired.SetResourceVersion(existing.GetResourceVersion())
	if err := r.Client.Update(ctx, desired); err != nil {
		return fmt.Errorf("update client Certificate %s: %w", key, err)
	}
	return nil
}

func (r *ClusterOrderReconciler) applyTenantMetricsSecrets(
	ctx context.Context,
	guest kubernetes.Interface,
	orderName string,
	caPEM, certPEM, keyPEM []byte,
) error {
	secrets := guest.CoreV1().Secrets(metricswrite.MonitoringNamespace)
	for _, desired := range metricswrite.NewClientSecrets(orderName, caPEM, certPEM, keyPEM).All() {
		existing, err := secrets.Get(ctx, desired.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			if _, err := secrets.Create(ctx, desired, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("create tenant Secret %s/%s: %w", desired.Namespace, desired.Name, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("get tenant Secret %s/%s: %w", desired.Namespace, desired.Name, err)
		}
		if secretDataEqual(existing.Data, desired.Data) {
			continue
		}
		existing.Data = desired.Data
		if existing.Labels == nil {
			existing.Labels = map[string]string{}
		}
		for name, value := range desired.Labels {
			existing.Labels[name] = value
		}
		if _, err := secrets.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("update tenant Secret %s/%s: %w", desired.Namespace, desired.Name, err)
		}
	}
	return nil
}

func (r *ClusterOrderReconciler) applyTenantMonitoringConfig(
	ctx context.Context,
	guest kubernetes.Interface,
	hc *hypershiftv1beta1.HostedCluster,
) error {
	options := metricswrite.MonitoringOptions{
		ClusterName:    hc.GetName(),
		WriteURL:       r.MetricsWrite.WriteURL,
		ScrapeInterval: r.MetricsWrite.ScrapeInterval,
		KeepKubePrefix: r.MetricsWrite.KeepKubePrefix,
		DropPrefixes:   r.MetricsWrite.DropPrefixes,
	}
	configMaps := guest.CoreV1().ConfigMaps(metricswrite.MonitoringNamespace)
	existing, err := configMaps.Get(ctx, metricswrite.MonitoringConfigName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		merged, mergeErr := metricswrite.MergeMonitoringConfig(nil, options)
		if mergeErr != nil {
			return mergeErr
		}
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      metricswrite.MonitoringConfigName,
				Namespace: metricswrite.MonitoringNamespace,
			},
			Data: map[string]string{metricswrite.MonitoringConfigKey: string(merged)},
		}
		if _, err := configMaps.Create(ctx, configMap, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create tenant %s ConfigMap: %w", metricswrite.MonitoringConfigName, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get tenant %s ConfigMap: %w", metricswrite.MonitoringConfigName, err)
	}

	current := existing.Data[metricswrite.MonitoringConfigKey]
	merged, err := metricswrite.MergeMonitoringConfig([]byte(current), options)
	if err != nil {
		return err
	}
	if string(merged) == current {
		return nil
	}
	if existing.Data == nil {
		existing.Data = map[string]string{}
	}
	existing.Data[metricswrite.MonitoringConfigKey] = string(merged)
	if _, err := configMaps.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update tenant %s ConfigMap: %w", metricswrite.MonitoringConfigName, err)
	}
	return nil
}

// revokeClusterMetricsWrite withdraws the tenant identity in the order the
// proxy relies on: record the serial, delete the Certificate, then remove the
// tenant's remote-write entry. It reports whether teardown may continue.
func (r *ClusterOrderReconciler) revokeClusterMetricsWrite(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
) (bool, ctrl.Result, error) {
	if !r.MetricsWrite.Enabled {
		return true, ctrl.Result{}, nil
	}
	log := ctrllog.FromContext(ctx).WithValues("clusterorder", instance.GetName())
	requeue := ctrl.Result{RequeueAfter: r.StatusPollInterval}
	certificateName := metricswrite.CertificateName(instance.GetName())

	// 1. Read the serial from the issued Secret before the Certificate (and
	// with it the Secret) is deleted: the Secret is the only record.
	serial := ""
	issued := &corev1.Secret{}
	err := r.apiReader.Get(ctx, client.ObjectKey{
		Namespace: r.MetricsWrite.Namespace,
		Name:      certificateName,
	}, issued)
	switch {
	case apierrors.IsNotFound(err):
		// Already gone: nothing to denylist, but the rest still has to run.
	case err != nil:
		return false, ctrl.Result{}, fmt.Errorf("get issued client Secret %s: %w", certificateName, err)
	default:
		serial, err = metricswrite.ParseSerial(issued.Data[metricswrite.CertKey])
		if err != nil {
			return false, ctrl.Result{}, fmt.Errorf("read serial from issued client Secret %s: %w", certificateName, err)
		}
	}

	// 2. Record the serial before the Certificate is deleted.
	if serial != "" {
		if err := r.appendRevokedSerial(ctx, serial); err != nil {
			return false, ctrl.Result{}, err
		}
	}

	// 3. Delete the Certificate.
	certificate := &unstructured.Unstructured{}
	certificate.SetAPIVersion(metricswrite.CertificateAPIVersion)
	certificate.SetKind(metricswrite.CertificateKind)
	certificate.SetNamespace(r.MetricsWrite.Namespace)
	certificate.SetName(certificateName)
	if err := r.Client.Delete(ctx, certificate); err != nil && !apierrors.IsNotFound(err) {
		return false, ctrl.Result{}, fmt.Errorf("delete client Certificate %s/%s: %w",
			r.MetricsWrite.Namespace, certificateName, err)
	}

	// 4. Remove the tenant's remote-write entry while the cluster is reachable.
	ns, err := r.findNamespace(ctx, instance)
	if err != nil {
		return false, ctrl.Result{}, err
	}
	if ns == nil {
		return true, ctrl.Result{}, nil
	}
	hc, err := r.findHostedCluster(ctx, instance, ns.GetName())
	if err != nil {
		return false, ctrl.Result{}, err
	}
	if hc == nil {
		return true, ctrl.Result{}, nil
	}
	guest, _, reachable, err := r.guestClusterClient(ctx, hc)
	if err != nil {
		return false, ctrl.Result{}, err
	}
	if guest == nil || !reachable {
		// The identity is already revoked; a departing cluster must not block
		// the order's teardown.
		log.Info("tenant cluster is not reachable; skipping remote-write removal")
		return true, ctrl.Result{}, nil
	}
	if err := removeTenantMonitoringEntry(ctx, guest); err != nil {
		if isTransientTenantError(err) {
			log.Info("tenant cluster is not accepting writes yet", "error", err.Error())
			return false, requeue, nil
		}
		return false, ctrl.Result{}, err
	}
	return true, ctrl.Result{}, nil
}

func (r *ClusterOrderReconciler) appendRevokedSerial(ctx context.Context, serial string) error {
	configMap := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: r.MetricsWrite.Namespace, Name: r.MetricsWrite.RevocationConfigMap}
	if err := r.apiReader.Get(ctx, key, configMap); err != nil {
		return fmt.Errorf("get write revocation ConfigMap %s: %w", key, err)
	}
	current := configMap.Data[metricswrite.RevocationKey]
	updated, err := metricswrite.AppendSerial(current, serial)
	if err != nil {
		return fmt.Errorf("record revoked serial %s: %w", serial, err)
	}
	if updated == current {
		return nil
	}
	if configMap.Data == nil {
		configMap.Data = map[string]string{}
	}
	configMap.Data[metricswrite.RevocationKey] = updated
	if err := r.Client.Update(ctx, configMap); err != nil {
		return fmt.Errorf("update write revocation ConfigMap %s: %w", key, err)
	}
	return nil
}

func removeTenantMonitoringEntry(ctx context.Context, guest kubernetes.Interface) error {
	configMaps := guest.CoreV1().ConfigMaps(metricswrite.MonitoringNamespace)
	existing, err := configMaps.Get(ctx, metricswrite.MonitoringConfigName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get tenant %s ConfigMap: %w", metricswrite.MonitoringConfigName, err)
	}
	current := existing.Data[metricswrite.MonitoringConfigKey]
	updated, err := metricswrite.RemoveMonitoringEntry([]byte(current))
	if err != nil {
		return err
	}
	if string(updated) == current {
		return nil
	}
	if existing.Data == nil {
		existing.Data = map[string]string{}
	}
	existing.Data[metricswrite.MonitoringConfigKey] = string(updated)
	if _, err := configMaps.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update tenant %s ConfigMap: %w", metricswrite.MonitoringConfigName, err)
	}
	return nil
}

func isTransientTenantError(err error) bool {
	return apierrors.IsServiceUnavailable(err) || apierrors.IsTimeout(err) ||
		apierrors.IsTooManyRequests(err) || isGuestAPITransportError(err) ||
		errors.Is(err, context.DeadlineExceeded)
}

func secretDataEqual(current, desired map[string][]byte) bool {
	if len(current) != len(desired) {
		return false
	}
	for name, value := range desired {
		if !bytes.Equal(current[name], value) {
			return false
		}
	}
	return true
}
