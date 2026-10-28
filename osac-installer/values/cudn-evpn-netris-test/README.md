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

With `evpn.enabled: true` in an infra values file, the installer configures the
OpenShift side of EVPN: FRR, route advertisements, NMState, per-node uplink and
VTEP addresses, the `tenant-vtep`, and fabric BGP sessions. The Netris fabric
side remains external; the fabric admin configures the links, VTEP range, and
switch-side node ASN. The `cudn_evpn` manager creates the CUDN later during
OSAC operation.
The Metal3 BareMetalHost inventory needed by BMaaS remains a separate
prerequisite.

Supply Netris credentials through a private instance values file and the
site-specific EVPN addresses through a private infra values file. For example,
save this as `cudn-evpn-netris-test-evpn.private.yaml` and replace the sample
node, interfaces, addresses, and ASNs with values from the cluster and fabric
admins:

```yaml
evpn:
  enabled: true
  nmstate:
    install: true
    channel: stable
  configureClusterNetwork: true
  vtepCIDR: 10.200.255.0/24
  bgp:
    asn: 4209999999
    acceptPrefixes:
      - 10.200.0.0/16
    waitForEstablished: true
    timeoutSeconds: 900
  nodes:
    - name: worker-0
      vtepIP: 10.200.255.1
      uplinks:
        - interface: ens6
          address: 169.254.133.2/31
          peerAddress: 169.254.133.3
          peerASN: 4200300002
```

From `osac-installer/`:

```bash
make install PLATFORM=openshift PROFILE=cudn-evpn-netris-test NS=osac \
  INFRA_VALUES_EXTRA="-f cudn-evpn-netris-test-evpn.private.yaml" \
  INSTANCE_VALUES_EXTRA="-f cudn-evpn-netris-test-secrets.local.yaml"
```

The Makefile profile selection is the installation wiring for this profile;
the standard CI workflows continue to use their existing profiles.
