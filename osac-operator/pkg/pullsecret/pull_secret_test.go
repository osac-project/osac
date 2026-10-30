package pullsecret

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const syntheticFirst = `{"auths":{"registry.invalid":{"username":"OSAC7034_SYNTHETIC_FIRST"}}}`
const syntheticRotated = `{"auths":{"registry.invalid":{"username":"OSAC7034_SYNTHETIC_ROTATED"}}}`

func fixture(test *testing.T) (*v1alpha1.ClusterOrder, client.WithWatch) {
	test.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		test.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		test.Fatal(err)
	}
	order := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
		Name: "fixture", Namespace: "hub", UID: "fixture-uid",
		Annotations: map[string]string{TenantAnnotation: "tenant-a", LastAppliedAnnotation: syntheticFirst},
	}, Spec: v1alpha1.ClusterOrderSpec{TemplateParameters: `{}`}}
	return order, fake.NewClientBuilder().WithScheme(scheme).WithObjects(order).Build()
}

func assertClean(test *testing.T, order *v1alpha1.ClusterOrder) {
	test.Helper()
	encoded, err := json.Marshal(order)
	if err != nil || strings.Contains(string(encoded), "OSAC7034_SYNTHETIC") {
		test.Fatal("persisted order contains synthetic credential material or cannot be serialized")
	}
	for _, payload := range []string{syntheticFirst, syntheticRotated} {
		if strings.Contains(string(encoded), base64.StdEncoding.EncodeToString([]byte(payload))) {
			test.Fatal("persisted order contains encoded synthetic credential material")
		}
	}
}

func TestMigrationRetriesAfterOrderWriteFailure(test *testing.T) {
	ctx := context.Background()
	order, kube := fixture(test)
	before := order.DeepCopy()
	order.Spec.PullSecret = syntheticFirst
	if err := kube.Patch(ctx, order, client.MergeFrom(before)); err != nil {
		test.Fatal(err)
	}
	failing := interceptor.NewClient(kube, interceptor.Funcs{
		Patch: func(ctx context.Context, underlying client.WithWatch, object client.Object, patch client.Patch, options ...client.PatchOption) error {
			if _, isOrder := object.(*v1alpha1.ClusterOrder); isOrder {
				return errors.New("OSAC7034_SYNTHETIC_WRITE_FAILURE")
			}
			return underlying.Patch(ctx, object, patch, options...)
		},
	})
	if changed, err := Migrate(ctx, failing, order); changed || !errors.Is(err, ErrInvalid) {
		test.Fatal("failed order write did not fail closed safely")
	}
	secret, err := Read(ctx, kube, order)
	if err != nil {
		test.Fatal(err)
	}
	revision := secret.Annotations[revisionAnnotation]
	if err := kube.Get(ctx, client.ObjectKeyFromObject(order), order); err != nil {
		test.Fatal(err)
	}
	if order.Spec.PullSecret != syntheticFirst || order.Spec.PullSecretRef != nil {
		test.Fatal("failed order write partially changed persisted order")
	}
	if changed, err := Migrate(ctx, kube, order); !changed || err != nil {
		test.Fatal("retry did not finish migration")
	}
	if order.Spec.PullSecretRevision != revision {
		test.Fatal("retry changed an unchanged projection revision")
	}
	if err := kube.Get(ctx, client.ObjectKeyFromObject(order), order); err != nil {
		test.Fatal(err)
	}
	assertClean(test, order)
}

func TestMigrationAndRotation(test *testing.T) {
	for _, mode := range []string{"inline", "parameters"} {
		test.Run(mode, func(test *testing.T) {
			ctx := context.Background()
			order, kube := fixture(test)
			before := order.DeepCopy()
			if mode == "inline" {
				order.Spec.PullSecret = syntheticFirst
			} else {
				encoded, err := json.Marshal(map[string]string{"pull_secret": syntheticFirst, "unrelated": "keep"})
				if err != nil {
					test.Fatal(err)
				}
				order.Spec.TemplateParameters = string(encoded)
			}
			if err := kube.Patch(ctx, order, client.MergeFrom(before)); err != nil {
				test.Fatal(err)
			}
			if changed, err := Migrate(ctx, kube, order); err != nil || !changed {
				test.Fatal("migration did not succeed")
			}
			persisted := &v1alpha1.ClusterOrder{}
			if err := kube.Get(ctx, client.ObjectKeyFromObject(order), persisted); err != nil {
				test.Fatal(err)
			}
			assertClean(test, persisted)
			if mode == "parameters" && !strings.Contains(order.Spec.TemplateParameters, "unrelated") {
				test.Fatal("migration lost unrelated parameters")
			}
			firstRevision := order.Spec.PullSecretRevision
			secret, err := Read(ctx, kube, order)
			if err != nil || firstRevision == "" || string(secret.Data[corev1.DockerConfigJsonKey]) != syntheticFirst {
				test.Fatal("invalid projection")
			}
			if !metav1.IsControlledBy(secret, order) {
				test.Fatal("projection is not garbage-collectable with its order")
			}
			first := syntheticFirst
			if err := Ensure(ctx, kube, order, &first); err != nil || order.Spec.PullSecretRevision != firstRevision {
				test.Fatal("unchanged payload changed scheduling revision")
			}
			rotated := syntheticRotated
			if err := Ensure(ctx, kube, order, &rotated); err != nil || order.Spec.PullSecretRevision == firstRevision {
				test.Fatal("rotation did not change scheduling revision")
			}
			order.Spec.PullSecret = syntheticFirst
			if err := Ensure(ctx, kube, order, nil); err != nil {
				test.Fatal(err)
			}
			secret, err = Read(ctx, kube, order)
			if err != nil || string(secret.Data[corev1.DockerConfigJsonKey]) != syntheticRotated {
				test.Fatal("stale inline value replaced authoritative reference")
			}
			assertClean(test, order)
			if err := kube.Patch(ctx, order, client.MergeFrom(persisted)); err != nil {
				test.Fatal(err)
			}
			if changed, err := Migrate(ctx, kube, order); err != nil || changed {
				test.Fatal("migration was not idempotent")
			}
		})
	}
}

func TestReferenceFailuresAndCollisions(test *testing.T) {
	for _, failure := range []string{"missing", "foreign-owner", "foreign-tenant", "wrong-type", "missing-key", "malformed", "forbidden"} {
		test.Run(failure, func(test *testing.T) {
			ctx := context.Background()
			order, kube := fixture(test)
			payload := syntheticFirst
			if err := Ensure(ctx, kube, order, &payload); err != nil {
				test.Fatal(err)
			}
			secret, err := Read(ctx, kube, order)
			if err != nil {
				test.Fatal(err)
			}
			switch failure {
			case "missing":
				if err := kube.Delete(ctx, secret); err != nil {
					test.Fatal(err)
				}
			case "foreign-owner":
				secret.OwnerReferences[0].UID = "other-order"
			case "foreign-tenant":
				secret.Annotations[TenantAnnotation] = "tenant-b"
			case "wrong-type":
				secret.Type = corev1.SecretTypeOpaque
			case "missing-key":
				secret.Data = nil
			case "malformed":
				secret.Data[corev1.DockerConfigJsonKey] = []byte("OSAC7034_SYNTHETIC_INVALID")
			case "forbidden":
				kube = interceptor.NewClient(kube, interceptor.Funcs{Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
					return errors.New("OSAC7034_SYNTHETIC_FORBIDDEN")
				}})
			}
			if failure != "missing" && failure != "forbidden" {
				if err := kube.Update(ctx, secret); err != nil {
					test.Fatal(err)
				}
			}
			order.Spec.PullSecret = syntheticFirst
			if err := Ensure(ctx, kube, order, nil); !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "SYNTHETIC") {
				test.Fatal("explicit reference failure did not fail closed safely")
			}
			if failure == "foreign-owner" || failure == "foreign-tenant" || failure == "wrong-type" {
				if err := Ensure(ctx, kube, order, &payload); !errors.Is(err, ErrInvalid) {
					test.Fatal("projection overwrote a foreign secret")
				}
			}
		})
	}
}
