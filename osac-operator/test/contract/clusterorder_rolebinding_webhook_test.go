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

package contract

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func renderOperatorChartDocuments(t *testing.T, setValues ...string) []map[string]any {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Fatalf("helm binary is required for the operator chart contract: %v", err)
	}
	chartDir := filepath.Join(repoRoot(), "osac-operator/charts/operator")
	args := make([]string, 0, 5+2*len(setValues))
	args = append(args, "template", "osac-operator", chartDir, "--namespace", "osac-operator")
	for _, value := range setValues {
		args = append(args, "--set", value)
	}
	output, err := exec.Command(helm, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\noutput:\n%s", err, string(output))
	}

	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(output), 4096)
	var documents []map[string]any
	for {
		var document map[string]any
		if err := decoder.Decode(&document); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode rendered operator chart: %v", err)
		}
		if len(document) != 0 {
			documents = append(documents, document)
		}
	}
	return documents
}

func TestClusterOrderAdminRoleBindingWebhookIsManagerScopedAndFailClosed(t *testing.T) {
	documents := renderOperatorChartDocuments(t)
	assertManagerCanCreateDelegatedAdminBinding(t, renderedManagerRole(t, documents))
	webhookConfig := renderedClusterOrderWebhook(t, documents)
	assertClusterOrderWebhookPolicy(t, webhookConfig)
	assertClusterOrderWebhookServiceAndCertificate(t, documents, webhookConfig)
	assertClusterOrderWebhookDeployment(t, documents)
}

func renderedClusterOrderWebhook(
	t *testing.T,
	documents []map[string]any,
) admissionregistrationv1.ValidatingWebhookConfiguration {
	t.Helper()
	webhookDocument := findRenderedDocument(
		t, documents, "ValidatingWebhookConfiguration", "osac-operator-clusterorder-rbac-webhook",
	)
	encoded, err := json.Marshal(webhookDocument)
	if err != nil {
		t.Fatalf("encode webhook configuration: %v", err)
	}
	var webhookConfig admissionregistrationv1.ValidatingWebhookConfiguration
	if err := json.Unmarshal(encoded, &webhookConfig); err != nil {
		t.Fatalf("decode webhook configuration: %v", err)
	}
	return webhookConfig
}

func assertClusterOrderWebhookPolicy(t *testing.T, config admissionregistrationv1.ValidatingWebhookConfiguration) {
	t.Helper()
	if config.APIVersion != "admissionregistration.k8s.io/v1" || config.Kind != "ValidatingWebhookConfiguration" {
		t.Fatalf("unexpected webhook type: %s %s", config.APIVersion, config.Kind)
	}
	if len(config.Webhooks) != 1 {
		t.Fatalf("rendered %d webhooks, want 1", len(config.Webhooks))
	}
	webhook := config.Webhooks[0]
	if webhook.FailurePolicy == nil || *webhook.FailurePolicy != admissionregistrationv1.Fail {
		t.Fatalf("failurePolicy = %v, want Fail", webhook.FailurePolicy)
	}
	if webhook.SideEffects == nil || *webhook.SideEffects != admissionregistrationv1.SideEffectClassNone {
		t.Fatalf("sideEffects = %v, want None", webhook.SideEffects)
	}
	if webhook.ClientConfig.Service == nil || webhook.ClientConfig.Service.Path == nil ||
		*webhook.ClientConfig.Service.Path != "/validate-clusterorder-admin-rolebinding" {
		t.Fatalf("webhook service path = %+v, want the ClusterOrder RoleBinding path", webhook.ClientConfig.Service)
	}
	if len(webhook.MatchConditions) != 1 || !strings.Contains(
		webhook.MatchConditions[0].Expression,
		"system:serviceaccount:osac-operator:osac-operator",
	) {
		t.Fatalf("matchConditions = %+v, want only the operator manager identity", webhook.MatchConditions)
	}
	if len(webhook.Rules) != 1 || len(webhook.Rules[0].Resources) != 1 ||
		webhook.Rules[0].Resources[0] != "rolebindings" {
		t.Fatalf("webhook rules = %+v, want namespaced RoleBinding create/update validation", webhook.Rules)
	}
	if webhook.Rules[0].Scope == nil || *webhook.Rules[0].Scope != admissionregistrationv1.NamespacedScope {
		t.Fatalf("webhook scope = %v, want Namespaced", webhook.Rules[0].Scope)
	}
	if webhook.ClientConfig.Service.Name != "osac-operator-clusterorder-rbac" ||
		webhook.ClientConfig.Service.Namespace != "osac-operator" || webhook.ClientConfig.Service.Port == nil ||
		*webhook.ClientConfig.Service.Port != 443 {
		t.Fatalf("webhook service reference = %+v", webhook.ClientConfig.Service)
	}
}

func assertClusterOrderWebhookServiceAndCertificate(
	t *testing.T,
	documents []map[string]any,
	config admissionregistrationv1.ValidatingWebhookConfiguration,
) {
	t.Helper()
	serviceDocument := findRenderedDocument(t, documents, "Service", "osac-operator-clusterorder-rbac")
	serviceJSON, err := json.Marshal(serviceDocument)
	if err != nil {
		t.Fatalf("encode webhook Service: %v", err)
	}
	var service corev1.Service
	if err := json.Unmarshal(serviceJSON, &service); err != nil {
		t.Fatalf("decode webhook Service: %v", err)
	}
	if len(service.Spec.Ports) != 1 || service.Spec.Ports[0].Port != 443 ||
		service.Spec.Ports[0].TargetPort.StrVal != "webhook" {
		t.Fatalf("webhook Service ports = %+v", service.Spec.Ports)
	}

	certificate := findRenderedDocument(t, documents, "Certificate", "osac-operator-clusterorder-rbac")
	certificateSpec, ok := certificate["spec"].(map[string]any)
	if !ok || certificateSpec["secretName"] != "osac-operator-clusterorder-rbac-tls" {
		t.Fatalf("webhook Certificate secretName = %v", certificateSpec["secretName"])
	}
	if config.Annotations["cert-manager.io/inject-ca-from"] != "osac-operator/osac-operator-clusterorder-rbac" {
		t.Fatalf("CA injection annotation = %q", config.Annotations["cert-manager.io/inject-ca-from"])
	}
}

func assertClusterOrderWebhookDeployment(t *testing.T, documents []map[string]any) {
	t.Helper()
	deploymentDocument := findRenderedDocument(t, documents, "Deployment", "osac-operator")
	deploymentJSON, err := json.Marshal(deploymentDocument)
	if err != nil {
		t.Fatalf("encode operator Deployment: %v", err)
	}
	var deployment appsv1.Deployment
	if err := json.Unmarshal(deploymentJSON, &deployment); err != nil {
		t.Fatalf("decode operator Deployment: %v", err)
	}
	manager, foundManager := findManagerContainerForWebhook(deployment)
	if !foundManager {
		t.Fatal("operator manager container not found")
	}
	if !containerHasPort(manager, "webhook", 9443) ||
		!containerMountsPath(manager, "clusterorder-webhook-tls", "/etc/webhook/tls") {
		t.Fatalf("operator manager webhook port/mount missing: ports=%+v mounts=%+v", manager.Ports, manager.VolumeMounts)
	}
	certDir := findContainerEnv(manager, "OSAC_WEBHOOK_CERT_DIR")
	if certDir == nil || certDir.Value != "/etc/webhook/tls" {
		t.Fatalf("webhook certificate directory env = %+v", certDir)
	}
	operatorNamespace := findContainerEnv(manager, "OSAC_OPERATOR_NAMESPACE")
	if operatorNamespace == nil || operatorNamespace.ValueFrom == nil ||
		operatorNamespace.ValueFrom.FieldRef == nil ||
		operatorNamespace.ValueFrom.FieldRef.FieldPath != "metadata.namespace" {
		t.Fatalf("operator namespace identity env = %+v", operatorNamespace)
	}
	operatorServiceAccount := findContainerEnv(manager, "OSAC_OPERATOR_SERVICE_ACCOUNT")
	if operatorServiceAccount == nil || operatorServiceAccount.ValueFrom == nil ||
		operatorServiceAccount.ValueFrom.FieldRef == nil ||
		operatorServiceAccount.ValueFrom.FieldRef.FieldPath != "spec.serviceAccountName" {
		t.Fatalf("operator ServiceAccount identity env = %+v", operatorServiceAccount)
	}
}

func assertManagerCanCreateDelegatedAdminBinding(t *testing.T, role clusterRole) {
	t.Helper()
	if !clusterRoleGrantsAnyName(role, "rbac.authorization.k8s.io", "rolebindings", "create") ||
		!clusterRoleGrantsAnyName(role, "rbac.authorization.k8s.io", "clusterroles", "bind") {
		t.Fatal("ClusterOrder controller is enabled but manager RBAC cannot create RoleBindings and bind admin")
	}
}

func TestClusterOrderAdminRoleBindingWebhookIsOmittedWhenControllerIsDisabled(t *testing.T) {
	documents := renderOperatorChartDocuments(t, "controllers.clusterOrder=false")
	managerRole := renderedManagerRole(t, documents)
	if clusterRoleGrantsAnyName(managerRole, "rbac.authorization.k8s.io", "rolebindings", "create") ||
		clusterRoleGrantsAnyName(managerRole, "rbac.authorization.k8s.io", "clusterroles", "bind") {
		t.Fatal("manager RoleBinding creation and admin bind permissions must be absent when ClusterOrder is disabled")
	}
	for _, document := range documents {
		if isNamedDocument(document, "ValidatingWebhookConfiguration", "osac-operator-clusterorder-rbac-webhook") ||
			isNamedDocument(document, "Service", "osac-operator-clusterorder-rbac") ||
			isNamedDocument(document, "Certificate", "osac-operator-clusterorder-rbac") {
			t.Fatal("ClusterOrder RoleBinding webhook rendered with the controller disabled")
		}
	}
	deploymentDocument := findRenderedDocument(t, documents, "Deployment", "osac-operator")
	deploymentJSON, err := json.Marshal(deploymentDocument)
	if err != nil {
		t.Fatalf("encode operator Deployment: %v", err)
	}
	var deployment appsv1.Deployment
	if err := json.Unmarshal(deploymentJSON, &deployment); err != nil {
		t.Fatalf("decode operator Deployment: %v", err)
	}
	manager, foundManager := findManagerContainerForWebhook(deployment)
	if !foundManager {
		t.Fatal("operator manager container not found")
	}
	for _, env := range manager.Env {
		if env.Name == "OSAC_WEBHOOK_CERT_DIR" {
			t.Fatal("webhook certificate directory was configured with ClusterOrder disabled")
		}
	}
	if containerMountsPath(manager, "clusterorder-webhook-tls", "/etc/webhook/tls") {
		t.Fatal("webhook certificate mount rendered with ClusterOrder disabled")
	}
}

func TestStandaloneClusterOrderWebhookBundleWiresAdmissionAndManager(t *testing.T) {
	documents := renderStandaloneWebhookDocuments(t)
	assertStandaloneWebhook(t, documents)
	assertStandaloneWebhookService(t, documents)
	assertStandaloneWebhookCertificate(t, documents)
	assertStandaloneManagerPatch(t)
}

func assertStandaloneWebhook(t *testing.T, documents []map[string]any) {
	t.Helper()
	webhookDocument := findRenderedDocument(
		t, documents, "ValidatingWebhookConfiguration", "osac-operator-clusterorder-rbac-webhook",
	)
	encodedWebhook, err := json.Marshal(webhookDocument)
	if err != nil {
		t.Fatalf("encode standalone webhook configuration: %v", err)
	}
	var webhookConfig admissionregistrationv1.ValidatingWebhookConfiguration
	if err := json.Unmarshal(encodedWebhook, &webhookConfig); err != nil {
		t.Fatalf("decode standalone webhook configuration: %v", err)
	}
	if len(webhookConfig.Webhooks) != 1 {
		t.Fatalf("standalone webhook count = %d, want 1", len(webhookConfig.Webhooks))
	}
	webhook := webhookConfig.Webhooks[0]
	if webhook.FailurePolicy == nil || *webhook.FailurePolicy != admissionregistrationv1.Fail {
		t.Fatalf("standalone failurePolicy = %v, want Fail", webhook.FailurePolicy)
	}
	if webhook.SideEffects == nil || *webhook.SideEffects != admissionregistrationv1.SideEffectClassNone {
		t.Fatalf("standalone sideEffects = %v, want None", webhook.SideEffects)
	}
	if webhook.ClientConfig.Service == nil || webhook.ClientConfig.Service.Path == nil ||
		*webhook.ClientConfig.Service.Path != "/validate-clusterorder-admin-rolebinding" {
		t.Fatalf("standalone webhook client config = %+v", webhook.ClientConfig)
	}
	if webhookConfig.Annotations["cert-manager.io/inject-ca-from"] != "osac-devel/osac-operator-clusterorder-rbac" {
		t.Fatalf("standalone CA injection annotation = %q", webhookConfig.Annotations["cert-manager.io/inject-ca-from"])
	}
	if webhook.ClientConfig.Service.Namespace != "osac-devel" ||
		webhook.ClientConfig.Service.Name != "osac-operator-clusterorder-rbac" {
		t.Fatalf("standalone webhook service reference = %+v", webhook.ClientConfig.Service)
	}
	if len(webhook.MatchConditions) != 1 || !strings.Contains(
		webhook.MatchConditions[0].Expression,
		"system:serviceaccount:osac-devel:",
	) {
		t.Fatalf("standalone matchConditions = %+v", webhook.MatchConditions)
	}
}

func assertStandaloneWebhookService(t *testing.T, documents []map[string]any) {
	t.Helper()
	serviceDocument := findRenderedDocument(t, documents, "Service", "osac-operator-clusterorder-rbac")
	serviceSpec, ok := serviceDocument["spec"].(map[string]any)
	if !ok {
		t.Fatal("standalone webhook Service has no spec")
	}
	serviceSelector, ok := serviceSpec["selector"].(map[string]any)
	if !ok || serviceSelector["osac.openshift.io/clusterorder-rbac-webhook"] != "true" {
		t.Fatalf("standalone webhook Service selector = %v", serviceSpec["selector"])
	}
}

func assertStandaloneWebhookCertificate(t *testing.T, documents []map[string]any) {
	t.Helper()
	certificate := findRenderedDocument(t, documents, "Certificate", "osac-operator-clusterorder-rbac")
	certificateSpec, ok := certificate["spec"].(map[string]any)
	if !ok || certificateSpec["secretName"] != "osac-operator-clusterorder-rbac-tls" {
		t.Fatalf("standalone webhook Certificate spec = %v", certificate["spec"])
	}
	dnsNames, ok := certificateSpec["dnsNames"].([]any)
	if !ok || !containsString(dnsNames, "osac-operator-clusterorder-rbac.osac-devel.svc") {
		t.Fatalf("standalone webhook certificate DNS names = %v", certificateSpec["dnsNames"])
	}
}

func assertStandaloneManagerPatch(t *testing.T) {
	t.Helper()
	patchPath := filepath.Join(repoRoot(), "osac-operator/config/webhook/manager-patch.yaml")
	patchData, err := os.ReadFile(patchPath)
	if err != nil {
		t.Fatalf("read legacy manager patch: %v", err)
	}
	var managerPatch appsv1.Deployment
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(patchData), 4096).Decode(&managerPatch); err != nil {
		t.Fatalf("decode legacy manager patch: %v", err)
	}
	if managerPatch.Name != "osac-operator-controller-manager" {
		t.Fatalf("legacy deployment patch name = %q", managerPatch.Name)
	}
	manager, found := findManagerContainerForWebhook(managerPatch)
	if !found || !containerHasPort(manager, "webhook", 9443) ||
		!containerMountsPath(manager, "clusterorder-webhook-tls", "/etc/webhook/tls") {
		t.Fatalf("legacy manager patch lacks webhook port or certificate mount: %+v", manager)
	}
	assertManagerWebhookEnv(t, manager)
	if managerPatch.Spec.Template.Labels["osac.openshift.io/clusterorder-rbac-webhook"] != "true" {
		t.Fatalf("legacy manager pod selector label = %v", managerPatch.Spec.Template.Labels)
	}
	if !volumeUsesSecret(
		managerPatch.Spec.Template.Spec.Volumes,
		"clusterorder-webhook-tls",
		"osac-operator-clusterorder-rbac-tls",
	) {
		t.Fatalf("legacy manager patch lacks webhook TLS Secret volume: %+v", managerPatch.Spec.Template.Spec.Volumes)
	}
}

func assertManagerWebhookEnv(t *testing.T, manager corev1.Container) {
	t.Helper()
	for _, name := range []string{
		"OSAC_WEBHOOK_CERT_DIR",
		"OSAC_OPERATOR_NAMESPACE",
		"OSAC_OPERATOR_SERVICE_ACCOUNT",
		"OSAC_CLUSTER_ORDER_NAMESPACE",
	} {
		if findContainerEnv(manager, name) == nil {
			t.Errorf("legacy manager patch lacks %s environment", name)
		}
	}
}

func renderStandaloneWebhookDocuments(t *testing.T) []map[string]any {
	t.Helper()
	kubectl, err := exec.LookPath("kubectl")
	if err != nil {
		t.Fatalf("kubectl binary is required for the standalone webhook contract: %v", err)
	}
	webhookDir := filepath.Join(repoRoot(), "osac-operator/config/webhook")
	output, err := exec.Command(kubectl, "kustomize", webhookDir).CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl kustomize failed: %v\noutput:\n%s", err, string(output))
	}

	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(output), 4096)
	var documents []map[string]any
	for {
		var document map[string]any
		if err := decoder.Decode(&document); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode standalone webhook bundle: %v", err)
		}
		if len(document) != 0 {
			documents = append(documents, document)
		}
	}
	return documents
}

func containsString(values []any, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func volumeUsesSecret(volumes []corev1.Volume, name, secretName string) bool {
	for _, volume := range volumes {
		if volume.Name == name && volume.Secret != nil && volume.Secret.SecretName == secretName {
			return true
		}
	}
	return false
}

func renderedManagerRole(t *testing.T, documents []map[string]any) clusterRole {
	t.Helper()
	document := findRenderedDocument(t, documents, "ClusterRole", "osac-operator-manager")
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode manager ClusterRole: %v", err)
	}
	var role clusterRole
	if err := json.Unmarshal(encoded, &role); err != nil {
		t.Fatalf("decode manager ClusterRole: %v", err)
	}
	return role
}

func findRenderedDocument(t *testing.T, documents []map[string]any, kind, name string) map[string]any {
	t.Helper()
	for _, document := range documents {
		if isNamedDocument(document, kind, name) {
			return document
		}
	}
	t.Fatalf("rendered %s/%s not found", kind, name)
	return nil
}

func isNamedDocument(document map[string]any, kind, name string) bool {
	if document["kind"] != kind {
		return false
	}
	metadata, ok := document["metadata"].(map[string]any)
	if !ok {
		return false
	}
	return metadata["name"] == name
}

func findManagerContainerForWebhook(deployment appsv1.Deployment) (corev1.Container, bool) {
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name == "manager" {
			return container, true
		}
	}
	return corev1.Container{}, false
}

func findContainerEnv(container corev1.Container, name string) *corev1.EnvVar {
	for i := range container.Env {
		if container.Env[i].Name == name {
			return &container.Env[i]
		}
	}
	return nil
}

func containerHasPort(container corev1.Container, name string, port int32) bool {
	for _, containerPort := range container.Ports {
		if containerPort.Name == name && containerPort.ContainerPort == port {
			return true
		}
	}
	return false
}

func containerMountsPath(container corev1.Container, volumeName, path string) bool {
	for _, mount := range container.VolumeMounts {
		if mount.Name == volumeName && mount.MountPath == path && mount.ReadOnly {
			return true
		}
	}
	return false
}
