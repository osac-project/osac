package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

type nicoPilotProvider struct {
	mockProvisioningProvider
	launches              int
	lastExtraVars         map[string]any
	outputs               map[string]any
	expectedCredentialIDs []int
	// provisionState is the create job state; empty means Succeeded.
	provisionState opv1alpha1.JobState
	// deprovisionOutputs overrides the default released artifacts when set.
	deprovisionOutputs map[string]any
}

func (p *nicoPilotProvider) TriggerProvisionWithExtraVars(ctx context.Context, _ client.Object, vars map[string]any) (*provisioning.ProvisionResult, error) {
	Expect(vars["nico_bmaas_create_allowed"]).To(BeTrue())
	Expect(provisioning.AAPCredentialIDsFromContext(ctx)).To(Equal(p.expectedCredentialIDs))
	p.lastExtraVars = vars
	p.launches++
	return &provisioning.ProvisionResult{JobID: "nico-create-1", InitialState: opv1alpha1.JobStatePending}, nil
}

func (p *nicoPilotProvider) GetProvisionStatusWithExtraVars(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatusWithExtraVars, error) {
	state := p.provisionState
	if state == "" {
		state = opv1alpha1.JobStateSucceeded
	}
	return provisioning.ProvisionStatusWithExtraVars{
		ProvisionStatus: provisioning.ProvisionStatus{JobID: jobID, State: state},
		ExtraVars:       p.outputs,
	}, nil
}

func (p *nicoPilotProvider) GetDeprovisionStatusWithExtraVars(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatusWithExtraVars, error) {
	outputs := p.deprovisionOutputs
	if outputs == nil {
		outputs = map[string]any{
			"nicoInstanceID":    "nico-instance-1",
			"nicoMachineID":     "nico-machine-1",
			"nicoCleanupStatus": "Released",
		}
	}
	return provisioning.ProvisionStatusWithExtraVars{
		ProvisionStatus: provisioning.ProvisionStatus{JobID: jobID, State: opv1alpha1.JobStateSucceeded},
		ExtraVars:       outputs,
	}, nil
}

const nicoTestBindings = `version: 2
bindings:
  - osacTenantID: pilot-tenant
    osacSubnetID: osac-pilot-subnet
    credentialIDs: [41]
  - osacTenantID: other-tenant
    osacSubnetID: other-subnet
    credentialIDs: [42]
operatingSystems:
  - diskImageID: centos-disk-image
    nicoOperatingSystemID: nico-centos-os
`

const nicoTestBindingsV1 = `version: 1
bindings:
  - osacTenantID: pilot-tenant
    osacSubnetID: osac-pilot-subnet
    credentialIDs: [41]
`

// nicoTestBindingsV1Body returns the pilot tenant binding with the given
// version, ready for an appended operatingSystems section.
func nicoTestBindingsV1Body(version int) string {
	return fmt.Sprintf("version: %d\nbindings:\n  - osacTenantID: pilot-tenant\n    osacSubnetID: osac-pilot-subnet\n    credentialIDs: [41]\n", version)
}

var _ = Describe("NICo BareMetalInstance lifecycle", func() {
	var bmi *v1alpha1.BareMetalInstance
	var reconciler *BareMetalInstanceReconciler
	var provider *nicoPilotProvider
	var req ctrl.Request

	BeforeEach(func() {
		provider = &nicoPilotProvider{outputs: map[string]any{
			"nicoInstanceID": "nico-instance-1", "nicoMachineID": "nico-machine-1", "nicoIPAddress": "10.217.98.2",
		}, expectedCredentialIDs: []int{41}}
		bmi = &v1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "nico-bmi-pilot-", Namespace: "default",
				Annotations: map[string]string{
					"osac.openshift.io/tenant":          "pilot-tenant",
					"osac.openshift.io/owner-reference": "BareMetalInstance/nico-bmi-pilot",
					diskImageIDAnnotation:               "centos-disk-image",
				},
			},
			Spec: v1alpha1.BareMetalInstanceSpec{
				TemplateID:         nicoBMaaSTemplateID,
				TemplateParameters: `{"imageURL":"http://artifacts/centos.qcow2"}`,
				Selector:           v1alpha1.HostSelectorSpec{HostSelector: map[string]string{"managedBy": "nico"}},
				NetworkAttachments: []v1alpha1.BareMetalNetworkAttachment{{SubnetRef: "osac-pilot-subnet", Interface: "eno1", Primary: true}},
			},
		}
		Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
		req = ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bmi)}
		reconciler = NewBareMetalInstanceReconciler(
			k8sClient, k8sClient.Scheme(),
			&mockInventoryClient{
				findFreeHostFunc: func(context.Context, map[string]string) (*inventory.Host, error) {
					return nil, fmt.Errorf("generic NICo inventory path must not run")
				},
				assignHostFunc: func(context.Context, string, string, map[string]string) (*inventory.Host, error) {
					return nil, fmt.Errorf("generic NICo host assignment must not run")
				},
			},
			nil, provider, nil, nil, nil, 0, 0, 0, time.Millisecond, 0,
		)
		bindingsPath := filepath.Join(GinkgoT().TempDir(), "bindings.yaml")
		Expect(os.WriteFile(bindingsPath, []byte(nicoTestBindings), 0600)).To(Succeed())
		reconciler.NicoBindingsConfigPath = bindingsPath
	})

	AfterEach(func() {
		fresh := &v1alpha1.BareMetalInstance{}
		if k8sClient.Get(ctx, req.NamespacedName, fresh) == nil {
			fresh.Finalizers = nil
			_ = k8sClient.Update(ctx, fresh)
			_ = k8sClient.Delete(ctx, fresh)
		}
	})

	reconcile := func() *v1alpha1.BareMetalInstance {
		_, err := reconciler.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		fresh := &v1alpha1.BareMetalInstance{}
		Expect(k8sClient.Get(ctx, req.NamespacedName, fresh)).To(Succeed())
		return fresh
	}

	It("persists one create intent, saves both IDs, and removes its finalizer only after verified cleanup", func() {
		provider.triggerDeprovisionFunc = func(ctx context.Context, _ client.Object, _ []opv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
			Expect(provisioning.AAPCredentialIDsFromContext(ctx)).To(Equal([]int{41}))
			return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionTriggered, JobID: "nico-delete-1", BlockDeletionOnFailure: true}, nil
		}
		fresh := reconcile() // management finalizer
		Expect(controllerutil.ContainsFinalizer(fresh, BareMetalInstanceManagementFinalizer)).To(BeTrue())
		Expect(fresh.Status.NicoCreateAttempted).To(BeFalse())
		fresh = reconcile() // durable marker, then one AAP create launch
		Expect(fresh.Status.NicoCreateAttempted).To(BeTrue())
		Expect(provider.launches).To(Equal(1))
		Expect(provider.lastExtraVars).To(HaveKeyWithValue("nico_bmaas_operating_system_id", "nico-centos-os"))
		Expect(fresh.Status.ProvisioningJobs).To(HaveLen(1))
		fresh = reconcile() // AAP artifacts
		Expect(fresh.Status.NicoInstanceID).To(Equal("nico-instance-1"))
		Expect(fresh.Status.NicoMachineID).To(Equal("nico-machine-1"))
		Expect(fresh.Spec.ExternalHostID).To(Equal("nico-machine-1"))
		Expect(fresh.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseReady))
		Expect(fresh.Status.NetworkAttachmentStatuses[0].IPAddress).To(Equal("10.217.98.2"))
		Expect(v1alpha1.DeriveProvisioningProgress(fresh.Status.Conditions).State).To(Equal(v1alpha1.StateReady))
		fresh = reconcile() // idempotent: no extra AAP launch
		Expect(fresh.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseReady))
		Expect(provider.launches).To(Equal(1))

		Expect(k8sClient.Delete(ctx, fresh)).To(Succeed())
		fresh = reconcile() // launch deprovision
		Expect(controllerutil.ContainsFinalizer(fresh, BareMetalInstanceManagementFinalizer)).To(BeTrue())
		_, err := reconciler.Reconcile(ctx, req) // successful deprovision artifacts
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, req.NamespacedName, &v1alpha1.BareMetalInstance{})).ToNot(Succeed())
	})

	It("selects the second tenant credential without changing the BMI contract", func() {
		// Network attachments are immutable. Create a distinct BMI for the
		// second tenant instead of changing the first tenant's allocation.
		Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
		bmi = &v1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "nico-bmi-other-", Namespace: "default",
				Annotations: map[string]string{
					"osac.openshift.io/tenant":          "other-tenant",
					"osac.openshift.io/owner-reference": "BareMetalInstance/nico-bmi-other",
					diskImageIDAnnotation:               "centos-disk-image",
				},
			},
			Spec: v1alpha1.BareMetalInstanceSpec{
				TemplateID:         nicoBMaaSTemplateID,
				Selector:           v1alpha1.HostSelectorSpec{HostSelector: map[string]string{"managedBy": "nico"}},
				NetworkAttachments: []v1alpha1.BareMetalNetworkAttachment{{SubnetRef: "other-subnet", Interface: "eno1", Primary: true}},
			},
		}
		Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
		req = ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bmi)}
		provider.expectedCredentialIDs = []int{42}
		reconcile()
		fresh := reconcile()
		Expect(fresh.Status.NicoCreateAttempted).To(BeTrue())
		Expect(provider.launches).To(Equal(1))
	})

	It("refuses a tenant and Subnet combination with no binding before creating", func() {
		bmi.Annotations["osac.openshift.io/tenant"] = "other-tenant"
		Expect(k8sClient.Update(ctx, bmi)).To(Succeed())
		reconcile() // finalizer is installed before any external create
		_, err := reconciler.Reconcile(ctx, req)
		Expect(err).To(MatchError(ContainSubstring("no NICo tenant binding")))
		fresh := &v1alpha1.BareMetalInstance{}
		Expect(k8sClient.Get(ctx, req.NamespacedName, fresh)).To(Succeed())
		Expect(fresh.Status.NicoCreateAttempted).To(BeFalse())
		Expect(provider.launches).To(BeZero())
	})

	It("keeps the cleanup finalizer if the tenant binding disappears before delete", func() {
		reconcile()
		reconcile()
		fresh := reconcile()
		Expect(fresh.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseReady))
		reconciler.NicoBindingsConfigPath = filepath.Join(GinkgoT().TempDir(), "missing-bindings.yaml")
		Expect(k8sClient.Delete(ctx, fresh)).To(Succeed())
		_, err := reconciler.Reconcile(ctx, req)
		Expect(err).To(MatchError(ContainSubstring("read NICo tenant bindings")))
		Expect(k8sClient.Get(ctx, req.NamespacedName, fresh)).To(Succeed())
		Expect(controllerutil.ContainsFinalizer(fresh, BareMetalInstanceManagementFinalizer)).To(BeTrue())
	})

	It("retains the finalizer when AAP succeeds without provider identifiers", func() {
		provider.outputs = map[string]any{}
		reconcile()
		reconcile()
		fresh := reconcile()
		Expect(fresh.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseFailed))
		Expect(fresh.Status.NicoCreateAttempted).To(BeTrue())
		Expect(provider.launches).To(Equal(1))
		Expect(k8sClient.Delete(ctx, fresh)).To(Succeed())
		_, err := reconciler.Reconcile(ctx, req)
		Expect(err).To(HaveOccurred())
		Expect(k8sClient.Get(ctx, req.NamespacedName, fresh)).To(Succeed())
		Expect(controllerutil.ContainsFinalizer(fresh, BareMetalInstanceManagementFinalizer)).To(BeTrue())
	})

	DescribeTable("rejects a DiskImage without an approved NICo Operating System before creating",
		func(diskImageID string) {
			if diskImageID == "" {
				delete(bmi.Annotations, diskImageIDAnnotation)
			} else {
				bmi.Annotations[diskImageIDAnnotation] = diskImageID
			}
			Expect(k8sClient.Update(ctx, bmi)).To(Succeed())
			reconcile() // finalizer
			fresh := reconcile()
			Expect(fresh.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseFailed))
			Expect(fresh.Status.NicoCreateAttempted).To(BeFalse())
			Expect(provider.launches).To(BeZero())
			condition := apimeta.FindStatusCondition(fresh.Status.Conditions, string(v1alpha1.HostConditionProvisionTemplateComplete))
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal(v1alpha1.HostConditionReasonUnsupportedDiskImage))
			Expect(v1alpha1.DeriveProvisioningProgress(fresh.Status.Conditions).State).To(Equal(v1alpha1.StateFailed))

			// Nothing was created in NICo, so delete needs no provider cleanup.
			Expect(k8sClient.Delete(ctx, fresh)).To(Succeed())
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, req.NamespacedName, &v1alpha1.BareMetalInstance{})).ToNot(Succeed())
		},
		Entry("unmapped DiskImage", "fedora-disk-image"),
		Entry("no DiskImage ID annotation", ""),
	)

	It("keeps an unsupported DiskImage terminal after the catalog gains it", func() {
		bmi.Annotations[diskImageIDAnnotation] = "fedora-disk-image"
		Expect(k8sClient.Update(ctx, bmi)).To(Succeed())
		reconcile() // finalizer
		fresh := reconcile()
		Expect(fresh.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseFailed))

		catalog := nicoTestBindings + "  - diskImageID: fedora-disk-image\n    nicoOperatingSystemID: nico-fedora-os\n"
		Expect(os.WriteFile(reconciler.NicoBindingsConfigPath, []byte(catalog), 0600)).To(Succeed())
		fresh = reconcile()
		Expect(fresh.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseFailed))
		Expect(fresh.Status.NicoCreateAttempted).To(BeFalse())
		Expect(provider.launches).To(BeZero())
	})

	It("does not create from bindings without the version 2 Operating System catalog", func() {
		Expect(os.WriteFile(reconciler.NicoBindingsConfigPath, []byte(nicoTestBindingsV1), 0600)).To(Succeed())
		reconcile() // finalizer
		_, err := reconciler.Reconcile(ctx, req)
		Expect(err).To(MatchError(ContainSubstring("requires the Operating System catalog")))
		fresh := &v1alpha1.BareMetalInstance{}
		Expect(k8sClient.Get(ctx, req.NamespacedName, fresh)).To(Succeed())
		Expect(fresh.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseFailed))
		Expect(fresh.Status.NicoCreateAttempted).To(BeFalse())
		Expect(provider.launches).To(BeZero())
	})

	It("deletes a created BMI with version 1 bindings, which need no catalog", func() {
		provider.triggerDeprovisionFunc = func(ctx context.Context, _ client.Object, _ []opv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
			Expect(provisioning.AAPCredentialIDsFromContext(ctx)).To(Equal([]int{41}))
			return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionTriggered, JobID: "nico-delete-1", BlockDeletionOnFailure: true}, nil
		}
		reconcile()
		reconcile()
		fresh := reconcile()
		Expect(fresh.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseReady))
		Expect(os.WriteFile(reconciler.NicoBindingsConfigPath, []byte(nicoTestBindingsV1), 0600)).To(Succeed())
		Expect(k8sClient.Delete(ctx, fresh)).To(Succeed())
		reconcile() // launch deprovision with the version 1 tenant binding
		_, err := reconciler.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, req.NamespacedName, &v1alpha1.BareMetalInstance{})).ToNot(Succeed())
	})

	DescribeTable("rejects an invalid Operating System catalog",
		func(config string, message string) {
			Expect(os.WriteFile(reconciler.NicoBindingsConfigPath, []byte(config), 0600)).To(Succeed())
			reconcile() // finalizer
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).To(MatchError(ContainSubstring(message)))
			Expect(provider.launches).To(BeZero())
		},
		Entry("missing Operating System ID",
			nicoTestBindingsV1Body(2)+"operatingSystems:\n  - diskImageID: centos-disk-image\n",
			"entry 0 is incomplete"),
		Entry("padded DiskImage ID",
			nicoTestBindingsV1Body(2)+"operatingSystems:\n  - diskImageID: ' centos-disk-image'\n    nicoOperatingSystemID: nico-centos-os\n",
			"entry 0 is incomplete"),
		Entry("duplicate DiskImage",
			nicoTestBindingsV1Body(2)+"operatingSystems:\n  - diskImageID: centos-disk-image\n    nicoOperatingSystemID: a\n  - diskImageID: centos-disk-image\n    nicoOperatingSystemID: b\n",
			"duplicate NICo Operating System catalog entry"),
		Entry("catalog in version 1",
			nicoTestBindingsV1Body(1)+"operatingSystems:\n  - diskImageID: centos-disk-image\n    nicoOperatingSystemID: nico-centos-os\n",
			"catalog requires bindings version 2"),
	)

	Context("when the NICo create job fails", func() {
		var deprovisionLaunches int

		BeforeEach(func() {
			deprovisionLaunches = 0
			provider.provisionState = opv1alpha1.JobStateFailed
			provider.triggerDeprovisionFunc = func(ctx context.Context, _ client.Object, _ []opv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
				Expect(provisioning.AAPCredentialIDsFromContext(ctx)).To(Equal([]int{41}))
				deprovisionLaunches++
				return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionTriggered, JobID: "nico-delete-1", BlockDeletionOnFailure: true}, nil
			}
		})

		createAndFail := func() *v1alpha1.BareMetalInstance {
			reconcile() // finalizer
			reconcile() // create intent and AAP launch
			fresh := reconcile()
			Expect(fresh.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseFailed))
			Expect(provider.launches).To(Equal(1))
			return fresh
		}

		It("releases the BMI without NICo cleanup when nothing was created", func() {
			provider.outputs = map[string]any{
				"nicoCreateOutcome": "NotCreated",
				"nicoFailureReason": "NICo Operating System nico-centos-os not found",
			}
			fresh := createAndFail()
			Expect(fresh.Status.NicoCreateOutcome).To(Equal("NotCreated"))
			condition := apimeta.FindStatusCondition(fresh.Status.Conditions, string(v1alpha1.HostConditionProvisionTemplateComplete))
			Expect(condition).NotTo(BeNil())
			Expect(condition.Message).To(ContainSubstring("NICo Operating System nico-centos-os not found"))

			Expect(k8sClient.Delete(ctx, fresh)).To(Succeed())
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, req.NamespacedName, &v1alpha1.BareMetalInstance{})).ToNot(Succeed())
			Expect(deprovisionLaunches).To(BeZero())
		})

		It("saves the created Instance and releases it through NICo on delete", func() {
			provider.outputs = map[string]any{
				"nicoCreateOutcome": "Created",
				"nicoInstanceID":    "nico-instance-1",
				"nicoMachineID":     "nico-machine-1",
				"nicoFailureReason": "Instance nico-instance-1 did not reach Ready before timeout",
			}
			fresh := createAndFail()
			Expect(fresh.Status.NicoCreateOutcome).To(Equal("Created"))
			Expect(fresh.Status.NicoInstanceID).To(Equal("nico-instance-1"))
			Expect(fresh.Status.NicoMachineID).To(Equal("nico-machine-1"))

			Expect(k8sClient.Delete(ctx, fresh)).To(Succeed())
			reconcile() // launch deprovision with the saved IDs
			Expect(deprovisionLaunches).To(Equal(1))
			_, err := reconciler.Reconcile(ctx, req) // Released artifacts
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, req.NamespacedName, &v1alpha1.BareMetalInstance{})).ToNot(Succeed())
		})

		It("releases a created Instance that never got a Machine", func() {
			provider.outputs = map[string]any{
				"nicoCreateOutcome": "Created",
				"nicoInstanceID":    "nico-instance-1",
				"nicoFailureReason": "Instance nico-instance-1 did not reach Ready before timeout",
			}
			provider.deprovisionOutputs = map[string]any{
				"nicoInstanceID":    "nico-instance-1",
				"nicoMachineID":     "",
				"nicoCleanupStatus": "Released",
			}
			fresh := createAndFail()
			Expect(fresh.Status.NicoInstanceID).To(Equal("nico-instance-1"))
			Expect(fresh.Status.NicoMachineID).To(BeEmpty())

			Expect(k8sClient.Delete(ctx, fresh)).To(Succeed())
			reconcile() // launch deprovision with the Instance ID only
			Expect(deprovisionLaunches).To(Equal(1))
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, req.NamespacedName, &v1alpha1.BareMetalInstance{})).ToNot(Succeed())
		})

		It("keeps reported IDs from an uncertain outcome and releases them through NICo", func() {
			provider.outputs = map[string]any{
				"nicoCreateOutcome": "Uncertain",
				"nicoInstanceID":    "nico-instance-1",
				"nicoMachineID":     "nico-machine-1",
			}
			fresh := createAndFail()
			Expect(fresh.Status.NicoCreateOutcome).To(Equal("Uncertain"))
			Expect(fresh.Status.NicoInstanceID).To(Equal("nico-instance-1"))
			Expect(fresh.Status.NicoMachineID).To(Equal("nico-machine-1"))

			Expect(k8sClient.Delete(ctx, fresh)).To(Succeed())
			reconcile() // launch deprovision with the reported IDs
			Expect(deprovisionLaunches).To(Equal(1))
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, req.NamespacedName, &v1alpha1.BareMetalInstance{})).ToNot(Succeed())
		})

		It("keeps the finalizer when the job reports no outcome", func() {
			provider.provisionState = opv1alpha1.JobStateCanceled
			provider.outputs = map[string]any{}
			fresh := createAndFail()
			Expect(fresh.Status.NicoCreateOutcome).To(Equal("Uncertain"))

			Expect(k8sClient.Delete(ctx, fresh)).To(Succeed())
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).To(MatchError(ContainSubstring("without a provider Instance ID")))
			Expect(k8sClient.Get(ctx, req.NamespacedName, fresh)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(fresh, BareMetalInstanceManagementFinalizer)).To(BeTrue())
			Expect(deprovisionLaunches).To(BeZero())
		})

		It("releases a BMI deleted while a create job that never reached NICo was running", func() {
			provider.provisionState = opv1alpha1.JobStateRunning
			reconcile()          // finalizer
			fresh := reconcile() // create intent and AAP launch
			Expect(provider.launches).To(Equal(1))

			Expect(k8sClient.Delete(ctx, fresh)).To(Succeed())
			provider.provisionState = opv1alpha1.JobStateFailed
			provider.outputs = map[string]any{"nicoCreateOutcome": "NotCreated"}
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, req.NamespacedName, &v1alpha1.BareMetalInstance{})).ToNot(Succeed())
			Expect(deprovisionLaunches).To(BeZero())
		})
	})

	It("releases a BMI deleted before any NICo create attempt", func() {
		fresh := reconcile() // install the cleanup finalizer
		Expect(fresh.Status.NicoCreateAttempted).To(BeFalse())
		Expect(k8sClient.Delete(ctx, fresh)).To(Succeed())
		_, err := reconciler.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, req.NamespacedName, &v1alpha1.BareMetalInstance{})).ToNot(Succeed())
		Expect(provider.launches).To(BeZero())
	})
})
