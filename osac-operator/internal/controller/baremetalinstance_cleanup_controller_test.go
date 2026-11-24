package controller

import (
	"context"
	"errors"
	"testing"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	cleanupBMNamespace  = "bm"
	cleanupNetNamespace = "net"
	cleanupBMID         = "bm-uuid"
	cleanupFinalizer    = "osac.openshift.io/baremetalinstance-cleanup"
)

func cleanupTestClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := bmfov1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(&v1alpha1.ExternalIPAttachment{}, autoExternalIPAttachmentOwnerIndexField, func(obj client.Object) []string {
			attachment, ok := obj.(*v1alpha1.ExternalIPAttachment)
			if !ok {
				return nil
			}
			return autoExternalIPAttachmentOwnerIndexValues(attachment)
		}).
		WithObjects(objects...).
		Build()
}

func cleanupTestBMI(id string) *bmfov1alpha1.BareMetalInstance {
	labels := map[string]string{}
	if id != "" {
		labels[osacBareMetalInstanceIDLabel] = id
	}
	return &bmfov1alpha1.BareMetalInstance{ObjectMeta: metav1.ObjectMeta{Name: "instance", Namespace: cleanupBMNamespace, Labels: labels}}
}

func cleanupTestEIP(name, marker, owner string) *v1alpha1.ExternalIP {
	ownerLabel := autoCreatedForLabel
	if marker == autoProvisionedLabel {
		ownerLabel = autoProvisionedForLabel
	}
	return &v1alpha1.ExternalIP{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cleanupNetNamespace,
		Labels: map[string]string{marker: labelValueTrue, ownerLabel: owner}}, Spec: v1alpha1.ExternalIPSpec{Pool: "pool"}}
}

func cleanupTestEIA(name, marker, owner string) *v1alpha1.ExternalIPAttachment {
	return &v1alpha1.ExternalIPAttachment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cleanupNetNamespace,
		Labels: map[string]string{marker: labelValueTrue}}, Spec: v1alpha1.ExternalIPAttachmentSpec{
		ExternalIP: "owned-ip", BaremetalInstance: &owner,
	}}
}

func reconcileCleanup(t *testing.T, r *BareMetalInstanceCleanupReconciler) ctrl.Result {
	t.Helper()
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "instance", Namespace: cleanupBMNamespace}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func getCleanupBMI(t *testing.T, c client.Client) *bmfov1alpha1.BareMetalInstance {
	t.Helper()
	bmi := &bmfov1alpha1.BareMetalInstance{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "instance", Namespace: cleanupBMNamespace}, bmi); err != nil {
		t.Fatal(err)
	}
	return bmi
}

func TestBareMetalInstanceCleanupArmsForManagedBMIWithNetworkingEnabled(t *testing.T) {
	for _, tc := range []struct {
		name    string
		marker  string
		owner   string
		bmID    string
		wantArm bool
	}{
		{"current", autoCreatedLabel, cleanupBMID, cleanupBMID, true},
		{"legacy", autoProvisionedLabel, cleanupBMID, cleanupBMID, true},
		{"foreign", autoCreatedLabel, "other", cleanupBMID, true},
		{"no owner", autoCreatedLabel, cleanupBMID, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cleanupTestClient(t, cleanupTestBMI(tc.bmID), cleanupTestEIP("owned-ip", tc.marker, tc.owner))
			r := NewBareMetalInstanceCleanupReconciler(c, cleanupBMNamespace, cleanupNetNamespace)
			reconcileCleanup(t, r)
			if got := controllerutil.ContainsFinalizer(getCleanupBMI(t, c), cleanupFinalizer); got != tc.wantArm {
				t.Fatalf("finalizer present = %t, want %t", got, tc.wantArm)
			}
		})
	}
	t.Run("no resource", func(t *testing.T) {
		c := cleanupTestClient(t, cleanupTestBMI(cleanupBMID))
		reconcileCleanup(t, NewBareMetalInstanceCleanupReconciler(c, cleanupBMNamespace, cleanupNetNamespace))
		if !controllerutil.ContainsFinalizer(getCleanupBMI(t, c), cleanupFinalizer) {
			t.Fatal("finalizer not armed for a managed BMI before an ExternalIP exists")
		}
	})
	t.Run("unmanaged", func(t *testing.T) {
		bmi := cleanupTestBMI(cleanupBMID)
		bmi.Annotations = map[string]string{osacManagementStateAnnotation: ManagementStateUnmanaged}
		c := cleanupTestClient(t, bmi)
		reconcileCleanup(t, NewBareMetalInstanceCleanupReconciler(c, cleanupBMNamespace, cleanupNetNamespace))
		assertCleanupFinalizer(t, c, false)
	})
	t.Run("networking disabled", func(t *testing.T) {
		c := cleanupTestClient(t, cleanupTestBMI(cleanupBMID))
		r := NewBareMetalInstanceCleanupReconciler(c, cleanupBMNamespace, "")
		reconcileCleanup(t, r)
		assertCleanupFinalizer(t, c, false)
	})
}

func TestBareMetalInstanceCleanupFinalizerProtectsChildrenCreatedAfterFirstReconcile(t *testing.T) {
	for _, marker := range []string{autoCreatedLabel, autoProvisionedLabel} {
		t.Run(marker, func(t *testing.T) {
			c := cleanupTestClient(t, cleanupTestBMI(cleanupBMID))
			r := NewBareMetalInstanceCleanupReconciler(c, cleanupBMNamespace, cleanupNetNamespace)
			reconcileCleanup(t, r)
			assertCleanupFinalizer(t, c, true)

			ctx := context.Background()
			eia := cleanupTestEIA("late-attachment", marker, cleanupBMID)
			eia.Finalizers = []string{"provider.example/detach"}
			eip := cleanupTestEIP("late-ip", marker, cleanupBMID)
			eip.Finalizers = []string{"provider.example/release"}
			if err := c.Create(ctx, eia); err != nil {
				t.Fatal(err)
			}
			if err := c.Create(ctx, eip); err != nil {
				t.Fatal(err)
			}
			bmi := getCleanupBMI(t, c)
			if err := c.Delete(ctx, bmi); err != nil {
				t.Fatal(err)
			}

			if result := reconcileCleanup(t, r); result.RequeueAfter <= 0 {
				t.Fatal("expected requeue while the late attachment's provider finalizer blocks deletion")
			}
			currentEIA := &v1alpha1.ExternalIPAttachment{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(eia), currentEIA); err != nil {
				t.Fatal(err)
			}
			if currentEIA.DeletionTimestamp.IsZero() {
				t.Fatal("expected the late attachment to be deleting")
			}
			currentEIP := &v1alpha1.ExternalIP{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(eip), currentEIP); err != nil {
				t.Fatal(err)
			}
			if !currentEIP.DeletionTimestamp.IsZero() {
				t.Fatal("ExternalIP deletion must wait for the late attachment")
			}
			assertCleanupFinalizer(t, c, true)
			assertCleanupObjectExists(t, c, currentEIP)
		})
	}
}

func TestBareMetalInstanceCleanupRetainsFinalizerUntilDeletionAfterExternalIPDisappears(t *testing.T) {
	bmi := cleanupTestBMI(cleanupBMID)
	bmi.Finalizers = []string{cleanupFinalizer}
	ip := cleanupTestEIP("owned-ip", autoCreatedLabel, cleanupBMID)
	c := cleanupTestClient(t, bmi, ip)
	r := NewBareMetalInstanceCleanupReconciler(c, cleanupBMNamespace, cleanupNetNamespace)
	if err := c.Delete(context.Background(), ip); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(context.Background(), getCleanupBMI(t, c)); err != nil {
		t.Fatal(err)
	}
	reconcileCleanup(t, r)
	if err := c.Get(context.Background(), types.NamespacedName{Name: "instance", Namespace: cleanupBMNamespace}, &bmfov1alpha1.BareMetalInstance{}); !apierrors.IsNotFound(err) {
		t.Fatalf("BareMetalInstance after empty-network cleanup = %v, want not found", err)
	}
}

func TestBareMetalInstanceCleanupRetainsFinalizerOnLiveBMI(t *testing.T) {
	bmi := cleanupTestBMI(cleanupBMID)
	bmi.Finalizers = []string{cleanupFinalizer}
	eia := cleanupTestEIA("owned-attachment", autoCreatedLabel, cleanupBMID)
	c := cleanupTestClient(t, bmi, eia)
	if result := reconcileCleanup(t, NewBareMetalInstanceCleanupReconciler(c, cleanupBMNamespace, cleanupNetNamespace)); result.RequeueAfter != 0 {
		t.Fatalf("live BMI reconcile result = %+v, want no polling until deletion", result)
	}
	assertCleanupFinalizer(t, c, true)
	assertCleanupObjectExists(t, c, eia)
}

func TestBareMetalInstanceCleanupSkipsUnmanagedBMIOnDeletion(t *testing.T) {
	bmi := cleanupTestBMI(cleanupBMID)
	bmi.Annotations = map[string]string{osacManagementStateAnnotation: ManagementStateUnmanaged}
	bmi.Finalizers = []string{cleanupFinalizer}
	eia := cleanupTestEIA("owned-attachment", autoCreatedLabel, cleanupBMID)
	eia.Finalizers = []string{"provider.example/detach"}
	eip := cleanupTestEIP("owned-ip", autoCreatedLabel, cleanupBMID)
	eip.Finalizers = []string{"provider.example/release"}
	c := cleanupTestClient(t, bmi, eia, eip)
	ctx := context.Background()
	if err := c.Delete(ctx, bmi); err != nil {
		t.Fatal(err)
	}

	reconcileCleanup(t, NewBareMetalInstanceCleanupReconciler(c, cleanupBMNamespace, cleanupNetNamespace))

	for _, object := range []client.Object{eia, eip} {
		current := object.DeepCopyObject().(client.Object)
		if err := c.Get(ctx, client.ObjectKeyFromObject(object), current); err != nil {
			t.Fatal(err)
		}
		if !current.GetDeletionTimestamp().IsZero() {
			t.Fatalf("unmanaged BMI cleanup marked %T %q for deletion", current, current.GetName())
		}
	}
	currentBMI := &bmfov1alpha1.BareMetalInstance{}
	err := c.Get(ctx, types.NamespacedName{Name: "instance", Namespace: cleanupBMNamespace}, currentBMI)
	if err == nil && controllerutil.ContainsFinalizer(currentBMI, cleanupFinalizer) {
		t.Fatal("cleanup finalizer was retained for an unmanaged BMI")
	}
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("get unmanaged BMI after dropping cleanup finalizer: %v", err)
	}
}

func TestBareMetalInstanceCleanupWaitsForProviderFinalizersAndPreservesOtherResources(t *testing.T) {
	for _, marker := range []string{autoCreatedLabel, autoProvisionedLabel} {
		t.Run(marker, func(t *testing.T) {
			bmi := cleanupTestBMI(cleanupBMID)
			bmi.Finalizers = []string{cleanupFinalizer, "provider.example/finalizer"}
			eia := cleanupTestEIA("owned-attachment", marker, cleanupBMID)
			eia.Finalizers = []string{"provider.example/detach"}
			eip := cleanupTestEIP("owned-ip", marker, cleanupBMID)
			eip.Finalizers = []string{"provider.example/release"}
			foreign := cleanupTestEIP("foreign", marker, "other")
			manual := cleanupTestEIA("manual", marker, cleanupBMID)
			manual.Labels = nil
			c := cleanupTestClient(t, bmi, eia, eip, foreign, manual)
			ctx := context.Background()
			if err := c.Delete(ctx, bmi); err != nil {
				t.Fatal(err)
			}
			r := NewBareMetalInstanceCleanupReconciler(c, cleanupBMNamespace, cleanupNetNamespace)
			if result := reconcileCleanup(t, r); result.RequeueAfter <= 0 {
				t.Fatal("expected requeue while attachment is deleting")
			}
			assertCleanupObjectExists(t, c, eip)
			assertCleanupFinalizer(t, c, true)

			currentEIA := &v1alpha1.ExternalIPAttachment{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(eia), currentEIA); err != nil {
				t.Fatal(err)
			}
			currentEIA.Finalizers = nil
			if err := c.Update(ctx, currentEIA); err != nil {
				t.Fatal(err)
			}
			if result := reconcileCleanup(t, r); result.RequeueAfter <= 0 {
				t.Fatal("expected requeue while ExternalIP is deleting")
			}
			assertCleanupFinalizer(t, c, true)
			currentEIP := &v1alpha1.ExternalIP{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(eip), currentEIP); err != nil {
				t.Fatal(err)
			}
			currentEIP.Finalizers = nil
			if err := c.Update(ctx, currentEIP); err != nil {
				t.Fatal(err)
			}
			// A new reconciler represents restart after the provider has finished.
			reconcileCleanup(t, NewBareMetalInstanceCleanupReconciler(c, cleanupBMNamespace, cleanupNetNamespace))
			assertCleanupFinalizer(t, c, false)
			assertCleanupObjectExists(t, c, foreign)
			assertCleanupObjectExists(t, c, manual)
		})
	}
}

func assertCleanupObjectExists(t *testing.T, c client.Client, obj client.Object) {
	t.Helper()
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(obj), obj.DeepCopyObject().(client.Object)); err != nil {
		t.Fatal(err)
	}
}

func assertCleanupFinalizer(t *testing.T, c client.Client, want bool) {
	t.Helper()
	if got := controllerutil.ContainsFinalizer(getCleanupBMI(t, c), cleanupFinalizer); got != want {
		t.Fatalf("cleanup finalizer present = %t, want %t", got, want)
	}
}

type failCleanupListClient struct {
	client.Client
	err error
}

func (c *failCleanupListClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return c.err
}

func TestBareMetalInstanceCleanupPropagatesListErrorForRetry(t *testing.T) {
	bmi := cleanupTestBMI(cleanupBMID)
	bmi.Finalizers = []string{cleanupFinalizer}
	c := cleanupTestClient(t, bmi, cleanupTestEIP("owned-ip", autoCreatedLabel, cleanupBMID))
	if err := c.Delete(context.Background(), bmi); err != nil {
		t.Fatal(err)
	}
	underlying := errors.New("temporary API failure")
	r := NewBareMetalInstanceCleanupReconciler(&failCleanupListClient{Client: c, err: underlying}, cleanupBMNamespace, cleanupNetNamespace)
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "instance", Namespace: cleanupBMNamespace}})
	if !errors.Is(err, underlying) {
		t.Fatalf("error = %v, want wrapped API error", err)
	}
	assertCleanupFinalizer(t, c, true)
	restarted := NewBareMetalInstanceCleanupReconciler(c, cleanupBMNamespace, cleanupNetNamespace)
	if result := reconcileCleanup(t, restarted); result.RequeueAfter <= 0 {
		t.Fatal("expected requeue after deleting ExternalIP")
	}
	assertCleanupFinalizer(t, c, true)
	reconcileCleanup(t, restarted)
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(bmi), &bmfov1alpha1.BareMetalInstance{}); !apierrors.IsNotFound(err) {
		t.Fatalf("BareMetalInstance after retry = %v, want not found", err)
	}
}
