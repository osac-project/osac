# NetBox Backend Configuration Guide

This guide covers configuring and operating the NetBox inventory backend for
the bare-metal-fulfillment-operator (BMF).

## Contents

- [Overview](#overview)
- [Prerequisites](#prerequisites)
- [Preparing the NetBox device pool](#preparing-the-netbox-device-pool)
- [BMC credentials and isolation](#bmc-credentials-and-isolation)
- [Configuring the installer](#configuring-the-installer)
- [Allocation and release](#allocation-and-release)
- [Drain, disable, and re-enable](#drain-disable-and-re-enable)
- [Recovery and diagnostics](#recovery-and-diagnostics)
- [References](#references)

## Overview

NetBox supplies device inventory and allocation state. BMF selects and claims
devices, then creates an on-demand BareMetalHost (BMH) and BMC Secret;
Metal3/BareMetalOperator (BMO) manages the BMH that OSAC provisions.

For a NetBox device with numeric ID `42`, BMF uses `netbox-42` as its BMH name,
`<bmh-namespace>/netbox-42` as its inventory host ID, and
`netbox-42-bmc-secret` for its generated Kubernetes BMC Secret.

## Prerequisites

NetBox mode requires `inventory.hostClass: metal3` and
`management.type: metal3`. Choose NetBox as the BMF inventory backend by
setting `bmf.netbox.enabled: true`; it cannot be enabled alongside the BMF
Metal3 or BCM inventory backend. Point `bmf.netbox.bmhNamespace` at the
namespace where BMO watches OSAC-managed BMHs.

Use NetBox 4.6 or later. BMF uses ETag/`If-Match` conditional updates to protect device claims and releases from overwriting concurrent changes; NetBox added this API behavior in 4.6 ([release notes](https://netbox.readthedocs.io/en/stable/release-notes/version-4.6/)).

## Preparing the NetBox device pool

Create these custom fields on NetBox `dcim.device` records. The operator reads these names directly; the `cf_` prefix below is used only in API filters.

| NetBox field | Requirement and use |
|---|---|
| `osac_managed` | Boolean. Set `true` to admit the device to new allocation searches; set `false` to drain it. |
| `osac_instance_id` | Text. Leave empty while free. BMF writes the BareMetalInstance UID when it claims the device and clears it on release. |
| `osac_bmc_address` | Text. Required. Must be a BMC URL with a scheme accepted by Metal3/BMO. Supported schemes include `https`, `ipmi`, `redfish-virtualmedia+https`, `idrac-virtualmedia+https`, `ilo5-virtualmedia+https`, and their `*-virtualmedia+http` variants. Plain `http` is rejected. |
| `osac_boot_mac` | Text. Required. Must parse as a six-byte MAC address. |
| `osac_interface_macs` | Optional JSON object mapping logical port names to MAC addresses. Values override native NetBox interface primary MACs with the same name. |

A free device must have NetBox status `staged`, `osac_managed=true`, and an empty `osac_instance_id`. Each `BareMetalInstance.spec.selector.hostSelector` entry becomes an exact custom-field filter (`cf_<key>=<value>`); all selectors are combined with the pool filters. Use custom-field names without `cf_`, and do not use the reserved pool fields as capability selectors. NetBox tags and device roles are not part of this match.

The search uses the first device returned by NetBox for the combined filters.

## BMC credentials and isolation

BMC usernames and passwords come from the OSAC Secret API, not from NetBox and not from a tenant Kubernetes Secret. For each NetBox device ID, provide exactly one OSAC Secret with:

- `metadata.tenant: system`.
- The label key `osac.openshift.io/netbox-device-<device-id>`; the label value is ignored.
- Non-empty `data.username` and `data.password` values.

Use the platform-approved process for creating system-scoped OSAC Secrets; the general tenant Secret workflow does not grant system scope. See [Managing Secrets](../secrets-guide.md). A missing or ambiguous Secret, empty credential data, or invalid BMC address/boot MAC causes the device to be quarantined as `failed` before it is claimed. BMF does not authenticate to the BMC before claiming; a wrong but non-empty password can fail later during Metal3/BMO reconciliation.

After resolving credentials, BMF copies them into the namespaced `netbox-<device-id>-bmc-secret` Kubernetes Secret and labels it `osac.openshift.io/managed-by=baremetal`. BMF refuses to adopt an existing same-name Secret without that ownership label, and only removes Secrets carrying its label during release. Do not put BMC credentials in NetBox custom fields.

## Configuring the installer

Use the OSAC umbrella chart values. The token Secret must be in the BMF release namespace and contain the configured key (default name `osac-netbox-token`, default key `token`). If that Secret is absent, the chart requires `bmf.netbox.tokenValue` and creates it; use a pre-created Secret for production rather than storing a token in a values file.

```yaml
bmf:
  netbox:
    enabled: true
    url: "https://netbox.example.org"
    bmhNamespace: "osac-host-inventory"
    tokenSecretKey: "token"
    tokenValue: ""
    caCert: "" # Optional NetBox CA PEM
    allowInsecureHTTP: false
  secrets:
    netboxToken: "osac-netbox-token"
    netboxCA: "osac-netbox-ca"
```

`bmf.netbox.url` must be an HTTP(S) origin only: do not append `/api`, a path, credentials, query, or fragment. HTTPS is the default and recommended transport. HTTP requires the explicit `allowInsecureHTTP: true` setting and should be limited to isolated development environments. If `caCert` is set, the chart mounts it through the `bmf.secrets.netboxCA` Secret (default `osac-netbox-ca`, key `ca.crt`); the client adds it to the system trust roots.

Also configure `bmf.secretAPI.url` and `bmf.secretAPI.issuerUrl`. The Secret API URL must use HTTPS, and its issuer must match the rendered `service.auth.issuerUrl`. `bmf.secretAPI.caBundle` trusts the Secret API and is separate from the NetBox `caCert`. The NetBox API identity needs permission to list/read devices and interfaces and update device status and custom fields.

The operator reads the token and NetBox CA when it starts. Restart the BMF operator after rotating either mounted Secret.

## Allocation and release

1. BMF searches NetBox for `status=staged`, `cf_osac_managed=true`, `cf_osac_instance_id__empty=true`, plus all `hostSelector` custom-field filters.
2. Before claiming, it validates `osac_bmc_address` and `osac_boot_mac` and resolves the unique system Secret. It conditionally patches the device with `If-Match`, setting status to `active` and `osac_instance_id` to the BareMetalInstance UID.
3. BMF creates the BMC Secret and BMH in the configured Metal3 namespace. Metal3/BMO then performs BMH lifecycle and provisioning.
4. During BareMetalInstance deletion, the BMF finalizer requests BMH deletion and waits until the BMH is absent, deletes its managed BMC Secret, then conditionally patches the NetBox device back to `staged` and clears `osac_instance_id`. The release patch leaves pool and connection fields unchanged.

If no device matches, the BareMetalInstance gets the generic condition reason `NoMatchingHosts` and message `No matching hosts available`, enters phase `Failed`, and is retried after the configured no-host interval (30 seconds by default). A NetBox API error is a reconciliation error, not a no-host result.

> **Concurrent NetBox edits during release:** BMF reads the device ETag (its revision identifier) before deleting the BMH and BMC Secret, then uses that ETag to mark the device free. If someone edits the NetBox record during cleanup, NetBox rejects the stale update and the BMF finalizer retries release. Do not clear `osac_instance_id` manually; allow the finalizer to complete.

## Drain, disable, and re-enable

1. Set `osac_managed=false` on devices being drained. This removes them from new allocation searches while preserving existing claims.
2. Delete each in-use BareMetalInstance and let the BMF finalizer release its NetBox claim. Wait until its NetBox device is no longer `active`, `osac_instance_id` is empty, and its `netbox-<id>` BMH and managed BMC Secret are gone.
3. Only after the pool has no active claims, disable `bmf.netbox.enabled` or switch inventory backends. Do not use backend switching to release devices.
4. To re-enable the pool, verify endpoint, token, CA, custom fields, and system Secrets. Set `osac_managed=true` only on devices confirmed unclaimed and `staged`, then enable the NetBox backend.

`bmaas-netbox-ci` is for fresh CI installs. Do not switch an existing release to it: its bootstrap does not migrate an existing NetBox database or inventory.

## Recovery and diagnostics

- **No matching hosts:** check the NetBox device status, `osac_managed`, empty `osac_instance_id`, and exact custom-field selector names and values. A no-host result or filter error can also mean a selector or pool custom field is absent or misnamed on `dcim.device`; BMF does not validate custom-field definitions at startup. The reported condition remains generic; the controller log includes the selector.
- **Device status `failed`:** repair the BMC address/MAC and the unique system Secret, then reset the NetBox device to `staged` only after confirming `osac_instance_id` is empty. Set `osac_managed=true` only if it should rejoin the pool. A wrong but non-empty BMC password can fail later at the BMH/BMO layer instead of triggering this quarantine.
- **Device status `active` with an owner or a stuck release:** do not reset status, clear the owner, remove the BareMetalInstance finalizer, or delete resources by hand. Let normal OSAC cleanup/reconciliation proceed; escalate inconsistent or unknown ownership through the approved support path.
- **API or TLS errors:** verify the origin URL, token, NetBox CA, NetBox account permissions, and Secret API issuer/CA settings. API failures do not mean the pool is empty.

Inspect objects without printing Secret data:

```bash
oc get baremetalinstance <instance> -n <instance-namespace> -o yaml
oc get baremetalhost netbox-<device-id> -n <bmh-namespace>
oc get secret netbox-<device-id>-bmc-secret -n <bmh-namespace> -o name
oc logs -n <bmf-namespace> <bmf-pod>
```

Treat operator logs as sensitive: they can include selector values and inventory host IDs. The NetBox client sanitizes API error details, but diagnostics should still be shared only through the approved support channel. Include the NetBox server version, OSAC chart/operator versions, device ID and status, BareMetalInstance condition, and sanitized relevant logs; never include tokens, BMC credentials, or CA private keys.

## References

- [NetBox inventory selection, credential resolution, claim, and release](../../../bare-metal-fulfillment-operator/internal/inventory/netbox.go)
- [NetBox API client, request headers, ETags, and endpoint validation](../../../bare-metal-fulfillment-operator/internal/netboxclient/client.go), [endpoint configuration](../../../bare-metal-fulfillment-operator/internal/netboxclient/config.go), and [client tests](../../../bare-metal-fulfillment-operator/internal/netboxclient/client_test.go)
- [BMF NetBox Helm values](../../../bare-metal-fulfillment-operator/charts/operator/values.yaml) and [inventory and Secret templates](../../../bare-metal-fulfillment-operator/charts/operator/templates/netbox-inventory-config.yaml)
- [OSAC umbrella chart values](../../../osac-installer/charts/osac/values.yaml) and [NetBox CI profile notes](../../../osac-installer/values/bmaas-netbox-ci/README.md)
