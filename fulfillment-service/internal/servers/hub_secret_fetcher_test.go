/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var _ = Describe("Hub secret fetcher", func() {

	var (
		fetcher      HubSecretFetcher
		hubLookup    *stubHubLookup
		clientSecret *corev1.Secret
		fakeClient   clnt.Client
	)

	BeforeEach(func() {
		clientSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-kubeconfig",
				Namespace: "clusters",
			},
			Data: map[string][]byte{
				"kubeconfig": []byte("apiVersion: v1\nkind: Config"),
				"password":   []byte("admin-pass"),
			},
		}

		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		fakeClient = fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(clientSecret).
			Build()

		hubLookup = &stubHubLookup{
			kubeconfig: validKubeconfig(),
			namespace:  "clusters",
		}

		var err error
		fetcher, err = NewHubSecretFetcher().
			SetLogger(logger).
			SetHubLookup(hubLookup).
			SetHubClientFactory(func(_ *rest.Config) (clnt.Client, error) {
				return fakeClient, nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Creation", func() {
		It("Can be built with all required parameters", func() {
			Expect(fetcher).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			_, err := NewHubSecretFetcher().
				SetHubLookup(hubLookup).
				SetHubClientFactory(func(_ *rest.Config) (clnt.Client, error) {
					return fakeClient, nil
				}).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger"))
		})

		It("Fails if hub lookup is not set", func() {
			_, err := NewHubSecretFetcher().
				SetLogger(logger).
				SetHubClientFactory(func(_ *rest.Config) (clnt.Client, error) {
					return fakeClient, nil
				}).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("hub lookup"))
		})

		It("Fails if hub client factory is not set", func() {
			_, err := NewHubSecretFetcher().
				SetLogger(logger).
				SetHubLookup(hubLookup).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("hub client factory"))
		})
	})

	Describe("Fetch", func() {
		It("Returns all data keys when no key coordinate is specified", func() {
			data, err := fetcher.Fetch(context.Background(), map[string]string{
				CoordinateHubID:      "hub-1",
				CoordinateNamespace:  "clusters",
				CoordinateSecretName: "my-kubeconfig",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(data).To(HaveKeyWithValue("kubeconfig", []byte("apiVersion: v1\nkind: Config")))
			Expect(data).To(HaveKeyWithValue("password", []byte("admin-pass")))
		})

		It("Returns only the specified key when key coordinate is set", func() {
			data, err := fetcher.Fetch(context.Background(), map[string]string{
				CoordinateHubID:      "hub-1",
				CoordinateNamespace:  "clusters",
				CoordinateSecretName: "my-kubeconfig",
				CoordinateKey:        "kubeconfig",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(data).To(HaveLen(1))
			Expect(data).To(HaveKeyWithValue("kubeconfig", []byte("apiVersion: v1\nkind: Config")))
		})

		It("Returns NotFound when specified key does not exist in the secret", func() {
			_, err := fetcher.Fetch(context.Background(), map[string]string{
				CoordinateHubID:      "hub-1",
				CoordinateNamespace:  "clusters",
				CoordinateSecretName: "my-kubeconfig",
				CoordinateKey:        "nonexistent",
			})
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.NotFound))
			Expect(st.Message()).To(ContainSubstring("nonexistent"))
		})

		It("Returns InvalidArgument when hub_id is missing", func() {
			_, err := fetcher.Fetch(context.Background(), map[string]string{
				CoordinateNamespace:  "clusters",
				CoordinateSecretName: "my-kubeconfig",
			})
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring(CoordinateHubID))
		})

		It("Returns InvalidArgument when namespace is missing", func() {
			_, err := fetcher.Fetch(context.Background(), map[string]string{
				CoordinateHubID:      "hub-1",
				CoordinateSecretName: "my-kubeconfig",
			})
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring(CoordinateNamespace))
		})

		It("Returns InvalidArgument when secret_name is missing", func() {
			_, err := fetcher.Fetch(context.Background(), map[string]string{
				CoordinateHubID:     "hub-1",
				CoordinateNamespace: "clusters",
			})
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring(CoordinateSecretName))
		})

		It("Returns NotFound when the Kubernetes secret does not exist", func() {
			_, err := fetcher.Fetch(context.Background(), map[string]string{
				CoordinateHubID:      "hub-1",
				CoordinateNamespace:  "clusters",
				CoordinateSecretName: "does-not-exist",
			})
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.NotFound))
		})

		It("Returns NotFound when the hub is not found", func() {
			hubLookup.err = status.Errorf(codes.NotFound, "hub not found")

			_, err := fetcher.Fetch(context.Background(), map[string]string{
				CoordinateHubID:      "nonexistent-hub",
				CoordinateNamespace:  "clusters",
				CoordinateSecretName: "my-kubeconfig",
			})
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.NotFound))
			Expect(st.Message()).To(ContainSubstring("nonexistent-hub"))
		})

		It("Returns Unavailable when the hub is unreachable", func() {
			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())

			unavailableFetcher, err := NewHubSecretFetcher().
				SetLogger(logger).
				SetHubLookup(hubLookup).
				SetHubClientFactory(func(_ *rest.Config) (clnt.Client, error) {
					return fake.NewClientBuilder().WithScheme(scheme).
						WithInterceptorFuncs(interceptor.Funcs{
							Get: func(_ context.Context, _ clnt.WithWatch, _ clnt.ObjectKey, _ clnt.Object, _ ...clnt.GetOption) error {
								return apierrors.NewServiceUnavailable("hub unavailable")
							},
						}).
						Build(), nil
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = unavailableFetcher.Fetch(context.Background(), map[string]string{
				CoordinateHubID:      "hub-1",
				CoordinateNamespace:  "clusters",
				CoordinateSecretName: "my-kubeconfig",
			})
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.Unavailable))
		})
	})
})

// stubHubLookup is a test double for HubLookup that returns pre-configured values.
type stubHubLookup struct {
	kubeconfig []byte
	namespace  string
	err        error
}

func (s *stubHubLookup) GetKubeconfig(_ context.Context, _ string) ([]byte, string, error) {
	if s.err != nil {
		return nil, "", s.err
	}
	return s.kubeconfig, s.namespace, nil
}

// validKubeconfig returns a minimal kubeconfig that passes clientcmd.RESTConfigFromKubeConfig parsing.
func validKubeconfig() []byte {
	return []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://localhost:6443
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: test-token
`)
}
