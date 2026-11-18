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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	authenticationv1 "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

type registryProbeGuestResolverFunc func(context.Context, *v1alpha1.ClusterOrder) (registryProbeGuestAPI, error)

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
				APIURL: "https://api.cluster.example.test:6443",
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

		credentials, result, err := r.prepareWorkerRegistryProbe(ctx, order, worker, image)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeZero())
		Expect(credentials.token).To(Equal("short-lived-worker-token"))
		Expect(credentials.apiURL).To(Equal(order.Status.APIURL))
		Expect(credentials.image).To(Equal(image))
		Expect(requestedSeconds).To(Equal(int64(workerRegistryProbeTokenLifetime / time.Second)))
		Expect(order.Status.Workers).To(BeEmpty(), "the token must not be written to ClusterOrder status")
	})

	It("adds a boot-time runner that waits for OSAC network proof before pulling", func() {
		order := newOrder()
		names, err := workerRegistryProbeNamesFor(order, "worker-0", 1)
		Expect(err).NotTo(HaveOccurred())
		credentials := workerRegistryProbeCredentials{
			names: names, apiURL: order.Status.APIURL, caData: []byte("test-ca"),
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
		Expect(files).To(HaveLen(9))
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
		Expect(string(fileContents["/etc/osac/registry-probe/url"])).To(ContainSubstring(names.configMap))
		Expect(string(fileContents["/etc/osac/registry-probe/success.json"])).To(ContainSubstring(image))
		Expect(string(fileContents["/usr/local/bin/osac-worker-registry-probe"])).To(ContainSubstring("podman pull"))

		systemd, ok := ignition["systemd"].(map[string]any)
		Expect(ok).To(BeTrue())
		units, ok := systemd["units"].([]any)
		Expect(ok).To(BeTrue())
		Expect(units).To(HaveLen(1))
		unit := units[0].(map[string]any)
		Expect(unit[jsonNameField]).To(Equal("osac-registry-probe.service"))
		Expect(unit["contents"]).To(ContainSubstring("After=network-online.target"))
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
