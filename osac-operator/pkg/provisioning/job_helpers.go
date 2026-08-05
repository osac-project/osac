package provisioning

import (
	"github.com/osac-project/osac-operator/api/v1alpha1"
)

// FindJobByID finds a job by its ID in the jobs array.
// Returns a pointer to the job if found, nil otherwise.
func FindJobByID(jobs []v1alpha1.JobStatus, jobID string) *v1alpha1.JobStatus {
	for i := range jobs {
		if jobs[i].JobID == jobID {
			return &jobs[i]
		}
	}
	return nil
}

// UpdateJob updates an existing job by ID with new values.
// Returns true if the job was found and updated, false otherwise.
func UpdateJob(jobs []v1alpha1.JobStatus, updatedJob v1alpha1.JobStatus) bool {
	job := FindJobByID(jobs, updatedJob.JobID)
	if job == nil {
		return false
	}
	*job = updatedJob
	return true
}

// AppendJob adds a new job to the jobs array and trims to maxHistory.
func AppendJob(jobs []v1alpha1.JobStatus, newJob v1alpha1.JobStatus, maxHistory int) []v1alpha1.JobStatus {
	jobs = append(jobs, newJob)
	if len(jobs) > maxHistory {
		jobs = jobs[len(jobs)-maxHistory:]
	}
	return jobs
}

// NeedsProvisionJob determines if a new provision job should be triggered.
// Returns true if no job exists, or if the previous job failed (allowing retry).
// Used by controllers without config-version-based provisioning (SecurityGroup,
// Subnet, VirtualNetwork). Controllers with ConfigVersion support should use
// EvaluateAction instead, which adds backoff and spec-change detection.
func NeedsProvisionJob(latestJob *v1alpha1.JobStatus) bool {
	// No job exists yet
	if latestJob == nil || latestJob.JobID == "" {
		return true
	}

	// Job is still running
	if !latestJob.State.IsTerminal() {
		return false
	}

	// Trigger new job if previous job failed (retry logic)
	return !latestJob.State.IsSuccessful()
}

// FindLatestJobByType finds the most recent job of the specified type by timestamp.
// Returns nil if no job of that type exists.
func FindLatestJobByType(jobs []v1alpha1.JobStatus, jobType v1alpha1.JobType) *v1alpha1.JobStatus {
	var latest *v1alpha1.JobStatus
	for i := range jobs {
		if jobs[i].Type == jobType {
			if latest == nil || jobs[i].Timestamp.After(latest.Timestamp.Time) {
				latest = &jobs[i]
			}
		}
	}
	return latest
}
