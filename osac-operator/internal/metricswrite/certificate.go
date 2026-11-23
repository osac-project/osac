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

package metricswrite

import (
	"errors"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	// CertificateAPIVersion and CertificateKind identify the cert-manager
	// object. The operator builds it as unstructured so it does not take a
	// dependency on the cert-manager API module.
	CertificateAPIVersion = "cert-manager.io/v1"
	CertificateKind       = "Certificate"
	IssuerKind            = "Issuer"
	certManagerGroup      = "cert-manager.io"

	// ClusterOrderLabel marks every object the operator creates for an order,
	// including tenant-cluster Secrets it cannot own through a reference.
	ClusterOrderLabel = "osac.openshift.io/clusterorder"
	// ClusterOrderUIDAnnotation and ClusterOrderNamespaceAnnotation record the
	// order the hub-side Certificate belongs to, so a reused order name cannot
	// adopt a certificate issued for a deleted predecessor.
	ClusterOrderUIDAnnotation       = "osac.openshift.io/clusterorder-uid"
	ClusterOrderNamespaceAnnotation = "osac.openshift.io/clusterorder-namespace"

	certificateNamePrefix = "osac-metrics-write-"
	clientAuthUsage       = "client auth"
	readyConditionType    = "Ready"
	conditionTrue         = "True"
	signingAlgorithm      = "ECDSA"
	signingKeySize        = int64(256)
)

// IssuerReady reports whether a cert-manager Issuer has reported its Ready
// condition as True. The write path is not advertised to a tenant cluster until
// the write CA Issuer can sign.
func IssuerReady(issuer *unstructured.Unstructured) bool {
	if issuer == nil {
		return false
	}
	conditions, found, err := unstructured.NestedSlice(issuer.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok || condition["type"] != readyConditionType {
			continue
		}
		return condition["status"] == conditionTrue
	}
	return false
}

// CertificateOptions carries everything the hub-side client certificate needs.
type CertificateOptions struct {
	// Namespace is where cert-manager runs and signs, normally the
	// observability namespace.
	Namespace string
	// OrderName scopes the Certificate and its Secret. CertificateName derives
	// the object name from it.
	OrderName string
	// OrderNamespace and OrderUID complete the ownership record in annotations.
	OrderNamespace string
	OrderUID       string
	// CommonName is the hosted cluster name. The write proxy derives the
	// cluster label from the verified certificate subject, so it must be the
	// name the store reports.
	CommonName string
	// Issuer is the name of the ready write CA Issuer.
	Issuer string
	// Duration and RenewBefore accept cert-manager duration strings. Empty
	// values leave cert-manager defaults in place.
	Duration    string
	RenewBefore string
}

// CertificateName returns the hub-side object and Secret name for an order.
func CertificateName(orderName string) string {
	return certificateNamePrefix + orderName
}

// ClientCertificate builds the cert-manager Certificate that identifies one
// tenant cluster to the write proxy. Only client auth is allowed; the proxy is
// the server and verifies this certificate against the write CA.
func ClientCertificate(opts CertificateOptions) (*unstructured.Unstructured, error) {
	switch {
	case opts.Namespace == "":
		return nil, errors.New("certificate namespace is required")
	case opts.OrderName == "":
		return nil, errors.New("certificate order name is required")
	case opts.CommonName == "":
		return nil, errors.New("certificate common name is required")
	case opts.Issuer == "":
		return nil, errors.New("certificate issuer is required")
	}

	name := CertificateName(opts.OrderName)
	spec := map[string]any{
		"commonName": opts.CommonName,
		"secretName": name,
		"usages":     []any{clientAuthUsage},
		"privateKey": map[string]any{
			"algorithm": signingAlgorithm,
			"size":      signingKeySize,
		},
		"issuerRef": map[string]any{
			"name":  opts.Issuer,
			"kind":  IssuerKind,
			"group": certManagerGroup,
		},
	}
	if opts.Duration != "" {
		spec["duration"] = opts.Duration
	}
	if opts.RenewBefore != "" {
		spec["renewBefore"] = opts.RenewBefore
	}

	metadata := map[string]any{
		"name":      name,
		"namespace": opts.Namespace,
		"labels":    map[string]any{ClusterOrderLabel: opts.OrderName},
	}
	annotations := map[string]any{}
	if opts.OrderUID != "" {
		annotations[ClusterOrderUIDAnnotation] = opts.OrderUID
	}
	if opts.OrderNamespace != "" {
		annotations[ClusterOrderNamespaceAnnotation] = opts.OrderNamespace
	}
	if len(annotations) > 0 {
		metadata["annotations"] = annotations
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": CertificateAPIVersion,
		"kind":       CertificateKind,
		"metadata":   metadata,
		"spec":       spec,
	}}, nil
}

// ClientSecrets holds the three tenant-cluster Secrets the cluster monitoring
// remote-write TLS configuration references. They live in the tenant cluster,
// so the operator applies them through the tenant kubeconfig and they carry no
// owner reference to the hub-side order.
type ClientSecrets struct {
	CA   *corev1.Secret
	Cert *corev1.Secret
	Key  *corev1.Secret
}

// All returns the Secrets in apply order.
func (s ClientSecrets) All() []*corev1.Secret {
	return []*corev1.Secret{s.CA, s.Cert, s.Key}
}

// NewClientSecrets builds the three Secrets from the keys cert-manager wrote
// into the issued Secret. Separating them keeps the key Secret readable by only
// the components that need it and makes each reference in the monitoring
// configuration name one purpose.
func NewClientSecrets(orderName string, caPEM, certPEM, keyPEM []byte) ClientSecrets {
	newSecret := func(name, dataKey string, value []byte) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: MonitoringNamespace,
				Labels:    map[string]string{ClusterOrderLabel: orderName},
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{dataKey: value},
		}
	}
	return ClientSecrets{
		CA:   newSecret(CABundleSecretName, CAKey, caPEM),
		Cert: newSecret(ClientCertSecretName, CertKey, certPEM),
		Key:  newSecret(ClientKeySecretName, KeyKey, keyPEM),
	}
}
