/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// DHCPLeaseResult is the structure returned by the query_dhcp_lease AAP job artifacts.
type DHCPLeaseResult struct {
	Leases []DHCPLease `json:"leases"`
}

// DHCPLease represents a single DHCP lease entry from the fabric manager.
type DHCPLease struct {
	SubnetRef  string `json:"subnet_ref"`
	Interface  string `json:"interface"`
	IPAddress  string `json:"ip_address"`
	MACAddress string `json:"mac_address"`
}

func (r *BareMetalInstanceReconciler) reconcileIPDiscovery(
	ctx context.Context,
	bareMetalInstance *v1alpha1.BareMetalInstance,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if len(bareMetalInstance.Spec.NetworkAttachments) == 0 {
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionIPDiscoveryComplete,
			metav1.ConditionTrue,
			"Skipped",
			"No network attachments configured",
		)
		return ctrl.Result{}, nil
	}

	// IP discovery must run only after the fabric port has moved to the tenant network
	// and the host has rebooted there; otherwise we would read/expose a provisioning
	// -network lease to the tenant.
	if !bareMetalInstance.IsStatusConditionTrue(v1alpha1.HostConditionNetworkHandoffComplete) {
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}

	if r.IPDiscoveryProvider == nil {
		log.Info("IP discovery provider not configured, skipping")
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionIPDiscoveryComplete,
			metav1.ConditionTrue,
			"Skipped",
			"IP discovery provider not configured",
		)
		return ctrl.Result{}, nil
	}

	log.Info("Reconciling IP discovery", "attachments", len(bareMetalInstance.Spec.NetworkAttachments))

	// Resolve each attachment's NIC MAC from the bare-metal management backend and
	// plumb a subnet-ref → MAC map into the job's extra_vars. The query_dhcp_lease role
	// matches DHCP leases by MAC when the host is not registered as a named fabric
	// server; an absent or partial mapping degrades to name-based matching.
	if r.ManagementClient != nil && bareMetalInstance.Spec.ExternalHostID != "" {
		ifaceMACs, macErr := r.ManagementClient.GetHostInterfaceMACs(ctx, bareMetalInstance.Spec.ExternalHostID)
		if macErr != nil {
			log.Error(macErr, "Failed to resolve host interface MACs; falling back to name-based lease matching",
				"host", bareMetalInstance.Spec.ExternalHostID)
		} else if subnetMACs := buildSubnetMACMap(bareMetalInstance.Spec.NetworkAttachments, ifaceMACs); len(subnetMACs) > 0 {
			ctx = provisioning.WithNetworkAttachmentMACs(ctx, subnetMACs)
		}
	}

	desiredVersion, err := provisioning.ComputeDesiredConfigVersion(struct {
		NetworkAttachments []v1alpha1.BareMetalNetworkAttachment
		ExternalHostID     string
		HostClass          string
	}{
		bareMetalInstance.Spec.NetworkAttachments,
		bareMetalInstance.Spec.ExternalHostID,
		bareMetalInstance.Spec.HostClass,
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	if bareMetalInstance.Status.IPDiscoveryJobs == nil {
		bareMetalInstance.Status.IPDiscoveryJobs = []opv1alpha1.JobStatus{}
	}

	result, err := provisioning.RunProvisioningLifecycle(
		ctx, r.IPDiscoveryProvider, bareMetalInstance,
		&provisioning.State{
			Jobs:                 &bareMetalInstance.Status.IPDiscoveryJobs,
			DesiredConfigVersion: desiredVersion,
		},
		provisioning.DefaultMaxJobHistory, r.ProvisionPollIntervalDuration,
		&provisioning.PollCallbacks{
			OnFailed: func(message string) {
				bareMetalInstance.SetStatusCondition(
					v1alpha1.HostConditionIPDiscoveryComplete,
					metav1.ConditionFalse,
					v1alpha1.HostConditionReasonTemplateFailed,
					message,
				)
			},
			OnSuccess: func(status provisioning.ProvisionStatus) {
				if r.AAPClient != nil {
					if parseErr := r.applyIPDiscoveryResults(ctx, bareMetalInstance, status.JobID); parseErr != nil {
						log.Error(parseErr, "Failed to parse IP discovery results")
						bareMetalInstance.SetStatusCondition(
							v1alpha1.HostConditionIPDiscoveryComplete,
							metav1.ConditionFalse,
							v1alpha1.HostConditionReasonTemplateFailed,
							fmt.Sprintf("Failed to parse IP discovery artifacts: %v", parseErr),
						)
						return
					}
				}
				r.initNetworkAttachmentStatuses(bareMetalInstance)

				bareMetalInstance.SetStatusCondition(
					v1alpha1.HostConditionIPDiscoveryComplete,
					metav1.ConditionTrue,
					"Succeeded",
					"DHCP lease discovery completed",
				)
			},
		},
		func() bool {
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(
				ctx, r.apiReaderOrClient(), client.ObjectKeyFromObject(bareMetalInstance), &v1alpha1.BareMetalInstance{},
				func(obj client.Object) []opv1alpha1.JobStatus {
					return obj.(*v1alpha1.BareMetalInstance).Status.IPDiscoveryJobs
				},
			)
		},
		func() error {
			return r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(bareMetalInstance), bareMetalInstance.Status)
		},
	)
	if err != nil {
		return result, err
	}

	if result.RequeueAfter > 0 {
		ipCond := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
		if ipCond == nil || ipCond.Reason != v1alpha1.HostConditionReasonTemplateFailed {
			bareMetalInstance.SetStatusCondition(
				v1alpha1.HostConditionIPDiscoveryComplete,
				metav1.ConditionFalse,
				v1alpha1.HostConditionReasonProgressing,
				"DHCP lease discovery in progress",
			)
		}
	}

	return result, nil
}

func (r *BareMetalInstanceReconciler) applyIPDiscoveryResults(
	ctx context.Context,
	bareMetalInstance *v1alpha1.BareMetalInstance,
	jobID string,
) error {
	log := logf.FromContext(ctx)

	job, err := r.AAPClient.GetJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("failed to get job %s for IP discovery results: %w", jobID, err)
	}

	if len(job.Artifacts) == 0 || string(job.Artifacts) == "null" || string(job.Artifacts) == "{}" {
		return fmt.Errorf("IP discovery job %s returned no artifacts; DHCP lease not yet available", jobID)
	}

	var result DHCPLeaseResult
	if err := json.Unmarshal(job.Artifacts, &result); err != nil {
		return fmt.Errorf("failed to parse IP discovery artifacts: %w", err)
	}

	leaseMap := make(map[string]DHCPLease)
	for _, lease := range result.Leases {
		leaseMap[lease.SubnetRef] = lease
	}

	var missing []string
	for i, na := range bareMetalInstance.Spec.NetworkAttachments {
		if i >= len(bareMetalInstance.Status.NetworkAttachmentStatuses) {
			bareMetalInstance.Status.NetworkAttachmentStatuses = append(
				bareMetalInstance.Status.NetworkAttachmentStatuses,
				v1alpha1.BareMetalNetworkAttachmentStatus{},
			)
		}
		status := &bareMetalInstance.Status.NetworkAttachmentStatuses[i]
		status.SubnetRef = na.SubnetRef
		status.Interface = na.Interface
		status.Primary = na.Primary

		if lease, ok := leaseMap[na.SubnetRef]; ok {
			if net.ParseIP(lease.IPAddress) == nil {
				log.Info("Skipping invalid IP address from DHCP lease",
					"interface", na.Interface, "subnet", na.SubnetRef, "ip", lease.IPAddress)
				missing = append(missing, na.SubnetRef)
			} else {
				status.IPAddress = lease.IPAddress
				log.Info("Discovered IP for attachment",
					"interface", na.Interface, "subnet", na.SubnetRef, "ip", lease.IPAddress)
			}
		} else {
			missing = append(missing, na.SubnetRef)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("DHCP lease not found for %d/%d network attachments (subnets: %v); will retry",
			len(missing), len(bareMetalInstance.Spec.NetworkAttachments), missing)
	}

	return nil
}

// buildSubnetMACMap maps each attachment's SubnetRef to the MAC address of its NIC,
// resolved from the host's interface-name → MAC mapping. When an attachment has no
// Interface set and the host exposes exactly one interface, that MAC is used (the
// single-NIC case). Attachments whose MAC cannot be resolved are omitted, so the role
// falls back to name-based matching for them.
func buildSubnetMACMap(
	attachments []v1alpha1.BareMetalNetworkAttachment,
	ifaceMACs map[string]string,
) map[string]string {
	subnetMACs := make(map[string]string)
	for _, na := range attachments {
		mac := ifaceMACs[na.Interface]
		if mac == "" && na.Interface == "" && len(ifaceMACs) == 1 {
			for _, only := range ifaceMACs {
				mac = only
			}
		}
		if mac != "" {
			subnetMACs[na.SubnetRef] = mac
		}
	}
	return subnetMACs
}

// initNetworkAttachmentStatuses ensures statuses exist for all attachments and syncs
// SubnetRef, Interface, and Primary from spec to status without overwriting
// any IP addresses already populated by applyIPDiscoveryResults.
func (r *BareMetalInstanceReconciler) initNetworkAttachmentStatuses(
	bareMetalInstance *v1alpha1.BareMetalInstance,
) {
	for i, na := range bareMetalInstance.Spec.NetworkAttachments {
		if i >= len(bareMetalInstance.Status.NetworkAttachmentStatuses) {
			bareMetalInstance.Status.NetworkAttachmentStatuses = append(
				bareMetalInstance.Status.NetworkAttachmentStatuses,
				v1alpha1.BareMetalNetworkAttachmentStatus{},
			)
		}
		bareMetalInstance.Status.NetworkAttachmentStatuses[i].SubnetRef = na.SubnetRef
		bareMetalInstance.Status.NetworkAttachmentStatuses[i].Interface = na.Interface
		bareMetalInstance.Status.NetworkAttachmentStatuses[i].Primary = na.Primary
	}
}
