package consoleproxy

import (
	"context"
	"io"
	"log/slog"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func validKubeconfig() []byte {
	return []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://remote-cluster:6443
  name: remote
contexts:
- context:
    cluster: remote
    user: test
  name: remote
current-context: remote
users:
- name: test
  user:
    token: test-token
`)
}

func kubeconfigWithExec() []byte {
	return []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://remote-cluster:6443
  name: remote
contexts:
- context:
    cluster: remote
    user: test
  name: remote
current-context: remote
users:
- name: test
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: /bin/sh
      args:
      - -c
      - echo should-not-run
      interactiveMode: Never
`)
}

func kubeconfigWithAuthProvider() []byte {
	return []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://remote-cluster:6443
  name: remote
contexts:
- context:
    cluster: remote
    user: test
  name: remote
current-context: remote
users:
- name: test
  user:
    auth-provider:
      name: oidc
      config:
        idp-issuer-url: https://accounts.example.com
        client-id: my-client
`)
}

func labeledSecret(namespace, name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels:    map[string]string{RemoteKubeconfigLabel: "true"},
		},
		Data: data,
	}
}

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

var _ = Describe("RemoteConfigResolver", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
	})

	DescribeTable("resolves remote kubeconfig",
		func(namespace string, objects []runtime.Object, wantHost, wantErr string) {
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objects...).
				Build()

			resolver := NewRemoteConfigResolver(c, discardLogger)
			config, _, err := resolver.ResolveConfig(context.Background(), namespace)

			if wantErr != "" {
				Expect(err).To(MatchError(ContainSubstring(wantErr)))
				return
			}

			Expect(err).NotTo(HaveOccurred())
			Expect(config.Host).To(Equal(wantHost))
			Expect(config.ExecProvider).To(BeNil(), "exec plugin must be stripped from parsed kubeconfig")
			Expect(config.AuthProvider).To(BeNil(), "auth-provider must be stripped from parsed kubeconfig")
		},
		Entry("success",
			"osac-dev",
			[]runtime.Object{
				labeledSecret("osac-dev", "remote-kubeconfig", map[string][]byte{
					remoteKubeconfigKey: validKubeconfig(),
				}),
			},
			"https://remote-cluster:6443", ""),
		Entry("no labeled secret",
			"osac-dev",
			[]runtime.Object{},
			"", "no remote kubeconfig secret found"),
		Entry("secret without kubeconfig key",
			"osac-dev",
			[]runtime.Object{
				labeledSecret("osac-dev", "bad-secret", map[string][]byte{
					"other-key": []byte("data"),
				}),
			},
			"", `has no "kubeconfig" key`),
		Entry("invalid kubeconfig data",
			"osac-dev",
			[]runtime.Object{
				labeledSecret("osac-dev", "bad-kubeconfig", map[string][]byte{
					remoteKubeconfigKey: []byte("not valid yaml {{{"),
				}),
			},
			"", "parsing kubeconfig"),
		Entry("multiple secrets picks first without error",
			"osac-dev",
			[]runtime.Object{
				labeledSecret("osac-dev", "first", map[string][]byte{
					remoteKubeconfigKey: validKubeconfig(),
				}),
				labeledSecret("osac-dev", "second", map[string][]byte{
					remoteKubeconfigKey: validKubeconfig(),
				}),
			},
			"https://remote-cluster:6443", ""),
		Entry("secret in wrong namespace is not found",
			"osac-dev",
			[]runtime.Object{
				labeledSecret("other-namespace", "remote-kubeconfig", map[string][]byte{
					remoteKubeconfigKey: validKubeconfig(),
				}),
			},
			"", "no remote kubeconfig secret found"),
		Entry("strips exec plugin from kubeconfig",
			"osac-dev",
			[]runtime.Object{
				labeledSecret("osac-dev", "exec-kubeconfig", map[string][]byte{
					remoteKubeconfigKey: kubeconfigWithExec(),
				}),
			},
			"https://remote-cluster:6443", ""),
		Entry("strips auth-provider from kubeconfig",
			"osac-dev",
			[]runtime.Object{
				labeledSecret("osac-dev", "oidc-kubeconfig", map[string][]byte{
					remoteKubeconfigKey: kubeconfigWithAuthProvider(),
				}),
			},
			"https://remote-cluster:6443", ""),
	)

	It("returns source containing the secret name", func() {
		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(labeledSecret("ns", "my-remote-kc", map[string][]byte{
				remoteKubeconfigKey: validKubeconfig(),
			})).
			Build()

		resolver := NewRemoteConfigResolver(c, discardLogger)
		_, source, err := resolver.ResolveConfig(context.Background(), "ns")
		Expect(err).NotTo(HaveOccurred())
		Expect(source).To(ContainSubstring("my-remote-kc"))
	})
})

var _ = Describe("LocalConfigResolver", func() {
	It("returns a copy of the provided config", func() {
		original := &rest.Config{Host: "https://local-cluster:6443"}
		resolver := NewLocalConfigResolver(original, discardLogger)

		config, source, err := resolver.ResolveConfig(context.Background(), "any-namespace")
		Expect(err).NotTo(HaveOccurred())
		Expect(config.Host).To(Equal(original.Host))
		Expect(source).To(ContainSubstring("local"))
		Expect(config).NotTo(BeIdenticalTo(original))
	})
})

var _ = Describe("AutoConfigResolver", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
	})

	Context("when remote secret exists", func() {
		It("uses remote", func() {
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(labeledSecret("ns", "remote-kc", map[string][]byte{
					remoteKubeconfigKey: validKubeconfig(),
				})).
				Build()

			remote := NewRemoteConfigResolver(c, discardLogger)
			local := NewLocalConfigResolver(&rest.Config{Host: "https://local:6443"}, discardLogger)
			resolver := NewAutoConfigResolver(remote, local, discardLogger)

			config, source, err := resolver.ResolveConfig(context.Background(), "ns")
			Expect(err).NotTo(HaveOccurred())
			Expect(config.Host).To(Equal("https://remote-cluster:6443"))
			Expect(source).To(ContainSubstring("remote"))
		})
	})

	Context("when no remote secret", func() {
		It("falls back to local", func() {
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			remote := NewRemoteConfigResolver(c, discardLogger)
			local := NewLocalConfigResolver(&rest.Config{Host: "https://local:6443"}, discardLogger)
			resolver := NewAutoConfigResolver(remote, local, discardLogger)

			config, source, err := resolver.ResolveConfig(context.Background(), "ns")
			Expect(err).NotTo(HaveOccurred())
			Expect(config.Host).To(Equal("https://local:6443"))
			Expect(source).To(ContainSubstring("local"))
		})
	})

	Context("when kubeconfig is malformed", func() {
		It("does not fall back", func() {
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(labeledSecret("ns", "bad", map[string][]byte{
					remoteKubeconfigKey: []byte("not valid {{{"),
				})).
				Build()

			remote := NewRemoteConfigResolver(c, discardLogger)
			local := NewLocalConfigResolver(&rest.Config{Host: "https://local:6443"}, discardLogger)
			resolver := NewAutoConfigResolver(remote, local, discardLogger)

			_, _, err := resolver.ResolveConfig(context.Background(), "ns")
			Expect(err).To(MatchError(ContainSubstring("no fallback")))
		})
	})

	Context("when kubeconfig key is missing", func() {
		It("does not fall back", func() {
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(labeledSecret("ns", "no-key", map[string][]byte{
					"wrong-key": validKubeconfig(),
				})).
				Build()

			remote := NewRemoteConfigResolver(c, discardLogger)
			local := NewLocalConfigResolver(&rest.Config{Host: "https://local:6443"}, discardLogger)
			resolver := NewAutoConfigResolver(remote, local, discardLogger)

			_, _, err := resolver.ResolveConfig(context.Background(), "ns")
			Expect(err).To(MatchError(ContainSubstring("no fallback")))
		})
	})
})
