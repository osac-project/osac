// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package awx

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/tests/integration/caas/internal/harness"
)

const createPlaybook = "osac-aap/playbook_osac_create_bare_metal_instance.yml"

var _ = Describe("Observed AWX execution evidence", func() {
	var (
		job      aap.Job
		pod      corev1.Pod
		selected *Installation
	)

	BeforeEach(func() {
		job = testAWXJob(7, "successful", "uid", "osac", "tenant", createPlaybook)
		job.ProjectID, job.ExecutionEnvironmentID, job.InstanceGroupID, job.SCMRevision = 1, 2, 3, "commit"
		pod = corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "execution", UID: "pod-uid"},
			Spec: corev1.PodSpec{
				NodeName:           "node",
				ServiceAccountName: "osac-sa",
				Containers:         []corev1.Container{{Name: "worker", Image: "image", ImagePullPolicy: corev1.PullNever}},
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{{Name: "worker", ImageID: "runtime-image"}},
			},
		}
		// No AWX client is installed: verification must use the supplied observation.
		selected = &Installation{
			ProjectCommit: "commit", EEImage: "image", EEPullPolicy: corev1.PullNever,
			projectID: 1, executionEnvironmentID: 2,
			instanceGroupIDs: map[string]int{
				"osac-bare-metal-fulfillment-ig": 3, "osac-cluster-fulfillment-ig": 4,
			},
		}
	})

	It("rejects missing verified groups even when the observed group ID is zero", func() {
		selected.instanceGroupIDs = nil
		job.InstanceGroupID = 0

		_, err := selected.CheckExecution(job, &metav1.ObjectMeta{}, createPlaybook, nil)
		Expect(err).To(MatchError(ContainSubstring("missing verified execution group")))
	})

	It("rejects an unsupported requested playbook before checking observed inputs", func() {
		_, err := selected.CheckExecution(job, &metav1.ObjectMeta{}, "other.yml", nil)
		Expect(err).To(MatchError(`unsupported execution playbook "other.yml"`))
	})

	DescribeTable(
		"selects each production group from the requested playbook",
		func(playbook string, groupID int) {
			actual, err := selected.executionGroupID(playbook)
			Expect(err).NotTo(HaveOccurred())
			Expect(actual).To(Equal(groupID))
		},
		Entry("bare-metal create", createPlaybook, 3),
		Entry("bare-metal delete",
			"osac-aap/playbook_osac_delete_bare_metal_instance.yml", 3),
		Entry("cluster create",
			"osac-aap/playbook_osac_create_hosted_cluster.yml", 4),
		Entry("cluster delete",
			"osac-aap/playbook_osac_delete_hosted_cluster.yml", 4),
	)

	DescribeTable(
		"validates resource scope and selected deployment inputs",
		func(change func(*aap.Job, *corev1.Pod), wantError string) {
			if change != nil {
				change(&job, &pod)
			}

			owner := &metav1.ObjectMeta{
				UID:         "uid",
				Namespace:   "osac",
				Annotations: map[string]string{harness.TenantAnnotation: "tenant"},
			}

			evidence, err := selected.CheckExecution(
				job,
				owner,
				createPlaybook,
				&JobPodObserver{pods: map[string]corev1.Pod{"7": pod}},
			)
			if wantError != "" {
				Expect(err).To(MatchError(ContainSubstring(wantError)))
				return
			}

			Expect(err).NotTo(HaveOccurred())
			Expect(evidence).To(HaveValue(Equal(AWXExecutionEvidence{
				JobID: "7", PodName: "execution", PodUID: "pod-uid", NodeName: "node",
				ImageReference: "image", ImageID: "runtime-image", SCMRevision: "commit",
			})))
		},
		Entry("retains successful execution evidence", nil, ""),
		Entry(
			"retains failed execution evidence for diagnostics",
			func(job *aap.Job, _ *corev1.Pod) { job.Status = "failed" },
			"",
		),
		Entry("rejects a foreign resource", func(job *aap.Job, _ *corev1.Pod) {
			*job = testAWXJob(7, "successful", "other", "osac", "tenant", createPlaybook)
		}, "another resource incarnation or scope"),
		Entry("rejects a foreign namespace", func(job *aap.Job, _ *corev1.Pod) {
			*job = testAWXJob(7, "successful", "uid", "other", "tenant", createPlaybook)
		}, "another resource incarnation or scope"),
		Entry("rejects a foreign tenant", func(job *aap.Job, _ *corev1.Pod) {
			*job = testAWXJob(7, "successful", "uid", "osac", "other", createPlaybook)
		}, "another resource incarnation or scope"),
		Entry(
			"rejects another playbook",
			func(job *aap.Job, _ *corev1.Pod) { job.Playbook = "other.yml" },
			"selected playbook",
		),
		Entry("rejects another project", func(job *aap.Job, _ *corev1.Pod) { job.ProjectID++ }, "selected playbook"),
		Entry(
			"rejects another revision",
			func(job *aap.Job, _ *corev1.Pod) { job.SCMRevision = "other" },
			"selected playbook",
		),
		Entry(
			"rejects another execution environment",
			func(job *aap.Job, _ *corev1.Pod) { job.ExecutionEnvironmentID++ },
			"selected playbook",
		),
		Entry(
			"rejects another execution group",
			func(job *aap.Job, _ *corev1.Pod) { job.InstanceGroupID++ },
			"selected playbook",
		),
		Entry(
			"rejects another service account",
			func(_ *aap.Job, pod *corev1.Pod) { pod.Spec.ServiceAccountName = "other" },
			"service account",
		),
		Entry(
			"rejects another requested image",
			func(_ *aap.Job, pod *corev1.Pod) { pod.Spec.Containers[0].Image = "other" },
			"another EE",
		),
		Entry(
			"rejects another pull policy",
			func(_ *aap.Job, pod *corev1.Pod) { pod.Spec.Containers[0].ImagePullPolicy = corev1.PullAlways },
			"another EE",
		),
		Entry(
			"rejects missing runtime image identity",
			func(_ *aap.Job, pod *corev1.Pod) { pod.Status.ContainerStatuses[0].ImageID = "" },
			"runtime image identity",
		),
	)
})
