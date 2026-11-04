// Package multicluster provides helpers for split-cluster integration tests.
package multicluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type awxSCMSelection struct {
	URL      string
	Revision string
	Refspec  string
}

func resolveAWXSCM(ctx context.Context, directory string, getenv func(string) string) (awxSCMSelection, error) {
	githubRef := getenv("GITHUB_REF")
	if strings.HasPrefix(githubRef, "refs/pull/") || strings.HasPrefix(getenv("GITHUB_EVENT_NAME"), "pull_request") {
		return resolvePRAWXSCM(ctx, githubRef, getenv)
	}
	revision, err := scmGit(ctx, directory, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return awxSCMSelection{}, fmt.Errorf("resolve local HEAD commit: %w", err)
	}
	branch, branchErr := scmGit(ctx, directory, "symbolic-ref", "--quiet", "--short", "HEAD")
	ref := getenv("OSAC_TEST_AWX_SCM_REF")
	if branchErr != nil && ref == "" {
		return awxSCMSelection{}, fmt.Errorf("detached HEAD requires OSAC_TEST_AWX_SCM_REF " +
			"pointing to a published ref containing HEAD")
	}
	remote := "origin"
	if branchErr == nil {
		if configured, configErr := scmGit(ctx, directory, "config", "--get", "branch."+branch+".remote"); configErr == nil {
			remote = configured
		}
		if ref == "" {
			ref, _ = scmGit(ctx, directory, "config", "--get", "branch."+branch+".merge")
			if ref == "" {
				ref = "refs/heads/" + branch
			}
		}
	}
	source := getenv("OSAC_TEST_AWX_SCM_URL")
	if source == "" {
		source, err = scmGit(ctx, directory, "config", "--null", "--get-all", "remote."+remote+".url")
		if err != nil {
			return awxSCMSelection{}, fmt.Errorf("no usable Git remote; set OSAC_TEST_AWX_SCM_URL to a public GitHub repository")
		}
		source = strings.Split(source, "\x00")[0]
	}
	return newAWXSCMSelection(ctx, directory, source, revision, ref)
}

func resolvePRAWXSCM(ctx context.Context, githubRef string, getenv func(string) string) (awxSCMSelection, error) {
	match := regexp.MustCompile(`^refs/pull/([1-9][0-9]*)/(head|merge)$`).FindStringSubmatch(githubRef)
	if len(match) == 0 {
		return awxSCMSelection{}, fmt.Errorf("PR SCM selection requires GITHUB_REF=refs/pull/<number>/{head,merge}")
	}
	if getenv("OSAC_TEST_AWX_SCM_URL") != "" || getenv("OSAC_TEST_AWX_SCM_REF") != "" {
		return awxSCMSelection{}, fmt.Errorf("OSAC_TEST_AWX_SCM_URL and OSAC_TEST_AWX_SCM_REF " +
			"are local-only; unset them for PR jobs")
	}
	file, err := os.Open(getenv("GITHUB_EVENT_PATH"))
	if err != nil {
		return awxSCMSelection{}, fmt.Errorf("PR pinning requires a readable GITHUB_EVENT_PATH with pull_request.head.sha")
	}
	defer func() { _ = file.Close() }()
	var event struct {
		Number      int `json:"number"`
		PullRequest struct {
			Head struct {
				SHA string `json:"sha"`
			} `json:"head"`
			Base struct {
				Repo struct {
					CloneURL string `json:"clone_url"`
				} `json:"repo"`
			} `json:"base"`
		} `json:"pull_request"`
	}
	if err := json.NewDecoder(io.LimitReader(file, 1024*1024)).Decode(&event); err != nil {
		return awxSCMSelection{}, fmt.Errorf("cannot decode PR SCM event; " +
			"require pull_request.head.sha and base.repo.clone_url")
	}
	if strconv.Itoa(event.Number) != match[1] {
		return awxSCMSelection{}, fmt.Errorf("PR event number does not match GITHUB_REF")
	}
	return newAWXSCMSelection(ctx, "", event.PullRequest.Base.Repo.CloneURL,
		event.PullRequest.Head.SHA, "refs/pull/"+match[1]+"/head")
}

func newAWXSCMSelection(ctx context.Context, directory, source, revision, ref string) (awxSCMSelection, error) {
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(revision) {
		return awxSCMSelection{}, fmt.Errorf("SCM pin must be a full Git commit SHA; PR jobs require pull_request.head.sha")
	}
	if !regexp.MustCompile(`^refs/(heads/.+|tags/.+|pull/[1-9][0-9]*/head)$`).MatchString(ref) {
		return awxSCMSelection{}, fmt.Errorf("OSAC_TEST_AWX_SCM_REF must be a full published " +
			"refs/heads, refs/tags, or refs/pull ref")
	}
	if _, err := scmGit(ctx, directory, "check-ref-format", ref); err != nil {
		return awxSCMSelection{}, fmt.Errorf("invalid SCM fetch ref; set OSAC_TEST_AWX_SCM_REF to a full published ref")
	}
	publicURL, err := publicAWXSCMURL(source)
	if err != nil {
		return awxSCMSelection{}, err
	}
	return awxSCMSelection{URL: publicURL, Revision: revision,
		Refspec: "+" + ref + ":refs/remotes/origin/osac-test"}, nil
}

func publicAWXSCMURL(source string) (string, error) {
	if strings.HasPrefix(source, "git@github.com:") {
		source = "https://github.com/" + strings.TrimPrefix(source, "git@github.com:")
	}
	parsed, err := url.Parse(source)
	if err == nil && parsed.Scheme == "ssh" && parsed.Host == "github.com" &&
		parsed.User != nil && parsed.User.String() == "git" {
		parsed.Scheme, parsed.User = "https", nil
	}
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" ||
		!regexp.MustCompile(`^/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(parsed.Path) {
		return "", fmt.Errorf("AWX SCM requires a credential-free public GitHub URL; " +
			"set OSAC_TEST_AWX_SCM_URL=https://github.com/<owner>/<repo>.git")
	}
	return parsed.String(), nil
}

func scmGit(ctx context.Context, directory string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = directory
	command.WaitDelay = 2 * time.Second
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("git command failed: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func verifyAWXSCM(ctx context.Context, selection awxSCMSelection) (string, error) {
	directory, err := os.MkdirTemp("", "osac-awx-scm-*")
	if err != nil {
		return "", fmt.Errorf("create AWX SCM preflight directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(directory) }()
	if _, err := scmGit(ctx, directory, "init", "--quiet"); err != nil {
		return "", fmt.Errorf("initialize AWX SCM preflight: %w", err)
	}
	if _, err := scmGit(ctx, directory, "fetch", "--no-tags", selection.URL, selection.Refspec); err != nil {
		return "", fmt.Errorf("AWX SCM fetch failed; publish the selected ref and check "+
			"the public source URL and OSAC_TEST_AWX_SCM_REF: %w", err)
	}
	if _, err := scmGit(ctx, directory, "checkout", "--detach", "--force", selection.Revision); err != nil {
		return "", fmt.Errorf("AWX SCM ref does not provide intended commit %s; "+
			"push that commit to the selected source/ref (no tip substitution): %w",
			selection.Revision, err)
	}
	return scmGit(ctx, directory, "rev-parse", "--verify", "HEAD")
}

func (selection awxSCMSelection) payload() ([]byte, error) {
	return json.Marshal(struct {
		URL     string `json:"scm_url"`
		Branch  string `json:"scm_branch"`
		Refspec string `json:"scm_refspec"`
		Delete  bool   `json:"scm_delete_on_update"`
	}{selection.URL, selection.Revision, selection.Refspec, true})
}

type awxProjectSync struct {
	ID       int
	Revision string
}

func awxProjectSyncComplete(output []byte, expected awxProjectSync) (bool, error) {
	var update struct {
		ID       int    `json:"id"`
		Status   string `json:"status"`
		Revision string `json:"scm_revision"`
	}
	if err := json.Unmarshal(output, &update); err != nil {
		return false, fmt.Errorf("cannot decode AWX project update response")
	}
	if update.ID != expected.ID || expected.ID <= 0 {
		return false, fmt.Errorf("AWX returned a different project update ID")
	}
	switch update.Status {
	case "new", "pending", "waiting", "running":
		return false, nil
	case "successful":
		if update.Revision != expected.Revision {
			return false, fmt.Errorf("AWX synced a different SCM revision; expected %s", expected.Revision)
		}
		return true, nil
	case "failed", "error", "canceled":
		return false, fmt.Errorf("AWX project update %d ended with status %s", update.ID, update.Status)
	default:
		return false, fmt.Errorf("AWX project update has an unknown status")
	}
}
