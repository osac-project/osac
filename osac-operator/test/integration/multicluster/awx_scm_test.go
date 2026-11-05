package multicluster

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func scmTestGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture git %s: %v\n%s", arguments[0], err, output)
	}
	return strings.TrimSpace(string(output))
}

func scmTestRepository(t *testing.T) (string, string) {
	t.Helper()
	directory := t.TempDir()
	scmTestGit(t, directory, "init", "--initial-branch=feature")
	scmTestGit(t, directory, "-c", "user.name=SCM Test", "-c", "user.email=scm@example.invalid",
		"-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "initial")
	revision := scmTestGit(t, directory, "rev-parse", "HEAD")
	scmTestGit(t, directory, "remote", "add", "origin", "https://github.com/example/fork.git")
	return directory, revision
}

func scmTestEnvironment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestAWXSCMLocalFork(t *testing.T) {
	directory, revision := scmTestRepository(t)
	selection, err := resolveAWXSCM(context.Background(), directory, scmTestEnvironment(nil))
	if err != nil {
		t.Fatal(err)
	}
	if selection.URL != "https://github.com/example/fork.git" || selection.Revision != revision ||
		selection.Refspec != "+refs/heads/feature:refs/remotes/origin/osac-test" {
		t.Fatalf("local fork must fetch its branch and pin local HEAD: %+v", selection)
	}
}

func TestAWXSCMDetachedRequiresRef(t *testing.T) {
	directory, revision := scmTestRepository(t)
	scmTestGit(t, directory, "checkout", "--detach", revision)
	_, err := resolveAWXSCM(context.Background(), directory, scmTestEnvironment(nil))
	if err == nil || !strings.Contains(err.Error(), "OSAC_TEST_AWX_SCM_REF") {
		t.Fatalf("detached checkout must explain the explicit fetch-ref requirement: %v", err)
	}
}

func TestAWXSCMPRPinsEventHead(t *testing.T) {
	directory, revision := scmTestRepository(t)
	event := filepath.Join(t.TempDir(), "event.json")
	body := `{"number":1565,"pull_request":{"head":{"sha":"` + revision +
		`"},"base":{"repo":{"clone_url":"https://github.com/example/upstream.git"}}}}`
	if err := os.WriteFile(event, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	scmTestGit(t, directory, "-c", "user.name=SCM Test", "-c", "user.email=scm@example.invalid",
		"-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "synthetic merge")
	selection, err := resolveAWXSCM(context.Background(), directory, scmTestEnvironment(map[string]string{
		"GITHUB_REF": "refs/pull/1565/merge", "GITHUB_EVENT_PATH": event,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if selection.URL != "https://github.com/example/upstream.git" || selection.Revision != revision ||
		selection.Refspec != "+refs/pull/1565/head:refs/remotes/origin/osac-test" {
		t.Fatalf("must pin event head, not checkout merge or moving PR alias: %+v", selection)
	}
	withoutCheckout, err := resolveAWXSCM(context.Background(), filepath.Join(t.TempDir(), "no-checkout"),
		scmTestEnvironment(map[string]string{
			"GITHUB_REF": "refs/pull/1565/head", "GITHUB_EVENT_PATH": event,
		}))
	if err != nil || withoutCheckout != selection {
		t.Fatalf("PR selection must not require a local merge commit or its parents: %+v, %v", withoutCheckout, err)
	}
}

func TestAWXSCMTrackingAndOverrides(t *testing.T) {
	directory, revision := scmTestRepository(t)
	scmTestGit(t, directory, "remote", "add", "contributor", "git@github.com:example/tracked.git")
	scmTestGit(t, directory, "config", "branch.feature.remote", "contributor")
	scmTestGit(t, directory, "config", "branch.feature.merge", "refs/heads/published")
	selection, err := resolveAWXSCM(context.Background(), directory, scmTestEnvironment(nil))
	if err != nil || selection.URL != "https://github.com/example/tracked.git" ||
		selection.Refspec != "+refs/heads/published:refs/remotes/origin/osac-test" {
		t.Fatalf("tracking source not selected: %+v, %v", selection, err)
	}
	scmTestGit(t, directory, "checkout", "--detach", revision)
	selection, err = resolveAWXSCM(context.Background(), directory, scmTestEnvironment(map[string]string{
		"OSAC_TEST_AWX_SCM_URL": "ssh://git@github.com/example/override.git",
		"OSAC_TEST_AWX_SCM_REF": "refs/tags/published",
	}))
	if err != nil || selection.URL != "https://github.com/example/override.git" ||
		selection.Revision != revision || selection.Refspec != "+refs/tags/published:refs/remotes/origin/osac-test" {
		t.Fatalf("detached overrides must retain HEAD: %+v, %v", selection, err)
	}
}

func TestAWXSCMUsesFirstFetchURL(t *testing.T) {
	directory, revision := scmTestRepository(t)
	scmTestGit(t, directory, "config", "--add", "remote.origin.url", "https://github.com/example/secondary.git")
	scmTestGit(t, directory, "config", "remote.origin.pushurl", "https://github.com/example/push-only.git")
	selection, err := resolveAWXSCM(context.Background(), directory, scmTestEnvironment(nil))
	if err != nil || selection.URL != "https://github.com/example/fork.git" || selection.Revision != revision {
		t.Fatalf("must use Git's first fetch URL, not the last URL or push URL: %+v, %v", selection, err)
	}
}

func TestAWXSCMRejectsUnsafeConfiguration(t *testing.T) {
	directory, _ := scmTestRepository(t)
	for _, source := range []string{
		"https://user:synthetic-sensitive-value@github.com/example/repo.git",
		"https://github.com/example/repo.git?token=synthetic-sensitive-value",
		"/tmp/private-repo", "http://localhost/repo", "ssh://git@other.invalid/example/repo",
		"https://github.com/example/repo.git\nsynthetic-sensitive-value",
	} {
		_, err := resolveAWXSCM(context.Background(), directory, scmTestEnvironment(map[string]string{
			"OSAC_TEST_AWX_SCM_URL": source,
		}))
		if err == nil || strings.Contains(err.Error(), "synthetic-sensitive-value") {
			t.Fatalf("unsupported source must fail without echoing it: %v", err)
		}
	}
	for _, ref := range []string{"main", "refs/heads/bad:ref", "refs/heads/bad\nref", "refs/heads/*"} {
		_, err := resolveAWXSCM(context.Background(), directory, scmTestEnvironment(map[string]string{
			"OSAC_TEST_AWX_SCM_REF": ref,
		}))
		if err == nil {
			t.Fatal("invalid ref accepted")
		}
	}
	scmTestGit(t, directory, "remote", "remove", "origin")
	if _, err := resolveAWXSCM(context.Background(), directory, scmTestEnvironment(nil)); err == nil {
		t.Fatal("missing source must require an override")
	}
}

func TestAWXSCMRejectsInvalidPREvents(t *testing.T) {
	for _, body := range []string{
		`not JSON synthetic-sensitive-value`,
		`{"number":7,"pull_request":{"head":{"sha":"bad"}}}`,
		`{"number":1565,"pull_request":{"head":{"sha":"bad"}}}`,
	} {
		event := filepath.Join(t.TempDir(), "event.json")
		if err := os.WriteFile(event, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := resolveAWXSCM(context.Background(), "", scmTestEnvironment(map[string]string{
			"GITHUB_REF": "refs/pull/1565/merge", "GITHUB_EVENT_PATH": event,
		}))
		if err == nil || strings.Contains(err.Error(), "synthetic-sensitive-value") {
			t.Fatalf("invalid event must fail safely: %v", err)
		}
	}
	for _, values := range []map[string]string{
		{"GITHUB_REF": "refs/pull/1565/merge"},
		{"GITHUB_EVENT_NAME": "pull_request", "GITHUB_REF": "refs/heads/main"},
		{"GITHUB_REF": "refs/pull/1565/head", "OSAC_TEST_AWX_SCM_REF": "refs/heads/main"},
	} {
		if _, err := resolveAWXSCM(context.Background(), "", scmTestEnvironment(values)); err == nil {
			t.Fatal("incomplete PR configuration must not fall back to local or latest head")
		}
	}
}

func TestAWXSCMRealFetchContract(t *testing.T) {
	directory, base := scmTestRepository(t)
	upstream := filepath.Join(t.TempDir(), "upstream.git")
	fork := filepath.Join(t.TempDir(), "fork.git")
	scmTestGit(t, directory, "init", "--bare", upstream)
	scmTestGit(t, directory, "init", "--bare", fork)
	scmTestGit(t, directory, "push", upstream, "HEAD:refs/heads/main")
	scmTestGit(t, upstream, "symbolic-ref", "HEAD", "refs/heads/main")
	scmTestGit(t, directory, "-c", "user.name=SCM Test", "-c", "user.email=scm@example.invalid",
		"-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fork-only commit")
	intended := scmTestGit(t, directory, "rev-parse", "HEAD")
	scmTestGit(t, directory, "-c", "user.name=SCM Test", "-c", "user.email=scm@example.invalid",
		"-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "newer branch tip")
	scmTestGit(t, directory, "push", fork, "HEAD:refs/heads/feature")
	scmTestGit(t, directory, "push", upstream, "HEAD:refs/pull/1565/head")
	clone := filepath.Join(t.TempDir(), "ordinary-clone")
	scmTestGit(t, directory, "clone", "--no-local", upstream, clone)
	scmTestGit(t, clone, "checkout", "--force", base)
	probe := exec.Command("git", "-C", clone, "cat-file", "-e", intended+"^{tree}")
	if err := probe.Run(); err == nil {
		t.Fatal("ordinary clone unexpectedly contains the intended commit tree")
	}
	command := exec.Command("git", "-C", clone, "checkout", "--force", intended)
	_, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("ordinary upstream clone unexpectedly supplied a commit reachable only through a PR ref")
	}
	for _, test := range []struct {
		name, source, ref, revision string
		wantError                   bool
	}{
		{"fork-ancestor-not-tip", fork, "refs/heads/feature", intended, false},
		{"fork-base-ancestor", fork, "refs/heads/feature", base, false},
		{"PR-ancestor-not-latest-head", upstream, "refs/pull/1565/head", intended, false},
		{"wrong-source", upstream, "refs/heads/main", intended, true},
		{"missing-branch", fork, "refs/heads/missing", intended, true},
		{"missing-sha", fork, "refs/heads/feature", strings.Repeat("a", 40), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual, err := verifyAWXSCM(context.Background(), awxSCMSelection{
				URL: test.source, Revision: test.revision,
				Refspec: "+" + test.ref + ":refs/remotes/origin/osac-test",
			})
			if (err != nil) != test.wantError {
				t.Fatalf("fetch contract error=%v, wantError=%v", err, test.wantError)
			}
			if !test.wantError && actual != test.revision {
				t.Fatalf("checked out %s instead of intended %s", actual, test.revision)
			}
			if !test.wantError {
				awxClone := filepath.Join(t.TempDir(), "awx-clone")
				scmTestGit(t, directory, "clone", "--no-local", test.source, awxClone)
				scmTestGit(t, awxClone, "fetch", "origin", "+"+test.ref+":refs/remotes/origin/osac-test")
				scmTestGit(t, awxClone, "checkout", "--force", test.revision)
				if checkedOut := scmTestGit(t, awxClone, "rev-parse", "HEAD"); checkedOut != actual {
					t.Fatalf("AWX clone/fetch contract checked out %s, preflight checked out %s", checkedOut, actual)
				}
			}
		})
	}
	scmTestGit(t, directory, "-c", "user.name=SCM Test", "-c", "user.email=scm@example.invalid",
		"-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "unpublished")
	unpublished := scmTestGit(t, directory, "rev-parse", "HEAD")
	_, err = verifyAWXSCM(context.Background(), awxSCMSelection{
		URL: fork, Revision: unpublished, Refspec: "+refs/heads/feature:refs/remotes/origin/osac-test",
	})
	if err == nil || !strings.Contains(err.Error(), "push that commit") {
		t.Fatalf("unpublished commit needs actionable error, not fallback: %v", err)
	}
	scmTestGit(t, directory, "push", upstream, "+"+base+":refs/pull/1565/head")
	if _, err := verifyAWXSCM(context.Background(), awxSCMSelection{
		URL: upstream, Revision: intended, Refspec: "+refs/pull/1565/head:refs/remotes/origin/osac-test",
	}); err == nil {
		t.Fatal("rewritten PR ref must not silently replace the event's intended commit")
	}
}

func TestAWXSCMPayloadAndSyncRevision(t *testing.T) {
	revision := strings.Repeat("a", 40)
	selection := awxSCMSelection{URL: "https://github.com/example/fork.git", Revision: revision,
		Refspec: "+refs/heads/feature:refs/remotes/origin/osac-test"}
	payload, err := selection.payload()
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 4 || decoded["scm_url"] != selection.URL || decoded["scm_branch"] != revision ||
		decoded["scm_refspec"] != selection.Refspec || decoded["scm_delete_on_update"] != true {
		t.Fatalf("incomplete SCM patch: %s", payload)
	}
	for _, status := range []string{
		"new", "pending", "waiting", "running", "successful", "failed", "error", "canceled", "unknown",
	} {
		t.Run(status, func(t *testing.T) {
			output := fmt.Sprintf(`{"id":42,"status":%q,"scm_revision":%q}`, status, revision)
			complete, err := awxProjectSyncComplete([]byte(output), awxProjectSync{ID: 42, Revision: revision})
			wantError := status == "failed" || status == "error" || status == "canceled" || status == "unknown"
			if complete != (status == "successful") || (err != nil) != wantError {
				t.Fatalf("status handling: complete=%v, error=%v", complete, err)
			}
		})
	}
	for _, output := range []string{
		`{"id":42,"status":"successful","scm_revision":"synthetic-sensitive-value"}`,
		`{"id":41,"status":"successful","scm_revision":"` + revision + `"}`,
		`{"id":42,"status":"successful"}`, `not JSON synthetic-sensitive-value`,
	} {
		complete, err := awxProjectSyncComplete([]byte(output), awxProjectSync{ID: 42, Revision: revision})
		if complete || err == nil || strings.Contains(err.Error(), "synthetic-sensitive-value") {
			t.Fatalf("must reject mismatched or malformed update without echoing response: %v", err)
		}
	}
}

func TestAWXSCMFetchErrorsDoNotEchoGitOutput(t *testing.T) {
	directory := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = init ]; then exit 0; fi\n" +
		"echo synthetic-sensitive-value >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(directory, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+":"+os.Getenv("PATH"))
	_, err := verifyAWXSCM(context.Background(), awxSCMSelection{})
	if err == nil || strings.Contains(err.Error(), "synthetic-sensitive-value") {
		t.Fatalf("git diagnostics must not expose arbitrary output: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := verifyAWXSCM(ctx, awxSCMSelection{}); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("preflight must respect its context budget: %v", err)
	}
}
