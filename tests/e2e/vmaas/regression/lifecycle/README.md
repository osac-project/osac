# VM lifecycle regression

`test_vm_lifecycle.py` migrates the OSAC-6110 Ansible VM lifecycle journey.
It creates a VirtualNetwork, Subnet and SecurityGroup, publishes a VM catalog
item through the private API, and creates a VM from it through `osac`. It checks the network references
and reported internal IP, stops and starts the VM through `osac edit`, then
requests a restart and waits for that VM's new VMI. Teardown deletes the VM,
catalog item and networking resources and waits for their removal.

The test uses the suite's existing tenant, InstanceType and storage tier
fixtures and creates a shared DiskImage for its catalog item. Unlike the
Ansible playbook, it does not create a tenant or reset a break-glass password.
The test removes its shared DiskImage; the suite separately creates and
removes its default InstanceType and DiskImage.

With provider networking enabled, the internal IP must be in the test Subnet.
With provider networking disabled, it must be in the cluster pod network;
this checks the fallback and does not prove tenant network isolation or guest
connectivity. This test does not log in to the guest.

From the repository root, run:

```bash
uv run pytest -n 0 tests/e2e/vmaas/regression/lifecycle/
```

The test requires deployed Fulfillment, operator, AAP, KubeVirt, storage and
tenant networking, a pullable VM image, `osac`, `grpcurl`, `kubectl`, public
tenant and private API credentials, and hub and VM-cluster kubeconfigs. It
uses the existing VMaaS fixture setup and its tenant prerequisites. The VMaaS
full tier selects regression tests; the default sanity selection does not.
The original Ansible playbook remains available.
