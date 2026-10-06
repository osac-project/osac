# CUDN EVPN/Netris VMaaS + BMaaS E2E profile

This is the explicit opt-in installer profile for the Phase 1 IPv4 CUDN EVPN
environment used with VMaaS and BMaaS. VMs and bare-metal instances use the
same EVPN/Netris NetworkClass and can therefore be attached to the same VPC.
It is intentionally separate from the base chart and all normal profiles.

The profile provides:

- the normal VMaaS infrastructure and OSAC instance values;
- BMaaS with the Metal3 backend enabled for bare-metal inventory;
- `netris` as the fabric manager and `cudn_evpn` as the k8s manager via the
  operator's nested `networkManagers` registration map;
- a default `NetworkClass` with `fabricManager: netris` and
  `k8sManager: cudn_evpn`;
- the standard Netris AAP instance-group configuration needed by the EVPN
  fabric and network fulfillment jobs.

Before installing, the target OpenShift cluster must already have the Phase 1
EVPN/BGP/VTEP prerequisites, the EVPN `FRRConfiguration`, the `cudn_evpn`
implementation, and the Metal3 BareMetalHost inventory needed by BMaaS. This
profile only registers and selects the managers; it does not install the FRR
operator, create the `FRRConfiguration`, provision the external fabric, or
implement the manager.

Netris passwords, SSH keys, and site-specific values must be supplied through
a private values file. From `osac-installer/`:

```bash
make install PLATFORM=openshift PROFILE=cudn-evpn-netris-test NS=osac \
  INSTANCE_VALUES_EXTRA="-f cudn-evpn-netris-test-secrets.local.yaml"
```

The Makefile profile selection is the installation wiring for this profile;
the standard CI workflows continue to use their existing profiles.
