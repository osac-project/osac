// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package awx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/osac-project/osac/tests/integration/caas/internal/harness"
)

var _ = Describe("AWX job observation", func() {
	const playbook = "osac-aap/create.yml"

	It("captures worker Pods through the dedicated watch client and stops its watch on Close", func(ctx SpecContext) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "execution", Namespace: "osac", UID: "execution",
				Labels: map[string]string{"ansible-awx-job-id": "7"},
			},
			Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "worker", ImageID: "retained-image"}}},
		}
		events := watch.NewRaceFreeFake()
		DeferCleanup(events.Stop)

		kube := kubefake.NewClientset()
		kube.PrependWatchReactor("pods", func(action ktesting.Action) (bool, watch.Interface, error) {
			defer GinkgoRecover()

			Expect(action.GetNamespace()).To(Equal(pod.Namespace))
			Expect(action.(ktesting.WatchAction).GetWatchRestrictions().Labels.String()).To(Equal("ansible-awx-job-id"))

			return true, events, nil
		})
		observer, err := ObserveJobPods(ctx, &harness.Environment{Namespace: "osac", WatchKube: kube})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(observer.Close)

		events.Add(pod)
		Eventually(ctx, func() (*corev1.Pod, error) { return observer.podForJob("7") }).
			WithTimeout(2 * time.Second).Should(Equal(pod))
		observer.Close()
		Expect(events.IsStopped()).To(BeTrue())
	})

	DescribeTable("finds all attempts for the original resource",
		func(ctx SpecContext, buildJobs func() []aap.Job, wantIDs []int, wantError string) {
			By("serving retained jobs through the shared AAP client")

			jobs := buildJobs()
			created := metav1.Now()
			installation := &Installation{Client: testAWXClient(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal("/api/v2/jobs/"))
				Expect(r.URL.Query().Get("playbook")).To(Equal(playbook))
				Expect(r.URL.Query().Get("created__gte")).To(Equal(created.UTC().Format(time.RFC3339Nano)))
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer installed-token"))
				Expect(json.NewEncoder(w).Encode(map[string]any{"results": jobs, "next": nil})).To(Succeed())
			})}

			By("selecting attempts by UID, namespace, tenant and playbook")

			resource := &metav1.ObjectMeta{
				UID:               "uid",
				CreationTimestamp: created,
				Namespace:         "osac",
				Annotations:       map[string]string{harness.TenantAnnotation: "tenant"},
			}

			observed, err := installation.JobsForResource(ctx, resource, playbook)
			if wantError != "" {
				Expect(err).To(MatchError(ContainSubstring(wantError)))
				return
			}

			Expect(err).NotTo(HaveOccurred())

			var ids []int

			for _, job := range observed {
				ids = append(ids, job.ID)
			}

			Expect(ids).To(Equal(wantIDs))
		},
		Entry("retains every attempt in submission order", func() []aap.Job {
			return []aap.Job{
				testAWXJob(5, "pending", "uid", "osac", "tenant", playbook),
				testAWXJob(4, "successful", "uid", "osac", "tenant", playbook),
				testAWXJob(3, "canceled", "uid", "osac", "tenant", playbook),
				testAWXJob(2, "error", "uid", "osac", "tenant", playbook),
				testAWXJob(1, "failed", "uid", "osac", "tenant", playbook),
			}
		}, []int{1, 2, 3, 4, 5}, ""),
		Entry("excludes replacement resources and other playbooks", func() []aap.Job {
			return []aap.Job{
				testAWXJob(1, "successful", "replacement", "osac", "tenant", playbook),
				testAWXJob(2, "successful", "uid", "osac", "tenant", "delete.yml"),
				testAWXJob(3, "successful", "uid", "osac", "tenant", playbook),
			}
		}, []int{3}, ""),
		Entry("rejects another namespace", func() []aap.Job {
			return []aap.Job{testAWXJob(1, "successful", "uid", "other", "tenant", playbook)}
		}, []int(nil), "inconsistent resource scope"),
		Entry("rejects another tenant", func() []aap.Job {
			return []aap.Job{testAWXJob(1, "successful", "uid", "osac", "other", playbook)}
		}, []int(nil), "inconsistent resource scope"),
		Entry("rejects unreadable resource metadata", func() []aap.Job {
			return []aap.Job{{ID: 1, Playbook: playbook, ExtraVars: "{"}}
		}, []int(nil), "unreadable resource metadata"),
		Entry("returns no attempts when nothing matches", func() []aap.Job { return nil }, []int(nil), ""),
	)

	DescribeTable("rejects missing scope before contacting AWX", func(ctx SpecContext, missing int) {
		inputs := []string{"uid", "osac", "tenant", "create.yml"}
		if missing < len(inputs) {
			inputs[missing] = ""
		}

		installation := &Installation{}

		resource := &metav1.ObjectMeta{
			UID:               types.UID(inputs[0]),
			CreationTimestamp: metav1.Now(),
			Namespace:         inputs[1],
			Annotations:       map[string]string{harness.TenantAnnotation: inputs[2]},
		}
		if missing == len(inputs) {
			resource.CreationTimestamp = metav1.Time{}
		}

		_, err := installation.JobsForResource(ctx, resource, inputs[3])
		Expect(err).To(HaveOccurred())
	},
		Entry("UID", 0),
		Entry("namespace", 1),
		Entry("tenant", 2),
		Entry("playbook", 3),
		Entry("creation time", 4),
	)

	DescribeTable(
		"checks whether automation in a namespace is idle",
		func(ctx SpecContext, status, scope string, blocked bool) {
			installation := &Installation{Client: testAWXClient(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Query().Get("not__status__in")).To(Equal("successful,failed,error,canceled"))
				Expect(
					json.NewEncoder(w).
						Encode(map[string]any{"results": []aap.Job{{ID: 11, Status: status, ExtraVars: scope}}, "next": nil}),
				).To(Succeed())
			})}

			err := installation.CheckIdle(ctx, "hub", "playbook.yml")
			if blocked {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		},
		Entry(
			"blocks active jobs",
			"running",
			`{"osac_job_vars":{"resource":{"metadata":{"uid":"order-uid","namespace":"hub"}}}}`,
			true,
		),
		Entry("blocks jobs with unreadable scope", "pending", `{}`, true),
		Entry(
			"ignores another namespace",
			"running",
			`{"osac_job_vars":{"resource":{"metadata":{"uid":"order-uid","namespace":"other"}}}}`,
			false,
		),
		Entry("accepts terminal jobs", "failed", `{}`, false),
	)

	DescribeTable("checks only this resource's outstanding attempts", func(ctx SpecContext, state string) {
		own := &metav1.ObjectMeta{
			UID:               "case-a",
			CreationTimestamp: metav1.Now(),
			Namespace:         "osac",
			Annotations:       map[string]string{"osac.openshift.io/tenant": "users"},
		}
		jobs := []aap.Job{
			testAWXJob(1, state, "case-a", "osac", "users", playbook),
			testAWXJob(2, "running", "case-b", "osac", "users", playbook),
		}
		installation := &Installation{Client: testAWXClient(func(w http.ResponseWriter, _ *http.Request) {
			Expect(json.NewEncoder(w).Encode(map[string]any{"results": jobs, "next": nil})).To(Succeed())
		})}

		err := installation.CheckIdleForResource(ctx, own, playbook)
		if state == "pending" || state == "running" {
			Expect(err).To(HaveOccurred())
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
	},
		Entry("pending", "pending"),
		Entry("running", "running"),
		Entry("successful", "successful"),
		Entry("failed", "failed"),
		Entry("error", "error"),
		Entry("canceled", "canceled"),
	)

	DescribeTable(
		"requires the installed project, image and both execution groups",
		func(ctx SpecContext, missing, serviceAccount, worker string) {
			const (
				commit = "0123456789012345678901234567890123456789"
				image  = "localhost/osac-aap:it"
			)

			By("serving the configured AWX identities locally")

			client := testAWXClient(func(w http.ResponseWriter, r *http.Request) {
				var result any

				switch r.URL.Path {
				case "/api/v2/projects/":
					result = aap.Project{
						ID:          8,
						SCMURL:      "https://example.com/osac.git",
						SCMBranch:   commit,
						SCMRevision: commit,
						SCMType:     "git",
						Status:      "successful",
					}
				case "/api/v2/execution_environments/":
					result = aap.ExecutionEnvironment{ID: 4, Image: image, Pull: "never"}
				case "/api/v2/instance_groups/":
					name := r.URL.Query().Get("name")
					if name == missing {
						Expect(json.NewEncoder(w).Encode(map[string]any{"results": []any{}})).To(Succeed())
						return
					}

					result = aap.InstanceGroup{
						ID:               map[string]int{"osac-bare-metal-fulfillment-ig": 3, "osac-cluster-fulfillment-ig": 5}[name],
						IsContainerGroup: true,
						PodSpecOverride: `{"spec":{"serviceAccountName":"` + serviceAccount + `","containers":[` +
							`{"name":"` + worker + `","image":"example.invalid/template-default"}]}}`,
					}
				default:
					Fail("unexpected configuration endpoint: " + r.URL.Path)
				}

				Expect(json.NewEncoder(w).Encode(map[string]any{"results": []any{result}})).To(Succeed())
			})
			environment := &harness.Environment{
				Namespace: "osac",
				Kube: kubefake.NewClientset(&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "caas-it-configuration", Namespace: "osac"},
					Data: map[string]string{
						"projectURL":    "https://example.com/osac.git",
						"projectCommit": commit,
						"eeImage":       image,
						"eePullPolicy":  "Never",
					},
				}),
			}

			By("resolving the selected identities from the fake installation")

			selected, err := checkConfiguration(ctx, environment, client)
			if missing != "" {
				Expect(err).To(MatchError("missing production container group " + missing))
				return
			}

			if serviceAccount != "osac-sa" || worker != "worker" {
				Expect(err).To(MatchError(ContainSubstring("required worker layout or service account")))
				return
			}

			Expect(err).NotTo(HaveOccurred())
			Expect(selected.projectID).To(Equal(8))
			Expect(selected.executionEnvironmentID).To(Equal(4))
			Expect(selected.instanceGroupIDs).To(Equal(map[string]int{
				"osac-bare-metal-fulfillment-ig": 3,
				"osac-cluster-fulfillment-ig":    5,
			}))
		},
		Entry("uses the assigned EE independently of template image defaults", "", "osac-sa", "worker"),
		Entry("rejects a missing bare-metal group", "osac-bare-metal-fulfillment-ig", "osac-sa", "worker"),
		Entry("rejects a missing cluster group", "osac-cluster-fulfillment-ig", "osac-sa", "worker"),
		Entry("rejects another service account", "", "other-sa", "worker"),
		Entry("rejects a missing worker container", "", "osac-sa", "other"),
	)
})

// testAWXClient serves one spec's HTTP fixtures through the production AAP client.
func testAWXClient(handler http.HandlerFunc) *aap.Client {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()

		handler(w, r)
	}))
	DeferCleanup(server.Close)

	return aap.NewClient(server.URL+"/api", "installed-token", false)
}

// testAWXJob creates a submitted resource snapshot with a fixed name and explicit scope.
func testAWXJob(id int, status, uid, namespace, tenant, playbook string) aap.Job {
	GinkgoHelper()

	data, err := json.Marshal(
		map[string]any{"osac_job_vars": map[string]any{"resource": map[string]any{"metadata": metav1.ObjectMeta{
			Name:        "instance",
			Namespace:   namespace,
			UID:         types.UID(uid),
			Annotations: map[string]string{"osac.openshift.io/tenant": tenant},
		}}}},
	)
	Expect(err).NotTo(HaveOccurred())

	return aap.Job{ID: id, Status: status, Playbook: playbook, ExtraVars: string(data)}
}
