// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package harness

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

var _ = Describe("Group discovery fixtures", func() {
	It("preserves other groups and metadata during concurrent key changes", func(ctx SpecContext) {
		kube := kubefake.NewClientset()
		groups := []string{"load-001", "load-100"}
		created, err := CreateIgnition(ctx, kube, "hub", groups)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Data).To(HaveLen(2))
		Expect(created.Data).To(HaveKeyWithValue("load-001.json", `{"ignition":{"version":"3.2.0"}}`))
		Expect(created.Data).To(HaveKeyWithValue("load-100.json", `{"ignition":{"version":"3.2.0"}}`))
		created.Labels = map[string]string{"unrelated": "preserved"}
		created.Annotations = map[string]string{"unrelated": "preserved"}
		created.Data["unrelated"] = "preserved"
		_, err = kube.CoreV1().ConfigMaps("hub").Update(ctx, created, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("changing independent group keys concurrently")

		results := make(chan error, len(groups))
		for _, group := range groups {
			go func() {
				fixture := &Ignition{Kube: kube, Namespace: "hub", Group: group}
				results <- fixture.Set(ctx, []byte("{invalid-"+group))
			}()
		}

		for range groups {
			Expect(<-results).ToNot(HaveOccurred())
		}

		for _, group := range groups {
			fixture := &Ignition{Kube: kube, Namespace: "hub", Group: group}
			content, err := fixture.Stored(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(Equal("{invalid-" + group))
		}

		current, err := kube.CoreV1().ConfigMaps("hub").Get(ctx, created.Name, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(current.Labels).To(Equal(created.Labels))
		Expect(current.Annotations).To(Equal(created.Annotations))
		Expect(current.Data).To(HaveKeyWithValue("unrelated", "preserved"))
	})
})

var _ = Describe("Waiting for served ignition", func() {
	DescribeTable("waits for the expected HTTP content",
		func(ctx SpecContext, initialStatus int, initial, expected string, wantRequests int32) {
			var requests atomic.Int32

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				defer GinkgoRecover()

				body := expected

				if requests.Add(1) == 1 {
					w.WriteHeader(initialStatus)

					body = initial
				}

				_, err := w.Write([]byte(body))
				Expect(err).NotTo(HaveOccurred())
			}))
			DeferCleanup(server.Close)
			kube, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
			Expect(err).NotTo(HaveOccurred())

			fixture := &Ignition{Kube: kube, Namespace: "hub", Group: "p1"}
			Expect(fixture.WaitForContent(ctx, []byte(expected))).To(Succeed())
			Expect(requests.Load()).To(Equal(wantRequests))
		},
		Entry("matching content", http.StatusOK, `{"ignition":{"version":"3.2.0"}}`,
			`{"ignition":{"version":"3.2.0"}}`, int32(1)),
		Entry("matching malformed content", http.StatusOK, "{invalid", "{invalid", int32(1)),
		Entry("delayed propagation", http.StatusOK, "old content", "expected content", int32(2)),
		Entry("transient missing file", http.StatusNotFound, "private response", "expected content", int32(2)),
	)

	DescribeTable("bounds unsuccessful waits without exposing content",
		func(ctx SpecContext, status int) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				defer GinkgoRecover()

				w.WriteHeader(status)
				_, err := w.Write([]byte("private response"))
				Expect(err).NotTo(HaveOccurred())
			}))
			DeferCleanup(server.Close)
			kube, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
			Expect(err).NotTo(HaveOccurred())

			fixture := &Ignition{Kube: kube, Namespace: "hub", Group: "p1"}

			bounded, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
			defer cancel()

			err = fixture.WaitForContent(bounded, []byte("private expected content"))
			Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
			Expect(err.Error()).NotTo(ContainSubstring("private response"))
			Expect(err.Error()).NotTo(ContainSubstring("private expected content"))

			if status != http.StatusOK {
				Expect(err.Error()).To(ContainSubstring("read discovery ignition through service proxy"))
			}
		},
		Entry("differing content", http.StatusOK),
		Entry("persistent HTTP error", http.StatusNotFound),
	)

	It("cancels an in-flight HTTP read", func(ctx SpecContext) {
		started := make(chan struct{})
		stopped := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
			close(stopped)
		}))
		DeferCleanup(server.Close)
		kube, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
		Expect(err).NotTo(HaveOccurred())

		fixture := &Ignition{Kube: kube, Namespace: "hub", Group: "p1"}
		waiting, cancel := context.WithCancel(ctx)
		DeferCleanup(cancel)

		result := make(chan error, 1)

		go func() { result <- fixture.WaitForContent(waiting, []byte("expected")) }()

		Eventually(started).WithContext(ctx).WithTimeout(time.Second).Should(BeClosed())
		cancel()

		var failure error

		Eventually(result).WithContext(ctx).WithTimeout(time.Second).Should(Receive(&failure))
		Expect(errors.Is(failure, context.Canceled)).To(BeTrue())
		Eventually(stopped).WithContext(ctx).WithTimeout(time.Second).Should(BeClosed())
	})
})

var _ = DescribeTable("Reading served ignition",
	func(ctx SpecContext, body string, status int) {
		By("serving the selected response through a local Kubernetes proxy endpoint")

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()

			Expect(r.URL.Path).To(Equal("/api/v1/namespaces/hub/services/http:caas-it-ignition:80/proxy/p1.json"))
			w.WriteHeader(status)
			_, err := w.Write([]byte(body))
			Expect(err).NotTo(HaveOccurred())
		}))
		DeferCleanup(server.Close)
		client, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
		Expect(err).NotTo(HaveOccurred())

		fixture := &Ignition{Kube: client, Namespace: "hub", Group: "p1"}
		Expect(fixture.URL()).To(Equal("http://caas-it-ignition.hub.svc/p1.json"))

		By("reading the served bytes without interpreting their JSON")

		got, err := fixture.Read(ctx)
		if status != http.StatusOK {
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring(body), "failed responses must keep their contents private")
			return
		}

		Expect(err).NotTo(HaveOccurred())
		Expect(string(got)).To(Equal(body))
	},
	Entry("returns valid ignition unchanged", `{"ignition":{"version":"3.2.0"}}`, http.StatusOK),
	Entry("returns malformed ignition unchanged", "{invalid", http.StatusOK),
	Entry("reports HTTP errors without exposing response data", "private data", http.StatusServiceUnavailable),
)
