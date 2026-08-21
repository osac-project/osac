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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var _ = Describe("Hub secret fetcher", func() {

	var (
		fetcher      HubSecretFetcher
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

		var err error
		fetcher, err = NewHubSecretFetcher().
			SetHubClientProvider(&stubHubClientProvider{client: fakeClient}).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Creation", func() {
		It("Can be built with all required parameters", func() {
			Expect(fetcher).ToNot(BeNil())
		})

		It("Fails if hub client provider is not set", func() {
			_, err := NewHubSecretFetcher().
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("hub client provider"))
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
			notFoundProvider := &stubHubClientProviderWithError{
				err: status.Errorf(codes.NotFound, "hub %q not found", "nonexistent-hub"),
			}
			notFoundFetcher, err := NewHubSecretFetcher().
				SetHubClientProvider(notFoundProvider).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = notFoundFetcher.Fetch(context.Background(), map[string]string{
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

			unavailableClient := fake.NewClientBuilder().WithScheme(scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(_ context.Context, _ clnt.WithWatch, _ clnt.ObjectKey, _ clnt.Object, _ ...clnt.GetOption) error {
						return apierrors.NewServiceUnavailable("hub unavailable")
					},
				}).
				Build()

			unavailableFetcher, err := NewHubSecretFetcher().
				SetHubClientProvider(&stubHubClientProvider{client: unavailableClient}).
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

		It("Returns Unauthenticated when hub returns 401", func() {
			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())

			unauthClient := fake.NewClientBuilder().WithScheme(scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(_ context.Context, _ clnt.WithWatch, _ clnt.ObjectKey, _ clnt.Object, _ ...clnt.GetOption) error {
						return apierrors.NewUnauthorized("token expired")
					},
				}).
				Build()

			unauthFetcher, err := NewHubSecretFetcher().
				SetHubClientProvider(&stubHubClientProvider{client: unauthClient}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = unauthFetcher.Fetch(context.Background(), map[string]string{
				CoordinateHubID:      "hub-1",
				CoordinateNamespace:  "clusters",
				CoordinateSecretName: "my-kubeconfig",
			})
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.Unauthenticated))
		})

		It("Returns Canceled when context is canceled", func() {
			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())

			canceledClient := fake.NewClientBuilder().WithScheme(scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, _ clnt.WithWatch, _ clnt.ObjectKey, _ clnt.Object, _ ...clnt.GetOption) error {
						return context.Canceled
					},
				}).
				Build()

			canceledFetcher, err := NewHubSecretFetcher().
				SetHubClientProvider(&stubHubClientProvider{client: canceledClient}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = canceledFetcher.Fetch(context.Background(), map[string]string{
				CoordinateHubID:      "hub-1",
				CoordinateNamespace:  "clusters",
				CoordinateSecretName: "my-kubeconfig",
			})
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.Canceled))
		})

		It("Returns DeadlineExceeded when context deadline is exceeded", func() {
			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())

			deadlineClient := fake.NewClientBuilder().WithScheme(scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, _ clnt.WithWatch, _ clnt.ObjectKey, _ clnt.Object, _ ...clnt.GetOption) error {
						return context.DeadlineExceeded
					},
				}).
				Build()

			deadlineFetcher, err := NewHubSecretFetcher().
				SetHubClientProvider(&stubHubClientProvider{client: deadlineClient}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = deadlineFetcher.Fetch(context.Background(), map[string]string{
				CoordinateHubID:      "hub-1",
				CoordinateNamespace:  "clusters",
				CoordinateSecretName: "my-kubeconfig",
			})
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.DeadlineExceeded))
		})

		It("Returns PermissionDenied when hub returns 403", func() {
			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())

			forbiddenClient := fake.NewClientBuilder().WithScheme(scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(_ context.Context, _ clnt.WithWatch, _ clnt.ObjectKey, _ clnt.Object, _ ...clnt.GetOption) error {
						return apierrors.NewForbidden(
							schema.GroupResource{Group: "", Resource: "secrets"},
							"my-kubeconfig",
							fmt.Errorf("insufficient permissions"),
						)
					},
				}).
				Build()

			forbiddenFetcher, err := NewHubSecretFetcher().
				SetHubClientProvider(&stubHubClientProvider{client: forbiddenClient}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = forbiddenFetcher.Fetch(context.Background(), map[string]string{
				CoordinateHubID:      "hub-1",
				CoordinateNamespace:  "clusters",
				CoordinateSecretName: "my-kubeconfig",
			})
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.PermissionDenied))
		})
	})
})

// stubHubClientProviderWithError is a test double for HubClientProvider that always returns an error.
type stubHubClientProviderWithError struct {
	err error
}

func (s *stubHubClientProviderWithError) GetClient(_ context.Context, _ string) (*HubClientInfo, error) {
	return nil, s.err
}
