package consoleproxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
)

const (
	VMClusterModeRemote = "remote"
	VMClusterModeLocal  = "local"
	VMClusterModeAuto   = "auto"

	RemoteKubeconfigLabel = "osac.openshift.io/remote-cluster-kubeconfig"
	remoteKubeconfigKey   = "kubeconfig"
)

// ConfigResolver resolves the rest.Config used to connect to the cluster
// where KubeVirt VMs run. The namespace parameter scopes the lookup
// to the ComputeInstance's namespace.
type ConfigResolver interface {
	ResolveConfig(ctx context.Context, namespace string) (*rest.Config, string, error)
}

func NewHubClient(hubConfig *rest.Config) (client.Client, error) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(osacv1alpha1.AddToScheme(scheme))
	return client.New(hubConfig, client.Options{Scheme: scheme})
}

// RemoteConfigResolver looks up a Secret labeled with
// osac.openshift.io/remote-cluster-kubeconfig in the given namespace
// and parses the kubeconfig from it.
type RemoteConfigResolver struct {
	client client.Client
	logger *slog.Logger
}

func NewRemoteConfigResolver(c client.Client, logger *slog.Logger) *RemoteConfigResolver {
	return &RemoteConfigResolver{client: c, logger: logger}
}

func (r *RemoteConfigResolver) ResolveConfig(ctx context.Context, namespace string) (*rest.Config, string, error) {
	var secretList corev1.SecretList
	err := r.client.List(ctx, &secretList,
		client.InNamespace(namespace),
		client.MatchingLabels{RemoteKubeconfigLabel: "true"},
	)
	if err != nil {
		return nil, "", fmt.Errorf("listing remote kubeconfig secrets in namespace %q: %w", namespace, err)
	}
	if len(secretList.Items) == 0 {
		return nil, "", &NoRemoteSecretError{Namespace: namespace}
	}
	if len(secretList.Items) > 1 {
		names := make([]string, len(secretList.Items))
		for i := range secretList.Items {
			names[i] = secretList.Items[i].Name
		}
		r.logger.WarnContext(ctx,
			fmt.Sprintf("%d secrets labeled %s found in namespace %q, using %q",
				len(secretList.Items), RemoteKubeconfigLabel, namespace, secretList.Items[0].Name),
			slog.Any("allSecrets", names),
		)
	}
	secret := secretList.Items[0]
	kubeconfigData, ok := secret.Data[remoteKubeconfigKey]
	if !ok || len(kubeconfigData) == 0 {
		return nil, "", fmt.Errorf("secret %q in namespace %q has no %q key", secret.Name, namespace, remoteKubeconfigKey)
	}
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
	if err != nil {
		return nil, "", fmt.Errorf("parsing kubeconfig from secret %q in namespace %q: %w", secret.Name, namespace, err)
	}
	config.ExecProvider = nil
	config.AuthProvider = nil
	r.logger.InfoContext(ctx, "Resolved VM cluster config from remote kubeconfig secret",
		slog.String("secret", secret.Name),
		slog.String("namespace", namespace),
	)
	return config, "remote (secret: " + secret.Name + ")", nil
}

// NoRemoteSecretError signals that no remote kubeconfig Secret was found.
// AutoConfigResolver uses this to decide whether fallback to local is safe.
type NoRemoteSecretError struct {
	Namespace string
}

func (e *NoRemoteSecretError) Error() string {
	return fmt.Sprintf("no remote kubeconfig secret found in namespace %q", e.Namespace)
}

// LocalConfigResolver returns the in-cluster rest.Config, meaning
// KubeVirt VMs are expected to run on the same cluster as the proxy.
type LocalConfigResolver struct {
	config *rest.Config
	logger *slog.Logger
}

func NewLocalConfigResolver(config *rest.Config, logger *slog.Logger) *LocalConfigResolver {
	return &LocalConfigResolver{config: config, logger: logger}
}

func (l *LocalConfigResolver) ResolveConfig(ctx context.Context, namespace string) (*rest.Config, string, error) {
	l.logger.DebugContext(ctx, "Using local in-cluster config for VM cluster",
		slog.String("namespace", namespace),
	)
	cfg := rest.CopyConfig(l.config)
	return cfg, "local (in-cluster)", nil
}

// AutoConfigResolver tries the remote resolver first. If no labeled
// Secret exists it falls back to the local in-cluster config. Any other
// remote error (API failures, malformed data) is returned without fallback
// to avoid masking real configuration problems.
type AutoConfigResolver struct {
	remote *RemoteConfigResolver
	local  *LocalConfigResolver
	logger *slog.Logger
}

func NewAutoConfigResolver(remote *RemoteConfigResolver, local *LocalConfigResolver, logger *slog.Logger) *AutoConfigResolver {
	return &AutoConfigResolver{remote: remote, local: local, logger: logger}
}

func (a *AutoConfigResolver) ResolveConfig(ctx context.Context, namespace string) (*rest.Config, string, error) {
	config, source, err := a.remote.ResolveConfig(ctx, namespace)
	if err == nil {
		return config, source, nil
	}

	if !isNoRemoteSecretError(err) {
		return nil, "", fmt.Errorf("remote kubeconfig lookup failed (no fallback for this error): %w", err)
	}

	a.logger.WarnContext(ctx, "Remote kubeconfig secret not found, falling back to local in-cluster config",
		slog.String("namespace", namespace),
		slog.String("remoteError", err.Error()),
	)
	return a.local.ResolveConfig(ctx, namespace)
}

func isNoRemoteSecretError(err error) bool {
	var target *NoRemoteSecretError
	return errors.As(err, &target)
}
