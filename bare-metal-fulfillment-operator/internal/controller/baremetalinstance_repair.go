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
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// FabricDomainUUIDLabel is the API ID label on FabricDomain hub CRs.
const FabricDomainUUIDLabel = shared.OsacPrefix + "/fabricdomain-uuid"

// networkRepairDetails is the JSON payload stored with a repair mark
// (design §Repair mark).
type networkRepairDetails struct {
	BareMetalInstance string   `json:"baremetalInstance"`
	Subnet            string   `json:"subnet"`
	Interface         string   `json:"interface"`
	FabricDomains     []string `json:"fabricDomains"`
}

// maybeMarkNetworkRepair writes the inventory repair mark when provider
// networking is enabled and a FabricDomain create job has started for a domain
// listing the host. If the required mark cannot be persisted, it returns an
// error and the caller must not release the host.
func (r *BareMetalInstanceReconciler) maybeMarkNetworkRepair(
	ctx context.Context,
	log logr.Logger,
	bmi *v1alpha1.BareMetalInstance,
) error {
	if !r.providerNetworkingEnabled() {
		return nil
	}
	if r.InventoryClient == nil {
		return fmt.Errorf("inventory client is required to check network repair marks")
	}
	hostID := bmi.Spec.ExternalHostID
	if hostID == "" {
		return nil
	}
	hostName := bmi.Spec.ExternalHostName
	if hostName == "" {
		return fmt.Errorf("network repair check requires ExternalHostName for the assigned host")
	}

	fabricDomainIDs, err := r.fabricDomainsListingHost(ctx, hostName)
	if err != nil {
		return err
	}
	if len(fabricDomainIDs) == 0 {
		return nil
	}

	details := networkRepairDetails{
		BareMetalInstance: bmi.Labels[BareMetalInstanceUUIDLabel],
		FabricDomains:     fabricDomainIDs,
	}
	if len(bmi.Spec.NetworkAttachments) > 0 {
		details.Subnet = bmi.Spec.NetworkAttachments[0].SubnetRef
		details.Interface = bmi.Spec.NetworkAttachments[0].Interface
	}
	payload, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("marshaling network repair details: %w", err)
	}

	log.Info("Writing network repair mark before inventory release")
	err = r.InventoryClient.MarkNetworkRepairRequired(ctx, hostID, string(payload))
	if err == nil {
		return nil
	}
	// Fail closed: do not release a FabricDomain member without a durable mark.
	if errors.Is(err, inventory.ErrNetworkRepairMarkUnsupported) {
		return fmt.Errorf("marking network repair required: %w; remove the host from FabricDomain.spec.servers or use a Metal3/Ironic inventory backend",
			err)
	}
	return fmt.Errorf("marking network repair required: %w", err)
}

func (r *BareMetalInstanceReconciler) fabricDomainsListingHost(ctx context.Context, hostName string) ([]string, error) {
	list := &opv1alpha1.FabricDomainList{}
	if err := r.apiReaderOrClient().List(ctx, list, client.InNamespace(r.NetworkingNamespace)); err != nil {
		return nil, fmt.Errorf("listing FabricDomains in %s: %w", r.NetworkingNamespace, err)
	}

	ids := make([]string, 0)
	for i := range list.Items {
		fd := &list.Items[i]
		if !fabricDomainListsServer(fd, hostName) {
			continue
		}
		id := fd.Labels[FabricDomainUUIDLabel]
		if id == "" {
			id = fd.Name
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func fabricDomainListsServer(fd *opv1alpha1.FabricDomain, hostName string) bool {
	if !fabricDomainCreateStarted(fd) {
		return false
	}
	for _, server := range fd.Spec.Servers {
		if server == hostName {
			return true
		}
	}
	return false
}

func fabricDomainCreateStarted(fd *opv1alpha1.FabricDomain) bool {
	for _, job := range fd.Status.Jobs {
		if job.Task == "create_fabric_domain" && job.JobID != "" {
			return true
		}
	}
	return false
}
