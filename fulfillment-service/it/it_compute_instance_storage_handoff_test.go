/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const computeStorageLabel = "compute-storage"

var _ = Describe("ComputeInstance storage handoff", Ordered, Label(computeStorageLabel), func() {
	var fixture *computeStorageFixture

	BeforeAll(func(ctx context.Context) {
		Expect(config.TestSuite).To(Equal(computeStorageLabel),
			"run the isolated installer SUITE=compute-storage phase")
		installComputeStorageRecorder(ctx)
	})

	BeforeEach(func(ctx context.Context) {
		computeStorageRecorderRequest(ctx, "reset", map[string]any{})
		fixture = newComputeStorageFixture(ctx)
	})

	AfterEach(func(ctx context.Context) {
		Expect(readComputeStorageCaptures(ctx).Unexpected).To(BeEmpty())
	})

	It("preserves per-disk identity and classes through provisioning and guarded deletion", func(ctx context.Context) {
		fixture.addClass(ctx, 0)
		fixture.addClass(ctx, 1)
		instance := fixture.createInstance(ctx)
		parent, ids := waitComputeStorageDiskRecords(ctx, instance.GetId(), fixture)
		job := waitComputeStorageLaunch(ctx, instance.GetId(), "osac-create-compute-instance")
		expectComputeStorageLaunch(job, parent, ids, fixture)

		By("persisting the same IDs and provisioning job on the managed resource")
		Eventually(func(g Gomega) {
			cr := getComputeStorageInstance(ctx, g, instance.GetId())
			g.Expect(cr.Spec.DiskVolumeIDs).To(Equal(ids))
			g.Expect(cr.Status.Jobs).To(ContainElement(WithTransform(
				func(item osacv1alpha1.NetworkJobStatus) string { return item.JobID },
				Equal(strconv.Itoa(job.ID)),
			)))
		}, opTimeout, time.Second).Should(Succeed())

		By("reobserving the instance without registering or launching another disk")
		instances := privatev1.NewComputeInstancesClient(tool.InternalView().ControllerConn())
		_, err := instances.Signal(ctx, privatev1.ComputeInstancesSignalRequest_builder{Id: instance.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Consistently(func(g Gomega) {
			current, getErr := instances.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: instance.GetId()}.Build())
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(computeStorageDiskIDs(current.GetObject())).To(Equal(ids))
			g.Expect(readComputeStorageCaptures(ctx).Jobs).To(HaveLen(1))
		}, 5*time.Second, time.Second).Should(Succeed())
		expectComputeStorageRecordAttributes(ctx, parent, ids, fixture)

		computeStorageRecorderRequest(ctx, fmt.Sprintf("complete/%d", job.ID), map[string]any{"status": "successful"})
		_, err = publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn()).Delete(ctx,
			publicv1.ComputeInstancesDeleteRequest_builder{Id: instance.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		deletion := waitComputeStorageLaunch(ctx, instance.GetId(), "osac-delete-compute-instance")

		By("retaining records while workload teardown has not completed")
		volumes := privatev1.NewVolumesClient(tool.InternalView().ControllerConn())
		for _, id := range ids {
			_, getErr := volumes.Get(ctx, privatev1.VolumesGetRequest_builder{Id: id}.Build())
			Expect(getErr).NotTo(HaveOccurred())
		}
		_, err = instances.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: instance.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())

		computeStorageRecorderRequest(ctx, fmt.Sprintf("complete/%d", deletion.ID), map[string]any{"status": "successful"})
		Eventually(func(g Gomega) {
			for _, id := range ids {
				_, getErr := volumes.Get(ctx, privatev1.VolumesGetRequest_builder{Id: id}.Build())
				g.Expect(status.Code(getErr)).To(Equal(codes.NotFound))
			}
			_, getErr := instances.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: instance.GetId()}.Build())
			g.Expect(status.Code(getErr)).To(Equal(codes.NotFound))
		}, opTimeout, time.Second).Should(Succeed())
		fixture.expectClassesUnchanged(ctx)
	})

	DescribeTable("blocks the VM job until each disk tier has exactly one class", func(ctx context.Context, matches int) {
		fixture.addClass(ctx, 0)
		for range matches {
			fixture.addClass(ctx, 1)
		}
		instance := fixture.createInstance(ctx)
		parent, ids := waitComputeStorageDiskRecords(ctx, instance.GetId(), fixture)

		By("waiting for the real operator to report the requested tier is not usable")
		Eventually(func(g Gomega) {
			cr := getComputeStorageInstance(ctx, g, instance.GetId())
			g.Expect(cr.Spec.DiskVolumeIDs).To(Equal(ids))
			condition := meta.FindStatusCondition(cr.Status.Conditions, string(osacv1alpha1.ComputeInstanceConditionProvisioned))
			g.Expect(condition).NotTo(BeNil())
			g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(condition.Reason).To(Equal(osacv1alpha1.ReasonTenantNotReady))
			g.Expect(condition.Message).To(ContainSubstring(fixture.tierNames[1]))
			g.Expect(cr.Status.Jobs).To(BeEmpty())
		}, opTimeout, time.Second).Should(Succeed())
		Consistently(func() []computeStorageCapture {
			return readComputeStorageCaptures(ctx).Jobs
		}, 5*time.Second, time.Second).Should(BeEmpty())
		expectComputeStorageRecordAttributes(ctx, parent, ids, fixture)

		By("fixing the mapping and letting the operator continue with the original Volume IDs")
		if matches == 0 {
			fixture.addClass(ctx, 1)
		} else {
			fixture.removeLastClass(ctx)
		}
		job := waitComputeStorageLaunch(ctx, instance.GetId(), "osac-create-compute-instance")
		expectComputeStorageLaunch(job, parent, ids, fixture)
		expectComputeStorageRecordAttributes(ctx, parent, ids, fixture)
	},
		Entry("missing additional-disk class", 0),
		Entry("ambiguous additional-disk class", 2),
	)
})

func getComputeStorageInstance(ctx context.Context, g Gomega, id string) *osacv1alpha1.ComputeInstance {
	objects := &osacv1alpha1.ComputeInstanceList{}
	g.Expect(tool.KubeClient().List(ctx, objects, crclient.InNamespace(hubNamespace),
		crclient.MatchingLabels{labels.ComputeInstanceUuid: id})).To(Succeed())
	g.Expect(objects.Items).To(HaveLen(1))
	return &objects.Items[0]
}

func computeStorageDiskIDs(instance *privatev1.ComputeInstance) map[string]string {
	ids := make(map[string]string)
	for _, disk := range instance.GetStatus().GetVolumes() {
		ids[disk.GetDiskKey()] = disk.GetVolumeId()
	}
	return ids
}

func waitComputeStorageDiskRecords(ctx context.Context, id string, fixture *computeStorageFixture) (*privatev1.ComputeInstance, map[string]string) {
	GinkgoHelper()
	instances := privatev1.NewComputeInstancesClient(tool.InternalView().ControllerConn())
	var parent *privatev1.ComputeInstance
	var ids map[string]string
	Eventually(func(g Gomega) {
		response, err := instances.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build())
		g.Expect(err).NotTo(HaveOccurred())
		parent = response.GetObject()
		ids = computeStorageDiskIDs(parent)
		g.Expect(ids).To(HaveLen(2))
		g.Expect(ids["boot"]).NotTo(BeEmpty())
		g.Expect(ids["disk-1"]).NotTo(BeEmpty())
		g.Expect(ids["boot"]).NotTo(Equal(ids["disk-1"]))
	}, opTimeout, time.Second).Should(Succeed())
	expectComputeStorageRecordAttributes(ctx, parent, ids, fixture)
	return parent, ids
}

func expectComputeStorageRecordAttributes(ctx context.Context, parent *privatev1.ComputeInstance, ids map[string]string, fixture *computeStorageFixture) {
	GinkgoHelper()
	volumes := privatev1.NewVolumesClient(tool.InternalView().ControllerConn())
	for index, key := range []string{"boot", "disk-1"} {
		name := "ci-" + parent.GetId() + "-" + key
		filter := diskRegistrationAPIFilter(name, parent.GetMetadata().GetTenant(), parent.GetMetadata().GetProject())
		response, err := volumes.List(ctx, privatev1.VolumesListRequest_builder{Filter: &filter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(response.GetTotal()).To(Equal(int32(1)))
		Expect(response.GetItems()).To(HaveLen(1))
		volume := response.GetItems()[0]
		Expect(volume.GetId()).To(Equal(ids[key]))
		Expect(volume.GetMetadata().GetTenant()).To(Equal(parent.GetMetadata().GetTenant()))
		Expect(volume.GetMetadata().GetProject()).To(Equal(parent.GetMetadata().GetProject()))
		Expect(volume.GetMetadata().GetName()).To(Equal(name))
		Expect(volume.GetSpec().GetStorageTier()).To(Equal(fixture.tierNames[index]))
		Expect(volume.GetSpec().GetSizeGib()).To(Equal([]int64{30, 12}[index]))
		Expect(volume.GetStatus().GetProvider()).To(Equal("netapp"))
	}
}

func waitComputeStorageLaunch(ctx context.Context, id, template string) computeStorageCapture {
	GinkgoHelper()
	var found computeStorageCapture
	Eventually(func(g Gomega) {
		captures := readComputeStorageCaptures(ctx)
		g.Expect(captures.Unexpected).To(BeEmpty())
		matches := make([]computeStorageCapture, 0)
		for _, job := range captures.Jobs {
			if job.Template == template && job.Payload.ExtraVars.JobVars.Resource.Labels[labels.ComputeInstanceUuid] == id {
				matches = append(matches, job)
			}
		}
		g.Expect(matches).To(HaveLen(1))
		found = matches[0]
	}, opTimeout, time.Second).Should(Succeed())
	return found
}

func expectComputeStorageLaunch(job computeStorageCapture, parent *privatev1.ComputeInstance, ids map[string]string, fixture *computeStorageFixture) {
	GinkgoHelper()
	vars := job.Payload.ExtraVars.JobVars
	Expect(vars.Resource.Spec.DiskVolumeIDs).To(Equal(ids))
	Expect(vars.Resource.Annotations["osac.openshift.io/tenant"]).To(Equal(parent.GetMetadata().GetTenant()))
	Expect(vars.Resource.Spec.BootDisk.StorageTier).To(Equal(fixture.tierNames[0]))
	Expect(vars.Resource.Spec.AdditionalDisks).To(HaveLen(1))
	Expect(vars.Resource.Spec.AdditionalDisks[0].StorageTier).To(Equal(fixture.tierNames[1]))
	Expect(vars.Classes).To(ConsistOf(
		osacv1alpha1.ResolvedStorageClass{Tier: fixture.tierNames[0], Name: fixture.classes[0].Name},
		osacv1alpha1.ResolvedStorageClass{Tier: fixture.tierNames[1], Name: fixture.classes[1].Name},
	))
	Expect(vars.Connections).To(BeEmpty(), "CSP storage does not send credentials or provider provisioning inputs")
	Expect(vars.TierDefinitions).To(BeEmpty(), "CSP classes must not trigger JIT storage provisioning")
}
