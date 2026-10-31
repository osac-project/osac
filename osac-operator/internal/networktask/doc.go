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

/*
Package networktask runs network manager tasks as AAP jobs and tracks them in
a CR's status.jobs (network manager integration contract §4.3-§4.9). One
Engine, created in cmd/main.go with New, is shared by every controller.

# What the engine does

  - Launch: Run records the next attempt as {state: Running, jobID: ""} with a
    resourceVersion-conditional status update (a conflict or a stale cached
    object launches nothing and is returned as an error: requeue and do not
    write the object again), ensures the label
    osac-<task>-<manager>-<API ID>-<attempt> in the AAP organization, launches
    the job template <prefix>-<task with dashes> with
    {osac_job_vars: Vars + {task, manager: {name, type, ansible_role}}} and
    writes the jobID. A task outside the manager type's fixed set (FabricTasks,
    KubernetesTasks) is rejected before any launch.
  - Tracking: Run reads the job and maps its end (contract §4.6, §4.7):
    successful runs the Request's AcceptFunc and marks the entry Succeeded in
    the same status write as the accepted outputs, or Failed with
    "OSAC rejected the <task> result: <reason>"; failed with a retryable
    osac_error, AAP error, the AAP timeout, a lost job (404) and a job AAP never
    started (cancelled after 30 minutes) become Retrying with backoff; other
    failures are Failed; a job cancelled by hand is launched again at once.
    Before cancelling a job AAP never started, the engine writes
    MessageNotStarted to the Running entry, so that AAP's canceled status is
    mapped to the retry and not to a cancel by hand, even after a restart.
    Cleanup tasks (delete_, detach_, release_, remove_, deprovision) are retried
    after every failure. A Running entry with an empty jobID is resolved by its
    label (lowest job ID tracked, others cancelled; absent for 2 minutes:
    launched again, or Cancelled while deleting).
  - Backoff: min(2m * 2^(n-1), 30m) after the n-th consecutive failure,
    counted in memory and seeded from the attempt after a restart (decision 7);
    query_dhcp_lease waits 30 s, and 5 min from its 20th attempt. The entry
    message is "<task> (<manager>) attempt <n> failed: <msg>; next retry at <RFC3339>".
  - Held launch: with Request.HoldLaunch, for a caller whose Vars are not
    complete for a launch, Run starts no job and only tracks one in progress. A
    Running entry that would be launched again (cancelled by hand, or lost)
    becomes Cancelled, so that LaunchDue reports it. A caller that launches
    only after a check (a storage gate) asks LaunchDue first and then holds
    every Run it did not check for.
  - Deletion: no non-cleanup job starts while the object (or, with
    Request.TargetDeleting, its target) is being deleted. With
    Request.TargetDeleting a running non-cleanup job is cancelled in AAP and its
    entry becomes Cancelled once AAP reports its end (BMaaS apply/query at the
    BMI deletionTimestamp, CaaS publish jobs of a deleted automatic
    ExternalIPAttachment). CancelForDelete cancels every running non-cleanup job
    of the object and reports when the delete may start. A Running entry whose
    job was not found by its label for 2 minutes becomes Cancelled with an empty
    jobID; every later CancelForDelete looks its label up again and cancels a
    job AAP created after all, until the finalizer is removed. A job that AAP
    creates only after the finalizer is gone is not seen: delete-wins then
    relies on the managers' tombstones.

# How a controller uses it

A networking resource controller reconciles roughly like this:

	// Create, once the parent is Ready (copy managers and network type first).
	if eng := r.Engine; !eng.ProvisioningEnabled() {
		networktask.SetProvisioningDisabled(obj, networktask.CreateSubnet) // no job; delete removes the finalizer
		...
	}
	input, _ := networktask.LoadInput(subnet)
	if input == nil { // first job: capture the input once
		resource, _ := networktask.Snapshot(subnet, "Subnet", subnet.Spec, "ipv4Cidr")
		input = networktask.NewInput(subnet, "Subnet", resource)
		input["virtual_network"] = vnSnapshot // Snapshot(vn, "VirtualNetwork", vn.Spec, "ipv4Cidr")
		ipam, _ := networktask.Layout(subnet.Spec.IPv4CIDR, subnet.Spec.AddressPlan)
		input["ipam"] = networktask.IPAMVars(ipam)
		before = func() error { subnet.Status.IPAM = ipam; return networktask.StoreInput(subnet, input) }
	}
	out, err := eng.Run(ctx, subnet, networktask.Request{
		Task: networktask.CreateSubnet, Manager: v1alpha1.NetworkJobManagerFabric,
		ManagerRef: subnet.Status.NetworkManagers.Fabric, Kind: "Subnet",
		Vars:         networktask.WithLaunchKeys(input, map[string]any{"network_type": "evpn"}),
		BeforeLaunch: before, // lands in the same write as the first Running entry
		Slot:         &networktask.SubnetSlot{VirtualNetworkID: subnet.Spec.VirtualNetwork},
		SerializeVNI: true,
		Accept: func(ctx context.Context, res networktask.Result) error {
			return networktask.AcceptSegment(ctx, eng.APIReader(), subnet, "evpn", res)
		},
	})
	if err != nil { return ctrl.Result{}, err }
	switch {
	case out.Succeeded(): // next task (Kubernetes create_subnet with network_segment)
	case out.Failed():    networktask.SetFailed(subnet, out.Entry.Message)
	default:              networktask.SetProgressing(subnet, networktask.ProgressMessage(out.Entry))
	}
	return ctrl.Result{RequeueAfter: out.RequeueAfter}, ...

On delete: call CancelForDelete until it returns true, then Run the cleanup
task of each manager whose create started (Started), in reverse manager order,
and remove the finalizer once all succeeded (none started: remove it at once).
Call Forget(uid) after the finalizer is gone.

Results: AcceptNoOutput (default), ParseBackendID, ParseVirtualNetworkResult,
ParseNetworkSegment and AcceptSegment, ParseDHCPLease, ParseExternalIP,
ParseClusterIngress and ParseVMAttachment validate the typed outputs of
contract §4.10; each returns a RejectError naming only the broken rule.

EVPN (EP #427): the Fabric create_virtual_network returns the L3 VNI
(ParseVirtualNetworkResult, recorded in VirtualNetwork status.l3Vni) and the
Fabric create_subnet returns network_segment {type, l2_vni}. The Subnet and
NetworkAttachment inputs store the VirtualNetwork snapshot with
status.l3Vni; network_segment is a launch key and never stored.

A SecurityGroup rule update stores the new input with StoreInput and calls
MarkForRerun for each recorded manager in the same status write that sets the
Updating (or Progressing) reason; Run then launches the next attempt at once.
When the Fabric run fails finally, Cancel the Kubernetes entry.

Jobs with manager none (VMaaS provision/deprovision, CaaS guest endpoint jobs)
use the same Run with Request.Template set; their Vars become osac_job_vars
plus task.
*/
package networktask
