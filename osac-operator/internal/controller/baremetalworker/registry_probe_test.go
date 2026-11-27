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
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

type registryProbeGuestResolverFunc func(context.Context, *v1alpha1.ClusterOrder) (registryProbeGuestAPI, error)

func mustWorkerRegistryProbeNames(order *v1alpha1.ClusterOrder, worker *v1alpha1.WorkerStatus) workerRegistryProbeNames {
	names, err := workerRegistryProbeNamesFor(order, worker.Name, worker.AttemptCount)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return names
}

func (f registryProbeGuestResolverFunc) Resolve(
	ctx context.Context, order *v1alpha1.ClusterOrder,
) (registryProbeGuestAPI, error) {
	return f(ctx, order)
}

var _ = Describe("CaaS worker registry probe", func() {
	ctx := context.Background()
	const (
		image      = "registry.example.test/ocp/release:4.22.0"
		pullSecret = `{"auths":{"registry.example.test":{"auth":"fake-secret"}}}`
	)

	newOrder := func() *v1alpha1.ClusterOrder {
		return &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name: "caas", Namespace: "orders", UID: types.UID("order-uid"),
				Labels:      map[string]string{clusterOrderIDLabel: "cluster-id"},
				Annotations: map[string]string{tenantAnnotationKey: "tenant-a"},
			},
			Spec: v1alpha1.ClusterOrderSpec{
				SupportsAutomaticCaaSEndpoints: true,
				PullSecret:                     pullSecret,
				ReleaseImage:                   image,
			},
			Status: v1alpha1.ClusterOrderStatus{
				APIURL:      "https://api.cluster.example.test:6443",
				ApiEndpoint: "192.168.40.240",
				ClusterReference: &v1alpha1.ClusterOrderClusterReferenceType{
					Namespace: "caas", HostedClusterName: "cluster",
				},
			},
		}
	}

	newWorker := func() *v1alpha1.WorkerStatus {
		return &v1alpha1.WorkerStatus{
			Name: "worker-0", NodeSet: "compute", AttemptCount: 1, Phase: workerPhaseProvisioning,
		}
	}

	It("uses the management API VIP while preserving the published callback URL", func() {
		order := newOrder()
		requestReceived := make(chan string, 1)
		responseWriteErr := make(chan error, 1)
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestReceived <- r.Host + r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"apiVersion":"v1","kind":"NamespaceList","metadata":{},"items":[]}`))
			responseWriteErr <- err
		}))
		defer server.Close()
		order.Status.APIURL = server.URL
		order.Status.ApiEndpoint = strings.TrimPrefix(server.URL, "https://")

		caData := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
		kubeconfig, err := json.Marshal(map[string]any{
			"apiVersion": "v1",
			"kind":       "Config",
			"clusters": []any{map[string]any{
				"name": "hosted-cluster",
				"cluster": map[string]any{
					"server":                     "https://kube-apiserver:6443",
					"certificate-authority-data": base64.StdEncoding.EncodeToString(caData),
				},
			}},
			"contexts": []any{map[string]any{
				"name": "hosted-context",
				"context": map[string]any{
					"cluster": "hosted-cluster",
					"user":    "probe",
				},
			}},
			"current-context": "hosted-context",
			"users": []any{map[string]any{
				"name": "probe",
				"user": map[string]any{"token": "test-token"},
			}},
		})
		Expect(err).NotTo(HaveOccurred())

		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		management := clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "caas-cluster", Name: workerRegistryProbeKubeconfigSecret,
			},
			Data: map[string][]byte{"kubeconfig": kubeconfig},
		}).Build()
		guest, err := (hostedClusterRegistryProbeResolver{management: management}).Resolve(ctx, order)
		Expect(err).NotTo(HaveOccurred())

		_, err = guest.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(<-requestReceived).To(Equal(strings.TrimPrefix(server.URL, "https://") + "/api/v1/namespaces"))
		Expect(<-responseWriteErr).NotTo(HaveOccurred())
	})

	It("reports guest disappearance only after management-side confirmation", func() {
		order := newOrder()
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(hypershiftv1beta1.AddToScheme(scheme)).To(Succeed())

		missingGuest := clientfake.NewClientBuilder().WithScheme(scheme).Build()
		_, err := (hostedClusterRegistryProbeResolver{management: missingGuest}).Resolve(ctx, order)
		Expect(errors.Is(err, ErrRegistryProbeGuestGone)).To(BeTrue(), "missing guest namespace must be authoritative: %v", err)

		liveGuest := clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "caas-cluster"}},
			&hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "caas"}},
		).Build()
		_, err = (hostedClusterRegistryProbeResolver{management: liveGuest}).Resolve(ctx, order)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrRegistryProbeGuestGone)).To(BeFalse(), "missing Secret with live guest must remain retryable: %v", err)

		deletingGuest := clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "caas-cluster"}},
			&hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
				Name: "cluster", Namespace: "caas", DeletionTimestamp: ptr.To(metav1.Now()), Finalizers: []string{"test"},
			}},
		).Build()
		_, err = (hostedClusterRegistryProbeResolver{management: deletingGuest}).Resolve(ctx, order)
		Expect(errors.Is(err, ErrRegistryProbeGuestGone)).To(BeTrue(), "deleting guest must be authoritative: %v", err)
	})

	DescribeTable("builds a management API URL from the published endpoint",
		func(endpoint, expected string) {
			got, err := registryProbeManagementAPIURL(endpoint)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(expected))
		},
		Entry("IPv4 VIP", "192.168.40.240", "https://192.168.40.240:6443"),
		Entry("endpoint with port", "127.0.0.1:7443", "https://127.0.0.1:7443"),
		Entry("IPv6 literal", "2001:db8::10", "https://[2001:db8::10]:6443"),
	)

	It("rejects a missing or URL-shaped management endpoint", func() {
		for _, endpoint := range []string{"", "https://192.168.40.240:6443", "192.168.40.240/path", "api.example.test:+6443", "[192.168.40.240"} {
			_, err := registryProbeManagementAPIURL(endpoint)
			Expect(err).To(HaveOccurred())
		}
	})

	It("limits the guest token to one worker ConfigMap and preserves probe results", func() {
		order := newOrder()
		worker := newWorker()
		names, err := workerRegistryProbeNamesFor(order, worker.Name, worker.AttemptCount)
		Expect(err).NotTo(HaveOccurred())
		kube := kubernetesfake.NewSimpleClientset()

		Expect(ensureWorkerRegistryProbeResources(ctx, kube, order, names, image)).To(Succeed())
		probeNamespace, err := kube.CoreV1().Namespaces().Get(ctx, names.namespace, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(probeNamespace.Annotations[tenantAnnotationKey]).To(Equal("tenant-a"))
		Expect(probeNamespace.Annotations[ownerReferenceAnnotation]).To(Equal("ClusterOrder/caas"))

		role, err := kube.RbacV1().Roles(names.namespace).Get(ctx, names.role, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(role.Rules).To(HaveLen(1))
		Expect(role.Rules[0].Resources).To(Equal([]string{"configmaps"}))
		Expect(role.Rules[0].ResourceNames).To(Equal([]string{names.configMap}))
		Expect(role.Rules[0].Verbs).To(ConsistOf("get", "patch", "update"))

		configMap, err := kube.CoreV1().ConfigMaps(names.namespace).Get(ctx, names.configMap, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		configMap.Data[workerRegistryProbeRequestKey] = workerRegistryProbeRequestedValue
		configMap.Data[workerRegistryProbeStatusKey] = "ready"
		_, err = kube.CoreV1().ConfigMaps(names.namespace).Update(ctx, configMap, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())

		Expect(ensureWorkerRegistryProbeResources(ctx, kube, order, names, image)).To(Succeed())
		configMap, err = kube.CoreV1().ConfigMaps(names.namespace).Get(ctx, names.configMap, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap.Data[workerRegistryProbeRequestKey]).To(Equal(workerRegistryProbeRequestedValue))
		Expect(configMap.Data[workerRegistryProbeStatusKey]).To(Equal("ready"))
	})

	It("treats an already-gone guest as completed cleanup", func() {
		order := newOrder()
		worker := newWorker()
		r := &Reconciler{registryProbeGuestResolver: registryProbeGuestResolverFunc(func(context.Context, *v1alpha1.ClusterOrder) (registryProbeGuestAPI, error) {
			return registryProbeGuestAPI{}, ErrRegistryProbeGuestGone
		})}

		Expect(r.cleanupFailedWorkerRegistryProbe(ctx, order, *worker)).To(Succeed())
	})

	It("issues only a bounded token for the per-worker ConfigMap reporter", func() {
		order := newOrder()
		worker := newWorker()
		kube := kubernetesfake.NewSimpleClientset()
		var requestedSeconds int64
		kube.PrependReactor("create", "serviceaccounts", func(action clienttesting.Action) (bool, runtime.Object, error) {
			if action.GetSubresource() != "token" {
				return false, nil, nil
			}
			create := action.(clienttesting.CreateAction)
			request := create.GetObject().(*authenticationv1.TokenRequest)
			requestedSeconds = *request.Spec.ExpirationSeconds
			return true, &authenticationv1.TokenRequest{Status: authenticationv1.TokenRequestStatus{
				Token: "short-lived-worker-token", ExpirationTimestamp: metav1.NewTime(time.Now().Add(workerRegistryProbeTokenLifetime)),
			}}, nil
		})
		r := &Reconciler{registryProbeGuestResolver: registryProbeGuestResolverFunc(func(context.Context, *v1alpha1.ClusterOrder) (registryProbeGuestAPI, error) {
			return registryProbeGuestAPI{client: kube, apiURL: order.Status.APIURL, caData: []byte("test-ca")}, nil
		})}

		credentials, result, err := r.prepareWorkerRegistryProbe(ctx, order, worker, image, "eno1")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeZero())
		Expect(credentials.token).To(Equal("short-lived-worker-token"))
		Expect(credentials.apiURL).To(Equal(order.Status.APIURL))
		Expect(credentials.image).To(Equal(image))
		Expect(credentials.interfaceName).To(Equal("eno1"))
		Expect(requestedSeconds).To(Equal(int64(workerRegistryProbeTokenLifetime / time.Second)))
		Expect(order.Status.Workers).To(BeEmpty(), "the token must not be written to ClusterOrder status")
	})

	It("resets a retained result when a fresh attempt reuses a probe name", func() {
		order := newOrder()
		worker := newWorker()
		kube := kubernetesfake.NewSimpleClientset()
		names := mustWorkerRegistryProbeNames(order, worker)
		Expect(ensureWorkerRegistryProbeResources(ctx, kube, order, names, image)).To(Succeed())
		configMap, err := kube.CoreV1().ConfigMaps(names.namespace).Get(ctx, names.configMap, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		configMap.Data[workerRegistryProbeRequestKey] = workerRegistryProbeRequestedValue
		configMap.Data[workerRegistryProbeStatusKey] = "ready"
		_, err = kube.CoreV1().ConfigMaps(names.namespace).Update(ctx, configMap, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())
		kube.PrependReactor("create", "serviceaccounts", func(action clienttesting.Action) (bool, runtime.Object, error) {
			if action.GetSubresource() != "token" {
				return false, nil, nil
			}
			return true, &authenticationv1.TokenRequest{Status: authenticationv1.TokenRequestStatus{
				Token: "short-lived-worker-token", ExpirationTimestamp: metav1.NewTime(time.Now().Add(workerRegistryProbeTokenLifetime)),
			}}, nil
		})
		r := &Reconciler{registryProbeGuestResolver: registryProbeGuestResolverFunc(func(context.Context, *v1alpha1.ClusterOrder) (registryProbeGuestAPI, error) {
			return registryProbeGuestAPI{client: kube, apiURL: order.Status.APIURL, caData: []byte("test-ca")}, nil
		})}

		_, result, err := r.prepareWorkerRegistryProbe(ctx, order, worker, image, "eno1")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeZero())
		configMap, err = kube.CoreV1().ConfigMaps(names.namespace).Get(ctx, names.configMap, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap.Data[workerRegistryProbeRequestKey]).To(Equal("false"))
		Expect(configMap.Data[workerRegistryProbeStatusKey]).To(Equal("pending"))
	})

	It("cleans all retained probe incarnations for a retiring worker", func() {
		order := newOrder()
		worker := newWorker()
		kube := kubernetesfake.NewSimpleClientset()
		for _, attempt := range []int32{0, 1} {
			worker.AttemptCount = attempt
			names := mustWorkerRegistryProbeNames(order, worker)
			Expect(ensureWorkerRegistryProbeResources(ctx, kube, order, names, image)).To(Succeed())
		}

		Expect(cleanupWorkerRegistryProbeForWorker(ctx, kube, order, worker.Name)).To(Succeed())
		for _, attempt := range []int32{0, 1} {
			worker.AttemptCount = attempt
			names := mustWorkerRegistryProbeNames(order, worker)
			_, err := kube.CoreV1().ConfigMaps(names.namespace).Get(ctx, names.configMap, metav1.GetOptions{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "attempt %d ConfigMap remains", attempt)
			_, err = kube.RbacV1().Roles(names.namespace).Get(ctx, names.role, metav1.GetOptions{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "attempt %d Role remains", attempt)
		}
	})

	It("cleans the prior attempt after a healthy counter reset", func() {
		order := newOrder()
		worker := newWorker()
		worker.Phase = workerPhaseReady
		worker.RegistryProbePrepared = true
		worker.ReadySince = ptr.To(metav1.NewTime(time.Now().Add(-minHealthyDuration - time.Minute)))
		worker.AttemptCount = 1
		worker.RetryCount = 1
		kube := kubernetesfake.NewSimpleClientset()
		attemptOne := mustWorkerRegistryProbeNames(order, worker)
		Expect(ensureWorkerRegistryProbeResources(ctx, kube, order, attemptOne, image)).To(Succeed())
		workers := []v1alpha1.WorkerStatus{*worker}
		resetHealthyWorkers(ctrllog.FromContext(ctx), order, workers, time.Now())
		Expect(workers[0].AttemptCount).To(Equal(int32(1)))
		Expect(workers[0].RetryCount).To(Equal(int32(0)))

		r := &Reconciler{registryProbeGuestResolver: registryProbeGuestResolverFunc(func(context.Context, *v1alpha1.ClusterOrder) (registryProbeGuestAPI, error) {
			return registryProbeGuestAPI{client: kube, apiURL: order.Status.APIURL, caData: []byte("test-ca")}, nil
		})}
		workers[0].RegistryProbePrepared = true
		Expect(r.cleanupFailedWorkerRegistryProbe(ctx, order, workers[0])).To(Succeed())
		_, err := kube.CoreV1().ConfigMaps(attemptOne.namespace).Get(ctx, attemptOne.configMap, metav1.GetOptions{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "healthy attempt ConfigMap was retained")
	})

	It("retries guest API setup after a temporary outage", func() {
		order := newOrder()
		worker := newWorker()
		kube := kubernetesfake.NewSimpleClientset()
		kube.PrependReactor("create", "serviceaccounts", func(action clienttesting.Action) (bool, runtime.Object, error) {
			if action.GetSubresource() != "token" {
				return false, nil, nil
			}
			return true, &authenticationv1.TokenRequest{Status: authenticationv1.TokenRequestStatus{
				Token: "short-lived-worker-token", ExpirationTimestamp: metav1.NewTime(time.Now().Add(workerRegistryProbeTokenLifetime)),
			}}, nil
		})
		attempts := 0
		r := &Reconciler{registryProbeGuestResolver: registryProbeGuestResolverFunc(func(context.Context, *v1alpha1.ClusterOrder) (registryProbeGuestAPI, error) {
			attempts++
			if attempts == 1 {
				return registryProbeGuestAPI{}, errors.New("HostedCluster API temporarily unavailable")
			}
			return registryProbeGuestAPI{client: kube, apiURL: order.Status.APIURL, caData: []byte("test-ca")}, nil
		})}

		_, result, err := r.prepareWorkerRegistryProbe(ctx, order, worker, image, "eno1")
		Expect(err).To(HaveOccurred())
		Expect(result).To(BeZero())

		credentials, result, err := r.prepareWorkerRegistryProbe(ctx, order, worker, image, "eno1")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeZero())
		Expect(credentials.interfaceName).To(Equal("eno1"))
	})

	It("adds a boot-time runner that waits for OSAC network proof before pulling", func() {
		order := newOrder()
		names, err := workerRegistryProbeNamesFor(order, "worker-0", 1)
		Expect(err).NotTo(HaveOccurred())
		credentials := workerRegistryProbeCredentials{
			names: names, apiURL: order.Status.APIURL, caData: []byte("test-ca"), interfaceName: "eno1",
			token: "scoped.jwt.token", image: image,
		}
		input := `{"ignition":{"version":"3.2.0"}}`
		output, err := injectWorkerRegistryProbeIgnition(input, credentials, pullSecret)
		Expect(err).NotTo(HaveOccurred())

		var ignition map[string]any
		Expect(json.Unmarshal([]byte(output), &ignition)).To(Succeed())
		storage, ok := ignition["storage"].(map[string]any)
		Expect(ok).To(BeTrue())
		files, ok := storage["files"].([]any)
		Expect(ok).To(BeTrue())
		Expect(files).To(HaveLen(11))
		fileContents := map[string][]byte{}
		for _, raw := range files {
			file := raw.(map[string]any)
			contents := file["contents"].(map[string]any)
			source := contents["source"].(string)
			encoded := strings.TrimPrefix(source, "data:application/octet-stream;base64,")
			decoded, decodeErr := base64.StdEncoding.DecodeString(encoded)
			Expect(decodeErr).NotTo(HaveOccurred())
			fileContents[file["path"].(string)] = decoded
		}
		Expect(string(fileContents["/etc/osac/registry-probe/auth.json"])).To(Equal(pullSecret))
		Expect(string(fileContents["/etc/osac/registry-probe/token"])).To(Equal(credentials.token))
		Expect(string(fileContents["/etc/osac/registry-probe/interface"])).To(Equal("eno1"))
		Expect(string(fileContents["/etc/osac/registry-probe/url"])).To(ContainSubstring(names.configMap))
		Expect(string(fileContents["/etc/osac/registry-probe/success.json"])).To(ContainSubstring(image))
		Expect(string(fileContents["/usr/local/bin/osac-worker-registry-probe"])).To(ContainSubstring("podman pull"))
		Expect(string(fileContents["/usr/local/bin/osac-worker-network-config"])).To(ContainSubstring("selected tenant device"))
		Expect(string(fileContents["/usr/local/bin/osac-worker-network-config"])).NotTo(ContainSubstring("802-3-ethernet.mtu 1400"))
		Expect(string(fileContents["/usr/local/bin/osac-worker-network-config"])).To(ContainSubstring("net.ipv4.ip_forward=1"))
		Expect(string(fileContents["/usr/local/bin/osac-worker-network-config"])).To(ContainSubstring("-f UUID,DEVICE connection show --active"))

		systemd, ok := ignition["systemd"].(map[string]any)
		Expect(ok).To(BeTrue())
		units, ok := systemd["units"].([]any)
		Expect(ok).To(BeTrue())
		Expect(units).To(HaveLen(2))
		networkUnit := units[0].(map[string]any)
		Expect(networkUnit[jsonNameField]).To(Equal("osac-worker-network-config.service"))
		Expect(networkUnit["contents"]).To(ContainSubstring("WantedBy=multi-user.target"))
		Expect(networkUnit["contents"]).To(ContainSubstring("After=NetworkManager.service"))
		Expect(networkUnit["contents"]).To(ContainSubstring("Before=network-online.target"))
		Expect(networkUnit["contents"]).To(ContainSubstring("OnSuccess=osac-registry-probe.service"))
		unit := units[1].(map[string]any)
		Expect(unit[jsonNameField]).To(Equal("osac-registry-probe.service"))
		Expect(unit["contents"]).To(ContainSubstring("Requires=osac-worker-network-config.service"))
		Expect(unit["contents"]).To(ContainSubstring("After=network-online.target osac-worker-network-config.service"))
	})

	It("rejects discovery Ignition that already defines the worker network unit", func() {
		order := newOrder()
		names, err := workerRegistryProbeNamesFor(order, "worker-0", 1)
		Expect(err).NotTo(HaveOccurred())
		credentials := workerRegistryProbeCredentials{
			names: names, apiURL: order.Status.APIURL, caData: []byte("test-ca"),
			token: "scoped.jwt.token", image: image,
		}
		input := `{"ignition":{"version":"3.2.0"},"systemd":{"units":[{"name":"osac-worker-network-config.service"}]}}`
		_, err = injectWorkerRegistryProbeIgnition(input, credentials, pullSecret)
		Expect(err).To(MatchError(ContainSubstring("worker network service")))
	})

	It("triggers only after network evidence and reports the worker pull result", func() {
		order := newOrder()
		worker := newWorker()
		names, err := workerRegistryProbeNamesFor(order, worker.Name, worker.AttemptCount)
		Expect(err).NotTo(HaveOccurred())
		kube := kubernetesfake.NewSimpleClientset()
		Expect(ensureWorkerRegistryProbeResources(ctx, kube, order, names, image)).To(Succeed())
		r := &Reconciler{registryProbeGuestResolver: registryProbeGuestResolverFunc(func(context.Context, *v1alpha1.ClusterOrder) (registryProbeGuestAPI, error) {
			return registryProbeGuestAPI{client: kube, apiURL: order.Status.APIURL, caData: []byte("test-ca")}, nil
		})}

		ready, err := r.reconcileWorkerRegistryProbeResult(ctx, order, worker, image)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse())
		configMap, err := kube.CoreV1().ConfigMaps(names.namespace).Get(ctx, names.configMap, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(configMap.Data[workerRegistryProbeRequestKey]).To(Equal(workerRegistryProbeRequestedValue))

		configMap.Data[workerRegistryProbeStatusKey] = "ready"
		configMap.Data[workerRegistryProbeReasonKey] = "ImagePulled"
		configMap.Data[workerRegistryProbeMessageKey] = "worker pulled image"
		_, err = kube.CoreV1().ConfigMaps(names.namespace).Update(ctx, configMap, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())

		ready, err = r.reconcileWorkerRegistryProbeResult(ctx, order, worker, image)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse(), "readiness status must be persisted before Agent binding")
		Expect(workerRegistryProbeConditionReady(worker, image)).To(BeTrue())
		_, getErr := kube.CoreV1().ServiceAccounts(names.namespace).Get(ctx, names.serviceAccount, metav1.GetOptions{})
		Expect(apierrors.IsNotFound(getErr)).To(BeTrue())

		ready, err = r.reconcileWorkerRegistryProbeResult(ctx, order, worker, image)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue())
	})
})
