// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package awx

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/osac-project/osac/osac-operator/pkg/aap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// jobResourceMetadata reads the resource snapshot submitted with the job. This
// remains available in AWX after the Kubernetes resource has been deleted.
func jobResourceMetadata(job aap.Job) (*metav1.ObjectMeta, error) {
	var inputs struct {
		Vars struct {
			Resource struct {
				Metadata metav1.ObjectMeta `json:"metadata"`
			} `json:"resource"`
		} `json:"osac_job_vars"`
	}

	if err := json.Unmarshal([]byte(job.ExtraVars), &inputs); err != nil {
		return nil, fmt.Errorf("job %d has unreadable resource metadata", job.ID)
	}

	return &inputs.Vars.Resource.Metadata, nil
}

// JobsForResource finds every retained job for the requested resource and playbook.
// The resource creation time bounds history; matching uses the UID, namespace
// and tenant stored in the submitted job inputs.
// A resource recreated with the same name has a different UID and is excluded.
//
// Failed and pending attempts are included. The caller checks their outcomes,
// allowing a test to catch an earlier failed attempt even if a later retry succeeds.
func (e *Installation) JobsForResource(
	ctx context.Context,
	resource metav1.Object,
	playbook string,
) ([]aap.Job, error) {
	uid := resource.GetUID()
	namespace := resource.GetNamespace()
	tenant := resource.GetAnnotations()["osac.openshift.io/tenant"]

	created := resource.GetCreationTimestamp()
	if uid == "" || namespace == "" || tenant == "" || playbook == "" || created.IsZero() {
		return nil, fmt.Errorf("AWX correlation requires resource UID, namespace, tenant, playbook and creation time")
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	attempts, err := e.Client.ListJobsByPlaybook(ctx, playbook, created.Time)
	if err != nil {
		return nil, err
	}

	var jobs []aap.Job

	for _, job := range attempts {
		if job.Playbook != playbook {
			continue
		}

		metadata, err := jobResourceMetadata(job)
		if err != nil {
			return nil, err
		}

		if metadata.UID != uid {
			continue
		}

		if metadata.Namespace != namespace || metadata.Annotations["osac.openshift.io/tenant"] != tenant {
			return nil, fmt.Errorf("job %d has inconsistent resource scope", job.ID)
		}

		slog.DebugContext(ctx, "observed scoped AWX attempt",
			"resourceUID", uid,
			"namespace", namespace,
			"playbook", playbook,
			"job", job.ID,
			"status", job.Status,
		)
		jobs = append(jobs, job)
	}

	slices.SortFunc(jobs, func(a, b aap.Job) int { return a.ID - b.ID })

	return jobs, nil
}

// CheckIdleForResource checks retained attempts for one resource incarnation.
// Qualification separately requires every attempt to succeed.
func (e *Installation) CheckIdleForResource(ctx context.Context, resource metav1.Object, playbooks ...string) error {
	for _, playbook := range playbooks {
		jobs, err := e.JobsForResource(ctx, resource, playbook)
		if err != nil {
			return err
		}

		for _, job := range jobs {
			switch job.Status {
			case "successful", "failed", "error", "canceled":
			default:
				return fmt.Errorf("AWX job %d is still %s", job.ID, job.Status)
			}
		}
	}

	return nil
}

// CheckIdle rejects active scoped automation and unreadable job inputs before reuse.
func (e *Installation) CheckIdle(ctx context.Context, namespace string, playbooks ...string) error {
	for _, playbook := range playbooks {
		jobs, err := e.Client.ListActiveJobsByPlaybook(ctx, playbook)
		if err != nil {
			return err
		}

		for _, job := range jobs {
			switch job.Status {
			case "successful", "failed", "error", "canceled":
				continue
			}

			metadata, err := jobResourceMetadata(job)
			if err != nil || metadata.Namespace == "" {
				return fmt.Errorf("active AWX job %d has unreadable resource scope", job.ID)
			}

			if metadata.Namespace == namespace {
				slog.DebugContext(ctx, "installation reuse blocked by active automation",
					"namespace", namespace,
					"job", job.ID,
					"status", job.Status,
					"playbook", playbook,
				)

				return fmt.Errorf("AWX job %d is still %s", job.ID, job.Status)
			}
		}
	}

	return nil
}
