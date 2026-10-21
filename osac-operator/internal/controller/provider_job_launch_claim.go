package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

const (
	providerJobLaunchClaimAnnotation = "osac.openshift.io/provider-job-launch-claim"
	providerJobLaunchClaimTimeout    = 2 * time.Minute
)

type providerJobLaunchClaim struct {
	ID            string           `json:"id"`
	JobType       v1alpha1.JobType `json:"jobType"`
	ClaimedAt     metav1.Time      `json:"claimedAt"`
	ConfigVersion string           `json:"configVersion,omitempty"`
}

func acquireProviderJobLaunchClaim(
	ctx context.Context,
	writer client.Client,
	reader client.Reader,
	resource client.Object,
	jobType v1alpha1.JobType,
	configVersion string,
) (*providerJobLaunchClaim, bool, bool, error) {
	var (
		claim    *providerJobLaunchClaim
		acquired bool
		resumed  bool
	)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := resource.DeepCopyObject().(client.Object)
		if err := reader.Get(ctx, client.ObjectKeyFromObject(resource), latest); err != nil {
			return err
		}
		if resource.GetUID() != latest.GetUID() {
			return fmt.Errorf("resource changed while claiming provider job launch")
		}
		if jobType == v1alpha1.JobTypeProvision && !latest.GetDeletionTimestamp().IsZero() {
			return nil
		}

		existing, err := providerJobLaunchClaimFromObject(latest)
		if err != nil {
			return err
		}
		if existing != nil {
			if existing.JobType != jobType || time.Now().Before(existing.ClaimedAt.Add(providerJobLaunchClaimTimeout)) {
				return nil
			}
			existing.ClaimedAt = metav1.Now()
			claim = existing
			resumed = true
		} else {
			claim = &providerJobLaunchClaim{
				ID:            string(uuid.NewUUID()),
				JobType:       jobType,
				ClaimedAt:     metav1.Now(),
				ConfigVersion: configVersion,
			}
		}

		encoded, err := json.Marshal(claim)
		if err != nil {
			return fmt.Errorf("encoding provider job launch claim: %w", err)
		}
		annotations := latest.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations[providerJobLaunchClaimAnnotation] = string(encoded)
		latest.SetAnnotations(annotations)
		if err := writer.Update(ctx, latest); err != nil {
			return err
		}
		copyProviderClaimMetadata(resource, latest)
		acquired = true
		return nil
	})
	if err != nil {
		return nil, false, false, err
	}
	return claim, acquired, resumed, nil
}

func providerJobLaunchClaimFromObject(resource client.Object) (*providerJobLaunchClaim, error) {
	value := resource.GetAnnotations()[providerJobLaunchClaimAnnotation]
	if value == "" {
		return nil, nil
	}
	claim := &providerJobLaunchClaim{}
	if err := json.Unmarshal([]byte(value), claim); err != nil {
		return nil, fmt.Errorf("invalid provider job launch claim on %s: %w", client.ObjectKeyFromObject(resource), err)
	}
	if claim.ID == "" || claim.JobType == "" || claim.ClaimedAt.IsZero() {
		return nil, fmt.Errorf("incomplete provider job launch claim on %s", client.ObjectKeyFromObject(resource))
	}
	return claim, nil
}

func providerJobLaunchClaimIsFresh(claim *providerJobLaunchClaim, now time.Time) bool {
	return now.Before(claim.ClaimedAt.Add(providerJobLaunchClaimTimeout))
}

func releaseProviderJobLaunchClaim(
	ctx context.Context,
	writer client.Client,
	reader client.Reader,
	resource client.Object,
	claimID string,
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := resource.DeepCopyObject().(client.Object)
		if err := reader.Get(ctx, client.ObjectKeyFromObject(resource), latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		claim, err := providerJobLaunchClaimFromObject(latest)
		if err != nil || claim == nil {
			return err
		}
		if claim.ID != claimID {
			return nil
		}
		annotations := latest.GetAnnotations()
		delete(annotations, providerJobLaunchClaimAnnotation)
		if len(annotations) == 0 {
			annotations = nil
		}
		latest.SetAnnotations(annotations)
		if err := writer.Update(ctx, latest); err != nil {
			return err
		}
		copyProviderClaimMetadata(resource, latest)
		return nil
	})
}

func copyProviderClaimMetadata(destination, source client.Object) {
	destination.SetAnnotations(source.GetAnnotations())
	destination.SetResourceVersion(source.GetResourceVersion())
}

func appendRecoveredProviderJob(
	jobs *[]v1alpha1.JobStatus,
	jobType v1alpha1.JobType,
	status provisioning.ProvisionStatus,
	claim *providerJobLaunchClaim,
	maxHistory int,
) {
	if status.JobID == "" || provisioning.FindJobByID(*jobs, status.JobID) != nil {
		return
	}
	timestamp := metav1.Now()
	if !status.StartTime.IsZero() {
		timestamp = metav1.NewTime(status.StartTime)
	}
	*jobs = provisioning.AppendJob(*jobs, v1alpha1.JobStatus{
		JobID:                  status.JobID,
		Type:                   jobType,
		State:                  status.State,
		Message:                status.MessageWithDetails(),
		Timestamp:              timestamp,
		ConfigVersion:          claim.ConfigVersion,
		BlockDeletionOnFailure: jobType == v1alpha1.JobTypeDeprovision,
	}, maxHistory)
}

func recoverProviderJobFromClaim(
	ctx context.Context,
	provider provisioning.ProvisioningProvider,
	resource client.Object,
	claim *providerJobLaunchClaim,
	jobs *[]v1alpha1.JobStatus,
	maxHistory int,
) (bool, error) {
	recoverer, ok := provider.(provisioning.ProvisioningJobLaunchRecoverer)
	if !ok {
		return false, fmt.Errorf("provisioning provider cannot recover job launch %q", claim.ID)
	}
	status, found, err := recoverer.FindJobByLaunchID(ctx, resource, claim.JobType, claim.ID)
	if err != nil || !found {
		return found, err
	}
	appendRecoveredProviderJob(jobs, claim.JobType, status, claim, maxHistory)
	return true, nil
}
