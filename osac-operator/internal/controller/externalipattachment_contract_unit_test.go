package controller

import (
	stdcontext "context"
	"encoding/json"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

func TestValidateExternalIPAttachmentProvisionResult(t *testing.T) {
	const (
		networkingNamespace = "attachment-networking"
		bmaasNamespace      = "attachment-bmaas"
		attachmentID        = "11111111-1111-4111-8111-111111111111"
		externalIPID        = "22222222-2222-4222-8222-222222222222"
		bmiID               = "33333333-3333-4333-8333-333333333333"
		subnetID            = "44444444-4444-4444-8444-444444444444"
		virtualNetworkID    = "55555555-5555-4555-8555-555555555555"
	)

	makeFixture := func(currentTargetIP string) (*ExternalIPAttachmentReconciler, *v1alpha1.ExternalIPAttachment, provisioning.ExternalIPAttachmentContext, provisioning.ProvisionStatusWithExtraVars) {
		scheme := runtime.NewScheme()
		if err := v1alpha1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		if err := bmfov1alpha1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		context := provisioning.ExternalIPAttachmentContext{
			ExternalIP: provisioning.ExternalIPAttachmentExternalIPContext{
				Name: "public-ip", Namespace: networkingNamespace, FulfillmentID: externalIPID,
				KubernetesUID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", TenantID: "tenant-a",
				Address: "198.51.100.10", State: "Allocated", Phase: "Ready",
			},
			Target: provisioning.ExternalIPAttachmentTargetContext{
				Kind: "BareMetalInstance", Name: "machine", Namespace: bmaasNamespace,
				FulfillmentID: bmiID, KubernetesUID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
				TenantID: "tenant-a", IPAddress: "10.0.1.10", Ready: true,
			},
			Subnet: provisioning.ExternalIPAttachmentSubnetContext{
				Name: "subnet", Namespace: networkingNamespace, FulfillmentID: subnetID,
				KubernetesUID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", TenantID: "tenant-a",
				IPv4CIDR: "10.0.1.0/24", VirtualNetworkID: virtualNetworkID, Phase: "Ready",
			},
			VirtualNetwork: provisioning.ExternalIPAttachmentVirtualNetworkContext{
				Name: "network", Namespace: networkingNamespace, FulfillmentID: virtualNetworkID,
				KubernetesUID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", TenantID: "tenant-a", Phase: "Ready",
			},
		}
		attachment := &v1alpha1.ExternalIPAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "attachment", Namespace: networkingNamespace,
				UID: types.UID(attachmentID), Generation: 2,
				Labels: map[string]string{osacExternalIPAttachmentIDLabel: attachmentID},
				Annotations: map[string]string{
					osacTenantKey: "tenant-a",
					osacExternalIPAttachmentContractVersionAnnotation: networkmanager.ExternalIPAttachmentContractVersionV1,
					osacImplementationStrategyAnnotation:              "agentless_net",
				},
			},
			Spec: v1alpha1.ExternalIPAttachmentSpec{ExternalIP: externalIPID, BaremetalInstance: ptr.To(bmiID)},
		}
		desiredVersion, err := externalIPAttachmentDesiredConfigVersion(
			attachment.Spec, "agentless_net", networkmanager.ExternalIPAttachmentContractVersionV1, context, "",
		)
		if err != nil {
			t.Fatal(err)
		}
		attachment.Status.DesiredConfigVersion = desiredVersion
		attachment.Status.ProvisioningJobs = []v1alpha1.JobStatus{{
			JobID: "attach-job", Type: v1alpha1.JobTypeProvision,
			State: v1alpha1.JobStateSucceeded, ConfigVersion: desiredVersion,
		}}

		externalIP := &v1alpha1.ExternalIP{
			ObjectMeta: metav1.ObjectMeta{
				Name: context.ExternalIP.Name, Namespace: context.ExternalIP.Namespace,
				UID:         types.UID(context.ExternalIP.KubernetesUID),
				Labels:      map[string]string{osacExternalIPIDLabel: externalIPID},
				Annotations: map[string]string{osacTenantKey: "tenant-a"},
			},
			Status: v1alpha1.ExternalIPStatus{
				Phase: v1alpha1.ExternalIPPhaseReady, State: v1alpha1.ExternalIPStateAllocated,
				Address: context.ExternalIP.Address,
			},
		}
		bmi := &bmfov1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name: context.Target.Name, Namespace: context.Target.Namespace,
				UID:         types.UID(context.Target.KubernetesUID),
				Labels:      map[string]string{osacBareMetalInstanceIDLabel: bmiID},
				Annotations: map[string]string{osacTenantKey: "tenant-a"},
			},
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Phase: bmfov1alpha1.BareMetalInstancePhaseReady,
				NetworkAttachmentStatuses: []bmfov1alpha1.BareMetalNetworkAttachmentStatus{{
					Primary: true, IPAddress: currentTargetIP, SubnetRef: subnetID,
				}},
			},
		}
		subnet := &v1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name: context.Subnet.Name, Namespace: context.Subnet.Namespace,
				UID:         types.UID(context.Subnet.KubernetesUID),
				Labels:      map[string]string{osacSubnetIDLabel: subnetID},
				Annotations: map[string]string{osacTenantKey: "tenant-a"},
			},
			Spec:   v1alpha1.SubnetSpec{VirtualNetwork: virtualNetworkID, IPv4CIDR: context.Subnet.IPv4CIDR},
			Status: v1alpha1.SubnetStatus{Phase: v1alpha1.SubnetPhaseReady},
		}
		virtualNetwork := &v1alpha1.VirtualNetwork{
			ObjectMeta: metav1.ObjectMeta{
				Name: context.VirtualNetwork.Name, Namespace: context.VirtualNetwork.Namespace,
				UID:         types.UID(context.VirtualNetwork.KubernetesUID),
				Labels:      map[string]string{osacVirtualNetworkIDLabel: virtualNetworkID},
				Annotations: map[string]string{osacTenantKey: "tenant-a"},
			},
			Status: v1alpha1.VirtualNetworkStatus{Phase: v1alpha1.VirtualNetworkPhaseReady},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(attachment, externalIP, bmi, subnet, virtualNetwork).
			WithStatusSubresource(&v1alpha1.ExternalIPAttachment{}).
			Build()
		reconciler := &ExternalIPAttachmentReconciler{
			Client: c, APIReader: c, NetworkingNamespace: networkingNamespace,
			BaremetalInstanceNamespace: bmaasNamespace,
		}
		input := map[string]any{
			"osac_job_vars": map[string]any{
				"resource": map[string]any{
					"metadata": map[string]any{
						"name": attachment.Name, "namespace": attachment.Namespace,
						"uid": string(attachment.UID), "generation": attachment.Generation,
						"labels": attachment.Labels, "annotations": attachment.Annotations,
					},
					"spec": map[string]any{"externalIP": externalIPID, "baremetalInstance": bmiID},
				},
				"external_ip_attachment_context": context,
			},
		}
		status := provisioning.ProvisionStatusWithExtraVars{
			ProvisionStatus: provisioning.ProvisionStatus{JobID: "attach-job", State: v1alpha1.JobStateSucceeded},
			InputExtraVars:  input,
			ExtraVars: map[string]any{"osac_result": map[string]any{
				"schemaVersion": "v1", "operation": provisioning.ExternalIPAttachmentAttachOperation,
				"resourceUID": string(attachment.UID), "observedGeneration": attachment.Generation,
				"data": map[string]any{},
			}},
		}
		return reconciler, attachment, context, status
	}

	t.Run("accepts a current successful attach artifact", func(t *testing.T) {
		reconciler, attachment, context, status := makeFixture("10.0.1.10")
		if err := reconciler.validateAttachmentProvisionResult(t.Context(), attachment, context, status); err != nil {
			t.Fatalf("valid attachment result was rejected: %v", err)
		}
	})

	t.Run("rejects an artifact after the target address changes", func(t *testing.T) {
		reconciler, attachment, context, status := makeFixture("10.0.1.11")
		if err := reconciler.validateAttachmentProvisionResult(t.Context(), attachment, context, status); err == nil {
			t.Fatal("stale attachment result was accepted")
		}
	})

	t.Run("marks Ready only after a matching AAP attach artifact", func(t *testing.T) {
		reconciler, attachment, context, status := makeFixture("10.0.1.10")
		attachment.Status.Phase = v1alpha1.ExternalIPAttachmentPhaseProgressing
		if err := reconciler.Client.Status().Update(t.Context(), attachment); err != nil {
			t.Fatal(err)
		}
		externalIP := &v1alpha1.ExternalIP{}
		if err := reconciler.Client.Get(t.Context(), types.NamespacedName{
			Namespace: networkingNamespace, Name: context.ExternalIP.Name,
		}, externalIP); err != nil {
			t.Fatal(err)
		}
		status.State = v1alpha1.JobStateSucceeded
		provider := &mockProvisioningProvider{
			getProvisionStatusWithExtraVarsFunc: func(stdcontext.Context, client.Object, string) (provisioning.ProvisionStatusWithExtraVars, error) {
				return status, nil
			},
		}
		reconciler.ProvisioningProvider = provider
		reconciler.NetworkProvisioningEnabled = true
		reconciler.StatusPollInterval = time.Second
		if _, err := reconciler.handleProvisioning(t.Context(), attachment, externalIP, nil, context); err != nil {
			t.Fatalf("valid AAP artifact was rejected: %v", err)
		}
		if attachment.Status.Phase != v1alpha1.ExternalIPAttachmentPhaseReady {
			t.Fatalf("phase = %q, want Ready", attachment.Status.Phase)
		}
		if err := reconciler.updateStatusWithRetry(
			t.Context(), types.NamespacedName{Namespace: networkingNamespace, Name: attachment.Name},
			attachment.UID, attachment.Generation, attachment.Status,
		); err != nil {
			t.Fatalf("persist Ready status: %v", err)
		}
		latest := &v1alpha1.ExternalIPAttachment{}
		if err := reconciler.Client.Get(t.Context(), types.NamespacedName{
			Namespace: networkingNamespace, Name: attachment.Name,
		}, latest); err != nil {
			t.Fatal(err)
		}
		if latest.Status.Phase != v1alpha1.ExternalIPAttachmentPhaseReady {
			t.Fatalf("persisted phase = %q, want Ready", latest.Status.Phase)
		}
	})

	t.Run("retains the finalizer when the detach artifact is invalid", func(t *testing.T) {
		reconciler, attachment, _, status := makeFixture("10.0.1.10")
		attachment.Finalizers = []string{osacExternalIPAttachmentFinalizer}
		status.JobID = "detach-job"
		status.State = v1alpha1.JobStateSucceeded
		status.ExtraVars["osac_result"].(map[string]any)["operation"] =
			provisioning.ExternalIPAttachmentAttachOperation
		if err := reconciler.Client.Update(t.Context(), attachment); err != nil {
			t.Fatal(err)
		}
		deleting := &v1alpha1.ExternalIPAttachment{}
		if err := reconciler.Client.Get(t.Context(), types.NamespacedName{
			Namespace: networkingNamespace, Name: attachment.Name,
		}, deleting); err != nil {
			t.Fatal(err)
		}
		deleting.Status.ProvisioningJobs = append(deleting.Status.ProvisioningJobs, v1alpha1.JobStatus{
			JobID: "detach-job", Type: v1alpha1.JobTypeDeprovision,
			State: v1alpha1.JobStateSucceeded, Timestamp: metav1.NewTime(time.Now().UTC()),
		})
		if err := reconciler.Client.Status().Update(t.Context(), deleting); err != nil {
			t.Fatal(err)
		}
		if err := reconciler.Client.Delete(t.Context(), deleting); err != nil {
			t.Fatal(err)
		}
		deleting = &v1alpha1.ExternalIPAttachment{}
		if err := reconciler.Client.Get(t.Context(), types.NamespacedName{
			Namespace: networkingNamespace, Name: attachment.Name,
		}, deleting); err != nil {
			t.Fatal(err)
		}
		if deleting.DeletionTimestamp.IsZero() {
			t.Fatal("fake API did not mark the ExternalIPAttachment as deleting")
		}
		if job := provisioning.FindLatestJobByType(deleting.Status.ProvisioningJobs, v1alpha1.JobTypeDeprovision); job == nil {
			t.Fatalf("fake API lost the detach job before reconciliation: %#v", deleting.Status.ProvisioningJobs)
		}
		resourceJSON, err := json.Marshal(deleting)
		if err != nil {
			t.Fatal(err)
		}
		var resource map[string]any
		if err := json.Unmarshal(resourceJSON, &resource); err != nil {
			t.Fatal(err)
		}
		status.InputExtraVars = map[string]any{
			"osac_job_vars": map[string]any{"resource": resource},
		}
		reconciler.ProvisioningProvider = &mockProvisioningProvider{
			getDeprovisionStatusWithExtraVarsFunc: func(stdcontext.Context, client.Object, string) (provisioning.ProvisionStatusWithExtraVars, error) {
				return status, nil
			},
		}
		reconciler.StatusPollInterval = time.Second
		result, err := reconciler.handleDelete(t.Context(), deleting)
		if err != nil {
			t.Fatalf("handleDelete() error = %v", err)
		}
		if result.RequeueAfter == 0 {
			t.Fatal("invalid detach artifact did not request a retry")
		}
		finalizerPresent := false
		for _, finalizer := range deleting.Finalizers {
			if finalizer == osacExternalIPAttachmentFinalizer {
				finalizerPresent = true
				break
			}
		}
		if !finalizerPresent {
			t.Fatal("ExternalIPAttachment finalizer was removed after an invalid detach artifact")
		}
		job := provisioning.FindLatestJobByType(deleting.Status.ProvisioningJobs, v1alpha1.JobTypeDeprovision)
		if job == nil || job.State != v1alpha1.JobStateFailed || !job.BlockDeletionOnFailure {
			t.Fatalf("detach job was not blocked for retry: %#v", job)
		}
	})
}
