// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package awx

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/osac-project/osac/osac-operator/pkg/aap"

	"github.com/osac-project/osac/tests/integration/caas/internal/harness"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// AWXExecutionEvidence records the container image seen in a real AWX worker Pod.
// JobID and SCMRevision identify the automation execution.
//
// ImageReference is the image requested by the Pod. ImageID is the image ID
// reported by the container runtime, providing evidence of what actually ran.
// PodName, PodUID and NodeName locate that execution even after AWX deletes its Pod.
type AWXExecutionEvidence struct {
	JobID          string `json:"job"`
	PodName        string `json:"pod"`
	PodUID         string `json:"uid"`
	NodeName       string `json:"node"`
	ImageReference string `json:"image"`
	ImageID        string `json:"imageID"`
	SCMRevision    string `json:"revision,omitempty"`
}

// Installation contains the deployment settings verified at suite startup.
// ProjectURL and ProjectCommit select the playbooks; EEImage and EEPullPolicy
// independently select the execution environment used to run them.
//
// The saved AWX project, execution environment and instance group IDs let each
// job check confirm that it used the verified configuration.
type Installation struct {
	Client        *aap.Client       `json:"-"`
	ProjectURL    string            `json:"projectURL"`
	ProjectCommit string            `json:"projectCommit"`
	EEImage       string            `json:"eeImage"`
	EEPullPolicy  corev1.PullPolicy `json:"eePullPolicy"`

	projectID              int
	executionEnvironmentID int
	instanceGroupIDs       map[string]int
}

// Open connects to AWX and verifies the selected project, execution image and required groups.
// The returned expectations are complete before execution checks can use them.
func Open(ctx context.Context, e *harness.Environment) (*Installation, error) {
	secret, err := e.Kube.CoreV1().Secrets(e.Namespace).Get(ctx, "awx-token", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	api := aap.NewClient("http://awx.awx.localhost:8080/api", string(secret.Data["token"]), false)

	return checkConfiguration(ctx, e, api)
}

// checkConfiguration verifies deployment inputs independently of observed jobs.
func checkConfiguration(
	ctx context.Context,
	e *harness.Environment,
	api *aap.Client,
) (*Installation, error) {
	configuration, err := e.Kube.CoreV1().ConfigMaps(e.Namespace).Get(ctx, "caas-it-configuration", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	selected := &Installation{
		Client:           api,
		ProjectURL:       configuration.Data["projectURL"],
		ProjectCommit:    configuration.Data["projectCommit"],
		EEImage:          configuration.Data["eeImage"],
		EEPullPolicy:     corev1.PullPolicy(configuration.Data["eePullPolicy"]),
		instanceGroupIDs: map[string]int{},
	}
	if selected.ProjectURL == "" || len(selected.ProjectCommit) != 40 || selected.EEImage == "" {
		return nil, fmt.Errorf("AWX Git URL, commit and EE image must be configured")
	}

	pull, ok := map[corev1.PullPolicy]string{
		corev1.PullAlways:       "always",
		corev1.PullIfNotPresent: "missing",
		corev1.PullNever:        "never",
	}[selected.EEPullPolicy]
	if !ok {
		return nil, fmt.Errorf("invalid EE pull policy %q", selected.EEPullPolicy)
	}

	projects, err := api.ListProjects(ctx, "osac")
	if err != nil {
		return nil, err
	}

	if len(projects) != 1 {
		return nil, fmt.Errorf("expected one OSAC AWX project")
	}

	project := projects[0]
	if project.SCMType != "git" || project.SCMURL != selected.ProjectURL || project.SCMBranch != selected.ProjectCommit ||
		project.SCMRevision != selected.ProjectCommit ||
		project.Status != "successful" {
		return nil, fmt.Errorf("AWX project has not synced the selected Git URL/commit")
	}

	selected.projectID = project.ID

	environments, err := api.ListExecutionEnvironments(ctx, "osac-ee")
	if err != nil {
		return nil, err
	}

	if len(environments) != 1 || environments[0].Image != selected.EEImage || environments[0].Pull != pull {
		return nil, fmt.Errorf("AWX execution environment differs from the selected image/pull policy")
	}

	selected.executionEnvironmentID = environments[0].ID
	if err := selected.checkInstanceGroups(ctx); err != nil {
		return nil, err
	}

	slog.InfoContext(ctx, "verified AWX deployment inputs",
		"projectID", selected.projectID,
		"revision", selected.ProjectCommit,
		"executionEnvironmentID", selected.executionEnvironmentID,
		"groups", len(selected.instanceGroupIDs),
	)

	return selected, nil
}

// checkInstanceGroups observes the production groups required by this suite.
func (selected *Installation) checkInstanceGroups(ctx context.Context) error {
	for _, name := range []string{"osac-bare-metal-fulfillment-ig", "osac-cluster-fulfillment-ig"} {
		groups, err := selected.Client.ListInstanceGroups(ctx, name)
		if err != nil {
			return err
		}

		if len(groups) != 1 || !groups[0].IsContainerGroup {
			return fmt.Errorf("missing production container group %s", name)
		}

		var pod corev1.Pod

		if err := yaml.Unmarshal([]byte(groups[0].PodSpecOverride), &pod); err != nil {
			return err
		}

		worker := false

		for _, container := range pod.Spec.Containers {
			if container.Name == "worker" {
				worker = true
			}
		}

		if !worker || pod.Spec.ServiceAccountName != "osac-sa" {
			return fmt.Errorf("%s differs from the required worker layout or service account", name)
		}

		selected.instanceGroupIDs[name] = groups[0].ID
		slog.DebugContext(ctx, "verified AWX production container group", "group", name, "id", groups[0].ID)
	}

	return nil
}

// CheckExecution checks a job's submitted resource, playbook, Git
// revision, execution environment and instance group against the configuration
// verified at startup. It also checks the saved worker Pod and
// returns the image information reported by the container runtime.
//
// The caller supplies its observed job after validating the installation and
// starting Pod observation. Job outcomes are checked separately, including
// failures that occurred before a Pod started.
func (selected *Installation) CheckExecution(
	job aap.Job,
	resource metav1.Object,
	playbook string,
	jobPods *JobPodObserver,
) (*AWXExecutionEvidence, error) {
	groupID, err := selected.executionGroupID(playbook)
	if err != nil {
		return nil, err
	}

	id := strconv.Itoa(job.ID)

	metadata, err := jobResourceMetadata(job)
	if err != nil {
		return nil, err
	}

	if metadata.UID != resource.GetUID() || metadata.Namespace != resource.GetNamespace() ||
		metadata.Annotations[harness.TenantAnnotation] != resource.GetAnnotations()[harness.TenantAnnotation] {
		return nil, fmt.Errorf("job submitted another resource incarnation or scope")
	}

	if job.Playbook != playbook || job.ProjectID != selected.projectID || job.SCMRevision != selected.ProjectCommit ||
		job.ExecutionEnvironmentID != selected.executionEnvironmentID ||
		job.InstanceGroupID != groupID {
		return nil, fmt.Errorf("job differs from the selected playbook, Git revision, EE or execution group")
	}

	pod, err := jobPods.podForJob(id)
	if err != nil {
		return nil, err
	}

	if pod.Spec.ServiceAccountName != "osac-sa" {
		return nil, fmt.Errorf("execution service account differs from production")
	}

	for _, container := range pod.Spec.Containers {
		if container.Name != "worker" {
			continue
		}

		if container.Image != selected.EEImage || container.ImagePullPolicy != selected.EEPullPolicy {
			return nil, fmt.Errorf("execution Pod requested another EE or pull policy")
		}

		for _, status := range pod.Status.ContainerStatuses {
			if status.Name == "worker" && status.ImageID != "" {
				slog.Debug("verified AWX execution runtime",
					"job", job.ID,
					"resourceUID", resource.GetUID(),
					"namespace", resource.GetNamespace(),
					"pod", pod.Name,
					"podUID", pod.UID,
					"node", pod.Spec.NodeName,
					"revision", job.SCMRevision,
				)

				return &AWXExecutionEvidence{
					JobID:          id,
					PodName:        pod.Name,
					PodUID:         string(pod.UID),
					NodeName:       pod.Spec.NodeName,
					ImageReference: container.Image,
					ImageID:        status.ImageID,
					SCMRevision:    job.SCMRevision,
				}, nil
			}
		}
	}

	return nil, fmt.Errorf("execution runtime image identity is unavailable")
}

// executionGroupID selects the verified production group independently of observed jobs.
func (selected *Installation) executionGroupID(playbook string) (int, error) {
	var name string

	switch playbook {
	case "osac-aap/playbook_osac_create_bare_metal_instance.yml",
		"osac-aap/playbook_osac_delete_bare_metal_instance.yml":
		name = "osac-bare-metal-fulfillment-ig"
	case "osac-aap/playbook_osac_create_hosted_cluster.yml",
		"osac-aap/playbook_osac_delete_hosted_cluster.yml":
		name = "osac-cluster-fulfillment-ig"
	default:
		return 0, fmt.Errorf("unsupported execution playbook %q", playbook)
	}

	id, ok := selected.instanceGroupIDs[name]
	if !ok {
		return 0, fmt.Errorf("missing verified execution group %s", name)
	}

	return id, nil
}
