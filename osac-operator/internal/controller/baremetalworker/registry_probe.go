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

package baremetalworker

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	workerRegistryProbeCondition        = "RegistryAccessReady"
	workerRegistryProbeNamespacePrefix  = "osac-probes-"
	workerRegistryProbeKubeconfigSecret = "service-network-admin-kubeconfig"
	workerRegistryProbeRequestKey       = "request"
	workerRegistryProbeRequestedValue   = "requested"
	workerRegistryProbeStatusKey        = "status"
	workerRegistryProbeImageKey         = "image"
	workerRegistryProbeReasonKey        = "reason"
	workerRegistryProbeMessageKey       = "message"
	workerRegistryProbeTokenLifetime    = time.Hour
	workerRegistryProbePollInterval     = 10 * time.Second
)

type registryProbeGuestAPI struct {
	client kubernetes.Interface
	apiURL string
	caData []byte
}

type registryProbeGuestResolver interface {
	Resolve(context.Context, *v1alpha1.ClusterOrder) (registryProbeGuestAPI, error)
}

type hostedClusterRegistryProbeResolver struct {
	management client.Reader
}

func (r hostedClusterRegistryProbeResolver) Resolve(
	ctx context.Context, order *v1alpha1.ClusterOrder,
) (registryProbeGuestAPI, error) {
	if r.management == nil || order == nil || order.Status.ClusterReference == nil ||
		order.Status.ClusterReference.Namespace == "" || order.Status.ClusterReference.HostedClusterName == "" {
		return registryProbeGuestAPI{}, fmt.Errorf("HostedCluster reference is unavailable for registry probe")
	}
	ref := order.Status.ClusterReference
	hcpNamespace := ref.Namespace + "-" + ref.HostedClusterName
	secret := &corev1.Secret{}
	if err := r.management.Get(ctx, client.ObjectKey{Namespace: hcpNamespace, Name: workerRegistryProbeKubeconfigSecret}, secret); err != nil {
		return registryProbeGuestAPI{}, fmt.Errorf("read HostedControlPlane registry probe kubeconfig: %w", err)
	}
	data := secret.Data["kubeconfig"]
	if len(data) == 0 {
		return registryProbeGuestAPI{}, fmt.Errorf("HostedControlPlane registry probe kubeconfig is empty")
	}
	config, err := clientcmd.Load(data)
	if err != nil {
		return registryProbeGuestAPI{}, fmt.Errorf("parse HostedControlPlane registry probe kubeconfig: %w", err)
	}
	currentContext := config.Contexts[config.CurrentContext]
	if currentContext == nil || currentContext.Cluster == "" {
		return registryProbeGuestAPI{}, fmt.Errorf("HostedControlPlane registry probe kubeconfig has no current cluster")
	}
	cluster := config.Clusters[currentContext.Cluster]
	if cluster == nil || cluster.InsecureSkipTLSVerify || len(cluster.CertificateAuthorityData) == 0 {
		return registryProbeGuestAPI{}, fmt.Errorf("HostedControlPlane registry probe kubeconfig has no verified certificate authority")
	}
	if err := validateRegistryProbeAPIURL(order.Status.APIURL); err != nil {
		return registryProbeGuestAPI{}, err
	}
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(data)
	if err != nil {
		return registryProbeGuestAPI{}, fmt.Errorf("build HostedControlPlane registry probe client config: %w", err)
	}
	// The HostedControlPlane kubeconfig uses the short kube-apiserver service
	// name, which resolves only from its own namespace. The management operator
	// reaches the same guest API through the published, certificate-valid URL.
	restConfig.Host = strings.TrimRight(order.Status.APIURL, "/")
	restConfig.Timeout = 15 * time.Second
	guest, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return registryProbeGuestAPI{}, fmt.Errorf("create HostedControlPlane registry probe client: %w", err)
	}
	return registryProbeGuestAPI{
		client: guest,
		apiURL: strings.TrimRight(order.Status.APIURL, "/"),
		caData: append([]byte(nil), cluster.CertificateAuthorityData...),
	}, nil
}

func validateRegistryProbeAPIURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("ClusterOrder APIURL is not a safe HTTPS registry-probe callback URL")
	}
	return nil
}

func equalProbeStringSets(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	return strings.Join(leftCopy, "\x00") == strings.Join(rightCopy, "\x00")
}

type workerRegistryProbeNames struct {
	namespace      string
	configMap      string
	serviceAccount string
	role           string
	roleBinding    string
}

type workerRegistryProbeCredentials struct {
	names  workerRegistryProbeNames
	apiURL string
	caData []byte
	token  string
	image  string
}

func workerRegistryProbeNamesFor(order *v1alpha1.ClusterOrder, workerName string, attempt int32) (workerRegistryProbeNames, error) {
	if order == nil {
		return workerRegistryProbeNames{}, fmt.Errorf("registry probe identity is incomplete")
	}
	clusterID := order.Labels[clusterOrderIDLabel]
	tenant := order.Annotations[tenantAnnotationKey]
	if order == nil || order.UID == "" || clusterID == "" || tenant == "" || workerName == "" || attempt < 0 {
		return workerRegistryProbeNames{}, fmt.Errorf("registry probe identity is incomplete")
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%d", workerName, order.UID, attempt)))
	suffix := hex.EncodeToString(sum[:8])
	return workerRegistryProbeNames{
		namespace:      workerRegistryProbeNamespaceName(clusterID),
		configMap:      "worker-" + suffix,
		serviceAccount: "registry-probe-" + suffix,
		role:           "registry-probe-" + suffix,
		roleBinding:    "registry-probe-" + suffix,
	}, nil
}

func workerRegistryProbeNamespaceName(clusterID string) string {
	sum := sha256.Sum256([]byte(clusterID))
	return workerRegistryProbeNamespacePrefix + hex.EncodeToString(sum[:8])
}

func (r *Reconciler) prepareWorkerRegistryProbe(
	ctx context.Context, order *v1alpha1.ClusterOrder, worker *v1alpha1.WorkerStatus, image string,
) (workerRegistryProbeCredentials, ctrl.Result, error) {
	if order == nil || worker == nil || image == "" {
		return workerRegistryProbeCredentials{}, ctrl.Result{}, fmt.Errorf("worker registry probe inputs are incomplete")
	}
	resolver := r.registryProbeGuestResolver
	if resolver == nil {
		resolver = hostedClusterRegistryProbeResolver{management: r.Client}
	}
	guest, err := resolver.Resolve(ctx, order)
	if err != nil {
		return workerRegistryProbeCredentials{}, ctrl.Result{RequeueAfter: workerRegistryProbePollInterval}, err
	}
	names, err := workerRegistryProbeNamesFor(order, worker.Name, worker.AttemptCount)
	if err != nil {
		return workerRegistryProbeCredentials{}, ctrl.Result{}, err
	}
	if err := ensureWorkerRegistryProbeResources(ctx, guest.client, order, names, image); err != nil {
		return workerRegistryProbeCredentials{}, ctrl.Result{}, err
	}
	serviceAccount, err := guest.client.CoreV1().ServiceAccounts(names.namespace).Get(
		ctx, names.serviceAccount, metav1.GetOptions{},
	)
	if err != nil {
		return workerRegistryProbeCredentials{}, ctrl.Result{}, fmt.Errorf("read worker registry probe ServiceAccount: %w", err)
	}
	if serviceAccount.Annotations[tenantAnnotationKey] != tenantOf(order) ||
		serviceAccount.Annotations[ownerReferenceAnnotation] != "ClusterOrder/"+order.Name {
		return workerRegistryProbeCredentials{}, ctrl.Result{}, fmt.Errorf("worker registry probe ServiceAccount ownership does not match ClusterOrder")
	}
	if serviceAccount.AutomountServiceAccountToken == nil || *serviceAccount.AutomountServiceAccountToken {
		return workerRegistryProbeCredentials{}, ctrl.Result{}, fmt.Errorf("worker registry probe ServiceAccount must not automount tokens")
	}
	seconds := int64(workerRegistryProbeTokenLifetime / time.Second)
	request := &authenticationv1.TokenRequest{Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: &seconds}}
	token, err := guest.client.CoreV1().ServiceAccounts(names.namespace).CreateToken(
		ctx, names.serviceAccount, request, metav1.CreateOptions{},
	)
	if err != nil {
		return workerRegistryProbeCredentials{}, ctrl.Result{RequeueAfter: workerRegistryProbePollInterval},
			fmt.Errorf("issue short-lived worker registry probe token: %w", err)
	}
	now := time.Now()
	if token.Status.Token == "" || !token.Status.ExpirationTimestamp.After(now) ||
		token.Status.ExpirationTimestamp.After(now.Add(workerRegistryProbeTokenLifetime+time.Minute)) {
		return workerRegistryProbeCredentials{}, ctrl.Result{}, fmt.Errorf("HostedCluster returned an invalid worker registry probe token")
	}
	currentAccount, err := guest.client.CoreV1().ServiceAccounts(names.namespace).Get(ctx, names.serviceAccount, metav1.GetOptions{})
	if err != nil || (serviceAccount.UID != "" && currentAccount.UID != serviceAccount.UID) ||
		currentAccount.Annotations[tenantAnnotationKey] != tenantOf(order) ||
		currentAccount.Annotations[ownerReferenceAnnotation] != "ClusterOrder/"+order.Name {
		return workerRegistryProbeCredentials{}, ctrl.Result{}, fmt.Errorf("worker registry probe ServiceAccount changed during token request")
	}
	return workerRegistryProbeCredentials{
		names: names, apiURL: guest.apiURL, caData: guest.caData, token: token.Status.Token, image: image,
	}, ctrl.Result{}, nil
}

func ensureWorkerRegistryProbeResources(
	ctx context.Context, kube kubernetes.Interface, order *v1alpha1.ClusterOrder,
	names workerRegistryProbeNames, image string,
) error {
	if kube == nil || order == nil || names.namespace == "" || names.configMap == "" || image == "" {
		return fmt.Errorf("worker registry probe resource identity is incomplete")
	}
	tenant := tenantOf(order)
	clusterID := order.Labels[clusterOrderIDLabel]
	owner := "ClusterOrder/" + order.Name
	meta := workerRegistryProbeObjectMeta(clusterID, tenant, owner)
	if err := ensureWorkerRegistryProbeNamespace(ctx, kube, names.namespace, clusterID, tenant, owner); err != nil {
		return err
	}
	if err := ensureWorkerRegistryProbeServiceAccount(ctx, kube, names, meta, tenant, owner); err != nil {
		return err
	}
	if err := ensureWorkerRegistryProbeRole(ctx, kube, names, meta, tenant, owner); err != nil {
		return err
	}
	if err := ensureWorkerRegistryProbeRoleBinding(ctx, kube, names, meta, tenant, owner); err != nil {
		return err
	}
	return ensureWorkerRegistryProbeConfigMap(ctx, kube, order, names, meta, tenant, owner, clusterID, image)
}

func workerRegistryProbeObjectMeta(clusterID, tenant, owner string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Labels: map[string]string{clusterOrderIDLabel: clusterID},
		Annotations: map[string]string{
			tenantAnnotationKey: tenant, ownerReferenceAnnotation: owner,
		},
	}
}

func ensureWorkerRegistryProbeNamespace(
	ctx context.Context, kube kubernetes.Interface, namespaceName, clusterID, tenant, owner string,
) error {
	namespaceClient := kube.CoreV1().Namespaces()
	namespace, err := namespaceClient.Get(ctx, namespaceName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		namespace, err = namespaceClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   namespaceName,
			Labels: map[string]string{clusterOrderIDLabel: clusterID},
			Annotations: map[string]string{
				tenantAnnotationKey: tenant, ownerReferenceAnnotation: owner,
			},
		}}, metav1.CreateOptions{})
	}
	if err != nil {
		return fmt.Errorf("ensure worker registry probe namespace: %w", err)
	}
	if namespace.Labels[clusterOrderIDLabel] != clusterID || namespace.Annotations[tenantAnnotationKey] != tenant ||
		namespace.Annotations[ownerReferenceAnnotation] != owner {
		return fmt.Errorf("worker registry probe namespace ownership does not match ClusterOrder")
	}
	return nil
}

func ensureWorkerRegistryProbeServiceAccount(
	ctx context.Context, kube kubernetes.Interface, names workerRegistryProbeNames,
	meta metav1.ObjectMeta, tenant, owner string,
) error {
	serviceAccounts := kube.CoreV1().ServiceAccounts(names.namespace)
	account, err := serviceAccounts.Get(ctx, names.serviceAccount, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		account = &corev1.ServiceAccount{ObjectMeta: *meta.DeepCopy()}
		account.Name = names.serviceAccount
		account.AutomountServiceAccountToken = ptr.To(false)
		account, err = serviceAccounts.Create(ctx, account, metav1.CreateOptions{})
	}
	if err != nil {
		return fmt.Errorf("ensure worker registry probe ServiceAccount: %w", err)
	}
	if account.Annotations[tenantAnnotationKey] != tenant || account.Annotations[ownerReferenceAnnotation] != owner {
		return fmt.Errorf("worker registry probe ServiceAccount ownership does not match ClusterOrder")
	}
	if account.AutomountServiceAccountToken == nil || *account.AutomountServiceAccountToken {
		return fmt.Errorf("worker registry probe ServiceAccount must not automount tokens")
	}
	return nil
}

func ensureWorkerRegistryProbeRole(
	ctx context.Context, kube kubernetes.Interface, names workerRegistryProbeNames,
	meta metav1.ObjectMeta, tenant, owner string,
) error {
	rbac := kube.RbacV1()
	role, err := rbac.Roles(names.namespace).Get(ctx, names.role, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		role = &rbacv1.Role{ObjectMeta: *meta.DeepCopy(), Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{""}, Resources: []string{"configmaps"}, ResourceNames: []string{names.configMap}, Verbs: []string{"get", "patch", "update"},
		}}}
		role.Name = names.role
		role, err = rbac.Roles(names.namespace).Create(ctx, role, metav1.CreateOptions{})
	}
	if err != nil {
		return fmt.Errorf("ensure worker registry probe Role: %w", err)
	}
	if role.Annotations[tenantAnnotationKey] != tenant || role.Annotations[ownerReferenceAnnotation] != owner ||
		len(role.Rules) != 1 || !equalProbeStringSets(role.Rules[0].APIGroups, []string{""}) ||
		!equalProbeStringSets(role.Rules[0].Resources, []string{"configmaps"}) ||
		!equalProbeStringSets(role.Rules[0].Verbs, []string{"get", "patch", "update"}) ||
		!equalProbeStringSets(role.Rules[0].ResourceNames, []string{names.configMap}) {
		return fmt.Errorf("worker registry probe Role ownership or scope does not match ClusterOrder")
	}
	return nil
}

func ensureWorkerRegistryProbeRoleBinding(
	ctx context.Context, kube kubernetes.Interface, names workerRegistryProbeNames,
	meta metav1.ObjectMeta, tenant, owner string,
) error {
	rbac := kube.RbacV1()
	binding, err := rbac.RoleBindings(names.namespace).Get(ctx, names.roleBinding, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		binding = &rbacv1.RoleBinding{
			ObjectMeta: *meta.DeepCopy(),
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: names.role},
			Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: names.serviceAccount, Namespace: names.namespace}},
		}
		binding.Name = names.roleBinding
		binding, err = rbac.RoleBindings(names.namespace).Create(ctx, binding, metav1.CreateOptions{})
	}
	if err != nil {
		return fmt.Errorf("ensure worker registry probe RoleBinding: %w", err)
	}
	if binding.Annotations[tenantAnnotationKey] != tenant || binding.Annotations[ownerReferenceAnnotation] != owner ||
		binding.RoleRef.APIGroup != rbacv1.GroupName || binding.RoleRef.Kind != "Role" || binding.RoleRef.Name != names.role ||
		len(binding.Subjects) != 1 || binding.Subjects[0].Kind != "ServiceAccount" ||
		binding.Subjects[0].Name != names.serviceAccount || binding.Subjects[0].Namespace != names.namespace {
		return fmt.Errorf("worker registry probe RoleBinding ownership does not match ClusterOrder")
	}
	return nil
}

func ensureWorkerRegistryProbeConfigMap(
	ctx context.Context, kube kubernetes.Interface, order *v1alpha1.ClusterOrder,
	names workerRegistryProbeNames, meta metav1.ObjectMeta, tenant, owner, clusterID, image string,
) error {
	configMaps := kube.CoreV1().ConfigMaps(names.namespace)
	cm, err := configMaps.Get(ctx, names.configMap, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		cm = &corev1.ConfigMap{ObjectMeta: *meta.DeepCopy(), Data: map[string]string{
			workerRegistryProbeRequestKey: "false",
			workerRegistryProbeStatusKey:  "pending",
			workerRegistryProbeImageKey:   image,
		}}
		cm.Name = names.configMap
		cm, err = configMaps.Create(ctx, cm, metav1.CreateOptions{})
	}
	if err != nil {
		return fmt.Errorf("ensure worker registry probe ConfigMap: %w", err)
	}
	if cm.Annotations[tenantAnnotationKey] != tenant || cm.Annotations[ownerReferenceAnnotation] != owner ||
		cm.Labels[clusterOrderIDLabel] != clusterID || cm.Data[workerRegistryProbeImageKey] != image {
		return fmt.Errorf("worker registry probe ConfigMap ownership or image does not match ClusterOrder")
	}
	return nil
}

func workerRegistryProbeURL(apiURL string, names workerRegistryProbeNames) string {
	parsed, err := url.Parse(apiURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	parsed.Path = path.Join(parsed.Path, "api", "v1", "namespaces", names.namespace, "configmaps", names.configMap)
	parsed.RawPath = ""
	return parsed.String()
}

func injectWorkerRegistryProbeIgnition(ignitionRaw string, credentials workerRegistryProbeCredentials, pullSecret string) (string, error) {
	if credentials.token == "" || len(credentials.caData) == 0 || credentials.apiURL == "" || credentials.image == "" {
		return "", fmt.Errorf("worker registry probe ignition inputs are incomplete")
	}
	probeURL := workerRegistryProbeURL(credentials.apiURL, credentials.names)
	if probeURL == "" {
		return "", fmt.Errorf("worker registry probe API URL is invalid")
	}
	var ignition map[string]any
	if err := json.Unmarshal([]byte(ignitionRaw), &ignition); err != nil {
		return "", fmt.Errorf("parse discovery ignition for worker registry probe: %w", err)
	}
	if ignition["storage"] == nil {
		ignition["storage"] = map[string]any{}
	}
	storage, ok := ignition["storage"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("discovery ignition storage section is not an object")
	}
	files, _ := storage["files"].([]any)
	probeFiles := registryProbeIgnitionFiles(credentials, pullSecret, probeURL)
	for _, probeFile := range probeFiles {
		fileMap := probeFile.(map[string]any)
		filePath := fileMap["path"].(string)
		for _, existing := range files {
			existingMap, ok := existing.(map[string]any)
			if ok && existingMap["path"] == filePath {
				return "", fmt.Errorf("discovery ignition already contains OSAC registry probe file %s", filePath)
			}
		}
		files = append(files, probeFile)
	}
	storage["files"] = files
	if ignition["systemd"] == nil {
		ignition["systemd"] = map[string]any{}
	}
	systemd, ok := ignition["systemd"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("discovery ignition systemd section is not an object")
	}
	units, _ := systemd["units"].([]any)
	for _, existing := range units {
		unitMap, ok := existing.(map[string]any)
		if ok && unitMap[jsonNameField] == "osac-registry-probe.service" {
			return "", fmt.Errorf("discovery ignition already contains osac-registry-probe.service")
		}
	}
	units = append(units, map[string]any{
		jsonNameField: "osac-worker-network-config.service",
		"enabled":     true,
		"contents":    workerNetworkConfigSystemdUnit,
	})
	units = append(units, map[string]any{
		jsonNameField: "osac-registry-probe.service",
		"enabled":     true,
		"contents":    registryProbeSystemdUnit,
	})
	systemd["units"] = units
	result, err := json.Marshal(ignition)
	if err != nil {
		return "", fmt.Errorf("encode worker registry probe ignition: %w", err)
	}
	return string(result), nil
}

func registryProbeIgnitionFiles(credentials workerRegistryProbeCredentials, pullSecret, probeURL string) []any {
	files := []struct {
		path string
		mode int
		data []byte
	}{
		{path: "/usr/local/bin/osac-worker-network-config", mode: 0755, data: []byte(workerNetworkConfigScript)},
		{path: "/usr/local/bin/osac-worker-registry-probe", mode: 0755, data: []byte(registryProbeScript)},
		{path: "/etc/osac/registry-probe/token", mode: 0600, data: []byte(credentials.token)},
		{path: "/etc/osac/registry-probe/ca.crt", mode: 0644, data: credentials.caData},
		{path: "/etc/osac/registry-probe/curl.conf", mode: 0600, data: []byte("header = \"Authorization: Bearer " + credentials.token + "\"\n")},
		{path: "/etc/osac/registry-probe/url", mode: 0644, data: []byte(probeURL)},
		{path: "/etc/osac/registry-probe/image", mode: 0644, data: []byte(credentials.image)},
		{path: "/etc/osac/registry-probe/auth.json", mode: 0600, data: []byte(pullSecret)},
		{path: "/etc/osac/registry-probe/success.json", mode: 0644, data: registryProbeResultPatch(credentials.image, "ready", "ImagePulled", "Worker pulled the ClusterVersion release image")},
		{path: "/etc/osac/registry-probe/failure.json", mode: 0644, data: registryProbeResultPatch(credentials.image, "failed", "ImagePullFailed", "Worker could not pull the release image; inspect the osac-registry-probe.service journal on this host")},
	}
	result := make([]any, 0, len(files))
	for _, file := range files {
		result = append(result, map[string]any{
			"path":      file.path,
			"mode":      file.mode,
			"overwrite": true,
			"user":      map[string]any{jsonNameField: "root"},
			"contents":  map[string]any{"source": "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(file.data)},
		})
	}
	return result
}

func registryProbeResultPatch(image, status, reason, message string) []byte {
	payload, _ := json.Marshal(map[string]any{"data": map[string]string{
		workerRegistryProbeStatusKey:  status,
		workerRegistryProbeReasonKey:  reason,
		workerRegistryProbeMessageKey: message,
		workerRegistryProbeImageKey:   image,
	}})
	return payload
}

const registryProbeSystemdUnit = `[Unit]
Description=OSAC CaaS worker registry access probe
Wants=network-online.target osac-worker-network-config.service
After=network-online.target osac-worker-network-config.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/osac-worker-registry-probe
TimeoutStartSec=30min
Restart=on-failure
RestartSec=10s

[Install]
WantedBy=multi-user.target
`

const workerNetworkConfigSystemdUnit = `[Unit]
Description=OSAC CaaS worker tenant network configuration
Wants=NetworkManager.service
After=NetworkManager.service
Before=network-online.target osac-registry-probe.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/osac-worker-network-config
Restart=on-failure
RestartSec=10s
TimeoutStartSec=30min
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
`

const workerNetworkConfigScript = `#!/usr/bin/bash
set -u

tenant_device=""
tenant_score=-1
while read -r route; do
  device="$(printf '%s\n' "$route" | awk '{for (i=1; i<=NF; i++) if ($i == "dev") {print $(i+1); exit}}')"
  source="$(printf '%s\n' "$route" | awk '{for (i=1; i<=NF; i++) if ($i == "src") {print $(i+1); exit}}')"
  metric_value="$(printf '%s\n' "$route" | awk '{for (i=1; i<=NF; i++) if ($i == "metric") {print $(i+1); exit}}')"
  [ -n "$device" ] || continue
  case "$device" in
    br-*|ovs-*|ovn-*|cni*|lo) continue ;;
  esac
  case "$metric_value" in
    ''|*[!0-9]*) metric_value=0 ;;
  esac
  priority=0
  case "$source" in
    10.200.*) priority=100 ;;
    10.*) priority=50 ;;
  esac
  score=$((priority * 1000000 + metric_value))
  if [ "$score" -gt "$tenant_score" ]; then
    tenant_device="$device"
    tenant_score="$score"
  fi
done < <(ip -4 route show default)

if [ -z "$tenant_device" ]; then
  echo "No tenant default route is available yet"
  exit 1
fi

connection="$(nmcli -t -f NAME,DEVICE connection show --active | awk -F: -v device="$tenant_device" '$2 == device {print $1; exit}')"
if [ -z "$connection" ]; then
  echo "No active NetworkManager connection for tenant device $tenant_device"
  exit 1
fi

nmcli connection modify "$connection" ipv4.route-metric 10 802-3-ethernet.mtu 1400
nmcli connection up "$connection"
printf 'net.ipv4.ip_forward=1\n' > /etc/sysctl.d/99-osac-caas-ip-forward.conf
sysctl -w net.ipv4.ip_forward=1 >/dev/null
if ! iptables -t mangle -C PREROUTING -i "$tenant_device" -p tcp --dport 443 --tcp-flags SYN,RST SYN -j TCPMSS --set-mss 1360 2>/dev/null; then
  iptables -t mangle -I PREROUTING 1 -i "$tenant_device" -p tcp --dport 443 --tcp-flags SYN,RST SYN -j TCPMSS --set-mss 1360
fi
`

const registryProbeScript = `#!/usr/bin/bash
set -u

dir=/etc/osac/registry-probe
url=$(cat "$dir/url")
image=$(cat "$dir/image")
ca="$dir/ca.crt"
curl_config="$dir/curl.conf"

for ((attempt=0; attempt<360; attempt++)); do
  response=$(curl --silent --show-error --max-time 10 --cacert "$ca" --config "$curl_config" "$url" 2>/dev/null) || response=""
  if printf '%s' "$response" | grep -Eq '"request"[[:space:]]*:[[:space:]]*"requested"'; then
    break
  fi
  sleep 5
done

if ! printf '%s' "${response:-}" | grep -Eq '"request"[[:space:]]*:[[:space:]]*"requested"'; then
  echo "OSAC worker registry probe request was not received before timeout"
  exit 1
fi

if podman pull --authfile "$dir/auth.json" "$image"; then
  result="$dir/success.json"
else
  result="$dir/failure.json"
fi

curl --silent --show-error --fail-with-body --max-time 20 --cacert "$ca" \
  --config "$curl_config" --header 'Content-Type: application/merge-patch+json' \
  --request PATCH --data-binary "@$result" "$url"
`

func registryProbeResultStatus(configMap *corev1.ConfigMap) (status, reason, message, image string) {
	if configMap == nil {
		return "", "", "", ""
	}
	return configMap.Data[workerRegistryProbeStatusKey], configMap.Data[workerRegistryProbeReasonKey],
		configMap.Data[workerRegistryProbeMessageKey], configMap.Data[workerRegistryProbeImageKey]
}

func updateWorkerRegistryProbeCondition(worker *v1alpha1.WorkerStatus, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	lastTransitionTime := now
	if previous := worker.RegistryAccessReady; previous != nil && previous.Status == status {
		lastTransitionTime = previous.LastTransitionTime
	}
	worker.RegistryAccessReady = &metav1.Condition{
		Type: workerRegistryProbeCondition, Status: status, Reason: reason,
		Message: message, ObservedGeneration: 0,
		LastTransitionTime: lastTransitionTime,
	}
}

func workerRegistryProbeConditionReady(worker *v1alpha1.WorkerStatus, image string) bool {
	return worker != nil && worker.RegistryAccessReady != nil &&
		worker.RegistryAccessReady.Type == workerRegistryProbeCondition &&
		worker.RegistryAccessReady.Status == metav1.ConditionTrue &&
		worker.RegistryAccessReady.Message == workerRegistryProbeReadyMessage(image)
}

func workerRegistryProbeReadyMessage(image string) string {
	return fmt.Sprintf("Tenant worker image source pulled ClusterVersion release image %s", image)
}

func requestWorkerRegistryProbe(ctx context.Context, kube kubernetes.Interface, names workerRegistryProbeNames) (*corev1.ConfigMap, error) {
	configMaps := kube.CoreV1().ConfigMaps(names.namespace)
	configMap, err := configMaps.Get(ctx, names.configMap, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get worker registry probe ConfigMap: %w", err)
	}
	if configMap.Data[workerRegistryProbeRequestKey] != workerRegistryProbeRequestedValue {
		configMap.Data[workerRegistryProbeRequestKey] = workerRegistryProbeRequestedValue
		configMap, err = configMaps.Update(ctx, configMap, metav1.UpdateOptions{})
		if err != nil {
			return nil, fmt.Errorf("request worker registry probe: %w", err)
		}
	}
	return configMap, nil
}

func cleanupWorkerRegistryProbeCredentials(ctx context.Context, kube kubernetes.Interface, names workerRegistryProbeNames) error {
	var errs []error
	if err := kube.RbacV1().RoleBindings(names.namespace).Delete(ctx, names.roleBinding, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete worker registry probe RoleBinding: %w", err))
	}
	if err := kube.RbacV1().Roles(names.namespace).Delete(ctx, names.role, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete worker registry probe Role: %w", err))
	}
	if err := kube.CoreV1().ServiceAccounts(names.namespace).Delete(ctx, names.serviceAccount, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete worker registry probe ServiceAccount: %w", err))
	}
	return errors.Join(errs...)
}

func (r *Reconciler) reconcileWorkerRegistryProbeResult(
	ctx context.Context, order *v1alpha1.ClusterOrder, worker *v1alpha1.WorkerStatus, image string,
) (bool, error) {
	names, err := workerRegistryProbeNamesFor(order, worker.Name, worker.AttemptCount)
	if err != nil {
		return false, err
	}
	resolver := r.registryProbeGuestResolver
	if resolver == nil {
		resolver = hostedClusterRegistryProbeResolver{management: r.Client}
	}
	guest, err := resolver.Resolve(ctx, order)
	if err != nil {
		return false, fmt.Errorf("resolve worker registry probe API: %w", err)
	}
	configMap, err := requestWorkerRegistryProbe(ctx, guest.client, names)
	if err != nil {
		return false, err
	}
	if configMap.Namespace != names.namespace || configMap.Name != names.configMap ||
		configMap.Annotations[tenantAnnotationKey] != tenantOf(order) ||
		configMap.Annotations[ownerReferenceAnnotation] != "ClusterOrder/"+order.Name ||
		configMap.Labels[clusterOrderIDLabel] != order.Labels[clusterOrderIDLabel] {
		return false, fmt.Errorf("worker registry probe ConfigMap ownership does not match ClusterOrder")
	}
	status, reason, _, resultImage := registryProbeResultStatus(configMap)
	if resultImage != image {
		return false, fmt.Errorf("worker registry probe result image does not match the ClusterVersion release image")
	}
	switch status {
	case "ready":
		readyMessage := workerRegistryProbeReadyMessage(image)
		wasReady := workerRegistryProbeConditionReady(worker, image)
		updateWorkerRegistryProbeCondition(worker, metav1.ConditionTrue, "ImagePulled", readyMessage)
		if err := cleanupWorkerRegistryProbeCredentials(ctx, guest.client, names); err != nil {
			return false, err
		}
		return wasReady, nil
	case "failed":
		if reason != "ImagePullFailed" {
			reason = "ImagePullFailed"
		}
		fullMessage := fmt.Sprintf("Tenant worker image source could not pull %s; inspect the osac-registry-probe.service journal on the worker",
			image)
		updateWorkerRegistryProbeCondition(worker, metav1.ConditionFalse, reason, fullMessage)
		worker.Phase = workerPhaseFailed
		worker.LastFailureReason = eventReasonRegistryAccessFailed
		worker.LastFailureMessage = fullMessage
		worker.ReadySince = nil
		failedAt := metav1.Now()
		worker.LastFailureTime = &failedAt
		observeProvisioningFailure(tenantOf(order), *worker)
		if r.recorder != nil {
			r.recorder.Eventf(order, nil, corev1.EventTypeWarning, eventReasonRegistryAccessFailed,
				"CheckWorkerRegistryAccess", "worker %s: %s", worker.Name, fullMessage)
		}
		if err := cleanupWorkerRegistryProbeCredentials(ctx, guest.client, names); err != nil {
			return false, err
		}
		return false, nil
	default:
		updateWorkerRegistryProbeCondition(worker, metav1.ConditionFalse, "ProbePending",
			fmt.Sprintf("Tenant worker image source is waiting to pull %s", image))
		return false, nil
	}
}

func workerRegistryProbeImage(order *v1alpha1.ClusterOrder, worker *v1alpha1.WorkerStatus) string {
	if order == nil || worker == nil {
		return ""
	}
	for _, request := range order.Spec.NodeRequests {
		if request.NodeSet == worker.NodeSet && request.ReleaseImage != "" {
			return request.ReleaseImage
		}
	}
	return order.Spec.ReleaseImage
}

func workerRegistryPullSecret(order *v1alpha1.ClusterOrder) string {
	if order.Spec.PullSecret != "" {
		return order.Spec.PullSecret
	}
	parameters := map[string]any{}
	if json.Unmarshal([]byte(order.Spec.TemplateParameters), &parameters) == nil {
		if pullSecret, ok := parameters["pull_secret"].(string); ok {
			return pullSecret
		}
	}
	return `{"auths":{}}`
}
