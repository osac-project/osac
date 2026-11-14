package aap_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/osac-operator/pkg/aap"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

var _ = Describe("Configuration reads", func() {
	const name = "osac & integration"
	ctx := context.Background()

	DescribeTable("reads named configuration through the authenticated client", func(endpoint, body string, read func(*aap.Client)) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			Expect(r.Method).To(Equal(http.MethodGet))
			Expect(r.URL.Path).To(Equal("/api/v2/" + endpoint + "/"))
			Expect(r.URL.Query().Get("name")).To(Equal(name))
			Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))
			_, err := w.Write([]byte(body))
			Expect(err).NotTo(HaveOccurred())
		}))
		defer server.Close()
		read(aap.NewClient(server.URL+"/api", "test-token", false))
	},
		Entry("projects", "projects", `{"results":[{"id":8,"scm_url":"https://example.com/osac.git","scm_branch":"commit","scm_revision":"commit","scm_type":"git","status":"successful"}]}`, func(client *aap.Client) {
			projects, err := client.ListProjects(ctx, name)
			Expect(err).NotTo(HaveOccurred())
			Expect(projects).To(Equal([]aap.Project{{ID: 8, SCMURL: "https://example.com/osac.git", SCMBranch: "commit", SCMRevision: "commit", SCMType: "git", Status: "successful"}}))
		}),
		Entry("execution environments", "execution_environments", `{"results":[{"id":4,"image":"localhost/osac-aap:it","pull":"never"}]}`, func(client *aap.Client) {
			environments, err := client.ListExecutionEnvironments(ctx, name)
			Expect(err).NotTo(HaveOccurred())
			Expect(environments).To(Equal([]aap.ExecutionEnvironment{{ID: 4, Image: "localhost/osac-aap:it", Pull: "never"}}))
		}),
		Entry("instance groups", "instance_groups", `{"results":[{"id":3,"is_container_group":true,"pod_spec_override":"spec: {}"}]}`, func(client *aap.Client) {
			groups, err := client.ListInstanceGroups(ctx, name)
			Expect(err).NotTo(HaveOccurred())
			Expect(groups).To(Equal([]aap.InstanceGroup{{ID: 3, IsContainerGroup: true, PodSpecOverride: "spec: {}"}}))
		}),
	)

	DescribeTable("decodes empty results and rejects malformed configuration", func(body string, valid bool) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			defer GinkgoRecover()
			_, err := w.Write([]byte(body))
			Expect(err).NotTo(HaveOccurred())
		}))
		defer server.Close()
		client := aap.NewClient(server.URL, "test-token", false)
		projects, err := client.ListProjects(ctx, name)
		if valid {
			Expect(err).NotTo(HaveOccurred())
			Expect(projects).To(BeEmpty())
		} else {
			Expect(err).To(HaveOccurred())
		}
	},
		Entry("empty results", `{"results":[]}`, true),
		Entry("invalid JSON", "{", false),
	)

	It("keeps private configuration responses out of errors and logs", func() {
		const private = "test-only-private-pod-override"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			defer GinkgoRecover()
			w.WriteHeader(http.StatusInternalServerError)
			_, err := w.Write([]byte(private))
			Expect(err).NotTo(HaveOccurred())
		}))
		defer server.Close()
		var output bytes.Buffer
		logger := logr.FromSlogHandler(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
		client := aap.NewClient(server.URL, "test-token", false)
		_, err := client.ListInstanceGroups(ctrllog.IntoContext(ctx, logger), name)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring(private))
		Expect(output.String()).To(ContainSubstring("status=500"))
		Expect(output.String()).NotTo(ContainSubstring(private))
	})
})
