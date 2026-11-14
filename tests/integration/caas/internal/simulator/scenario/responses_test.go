// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package scenario

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/assisted"
	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

var _ = Describe("Native component response inputs", func() {
	Context("with two independently configured cases", func() {
		var (
			a, b         caseSettings
			settings     *corev1.ConfigMap
			cluster      *capiv1.Cluster
			hc           *hypershiftv1.HostedCluster
			infra, other *assistedv1.InfraEnv
			cp           *hypershiftv1.HostedControlPlane
			c            client.Client
			responses    Responses
		)

		BeforeEach(func() {
			a = caseSettings{
				ID: "case-a", Profile: Normal, DiscoveryURL: "http://ignition.release.svc/p1/ignition.json",
				Roots: map[string]types.UID{infraEnvRoot: "infra-a", hostedClusterRoot: "hc-a", clusterRoot: "cluster"},
			}
			b = caseSettings{
				ID: "case-b", Profile: Normal, DiscoveryURL: "http://ignition.release.svc/p2/ignition.json",
				Roots: map[string]types.UID{infraEnvRoot: "infra-b", hostedClusterRoot: "hc-b", clusterRoot: "cluster-b"},
			}
			settings = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "settings", Name: settingsName}}
			Expect(setCase(settings, "case-a", a)).To(Succeed())
			Expect(setCase(settings, "case-b", b)).To(Succeed())
			foreign := settings.DeepCopy()
			foreign.Namespace = "other"
			hc = &hypershiftv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "cluster", UID: "hc-a"},
				Spec:       hypershiftv1.HostedClusterSpec{InfraID: "cluster-infrastructure"},
			}
			cluster = &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{
				Namespace: "hub-cluster", Name: "cluster-infrastructure", UID: "cluster",
				Annotations: map[string]string{kube.HostedClusterAnnotation: "hub/cluster"},
			}}

			cp = &hypershiftv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
				Namespace: "hub-cluster",
				Name:      "control-plane",
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(cluster, capiv1.GroupVersion.WithKind("Cluster")),
				},
			}}
			cluster.Spec.ControlPlaneRef = &corev1.ObjectReference{
				APIVersion: hypershiftv1.GroupVersion.String(), Kind: "HostedControlPlane",
				Namespace: cp.Namespace, Name: cp.Name,
			}

			infra = &assistedv1.InfraEnv{ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "discovery", UID: "infra-a"}}
			other = infra.DeepCopy()
			other.SetUID("infra-b")

			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			Expect(capiv1.AddToScheme(scheme)).To(Succeed())
			Expect(hypershiftv1.AddToScheme(scheme)).To(Succeed())

			c = fake.NewClientBuilder().WithScheme(scheme).WithObjects(settings, foreign, hc, cluster).Build()
			responses = Responses{Reader: c, Namespace: "settings"}
		})

		DescribeTable(
			"rejects a mismatched control-plane reference",
			func(ctx SpecContext, change func(*corev1.ObjectReference)) {
				Expect(c.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())
				change(cluster.Spec.ControlPlaneRef)
				Expect(c.Update(ctx, cluster)).To(Succeed())

				_, _, err := responses.Availability(ctx, cp)
				Expect(err).To(MatchError(ContainSubstring("control plane differs from its original Cluster")))
			},
			Entry("API version", func(ref *corev1.ObjectReference) { ref.APIVersion = "other.example/v1" }),
			Entry("kind", func(ref *corev1.ObjectReference) { ref.Kind = "OtherControlPlane" }),
			Entry("namespace", func(ref *corev1.ObjectReference) { ref.Namespace = "other" }),
			Entry("UID", func(ref *corev1.ObjectReference) { ref.UID = "other-control-plane" }),
		)

		DescribeTable("accepts an optional matching control-plane UID", func(ctx SpecContext, uid types.UID) {
			cp.UID = "original-control-plane"

			Expect(c.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())
			cluster.Spec.ControlPlaneRef.UID = uid
			Expect(c.Update(ctx, cluster)).To(Succeed())

			available, found, err := responses.Availability(ctx, cp)
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(available).To(BeTrue())
		},
			Entry("unset UID", types.UID("")),
			Entry("original UID", types.UID("original-control-plane")),
		)

		DescribeTable(
			"observes live controls without coupling components or cases",
			func(ctx SpecContext, profile Profile, installation assisted.InstallationResponse, available, holdDetach bool) {
				By("reading the original settings before changing the selected case")

				initial, err := responses.Installation(ctx, infra)
				Expect(err).NotTo(HaveOccurred())
				Expect(initial).To(Equal(assisted.InstallationComplete))

				endpoint, err := responses.DiscoveryURL(ctx, infra)
				Expect(err).NotTo(HaveOccurred())
				Expect(endpoint).To(Equal(a.DiscoveryURL))

				By("changing the profile and invalidating only the discovery URL")
				Expect(c.Get(ctx, client.ObjectKeyFromObject(settings), settings)).To(Succeed())

				a.Profile = profile
				a.DiscoveryURL = "invalid-discovery-url"
				Expect(setCase(settings, "case-a", a)).To(Succeed())
				Expect(c.Update(ctx, settings)).To(Succeed())

				By("routing each component to its own response despite the discovery error")

				got, err := responses.Installation(ctx, infra)
				Expect(err).NotTo(HaveOccurred())
				Expect(got).To(Equal(installation))

				actualAvailability, found, err := responses.Availability(ctx, cp)
				Expect(err).NotTo(HaveOccurred())
				Expect(found).To(BeTrue())
				Expect(actualAvailability).To(Equal(available))

				actualHold, err := responses.HoldDetach(ctx, cluster)
				Expect(err).NotTo(HaveOccurred())
				Expect(actualHold).To(Equal(holdDetach))

				_, err = responses.DiscoveryURL(ctx, infra)
				Expect(err).To(HaveOccurred())

				By("leaving the other case's discovery and installation unchanged")

				endpoint, err = responses.DiscoveryURL(ctx, other)
				Expect(err).NotTo(HaveOccurred())
				Expect(endpoint).To(Equal(b.DiscoveryURL))

				otherInstallation, err := responses.Installation(ctx, other)
				Expect(err).NotTo(HaveOccurred())
				Expect(otherInstallation).To(Equal(assisted.InstallationComplete))
			},
			Entry("normal progress", Normal, assisted.InstallationComplete, true, false),
			Entry("installation held", InstallationStalled, assisted.InstallationHeld, true, false),
			Entry("installation failed", InstallationFailed, assisted.InstallationFailed, true, false),
			Entry("detach held", DetachStalled, assisted.InstallationComplete, true, true),
			Entry("control-plane availability held", ControlPlaneStalled, assisted.InstallationComplete, false, false),
		)

		It("uses the HostedCluster name when infraID is omitted", func(ctx SpecContext) {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), hc)).To(Succeed())
			hc.Spec.InfraID = ""
			Expect(c.Update(ctx, hc)).To(Succeed())
			cluster.SetName(hc.GetName())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(settings), settings)).To(Succeed())

			a.Profile = DetachStalled
			Expect(setCase(settings, "case-a", a)).To(Succeed())
			Expect(c.Update(ctx, settings)).To(Succeed())
			hold, err := responses.HoldDetach(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(hold).To(BeTrue())
		})

		It("waits when the linked HostedCluster is absent", func(ctx SpecContext) {
			Expect(c.Delete(ctx, hc)).To(Succeed())
			_, found, err := responses.Availability(ctx, cp)
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeFalse())

			hold, err := responses.HoldDetach(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(hold).To(BeFalse())
		})

		It("waits when the Cluster has no case binding", func(ctx SpecContext) {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(settings), settings)).To(Succeed())
			delete(a.Roots, clusterRoot)
			Expect(setCase(settings, "case-a", a)).To(Succeed())
			Expect(c.Update(ctx, settings)).To(Succeed())

			_, found, err := responses.Availability(ctx, cp)
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeFalse())

			hold, err := responses.HoldDetach(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(hold).To(BeFalse())
		})

		It("ignores replacement HostedCluster settings for a surviving Cluster", func(ctx SpecContext) {
			Expect(c.Delete(ctx, hc)).To(Succeed())
			hc.UID = "hc-b"
			hc.ResourceVersion = ""
			Expect(c.Create(ctx, hc)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(settings), settings)).To(Succeed())

			b.Profile = DetachStalled
			Expect(setCase(settings, "case-b", b)).To(Succeed())
			Expect(c.Update(ctx, settings)).To(Succeed())

			_, found, err := responses.Availability(ctx, cp)
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeFalse())

			hold, err := responses.HoldDetach(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(hold).To(BeFalse())
		})

		It("rejects duplicate case bindings for a Cluster UID", func(ctx SpecContext) {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(settings), settings)).To(Succeed())

			b.Roots[clusterRoot] = cluster.UID
			Expect(setCase(settings, "case-b", b)).To(Succeed())
			Expect(c.Update(ctx, settings)).To(Succeed())

			_, _, err := responses.Availability(ctx, cp)
			Expect(err).To(MatchError(ContainSubstring("multiple settings match native root")))
			_, err = responses.HoldDetach(ctx, cluster)
			Expect(err).To(MatchError(ContainSubstring("multiple settings match native root")))
		})

		It("selects settings by the live HostedCluster UID", func(ctx SpecContext) {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(settings), settings)).To(Succeed())

			a.Roots[hostedClusterRoot] = "unrelated-root"
			Expect(setCase(settings, "case-a", a)).To(Succeed())
			Expect(c.Update(ctx, settings)).To(Succeed())
			_, found, err := responses.Availability(ctx, cp)
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeFalse())

			hold, err := responses.HoldDetach(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(hold).To(BeFalse())
		})

		DescribeTable("rejects invalid native HostedCluster links", func(ctx SpecContext, link string) {
			cluster.SetAnnotations(map[string]string{kube.HostedClusterAnnotation: link})
			_, err := responses.HoldDetach(ctx, cluster)
			Expect(err).To(HaveOccurred())
		},
			Entry("missing link", ""),
			Entry("missing namespace", "/cluster"),
			Entry("missing name", "hub/"),
		)

		It("rejects a Cluster outside the linked control-plane namespace", func(ctx SpecContext) {
			cluster.SetNamespace("other-cluster")
			_, err := responses.HoldDetach(ctx, cluster)
			Expect(err).To(HaveOccurred())
		})

		It("rejects a Cluster with another infrastructure name", func(ctx SpecContext) {
			cluster.SetName("other-infrastructure")
			_, err := responses.HoldDetach(ctx, cluster)
			Expect(err).To(HaveOccurred())
		})

		It("rejects a replacement Cluster as the source of an old control plane's settings", func(ctx SpecContext) {
			By("confirming that the original Cluster supplies availability")

			_, found, err := responses.Availability(ctx, cp)
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())

			By("replacing the Cluster while retaining its name")
			Expect(c.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())
			Expect(c.Delete(ctx, cluster)).To(Succeed())
			cluster.SetUID("replacement-cluster")
			cluster.SetResourceVersion("")
			Expect(c.Create(ctx, cluster)).To(Succeed())
			_, _, err = responses.Availability(ctx, cp)
			Expect(err).To(HaveOccurred())
		})
	})

	Context("with a shared settings store", func() {
		var (
			c             client.WithWatch
			first, second *Scenario
		)

		BeforeEach(func(ctx SpecContext) {
			store := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Namespace: "settings", Name: settingsName, UID: "original-store",
				Annotations: map[string]string{"foreign.example/metadata": "retained"},
			}}
			c = settingsClient(store)
			first = &Scenario{Client: c, Namespace: "settings", Name: "case-a"}
			second = &Scenario{Client: c, Namespace: "settings", Name: "case-b"}

			Expect(first.Create(ctx, Normal, "http://ignition/p1")).To(Succeed())
			Expect(second.Create(ctx, Normal, "http://ignition/p2")).To(Succeed())
		})

		DescribeTable("keeps bindings exclusive and preserves rejected cases", func(ctx SpecContext, kind string) {
			Expect(first.bind(ctx, kind, "root")).To(Succeed())
			store, _, err := readSettings(ctx, c, "settings")
			Expect(err).NotTo(HaveOccurred())

			before := store.DeepCopy()

			Expect(first.bind(ctx, kind, "root")).To(Succeed())
			Expect(second.bind(ctx, kind, "root")).To(MatchError(ContainSubstring("bound to another case")))
			Expect(first.bind(ctx, kind, "replacement")).To(MatchError(ContainSubstring("incarnation changed")))
			Expect(second.bind(ctx, kind, "")).To(MatchError("native root UID is required"))
			Expect(c.Get(ctx, client.ObjectKeyFromObject(store), store)).To(Succeed())
			Expect(store).To(Equal(before))

			Expect(second.bind(ctx, kind, "another-root")).To(Succeed())
			_, cases, err := readSettings(ctx, c, "settings")
			Expect(err).NotTo(HaveOccurred())
			Expect(cases[first.Name].Roots[kind]).To(Equal(types.UID("root")))
			Expect(cases[second.Name].Roots[kind]).To(Equal(types.UID("another-root")))
		}, Entry("InfraEnv", infraEnvRoot), Entry("HostedCluster", hostedClusterRoot), Entry("Cluster", clusterRoot))

		DescribeTable("permits exactly one concurrent binder", func(ctx SpecContext, kind string) {
			before, _, err := readSettings(ctx, c, "settings")
			Expect(err).NotTo(HaveOccurred())

			first.Client = pairedSettingsReads(c)
			second.Client = first.Client
			results := make(chan error, 2)

			for _, scenario := range []*Scenario{first, second} {
				go func() { results <- scenario.bind(ctx, kind, "root") }()
			}

			outcomes := make([]error, 2)
			for i := range outcomes {
				Eventually(results).WithContext(ctx).Should(Receive(&outcomes[i]))
			}

			Expect(outcomes).To(ConsistOf(Succeed(), MatchError(ContainSubstring("bound to another case"))))

			store, cases, err := readSettings(ctx, c, "settings")
			Expect(err).NotTo(HaveOccurred())
			Expect([]types.UID{cases[first.Name].Roots[kind], cases[second.Name].Roots[kind]}).
				To(ConsistOf(types.UID("root"), types.UID("")))
			Expect(cases[first.Name].ID).To(Equal(first.caseID))
			Expect(cases[second.Name].ID).To(Equal(second.caseID))

			for name, entry := range cases {
				if entry.Roots[kind] == "" {
					Expect(store.Data[name]).To(Equal(before.Data[name]))
				}
			}
		},
			Entry("InfraEnv", infraEnvRoot, SpecTimeout(5*time.Second)),
			Entry("HostedCluster", hostedClusterRoot, SpecTimeout(5*time.Second)),
			Entry("Cluster", clusterRoot, SpecTimeout(5*time.Second)),
		)

		It("preserves concurrent distinct bindings and profile changes", func(ctx SpecContext) {
			By("binding distinct roots from the same settings revision")

			first.Client = pairedSettingsReads(c)

			second.Client = first.Client
			results := make(chan error, 2)
			go func() { results <- first.BindInfraEnv(ctx, "infra-a") }()
			go func() { results <- second.BindHostedCluster(ctx, "hc-b") }()

			for range 2 {
				var err error

				Eventually(results).WithContext(ctx).Should(Receive(&err))
				Expect(err).ToNot(HaveOccurred())
			}

			By("changing each profile from the same settings revision")

			first.Client = pairedSettingsReads(c)
			second.Client = first.Client
			go func() { results <- first.SetProfile(ctx, InstallationStalled) }()
			go func() { results <- second.SetProfile(ctx, DetachStalled) }()

			for range 2 {
				var err error

				Eventually(results).WithContext(ctx).Should(Receive(&err))
				Expect(err).ToNot(HaveOccurred())
			}

			store, cases, err := readSettings(ctx, c, "settings")
			Expect(err).NotTo(HaveOccurred())
			Expect(store.UID).To(Equal(types.UID("original-store")))
			Expect(store.Annotations).To(HaveKeyWithValue("foreign.example/metadata", "retained"))
			Expect(cases[first.Name]).To(Equal(caseSettings{
				ID: first.caseID, Profile: InstallationStalled, DiscoveryURL: "http://ignition/p1",
				Roots: map[string]types.UID{infraEnvRoot: "infra-a"},
			}))
			Expect(cases[second.Name]).To(Equal(caseSettings{
				ID: second.caseID, Profile: DetachStalled, DiscoveryURL: "http://ignition/p2",
				Roots: map[string]types.UID{hostedClusterRoot: "hc-b"},
			}))
		}, SpecTimeout(5*time.Second))

		It("retires only its entry before naturally deleting the last store", func(ctx SpecContext) {
			By("applying a profile without rewriting an unchanged entry")
			Expect(first.BindCluster(ctx, "cluster")).To(Succeed())
			Expect(first.SetProfile(ctx, DetachStalled)).To(Succeed())
			store, _, err := readSettings(ctx, c, "settings")
			Expect(err).NotTo(HaveOccurred())

			before := store.DeepCopy()

			Expect(first.SetProfile(ctx, DetachStalled)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(store), store)).To(Succeed())
			Expect(store).To(Equal(before))

			By("retiring cases one at a time while preserving shared metadata")
			Expect(first.Delete(ctx)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(store), store)).To(Succeed())
			Expect(store.Data).To(Equal(map[string]string{second.Name: before.Data[second.Name]}))
			Expect(store.UID).To(Equal(before.UID))
			Expect(store.Annotations).To(Equal(before.Annotations))
			Expect(second.Delete(ctx)).To(Succeed())
			Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(store), store))).To(BeTrue())
		})

		DescribeTable("preserves replacement settings", func(ctx SpecContext, replacement string) {
			store, _, err := readSettings(ctx, c, "settings")
			Expect(err).NotTo(HaveOccurred())

			if replacement == "store" {
				Expect(c.Delete(ctx, store)).To(Succeed())
				store.UID, store.ResourceVersion = "replacement-store", ""
				Expect(c.Create(ctx, store)).To(Succeed())
			} else {
				Expect(first.Delete(ctx)).To(Succeed())
				newCase := &Scenario{Client: c, Namespace: first.Namespace, Name: first.Name}
				Expect(newCase.Create(ctx, Normal, "http://ignition/new-case")).To(Succeed())
				Expect(newCase.caseID).NotTo(Equal(first.caseID))
			}

			Expect(c.Get(ctx, client.ObjectKeyFromObject(store), store)).To(Succeed())
			before := store.DeepCopy()

			Expect(first.SetProfile(ctx, DetachStalled)).To(MatchError("case settings were replaced"))
			Expect(first.BindCluster(ctx, "cluster")).To(MatchError("case settings were replaced"))
			Expect(first.Delete(ctx)).To(MatchError("case settings were replaced"))
			Expect(c.Get(ctx, client.ObjectKeyFromObject(store), store)).To(Succeed())
			Expect(store).To(Equal(before))
		}, Entry("store UID", "store"), Entry("case identity", "case"))

		DescribeTable("preserves a new case racing with last-case retirement", func(ctx SpecContext, deleteFirst bool) {
			Expect(second.Delete(ctx)).To(Succeed())

			written := make(chan struct{})

			var committed sync.Once

			first.Client = interceptor.NewClient(pairedSettingsReads(c), interceptor.Funcs{
				Update: func(ctx context.Context, c client.WithWatch, object client.Object, opts ...client.UpdateOption) error {
					if deleteFirst {
						select {
						case <-written:
						case <-ctx.Done():
							return ctx.Err()
						}
					}

					err := c.Update(ctx, object, opts...)

					if !deleteFirst {
						committed.Do(func() { close(written) })
					}

					return err
				},
				Delete: func(ctx context.Context, c client.WithWatch, object client.Object, opts ...client.DeleteOption) error {
					if !deleteFirst {
						select {
						case <-written:
						case <-ctx.Done():
							return ctx.Err()
						}
					}

					err := c.Delete(ctx, object, opts...)

					if deleteFirst {
						committed.Do(func() { close(written) })
					}

					return err
				},
			})

			newCase := &Scenario{Client: first.Client, Namespace: "settings", Name: "new-case"}
			results := make(chan error, 2)
			go func() { results <- first.Delete(ctx) }()
			go func() { results <- newCase.Create(ctx, InstallationStalled, "http://ignition/new-case") }()

			for range 2 {
				var err error

				Eventually(results).WithContext(ctx).Should(Receive(&err))
				Expect(err).ToNot(HaveOccurred())
			}

			store, cases, err := readSettings(ctx, c, "settings")
			Expect(err).NotTo(HaveOccurred())
			Expect(cases).To(Equal(map[string]caseSettings{newCase.Name: {
				ID: newCase.caseID, Profile: InstallationStalled, DiscoveryURL: "http://ignition/new-case",
			}}))

			if deleteFirst {
				Expect(store.UID).To(Equal(types.UID("created-store")))
			} else {
				Expect(store.UID).To(Equal(types.UID("original-store")))
			}
		},
			Entry("deletion commits first", true, SpecTimeout(5*time.Second)),
			Entry("creation commits first", false, SpecTimeout(5*time.Second)),
		)

		DescribeTable("rejects invalid case inputs without modifying the store",
			func(ctx SpecContext, profile Profile, endpoint string) {
				store, _, err := readSettings(ctx, c, "settings")
				Expect(err).NotTo(HaveOccurred())

				before := store.DeepCopy()
				invalid := &Scenario{Client: c, Namespace: "settings", Name: "invalid"}
				Expect(invalid.Create(ctx, profile, endpoint)).NotTo(Succeed())
				Expect(c.Get(ctx, client.ObjectKeyFromObject(store), store)).To(Succeed())
				Expect(store).To(Equal(before))
			},
			Entry("unknown profile", Profile("invalid"), "http://ignition"),
			Entry("relative URL", Normal, "/ignition"),
			Entry("unsupported URL scheme", Normal, "file:///ignition"),
		)
	})

	It("creates distinct cases concurrently when the shared store is absent", func(ctx SpecContext) {
		c := pairedSettingsReads(settingsClient())
		first := &Scenario{Client: c, Namespace: "settings", Name: "case-a"}
		second := &Scenario{Client: c, Namespace: "settings", Name: "case-b"}
		results := make(chan error, 2)

		for _, scenario := range []*Scenario{first, second} {
			go func() { results <- scenario.Create(ctx, Normal, "http://ignition") }()
		}

		for range 2 {
			var err error

			Eventually(results).WithContext(ctx).Should(Receive(&err))
			Expect(err).ToNot(HaveOccurred())
		}

		store, cases, err := readSettings(ctx, c, "settings")
		Expect(err).NotTo(HaveOccurred())
		Expect(store.UID).To(Equal(types.UID("created-store")))
		Expect(first.uid).To(Equal(store.UID))
		Expect(second.uid).To(Equal(store.UID))
		Expect(cases).To(HaveLen(2))
		Expect(cases[first.Name].ID).To(Equal(first.caseID))
		Expect(cases[second.Name].ID).To(Equal(second.caseID))
		Expect(first.caseID).NotTo(BeEmpty())
		Expect(first.caseID).NotTo(Equal(second.caseID))
	}, SpecTimeout(5*time.Second))

	Context("without a valid unique binding", func() {
		var (
			infra     *assistedv1.InfraEnv
			c         client.Client
			responses Responses
		)

		BeforeEach(func() {
			infra = &assistedv1.InfraEnv{ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "discovery", UID: "infra"}}

			c = fake.NewClientBuilder().Build()
			responses = Responses{Reader: c, Namespace: "settings"}
		})

		It("waits without supplying discovery or installation progress when settings are missing", func(ctx SpecContext) {
			installation, err := responses.Installation(ctx, infra)
			Expect(err).NotTo(HaveOccurred())
			Expect(installation).To(BeZero())

			endpoint, err := responses.DiscoveryURL(ctx, infra)
			Expect(err).NotTo(HaveOccurred())
			Expect(endpoint).To(BeEmpty())
		})

		It("rejects an empty native root UID", func(ctx SpecContext) {
			infra.SetUID("")
			_, err := responses.Installation(ctx, infra)
			Expect(err).To(HaveOccurred())
		})

		DescribeTable("rejects invalid settings without assuming normal progress", func(ctx SpecContext, data string) {
			settings := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: settingsName, Namespace: "settings"},
				Data:       map[string]string{"case": data},
			}
			Expect(c.Create(ctx, settings)).To(Succeed())
			_, err := responses.Installation(ctx, infra)
			Expect(err).To(HaveOccurred())
		},
			Entry("malformed JSON", "{"),
			Entry("missing identity", `{"profile":"normal","roots":{"InfraEnv":"infra"}}`),
			Entry("invalid profile", `{"id":"case","profile":"invalid","roots":{"InfraEnv":"infra"}}`),
		)

		It("rejects ambiguous bindings even when either binding is valid on its own", func(ctx SpecContext) {
			settings := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: settingsName, Namespace: "settings"}}
			entry := caseSettings{ID: "case", Profile: Normal, Roots: map[string]types.UID{infraEnvRoot: "infra"}}
			Expect(setCase(settings, "case", entry)).To(Succeed())
			Expect(c.Create(ctx, settings)).To(Succeed())
			installation, err := responses.Installation(ctx, infra)
			Expect(err).NotTo(HaveOccurred())
			Expect(installation).To(Equal(assisted.InstallationComplete))

			entry.ID = "duplicate"
			Expect(setCase(settings, "duplicate", entry)).To(Succeed())
			Expect(c.Update(ctx, settings)).To(Succeed())
			_, err = responses.Installation(ctx, infra)
			Expect(err).To(MatchError(ContainSubstring("multiple settings match native root")))
		})
	})
})

// settingsClient supplies a concrete store UID while retaining the versioned fake's conflict checks.
func settingsClient(objects ...client.Object) client.WithWatch {
	return fake.NewClientBuilder().WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, object client.Object, opts ...client.CreateOption) error {
			if object.GetName() == settingsName && object.GetUID() == "" {
				object.SetUID("created-store")
			}

			return c.Create(ctx, object, opts...)
		},
	}).Build()
}

// pairedSettingsReads snapshots the first two store reads before either caller can write.
func pairedSettingsReads(c client.WithWatch) client.WithWatch {
	var reads atomic.Int32

	ready := make(chan struct{})

	return interceptor.NewClient(c, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, object client.Object,
			opts ...client.GetOption,
		) error {
			err := c.Get(ctx, key, object, opts...)
			if key.Name != settingsName {
				return err
			}

			n := reads.Add(1)
			if n > 2 {
				return err
			}

			if n == 2 {
				close(ready)
			}

			select {
			case <-ready:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
}
