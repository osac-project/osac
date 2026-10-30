package pullsecret_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	. "github.com/osac-project/osac/osac-operator/pkg/pullsecret"
)

const fixtureFirst = `{"auths":{"registry.invalid":{"username":"OSAC7034_SYNTHETIC_FIRST"}}}`
const fixtureRotated = `{"auths":{"registry.invalid":{"username":"OSAC7034_SYNTHETIC_ROTATED"}}}`

func TestPullSecret(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Pull Secret Projection Suite")
}

var _ = Describe("Same-hub pull-secret projections", func() {
	var order *v1alpha1.ClusterOrder
	var kube client.WithWatch
	var namespace *corev1.Namespace
	ctx := context.Background()

	BeforeEach(func() {
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		order = &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "fixture", Namespace: "hub", UID: "order-uid",
			Annotations: map[string]string{TenantAnnotation: "tenant-a", LastAppliedAnnotation: fixtureFirst}},
			Spec: v1alpha1.ClusterOrderSpec{TemplateParameters: `{}`}}
		kube = fake.NewClientBuilder().WithScheme(scheme).WithObjects(order).Build()
		namespace = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "hub-fixture"}}
		Expect(BindNamespace(order, namespace)).To(Succeed())
		namespace.UID = "namespace-uid"
		Expect(kube.Create(ctx, namespace)).To(Succeed())
	})

	project := func() {
		fixturePayload := fixtureFirst
		Expect(Ensure(ctx, kube, order, &fixturePayload)).To(Succeed())
		Expect(kube.Update(ctx, order)).To(Succeed())
	}

	assertClean := func() {
		persisted := &v1alpha1.ClusterOrder{}
		Expect(kube.Get(ctx, client.ObjectKeyFromObject(order), persisted)).To(Succeed())
		encoded, err := json.Marshal(persisted)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).NotTo(ContainSubstring("OSAC7034_SYNTHETIC"))
		for _, fixturePayload := range []string{fixtureFirst, fixtureRotated} {
			Expect(string(encoded)).NotTo(ContainSubstring(base64.StdEncoding.EncodeToString([]byte(fixturePayload))))
		}
	}

	It("persists references only and updates both projections without changing stable revisions", func() {
		project()
		assertClean()
		firstRevision := order.Spec.PullSecretRevision
		Expect(EnsureHosted(ctx, kube, order, namespace)).To(Succeed())
		target := &corev1.Secret{}
		key := client.ObjectKey{Namespace: namespace.Name, Name: HostedName(order)}
		Expect(kube.Get(ctx, key, target)).To(Succeed())
		Expect(metav1.IsControlledBy(target, namespace)).To(BeTrue())
		Expect(metav1.IsControlledBy(target, order)).To(BeFalse())
		Expect(string(target.Data[corev1.DockerConfigJsonKey])).To(Equal(fixtureFirst))
		fixturePayload := fixtureFirst
		Expect(Ensure(ctx, kube, order, &fixturePayload)).To(Succeed())
		Expect(order.Spec.PullSecretRevision).To(Equal(firstRevision))
		fixturePayload = fixtureRotated
		Expect(Ensure(ctx, kube, order, &fixturePayload)).To(Succeed())
		Expect(order.Spec.PullSecretRevision).NotTo(Equal(firstRevision))
		Expect(kube.Update(ctx, order)).To(Succeed())
		Expect(EnsureHosted(ctx, kube, order, namespace)).To(Succeed())
		Expect(kube.Get(ctx, key, target)).To(Succeed())
		Expect(string(target.Data[corev1.DockerConfigJsonKey])).To(Equal(fixtureRotated))
		version := target.ResourceVersion
		Expect(EnsureHosted(ctx, kube, order, namespace)).To(Succeed())
		Expect(kube.Get(ctx, key, target)).To(Succeed())
		Expect(target.ResourceVersion).To(Equal(version))
		assertClean()
	})

	DescribeTable("rejects fresh inline inputs without converting them", func(parameters string, inline string) {
		order.Spec.TemplateParameters, order.Spec.PullSecret = parameters, inline
		fixturePayload := fixtureFirst
		Expect(Ensure(ctx, kube, order, &fixturePayload)).To(MatchError(ErrInvalid))
		secrets := &corev1.SecretList{}
		Expect(kube.List(ctx, secrets)).To(Succeed())
		Expect(secrets.Items).To(BeEmpty())
	}, Entry("inline field", `{}`, fixtureFirst), Entry("template parameter", `{"pull_secret":"fixture"}`, ""), Entry("malformed parameters", `[]`, ""))

	DescribeTable("fails closed on invalid source references", func(mode string) {
		project()
		source, err := Read(ctx, kube, order)
		Expect(err).NotTo(HaveOccurred())
		switch mode {
		case "missing":
			Expect(kube.Delete(ctx, source)).To(Succeed())
		case "malformed":
			source.Data[corev1.DockerConfigJsonKey] = []byte("fixture-invalid-json")
		case "type":
			source.Type = corev1.SecretTypeOpaque
		case "tenant":
			source.Annotations[TenantAnnotation] = "tenant-b"
		case "project":
			source.Annotations[ProjectAnnotation] = "project-b"
		case "owner":
			source.OwnerReferences[0].UID = "other-order"
		case "stale":
			order.Spec.PullSecretRevision = "stale-revision"
		case "forbidden":
			kube = interceptor.NewClient(kube, interceptor.Funcs{Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
				return errors.New("fixture forbidden")
			}})
		}
		if mode != "missing" && mode != "forbidden" {
			Expect(kube.Update(ctx, source)).To(Succeed())
		}
		Expect(EnsureHosted(ctx, kube, order, namespace)).To(MatchError(ErrInvalid))
	}, Entry("missing", "missing"), Entry("malformed", "malformed"), Entry("wrong type", "type"), Entry("foreign tenant", "tenant"), Entry("foreign project", "project"), Entry("foreign owner", "owner"), Entry("unpublished revision", "stale"), Entry("forbidden", "forbidden"))

	DescribeTable("rejects namespace and target collisions", func(mode string) {
		project()
		Expect(EnsureHosted(ctx, kube, order, namespace)).To(Succeed())
		switch mode {
		case "namespace-owner":
			namespace.Annotations[OrderUIDAnnotation] = "other-order"
		case "namespace-tenant":
			namespace.Annotations[TenantAnnotation] = "tenant-b"
		case "namespace-name":
			namespace.Name = "unrelated"
		case "namespace-deleting":
			now := metav1.Now()
			namespace.DeletionTimestamp = &now
		default:
			target := &corev1.Secret{}
			Expect(kube.Get(ctx, client.ObjectKey{Namespace: namespace.Name, Name: HostedName(order)}, target)).To(Succeed())
			if mode == "namespace-reused" {
				namespace.UID = types.UID("replacement-namespace")
			} else {
				target.OwnerReferences = nil
				Expect(kube.Update(ctx, target)).To(Succeed())
			}
		}
		Expect(EnsureHosted(ctx, kube, order, namespace)).To(MatchError(ErrInvalid))
	}, Entry("foreign namespace owner", "namespace-owner"), Entry("foreign namespace tenant", "namespace-tenant"), Entry("foreign namespace name", "namespace-name"), Entry("deleting namespace", "namespace-deleting"), Entry("reused namespace", "namespace-reused"), Entry("foreign target", "target-owner"))

	It("does not adopt an existing unmarked namespace or source Secret", func() {
		namespace.Annotations = nil
		Expect(BindNamespace(order, namespace)).To(MatchError(ErrInvalid))
		foreign := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: order.Name + "-pull-secret", Namespace: order.Namespace}, Type: corev1.SecretTypeDockerConfigJson}
		Expect(kube.Create(ctx, foreign)).To(Succeed())
		fixturePayload := fixtureFirst
		Expect(Ensure(ctx, kube, order, &fixturePayload)).To(MatchError(ErrInvalid))
	})

	It("retries partial projection updates without changing the opaque revision", func() {
		project()
		fixturePayload := fixtureRotated
		persisted := order.DeepCopy()
		Expect(Ensure(ctx, kube, order, &fixturePayload)).To(Succeed())
		revision := order.Spec.PullSecretRevision
		Expect(EnsureHosted(ctx, kube, persisted, namespace)).To(MatchError(ErrInvalid))
		Expect(Ensure(ctx, kube, persisted, &fixturePayload)).To(Succeed())
		Expect(persisted.Spec.PullSecretRevision).To(Equal(revision))
		Expect(EnsureHosted(ctx, kube, persisted, namespace)).To(Succeed())
	})

	It("fails safely when a target patch is forbidden and succeeds on retry", func() {
		project()
		Expect(EnsureHosted(ctx, kube, order, namespace)).To(Succeed())
		fixturePayload := fixtureRotated
		Expect(Ensure(ctx, kube, order, &fixturePayload)).To(Succeed())
		failing := interceptor.NewClient(kube, interceptor.Funcs{Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
			return errors.New("fixture forbidden patch")
		}})
		Expect(EnsureHosted(ctx, failing, order, namespace)).To(MatchError(ErrInvalid))
		Expect(EnsureHosted(ctx, kube, order, namespace)).To(Succeed())
	})
})
