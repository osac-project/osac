// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package assisted

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// discoveryPullSecret supplies a valid native authentication input without live credentials.
func discoveryPullSecret(namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "pull-secret"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{"example.invalid":{"auth":"dGVzdDp0ZXN0"}}}`),
		},
	}
}

// registrationInputCase supplies native registration fields and the expected validation result.
type registrationInputCase struct {
	installed    bool
	conditions   []hivev1.ClusterInstallCondition
	pullSecret   *corev1.LocalObjectReference
	imageSet     *hivev1.ClusterImageSetReference
	releaseImage string
	ignition     *installv1.IgnitionEndpoint
	wantError    bool
}

var _ = Describe("InfraEnv discovery publication", func() {
	DescribeTable("validates native inputs before waiting for an artifact URL", func(ctx SpecContext, valid bool) {
		infra := &assistedv1.InfraEnv{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "discovery", UID: "original"}}
		if valid {
			infra.Spec.PullSecretRef = &corev1.LocalObjectReference{Name: "pull-secret"}
		}

		c := fake.NewClientBuilder().WithScheme(agentScheme()).WithObjects(infra, discoveryPullSecret(infra.Namespace)).
			WithStatusSubresource(infra).Build()
		reads := 0
		r := &InfraEnvReconciler{
			Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"agents"}},
			DiscoveryURL: func(context.Context, *assistedv1.InfraEnv) (string, error) {
				reads++
				return "", nil
			},
		}
		result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(infra)})
		Expect(c.Get(ctx, client.ObjectKeyFromObject(infra), infra)).To(Succeed())
		Expect(infra.Status.BootArtifacts.DiscoveryIgnitionURL).To(BeEmpty())

		if valid {
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).NotTo(BeZero())
			Expect(reads).To(Equal(1))
			Expect(infra.Status.Conditions).To(BeEmpty())
		} else {
			Expect(err).To(MatchError("discovery pull secret reference is required"))
			Expect(reads).To(BeZero())
			Expect(conditionsv1.IsStatusConditionFalse(infra.Status.Conditions, assistedv1.ImageCreatedCondition)).To(BeTrue())
		}
	}, Entry("valid inputs", true), Entry("invalid inputs", false))

	DescribeTable(
		"requires registration inputs without later day-two credentials",
		func(ctx SpecContext, inputs registrationInputCase) {
			agent, c := installingAgent()
			deployment := &hivev1.ClusterDeployment{}
			Expect(c.Get(ctx, client.ObjectKey{
				Namespace: agent.Spec.ClusterDeploymentName.Namespace,
				Name:      agent.Spec.ClusterDeploymentName.Name,
			}, deployment)).To(Succeed())

			installation := &installv1.AgentClusterInstall{}
			Expect(c.Get(ctx, client.ObjectKey{
				Namespace: deployment.Namespace,
				Name:      deployment.Spec.ClusterInstallRef.Name,
			}, installation)).To(Succeed())
			deployment.Spec.ClusterMetadata = nil
			deployment.Spec.Installed = inputs.installed
			deployment.Spec.PullSecretRef = inputs.pullSecret
			installation.Spec.ImageSetRef = inputs.imageSet
			installation.Spec.IgnitionEndpoint = inputs.ignition
			installation.Status.Conditions = inputs.conditions
			image := &hivev1.ClusterImageSet{
				ObjectMeta: metav1.ObjectMeta{Name: "release"},
				Spec:       hivev1.ClusterImageSetSpec{ReleaseImage: inputs.releaseImage},
			}
			Expect(c.Create(ctx, image)).To(Succeed())
			Expect(c.Update(ctx, deployment)).To(Succeed())
			Expect(c.Update(ctx, installation)).To(Succeed())
			r := &InfraEnvReconciler{Client: c, APIReader: c}

			err := r.registrationInputs(ctx, deployment)
			if inputs.wantError {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		},
		Entry("day one", registrationInputCase{
			pullSecret:   &corev1.LocalObjectReference{Name: "pull-secret"},
			imageSet:     &hivev1.ClusterImageSetReference{Name: "release"},
			releaseImage: "release-image",
		}),
		Entry("missing day-one ignition CA", registrationInputCase{
			pullSecret:   &corev1.LocalObjectReference{Name: "pull-secret"},
			imageSet:     &hivev1.ClusterImageSetReference{Name: "release"},
			releaseImage: "release-image",
			ignition: &installv1.IgnitionEndpoint{
				CaCertificateReference: &installv1.CaCertificateReference{Namespace: "control-plane", Name: "absent"},
			},
			wantError: true,
		}),
		Entry("installed day two", registrationInputCase{installed: true}),
		Entry("completed installation", registrationInputCase{
			conditions: []hivev1.ClusterInstallCondition{{
				Type: installv1.ClusterCompletedCondition, Status: corev1.ConditionTrue, Reason: installv1.ClusterInstalledReason,
			}},
		}),
		Entry("installed reason with false completion follows native classification", registrationInputCase{
			conditions: []hivev1.ClusterInstallCondition{{
				Type: installv1.ClusterCompletedCondition, Status: corev1.ConditionFalse, Reason: installv1.ClusterInstalledReason,
			}},
		}),
		Entry("installed reason with unknown completion follows native classification", registrationInputCase{
			conditions: []hivev1.ClusterInstallCondition{
				{
					Type:   installv1.ClusterCompletedCondition,
					Status: corev1.ConditionUnknown,
					Reason: installv1.ClusterInstalledReason,
				},
			},
		}),
		Entry("in-progress completion requires day-one inputs", registrationInputCase{
			conditions: []hivev1.ClusterInstallCondition{{
				Type: installv1.ClusterCompletedCondition, Status: corev1.ConditionFalse,
				Reason: installv1.ClusterInstallationInProgressReason,
			}},
			wantError: true,
		}),
		Entry("a true condition with another reason requires day-one inputs", registrationInputCase{
			conditions: []hivev1.ClusterInstallCondition{{
				Type: installv1.ClusterCompletedCondition, Status: corev1.ConditionTrue,
				Reason: installv1.ClusterInstallationInProgressReason,
			}},
			wantError: true,
		}),
		Entry("installed registration validates a supplied pull-secret reference", registrationInputCase{
			installed: true, pullSecret: &corev1.LocalObjectReference{Name: "absent"}, wantError: true,
		}),
		Entry("missing image reference", registrationInputCase{
			pullSecret:   &corev1.LocalObjectReference{Name: "pull-secret"},
			releaseImage: "release-image", wantError: true,
		}),
		Entry("empty release image", registrationInputCase{
			pullSecret: &corev1.LocalObjectReference{Name: "pull-secret"},
			imageSet:   &hivev1.ClusterImageSetReference{Name: "release"},
			wantError:  true,
		}),
		Entry("missing pull reference", registrationInputCase{
			imageSet:     &hivev1.ClusterImageSetReference{Name: "release"},
			releaseImage: "release-image", wantError: true,
		}),
	)

	var (
		infra  *assistedv1.InfraEnv
		secret *corev1.Secret
	)

	BeforeEach(func() {
		infra = &assistedv1.InfraEnv{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "agents", Name: "discovery", UID: "original",
				Finalizers: []string{"foreign.example/cleanup"}, Annotations: map[string]string{"retained": "yes"},
			},
			Spec: assistedv1.InfraEnvSpec{PullSecretRef: &corev1.LocalObjectReference{Name: "pull-secret"}},
		}
		infra.Status.BootArtifacts.InitrdURL = "https://example.invalid/initrd"
		secret = discoveryPullSecret(infra.Namespace)
	})

	DescribeTable(
		"publishes artifacts only after validating native inputs",
		func(ctx SpecContext, variant string, valid bool) {
			switch variant {
			case "missing reference":
				infra.Spec.PullSecretRef = nil
			case "missing secret":
				secret.Name = "other"
			case "valid":
			default:
				secret.Data[corev1.DockerConfigJsonKey] = []byte(variant)
			}

			c := fake.NewClientBuilder().
				WithScheme(agentScheme()).
				WithObjects(infra, secret).
				WithStatusSubresource(infra).
				Build()
			r := &InfraEnvReconciler{
				Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"agents"}},
				DiscoveryURL: func(context.Context, *assistedv1.InfraEnv) (string, error) {
					return "http://ignition.release.svc/ignition.json", nil
				},
			}
			request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(infra)}

			_, err := r.Reconcile(ctx, request)
			if valid {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(HaveOccurred())
			}

			current := &assistedv1.InfraEnv{}
			Expect(c.Get(ctx, request.NamespacedName, current)).To(Succeed())
			Expect(current.UID).To(Equal(infra.UID))
			Expect(current.Spec).To(Equal(infra.Spec))
			Expect(current.Annotations).To(Equal(infra.Annotations))
			Expect(current.Finalizers).To(ConsistOf("foreign.example/cleanup", infraEnvFinalizer))
			Expect(current.Status.BootArtifacts.InitrdURL).To(Equal(infra.Status.BootArtifacts.InitrdURL))

			if valid {
				Expect(current.Status.BootArtifacts.DiscoveryIgnitionURL).To(Equal("http://ignition.release.svc/ignition.json"))
				Expect(conditionsv1.IsStatusConditionTrue(current.Status.Conditions, assistedv1.ImageCreatedCondition)).To(BeTrue())
			} else {
				Expect(current.Status.BootArtifacts.DiscoveryIgnitionURL).To(BeEmpty())
				Expect(
					conditionsv1.IsStatusConditionFalse(current.Status.Conditions, assistedv1.ImageCreatedCondition),
				).To(BeTrue())
			}

			By("settling without rewriting status or metadata")

			before := current.DeepCopy()

			_, err = r.Reconcile(ctx, request)
			if valid {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(HaveOccurred())
			}

			Expect(c.Get(ctx, request.NamespacedName, current)).To(Succeed())
			Expect(current).To(Equal(before))
		},
		Entry("valid discovery inputs", "valid", true),
		Entry("missing pull-secret reference", "missing reference", false),
		Entry("missing credentials", "missing secret", false),
		Entry("malformed credentials", "invalid", false),
		Entry("empty authentication map", `{"auths":{}}`, false),
		Entry("non-object entry", `{"auths":{"example.invalid":[]}}`, false),
		Entry("non-string auth", `{"auths":{"example.invalid":{"auth":42}}}`, false),
		Entry("null auth", `{"auths":{"example.invalid":{"auth":null}}}`, false),
		Entry("invalid base64 auth", `{"auths":{"example.invalid":{"auth":"!"}}}`, false),
		Entry("auth without a colon", `{"auths":{"example.invalid":{"auth":"dGVzdA=="}}}`, false),
		Entry("omitted auth", `{"auths":{"example.invalid":{}}}`, true),
		Entry("empty auth", `{"auths":{"example.invalid":{"auth":""}}}`, true),
		Entry("null entry", `{"auths":{"example.invalid":null}}`, true),
	)

	It("updates discovery across ClusterDeployment events", func(ctx SpecContext) {
		infra.Spec.ClusterRef = &assistedv1.ClusterReference{Namespace: "cp", Name: "deployment"}
		c := fake.NewClientBuilder().WithScheme(agentScheme()).WithObjects(infra, secret).
			WithStatusSubresource(infra).Build()
		r := &InfraEnvReconciler{
			Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"agents"}},
			DiscoveryURL: func(context.Context, *assistedv1.InfraEnv) (string, error) {
				return "http://ignition.release.svc/ignition.json", nil
			},
		}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(infra)}
		result, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).NotTo(BeZero())
		Expect(c.Get(ctx, request.NamespacedName, infra)).To(Succeed())
		condition := conditionsv1.FindStatusCondition(infra.Status.Conditions, assistedv1.ImageCreatedCondition)
		Expect(condition.Status).To(Equal(corev1.ConditionUnknown))
		Expect(condition.Reason).To(Equal(assistedv1.MissingClusterDeploymentReason))
		Expect(infra.Status.BootArtifacts.DiscoveryIgnitionURL).To(BeEmpty())

		deployment := &hivev1.ClusterDeployment{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "deployment"}}
		Expect(c.Create(ctx, deployment)).To(Succeed())
		_, err = r.Reconcile(ctx, request)
		Expect(err).To(HaveOccurred(), "an empty deployment cannot register a backend cluster")
		Expect(c.Get(ctx, request.NamespacedName, infra)).To(Succeed())
		Expect(conditionsv1.IsStatusConditionTrue(infra.Status.Conditions, assistedv1.ImageCreatedCondition)).To(BeFalse())

		deployment.Spec.Installed = true
		deployment.Spec.ClusterInstallRef = &hivev1.ClusterInstallLocalReference{
			Group: installv1.GroupVersion.Group, Version: installv1.GroupVersion.Version,
			Kind: "AgentClusterInstall", Name: "installation",
		}
		Expect(c.Update(ctx, deployment)).To(Succeed())
		result, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).NotTo(BeZero())

		installation := &installv1.AgentClusterInstall{
			ObjectMeta: metav1.ObjectMeta{Namespace: deployment.Namespace, Name: "installation"},
			Spec:       installv1.AgentClusterInstallSpec{ClusterDeploymentRef: corev1.LocalObjectReference{Name: "other"}},
		}
		Expect(c.Create(ctx, installation)).To(Succeed())
		_, err = r.Reconcile(ctx, request)
		Expect(err).To(HaveOccurred(), "the native installation association must match")

		installation.Spec.ClusterDeploymentRef.Name = deployment.Name
		Expect(c.Update(ctx, installation)).To(Succeed())
		_, err = r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, request.NamespacedName, infra)).To(Succeed())
		Expect(conditionsv1.IsStatusConditionTrue(infra.Status.Conditions, assistedv1.ImageCreatedCondition)).To(BeTrue())
		Expect(infra.Status.BootArtifacts.DiscoveryIgnitionURL).NotTo(BeEmpty())

		By("following the deleted deployment's exact reference within the served namespaces")

		otherDeployment := &assistedv1.InfraEnv{
			ObjectMeta: metav1.ObjectMeta{Namespace: infra.Namespace, Name: "other-deployment"},
			Spec: assistedv1.InfraEnvSpec{
				ClusterRef: &assistedv1.ClusterReference{Namespace: "elsewhere", Name: deployment.Name},
			},
		}
		outOfScope := &assistedv1.InfraEnv{
			ObjectMeta: metav1.ObjectMeta{Namespace: "outside", Name: infra.Name},
			Spec:       assistedv1.InfraEnvSpec{ClusterRef: infra.Spec.ClusterRef.DeepCopy()},
		}

		Expect(c.Create(ctx, otherDeployment)).To(Succeed())
		Expect(c.Create(ctx, outOfScope)).To(Succeed())

		Expect(c.Delete(ctx, deployment)).To(Succeed())

		r.Scope.AgentNamespaces = []string{"unavailable", infra.Namespace}
		r.Client = interceptor.NewClient(c, interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if (&client.ListOptions{}).ApplyOptions(opts).Namespace == "unavailable" {
					return errors.New("namespace list unavailable")
				}

				return c.List(ctx, list, opts...)
			},
		})
		requests := r.infraEnvsForDeployment(ctx, deployment)
		Expect(requests).To(ConsistOf(request))
		result, err = r.Reconcile(ctx, requests[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).NotTo(BeZero())
		Expect(c.Get(ctx, request.NamespacedName, infra)).To(Succeed())
		condition = conditionsv1.FindStatusCondition(infra.Status.Conditions, assistedv1.ImageCreatedCondition)
		Expect(condition.Status).To(Equal(corev1.ConditionUnknown))
		Expect(condition.Reason).To(Equal(assistedv1.MissingClusterDeploymentReason))
	})

	DescribeTable("retains discovery status through registration and credential recovery",
		func(ctx SpecContext, credentialFailure string) {
			infra.Spec.ClusterRef = &assistedv1.ClusterReference{Namespace: "cp", Name: "deployment"}
			infra.Status.BootArtifacts.DiscoveryIgnitionURL = "http://ignition.release.svc/previous.json"
			deployment := &hivev1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "deployment"},
				Spec: hivev1.ClusterDeploymentSpec{
					Installed: true,
					ClusterInstallRef: &hivev1.ClusterInstallLocalReference{
						Group: installv1.GroupVersion.Group, Kind: "AgentClusterInstall", Name: "installation",
					},
				},
			}
			installation := &installv1.AgentClusterInstall{
				ObjectMeta: metav1.ObjectMeta{Namespace: deployment.Namespace, Name: "installation"},
				Spec: installv1.AgentClusterInstallSpec{
					ClusterDeploymentRef: corev1.LocalObjectReference{Name: deployment.Name},
				},
			}
			failure := errors.New("credential read unavailable")
			refuse := false
			reads, artifacts := 0, 0
			c := fake.NewClientBuilder().WithScheme(agentScheme()).WithObjects(infra, secret, deployment, installation).
				WithStatusSubresource(infra).WithInterceptorFuncs(interceptor.Funcs{Get: func(
				ctx context.Context, c client.WithWatch, key client.ObjectKey, object client.Object, opts ...client.GetOption,
			) error {
				if _, credential := object.(*corev1.Secret); credential {
					reads++

					if refuse {
						return failure
					}
				}

				return c.Get(ctx, key, object, opts...)
			}}).Build()
			r := &InfraEnvReconciler{
				Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"agents"}},
				DiscoveryURL: func(context.Context, *assistedv1.InfraEnv) (string, error) {
					artifacts++

					return "http://ignition.release.svc/recovered.json", nil
				},
			}
			request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(infra)}

			By("losing registration and credentials together")

			Expect(c.Delete(ctx, installation)).To(Succeed())
			Expect(c.Delete(ctx, secret)).To(Succeed())
			result, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).NotTo(BeZero())
			Expect(reads).To(BeZero(), "registration failure must precede discovery credential reads")
			Expect(artifacts).To(BeZero())
			Expect(c.Get(ctx, request.NamespacedName, infra)).To(Succeed())
			condition := conditionsv1.FindStatusCondition(infra.Status.Conditions, assistedv1.ImageCreatedCondition)
			Expect(condition.Status).To(Equal(corev1.ConditionUnknown))
			Expect(condition.Reason).To(Equal(assistedv1.MissingClusterDeploymentReason))
			Expect(infra.Status.BootArtifacts.DiscoveryIgnitionURL).To(Equal("http://ignition.release.svc/previous.json"))

			By("restoring registration while discovery credentials remain unavailable")

			installation.ResourceVersion = ""
			Expect(c.Create(ctx, installation)).To(Succeed())

			if credentialFailure != "missing" {
				secret.ResourceVersion = ""
				if credentialFailure == "malformed" {
					secret.Data[corev1.DockerConfigJsonKey] = []byte("invalid")
				}

				Expect(c.Create(ctx, secret)).To(Succeed())

				refuse = credentialFailure == "API"
			}

			_, err = r.Reconcile(ctx, request)

			switch credentialFailure {
			case "missing":
				Expect(apierrors.IsNotFound(err)).To(BeTrue())
			case "malformed":
				Expect(err).To(MatchError("invalid pull secret"))
			case "API":
				Expect(errors.Is(err, failure)).To(BeTrue())
			}

			Expect(reads).To(Equal(1))
			Expect(artifacts).To(BeZero())
			Expect(c.Get(ctx, request.NamespacedName, infra)).To(Succeed())
			condition = conditionsv1.FindStatusCondition(infra.Status.Conditions, assistedv1.ImageCreatedCondition)
			Expect(condition.Status).To(Equal(corev1.ConditionFalse))
			Expect(condition.Reason).To(Equal(assistedv1.ImageCreationErrorReason))
			Expect(infra.Status.BootArtifacts.DiscoveryIgnitionURL).To(Equal("http://ignition.release.svc/previous.json"))

			By("publishing the current artifact after credential recovery on the same InfraEnv")

			refuse = false

			if credentialFailure == "missing" {
				secret.ResourceVersion = ""
				Expect(c.Create(ctx, secret)).To(Succeed())
			} else {
				secret.Data = discoveryPullSecret(infra.Namespace).Data
				Expect(c.Update(ctx, secret)).To(Succeed())
			}

			_, err = r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(artifacts).To(Equal(1))
			Expect(c.Get(ctx, request.NamespacedName, infra)).To(Succeed())
			Expect(infra.UID).To(BeEquivalentTo("original"))
			Expect(infra.Annotations).To(HaveKeyWithValue("retained", "yes"))
			Expect(infra.Finalizers).To(ConsistOf("foreign.example/cleanup", infraEnvFinalizer))
			Expect(infra.Status.BootArtifacts.InitrdURL).To(Equal("https://example.invalid/initrd"))
			Expect(infra.Status.BootArtifacts.DiscoveryIgnitionURL).To(Equal("http://ignition.release.svc/recovered.json"))
			Expect(conditionsv1.IsStatusConditionTrue(infra.Status.Conditions, assistedv1.ImageCreatedCondition)).To(BeTrue())
		},
		Entry("missing credentials", "missing"),
		Entry("malformed credentials", "malformed"),
		Entry("credential API failure", "API"),
	)

	It("keeps retrying a credential read failure until the API recovers", func(ctx SpecContext) {
		failure := errors.New("credential read unavailable")
		refuse := true
		c := fake.NewClientBuilder().WithScheme(agentScheme()).WithObjects(infra, secret).
			WithStatusSubresource(infra).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(
				ctx context.Context, c client.WithWatch, key client.ObjectKey,
				object client.Object, opts ...client.GetOption,
			) error {
				if _, credential := object.(*corev1.Secret); credential && refuse {
					return failure
				}

				return c.Get(ctx, key, object, opts...)
			},
		}).Build()

		r := &InfraEnvReconciler{
			Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"agents"}},
			DiscoveryURL: func(context.Context, *assistedv1.InfraEnv) (string, error) {
				return "http://ignition.release.svc/ignition.json", nil
			},
		}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(infra)}
		for range 2 {
			_, err := r.Reconcile(ctx, request)
			Expect(err).To(MatchError(failure))
		}

		Expect(c.Get(ctx, request.NamespacedName, infra)).To(Succeed())
		Expect(conditionsv1.IsStatusConditionFalse(infra.Status.Conditions, assistedv1.ImageCreatedCondition)).To(BeTrue())

		refuse = false
		_, err := r.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, request.NamespacedName, infra)).To(Succeed())
		Expect(infra.Status.BootArtifacts.DiscoveryIgnitionURL).NotTo(BeEmpty())
		Expect(conditionsv1.IsStatusConditionTrue(infra.Status.Conditions, assistedv1.ImageCreatedCondition)).To(BeTrue())
	})

	It("cleans its own finalizer and preserves foreign cleanup", func(ctx SpecContext) {
		infra.Finalizers = append(infra.Finalizers, infraEnvFinalizer)
		c := fake.NewClientBuilder().WithScheme(agentScheme()).WithObjects(infra).Build()
		Expect(c.Delete(ctx, infra)).To(Succeed())
		r := &InfraEnvReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"agents"}}}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(infra)})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(infra), infra)).To(Succeed())
		Expect(infra.Finalizers).To(ConsistOf("foreign.example/cleanup"))
	})

	It("ignores environments outside served namespaces", func(ctx SpecContext) {
		c := fake.NewClientBuilder().WithScheme(agentScheme()).WithObjects(infra).Build()
		r := &InfraEnvReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"foreign"}}}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(infra)})
		Expect(err).NotTo(HaveOccurred())

		current := &assistedv1.InfraEnv{}
		Expect(c.Get(ctx, client.ObjectKeyFromObject(infra), current)).To(Succeed())
		Expect(current.Finalizers).To(Equal(infra.Finalizers))
		Expect(current.Status).To(Equal(infra.Status))
	})

	DescribeTable("preserves API failures", func(ctx SpecContext, operation string) {
		failure := errors.New("API unavailable")
		c := fake.NewClientBuilder().WithScheme(agentScheme()).WithObjects(infra, secret).WithStatusSubresource(infra).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(
					ctx context.Context,
					c client.WithWatch,
					key client.ObjectKey,
					object client.Object,
					opts ...client.GetOption,
				) error {
					if operation == "read" {
						return failure
					}

					return c.Get(ctx, key, object, opts...)
				},
				SubResourcePatch: func(
					context.Context,
					client.Client,
					string,
					client.Object,
					client.Patch,
					...client.SubResourcePatchOption,
				) error {
					return failure
				},
			}).Build()
		r := &InfraEnvReconciler{
			Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"agents"}},
			DiscoveryURL: func(context.Context, *assistedv1.InfraEnv) (string, error) {
				return "http://ignition.release.svc/ignition.json", nil
			},
		}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(infra)})
		Expect(errors.Is(err, failure)).To(BeTrue())
	}, Entry("reading the environment", "read"), Entry("publishing status", "status"))
})
