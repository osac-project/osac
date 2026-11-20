# Disconnected SNO-VM Network & DNS Architecture 

**Project:** OSAC Single Node OpenShift on KubeVirt  
**Host Cluster:** sno-n45-u19.pool.se-lab.eng.rdu2.dc.redhat.com (OCP 4.22)  
**Guest Cluster:** osac.pool.se-lab.eng.rdu2.dc.redhat.com (OCP 4.22.6)  
**Date:** 2026-09-22  
**Status:** ✅ Deployed and Verified  
- [Disconnected SNO-VM Network \& DNS Architecture](#disconnected-sno-vm-network--dns-architecture)
  - [1. Executive Summary](#1-executive-summary)
  - [2. Network Topology](#2-network-topology)
    - [2.1 Architecture Overview](#21-architecture-overview)
    - [2.2 Network Diagram](#22-network-diagram)
    - [2.3 VM NIC Configuration](#23-vm-nic-configuration)
  - [3. Domain Naming Decision](#3-domain-naming-decision)
    - [3.1 Options Evaluated](#31-options-evaluated)
    - [3.2 Wildcard Hijack Explanation](#32-wildcard-hijack-explanation)
    - [3.3 Final Domain Configuration](#33-final-domain-configuration)
  - [4. IP Plane Architecture](#4-ip-plane-architecture)
    - [4.1 Network Planes](#41-network-planes)
    - [4.2 IP Assignments on br-sno](#42-ip-assignments-on-br-sno)
    - [4.3 Strict Rules](#43-strict-rules)
  - [5. DNS Architecture](#5-dns-architecture)
    - [5.1 DNS Server: dnsmasq on bastion-vm](#51-dns-server-dnsmasq-on-bastion-vm)
    - [5.2 The no-resolv Loop Prevention](#52-the-no-resolv-loop-prevention)
    - [5.3 Bastion /etc/hosts](#53-bastion-etchosts)
    - [5.4 Two Separate DNS Paths](#54-two-separate-dns-paths)
    - [5.5 Host DNS (DO NOT MODIFY)](#55-host-dns-do-not-modify)
    - [5.6 Corporate DNS](#56-corporate-dns)
  - [6. VM Specification](#6-vm-specification)
    - [6.1 osac-sno-vm Hardware](#61-osac-sno-vm-hardware)
    - [6.2 Storage](#62-storage)
    - [6.3 Network Interface](#63-network-interface)
    - [6.4 Boot Sequence](#64-boot-sequence)
  - [7. Install Configuration](#7-install-configuration)
    - [7.1 install-config.yaml Key Fields](#71-install-configyaml-key-fields)
    - [7.2 Network CIDR Shift Rationale](#72-network-cidr-shift-rationale)
    - [7.3 Image Digest Sources](#73-image-digest-sources)
  - [8. Agent Configuration](#8-agent-configuration)
    - [8.1 agent-config.yaml Key Fields](#81-agent-configyaml-key-fields)
    - [8.2 Critical nmstate Details](#82-critical-nmstate-details)
  - [9. AAP Boundary](#9-aap-boundary)
    - [9.1 Separation of Concerns](#91-separation-of-concerns)
    - [9.2 Key Boundary Rules](#92-key-boundary-rules)
  - [10. Laptop Access](#10-laptop-access)
    - [10.1 Network Access via sshuttle](#101-network-access-via-sshuttle)
    - [10.2 Local /etc/hosts](#102-local-etchosts)
    - [10.3 Verification](#103-verification)
  - [11. Validation Results](#11-validation-results)
    - [11.1 Pre-Deployment Simulation (6 Tests)](#111-pre-deployment-simulation-6-tests)
    - [11.2 Live Deployment Verification](#112-live-deployment-verification)
  - [12. Known Issues \& Caveats](#12-known-issues--caveats)
    - [12.1 OSAC-5005: macvlan on OVS br-ex Incompatibility](#121-osac-5005-macvlan-on-ovs-br-ex-incompatibility)
    - [12.2 OSAC-5002: DNS Resolution Gap in InfraEnv Ignition](#122-osac-5002-dns-resolution-gap-in-infraenv-ignition)
    - [12.3 OSAC-3828: OVN EVPN Long-Term Fix](#123-osac-3828-ovn-evpn-long-term-fix)
    - [12.4 OSAC-5239: Linux Bridge + Multus Confirmation](#124-osac-5239-linux-bridge--multus-confirmation)
    - [12.5 OSAC-4882: Agent-Based KubeVirt VM Deployment Spike](#125-osac-4882-agent-based-kubevirt-vm-deployment-spike)
    - [12.6 General Caveats](#126-general-caveats)
  - [13. Deployed Configuration Files](#13-deployed-configuration-files)
    - [13.1 dnsmasq Configuration](#131-dnsmasq-configuration)
    - [13.2 Network Attachment Definition (NAD)](#132-network-attachment-definition-nad)
    - [13.3 Bastion /etc/hosts](#133-bastion-etchosts)
    - [13.4 CoreDNS Forward Zone Patch (Post-Install, Optional)](#134-coredns-forward-zone-patch-post-install-optional)
    - [13.5 Linux Bridge Creation on Host (One-Time Setup)](#135-linux-bridge-creation-on-host-one-time-setup)

---

## 1. Executive Summary

This document describes the complete network and DNS architecture for deploying a **Single Node OpenShift (SNO) virtual machine** using the **Agent-based Installer** on an existing OCP 4.22 OpenShiftVirt host cluster.

**What was deployed:**
- A fully functional OCP 4.22.6 SNO cluster running as a KubeVirt VM (`osac-sno-vm`) on host `sno-n45-u19`
- Supporting infrastructure: bastion VM (DNS, install orchestration) and registry VM (disconnected mirror)
- Pure L2 network isolation via a software-defined Linux bridge (`br-sno`) with Multus secondary NICs

**Why this approach:**
- The host cluster's OVN-Kubernetes pod network cannot provide the static IPs, stable MACs, and direct L2 adjacency required by the Agent-based Installer
- A dedicated bridge network (`192.168.100.0/24`) provides deterministic networking without modifying any host-level configuration (no MCO, no iptables, no NMState)

**Outcome:**
- All 6 pre-deployment validation tests passed
- Live deployment verified: node Ready, all ClusterOperators healthy, API responding, registry reachable, DNS resolving from the node and the application pods, laptop `oc login` & web ui login successful.

---

## 2. Network Topology

### 2.1 Architecture Overview

The solution uses **Pure L2 Multus Isolation** — a software-only Linux bridge on the host node with zero physical NIC dependencies and zero host-level firewall modifications.

### 2.2 Network Diagram

```
┌─────────────────────────────────────────────────────────────────────────┐
│  HOST: sno-n45-u19.pool.se-lab.eng.rdu2.dc.redhat.com  (10.6.76.15)   │
│  OCP 4.22 Single-Node OpenShift                                        │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐  │
│  │  Linux Bridge: br-sno  (192.168.100.1/24)                        │  │
│  │  Software-only — NO physical NIC slaved                          │  │
│  │                                                                   │  │
│  │  ┌─────────────┐  ┌──────────────┐  ┌─────────────────────────┐  │  │
│  │  │ bastion-vm  │  │ registry-vm  │  │  osac-sno-vm            │  │  │
│  │  │             │  │              │  │                         │  │  │
│  │  │ eth1:       │  │ eth1:        │  │ enp1s0:                   │  │  │
│  │  │ 192.168.    │  │ 192.168.     │  │ 192.168.100.10          │  │  │
│  │  │ 100.2       │  │ 100.3        │  │ MAC: 52:54:00:10:09:83  │  │  │
│  │  │ (bridge)    │  │ (bridge)     │  │ (bridge)                │  │  │
│  │  │             │  │              │  │                         │  │  │
│  │  │ eth0:       │  │ eth0:        │  │ [single NIC only]       │  │  │
│  │  │ 10.0.2.x    │  │ 10.0.2.x    │  │                         │  │  │
│  │  │ (masq/NAT)  │  │ (masq/NAT)  │  │                         │  │  │
│  │  └─────────────┘  └──────────────┘  └─────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────────────────┘  │
│                                                                         │
│  NAD: br-lab-nad  →  bridge CNI on br-sno                              │
│       macspoofchk: false  |  promiscMode: true                         │
│                                                                         │
│  OVN Pod Network: 10.128.0.0/14  (virt-launcher pods)                  │
│  ClusterIP:       172.30.0.0/16  (K8s services)                        │
└─────────────────────────────────────────────────────────────────────────┘
```

### 2.3 VM NIC Configuration

| VM | eth0 (Primary) | eth1 (Secondary) | Role |
|---|---|---|---|
| **bastion-vm** | masquerade (10.0.2.x NAT) | bridge on br-sno (192.168.100.2) | DNS server, install orchestrator, bastion between internet and this cluster, Image Mirroring Host |
| **registry-vm** | masquerade (10.0.2.x NAT) | bridge on br-sno (192.168.100.3) | CentOS stream 9 based Red Hat Quay image mirror (port 8443) |
| **osac-sno-vm** | bridge on br-sno (192.168.100.10) | — | SNO cluster (single NIC) |

**Key design decisions:**
- bastion-vm and registry-vm have **dual NICs**: masquerade for outbound internet (pulling images, packages) + bridge for L2 adjacency with the SNO-VM
- osac-sno-vm has a **single NIC** on the bridge — it relies entirely on bastion for DNS and registry for images
- MAC address `52:54:00:10:09:83` is explicitly set in the VM spec and referenced in the agent-config for deterministic boot

---

## 3. Domain Naming Decision

### 3.1 Options Evaluated

| Criteria | Option A: Subdomain | Option B: Sibling Domain |
|---|---|---|
| **Cluster name** | `osac` | `osac` |
| **Base domain** | `sno-n45-u19.pool.se-lab.eng.rdu2.dc.redhat.com` | `pool.se-lab.eng.rdu2.dc.redhat.com` |
| **API endpoint** | `api.osac.sno-n45-u19.pool.se-lab...` | `api.osac.pool.se-lab...` |
| **Wildcard apps** | `*.apps.osac.sno-n45-u19.pool.se-lab...` | `*.apps.osac.pool.se-lab...` |
| **Wildcard hijack?** | ❌ **YES — FATAL** | ✅ No conflict |
| **Result** | **REJECTED** | **SELECTED** |

### 3.2 Wildcard Hijack Explanation

The host cluster (`sno-n45-u19`) already publishes a wildcard DNS record:

```
*.sno-n45-u19.pool.se-lab.eng.rdu2.dc.redhat.com  →  10.6.76.15
```

This wildcard matches **all** subdomains, including any child domains:

```
DNS Resolution Tree (Option A — BROKEN):

  pool.se-lab.eng.rdu2.dc.redhat.com
  └── sno-n45-u19                          ← Host cluster base domain
      ├── api.sno-n45-u19...               → 10.6.76.15  (HOST API ✓)
      ├── *.sno-n45-u19...                 → 10.6.76.15  (HOST wildcard)
      │
      └── osac.sno-n45-u19...              ← Guest would live HERE
          ├── api.osac.sno-n45-u19...      → 10.6.76.15  ✗ HIJACKED!
          │     (matches *.sno-n45-u19... wildcard)
          ├── api-int.osac.sno-n45-u19...  → 10.6.76.15  ✗ HIJACKED!
          └── *.apps.osac.sno-n45-u19...   → 10.6.76.15  ✗ HIJACKED!

  Result: All guest DNS queries resolve to the HOST IP (10.6.76.15)
          instead of the SNO-VM IP (192.168.100.10). Installation fails.
```

```
DNS Resolution Tree (Option B — CORRECT):

  pool.se-lab.eng.rdu2.dc.redhat.com       ← Guest base domain (SIBLING)
  ├── sno-n45-u19.pool.se-lab...           ← Host cluster (separate branch)
  │   ├── api.sno-n45-u19...               → 10.6.76.15  (HOST API ✓)
  │   └── *.sno-n45-u19...                 → 10.6.76.15  (HOST wildcard ✓)
  │
  └── osac.pool.se-lab...                  ← Guest cluster (SIBLING branch)
      ├── api.osac.pool.se-lab...          → 192.168.100.10  ✓ CORRECT
      ├── api-int.osac.pool.se-lab...      → 192.168.100.10  ✓ CORRECT
      └── *.apps.osac.pool.se-lab...       → 192.168.100.10  ✓ CORRECT

  Result: Guest DNS is in a completely separate branch of the DNS tree.
          The host wildcard cannot intercept it.
```

### 3.3 Final Domain Configuration

| Record | FQDN | Target IP |
|---|---|---|
| API | `api.osac.pool.se-lab.eng.rdu2.dc.redhat.com` | 192.168.100.10 |
| API-INT | `api-int.osac.pool.se-lab.eng.rdu2.dc.redhat.com` | 192.168.100.10 |
| Apps Wildcard | `*.apps.osac.pool.se-lab.eng.rdu2.dc.redhat.com` | 192.168.100.10 |

---

## 4. IP Plane Architecture

### 4.1 Network Planes

| Plane | CIDR | Purpose | Used by SNO-VM? |
|---|---|---|---|
| **br-sno bridge** | `192.168.100.0/24` | L2 segment for bastion, registry, SNO-VM | ✅ Yes — primary network |
| **ClusterIP** | `172.30.0.0/16` | K8s service VIPs (host cluster) | ❌ No — never routable from SNO-VM |
| **OVN pod network** | `10.128.0.0/14` | Pod IPs (virt-launcher pods on host) | ❌ No — internal to host OVN |
| **KubeVirt NAT** | `10.0.2.0/24` | Guest-side masquerade NICs | ❌ No — SNO-VM uses bridge only |

### 4.2 IP Assignments on br-sno

| IP Address | Host | Role |
|---|---|---|
| `192.168.100.1` | Host node (br-sno interface) | Bridge gateway |
| `192.168.100.2` | bastion-vm eth1 | DNS server, install orchestrator |
| `192.168.100.3` | registry-vm eth1 | OCP image mirror |
| `192.168.100.10` | osac-sno-vm eth0 | SNO cluster node |

### 4.3 Strict Rules

> **RULE 1:** The SNO-VM's `dns-resolver` in nmstate MUST be `192.168.100.2` (bastion). **NEVER** `192.168.100.1` (host bridge — not a DNS server). **NEVER** `172.30.0.10` (ClusterIP — not routable from bridge).

> **RULE 2:** The SNO-VM has NO masquerade NIC. It exists exclusively on the `192.168.100.0/24` bridge segment. All DNS, registry, and external access flows through bastion.

> **RULE 3:** The host's `192.168.100.1` is a bridge endpoint only. It does NOT run DNS, DHCP, or NAT for the bridge network.

> **RULE 4:** Guest cluster networks MUST be shifted from defaults to avoid collision with host cluster:
> - clusterNetwork: `10.132.0.0/14` (not `10.128.0.0/14`)
> - serviceNetwork: `172.31.0.0/16` (not `172.30.0.0/16`)

---

## 5. DNS Architecture

### 5.1 DNS Server: dnsmasq on bastion-vm

The bastion-vm runs dnsmasq to serve DNS for the `192.168.100.0/24` bridge network. It is the **sole DNS authority** for the SNO-VM during installation.

**Configuration file:** `/etc/dnsmasq.d/osac-sno.conf`

```ini
# Listen on bridge interface only (eth1) and loopback
interface=eth1
listen-address=127.0.0.1,192.168.100.2

# CRITICAL: Do not read /etc/resolv.conf for upstream servers
# Without this, dnsmasq reads resolv.conf which has 172.30.0.10
# (CoreDNS), creating a forwarding loop
no-resolv

# OCP cluster DNS records (all point to SNO-VM IP)
address=/api.osac.pool.se-lab.eng.rdu2.dc.redhat.com/192.168.100.10
address=/api-int.osac.pool.se-lab.eng.rdu2.dc.redhat.com/192.168.100.10
address=/.apps.osac.pool.se-lab.eng.rdu2.dc.redhat.com/192.168.100.10

# Registry VM accessible by IP within br-sno, but also by service name
address=/registry-vm-svc.aap-portal-lab.svc.cluster.local/192.168.100.3

# Upstream DNS for everything else (corporate DNS)
server=10.6.73.2
```

### 5.2 The no-resolv Loop Prevention

This is the most critical DNS design decision. Without `no-resolv`, a DNS forwarding loop occurs:

```
WITHOUT no-resolv (BROKEN):

  SNO-VM (192.168.100.10)
    │  Query: api.osac.pool.se-lab...
    ▼
  dnsmasq (192.168.100.2)
    │  dnsmasq reads /etc/resolv.conf → finds 172.30.0.10
    │  Forwards unknown queries to 172.30.0.10
    ▼
  CoreDNS (172.30.0.10) — host cluster DNS
    │  No zone match → forwards to upstream
    │  Upstream configured as... bastion pod? → LOOP
    ▼
  ∞ LOOP or SERVFAIL
```

```
WITH no-resolv (CORRECT):

  SNO-VM (192.168.100.10)
    │  Query: api.osac.pool.se-lab...
    ▼
  dnsmasq (192.168.100.2)
    │  Matches address= record → returns 192.168.100.10
    │  OR: no match → forwards to server=10.6.73.2 (corporate DNS)
    ▼
  Corporate DNS (10.6.73.2)
    │  Resolves external names normally
    ▼
  Response returned to SNO-VM ✓
```

### 5.3 Bastion /etc/hosts

The bastion itself needs to resolve the SNO API endpoints for `openshift-install agent wait-for` commands. Since `wait-for` uses Go's net.Resolver which checks `/etc/hosts` before DNS:

```
# /etc/hosts on bastion-vm
192.168.100.10  api.osac.pool.se-lab.eng.rdu2.dc.redhat.com
192.168.100.10  api-int.osac.pool.se-lab.eng.rdu2.dc.redhat.com
```

### 5.4 Two Separate DNS Paths

It is essential to understand that the bastion has **two independent DNS paths**:

| Path | Source | Mechanism | Upstream |
|---|---|---|---|
| **Bastion's own lookups** | Processes on bastion (e.g., `openshift-install`, `curl`) | `/etc/hosts` first, then `/etc/resolv.conf` (172.30.0.10 → CoreDNS) | Host CoreDNS → corporate DNS |
| **dnsmasq serving SNO-VM** | Queries from 192.168.100.10 arriving on eth1 | dnsmasq `address=` records, then `server=10.6.73.2` | Corporate DNS directly (no CoreDNS) |

These paths MUST remain separate. The bastion's own `/etc/resolv.conf` points at `172.30.0.10` (host CoreDNS) — this is correct for the bastion as a pod on the host cluster. But dnsmasq must NOT use this upstream (hence `no-resolv` + explicit `server=10.6.73.2`).

### 5.5 Host DNS (DO NOT MODIFY)

The host node has its own dnsmasq configuration:

| File | Purpose | Action |
|---|---|---|
| `/etc/dnsmasq.d/single-node.conf` | Serves host cluster DNS (api.sno-n45-u19...) | **DO NOT MODIFY** |
| `/etc/resolv.conf` | `nameserver 10.6.76.15` (self), `10.6.73.2` (corporate) | **DO NOT MODIFY** |

The host's dnsmasq and the bastion's dnsmasq are completely independent — different processes, different machines, different zones.

### 5.6 Corporate DNS

| Server | IP | Role |
|---|---|---|
| Corporate DNS | `10.6.73.2` | Upstream for all non-cluster names |
| Host IP | `10.6.76.15` | Host's own IP — NOT a general-purpose DNS server |

> **WARNING:** `10.6.76.15` is the host node's IP. It runs dnsmasq for the HOST cluster only. Do NOT use it as upstream DNS for the guest cluster.

---

## 6. VM Specification

### 6.1 osac-sno-vm Hardware

| Resource | Value |
|---|---|
| **CPU** | 24 cores |
| **Memory** | 44Gi |
| **Machine type** | q35 |
| **Firmware** | UEFI (default for q35) |

### 6.2 Storage

| Disk | Size | Storage Class | Device | Boot Order | Purpose |
|---|---|---|---|---|---|
| rootdisk | 100Gi | lvms-vg1 | `/dev/vda` | 1 | RHCOS root filesystem |
| datadisk | 200Gi | lvms-vg1 | `/dev/vdb` | — | OSAC data (raw) |
| ISO CD-ROM | — | — | SATA | 2 | Agent installer ISO |

### 6.3 Network Interface

| Property | Value |
|---|---|
| **NIC count** | 1 (single) |
| **NIC type** | bridge |
| **Bridge NAD** | br-lab-nad |
| **MAC address** | `52:54:00:10:09:83` |
| **IP** | `192.168.100.10/24` (static via nmstate) |

### 6.4 Boot Sequence

1. VM created with `running: false` — allows MAC address verification before boot
2. Verify MAC matches agent-config.yaml: `virtctl console osac-sno-vm` or inspect VM spec
3. Start VM: `virtctl start osac-sno-vm`
4. VM boots from ISO (bootOrder: 2 for first boot), agent installer takes over
5. Agent writes RHCOS to `/dev/vda`, reboots from disk (bootOrder: 1)
6. Installation continues autonomously via agent

---

## 7. Install Configuration

### 7.1 install-config.yaml Key Fields

```yaml
apiVersion: v1
baseDomain: pool.se-lab.eng.rdu2.dc.redhat.com
metadata:
  name: osac

# platform: none — Agent-based Installer on bare-metal-like VM
platform:
  none: {}

networking:
  # Machine network matches br-sno bridge subnet
  machineNetwork:
    - cidr: 192.168.100.0/24

  # SHIFTED from default 10.128.0.0/14 to avoid host cluster collision
  clusterNetwork:
    - cidr: 10.132.0.0/14
      hostPrefix: 23

  # SHIFTED from default 172.30.0.0/16 to avoid host cluster collision
  serviceNetwork:
    - cidr: 172.31.0.0/16

  networkType: OVNKubernetes
```

### 7.2 Network CIDR Shift Rationale

| Network | Default | Shifted To | Reason |
|---|---|---|---|
| clusterNetwork | `10.128.0.0/14` | `10.132.0.0/14` | Host cluster uses `10.128.0.0/14` for its OVN pod network. Overlapping CIDRs would cause routing ambiguity. |
| serviceNetwork | `172.30.0.0/16` | `172.31.0.0/16` | Host cluster uses `172.30.0.0/16` for ClusterIP services. If the SNO-VM's internal services used the same range, packets could leak to the host. |

### 7.3 Image Digest Sources

OCP 4.22 uses `imageDigestSources` (NOT the deprecated `imageContentSources`):

```yaml
imageDigestSources:
  - source: quay.io/openshift-release-dev/ocp-release
    mirrors:
      - 192.168.100.3:8443/openshift/release-images
  - source: quay.io/openshift-release-dev/ocp-v4.0-art-dev
    mirrors:
      - 192.168.100.3:8443/openshift/release
```

**Registry port:** `8443` (TLS-enabled mirror registry on registry-vm)

---

## 8. Agent Configuration

### 8.1 agent-config.yaml Key Fields

```yaml
apiVersion: v1beta1
kind: AgentConfig
metadata:
  name: osac
rendezvousIP: 192.168.100.10

hosts:
  - hostname: osac-sno
    role: master
    interfaces:
      - name: enp1s0
        macAddress: "52:54:00:10:09:83"
    rootDeviceHints:
      deviceName: /dev/vda
    networkConfig:
      interfaces:
        - name: enp1s0
          type: ethernet
          state: up
          mac-address: "52:54:00:10:09:83"
          ipv4:
            enabled: true
            dhcp: false
            address:
              - ip: 192.168.100.10
                prefix-length: 24
          ipv6:
            enabled: false
      dns-resolver:
        config:
          server:
            - 192.168.100.2
      routes:
        config:
          - destination: 0.0.0.0/0
            next-hop-address: 192.168.100.1
            next-hop-interface: enp1s0
```

### 8.2 Critical nmstate Details

| Field | Value | Why |
|---|---|---|
| `dns-resolver.config.server` | `192.168.100.2` | Bastion dnsmasq — the ONLY DNS server reachable on the bridge |
| `routes.destination: 0.0.0.0/0` | `next-hop: 192.168.100.1` | Host bridge IP — provides default route for external traffic |
| `mac-address` | `52:54:00:10:09:83` | Must match VM spec exactly — agent uses MAC to identify the correct NIC |
| `rootDeviceHints.deviceName` | `/dev/vda` | Ensures RHCOS installs to the rootdisk PVC, not the datadisk |
| `dhcp: false` | Static IP | No DHCP server on br-sno — all IPs are statically assigned |

---

## 9. AAP Boundary

### 9.1 Separation of Concerns

```
┌──────────────────────────────────┐     ┌──────────────────────────────────┐
│         AAP RESPONSIBILITY       │     │    AGENT INSTALLER RESPONSIBILITY │
│                                  │     │                                  │
│  ● Create VM manifest (YAML)    │     │  ● Boot from ISO                 │
│  ● Attach rootdisk PVC          │     │  ● Apply nmstate network config  │
│  ● Attach datadisk PVC          │     │  ● Resolve DNS via bastion       │
│  ● Attach ISO CD-ROM            │     │  ● Pull images from registry     │
│  ● Configure bridge NIC + NAD   │     │  ● Write RHCOS to /dev/vda       │
│  ● Set MAC address              │     │  ● Bootstrap single-node cluster │
│  ● Power on VM (virtctl start)  │     │  ● Configure OVN-Kubernetes      │
│  ● DONE — no further action     │     │  ● Start all ClusterOperators    │
│                                  │     │  ● Report install complete       │
└──────────────────────────────────┘     └──────────────────────────────────┘
```

### 9.2 Key Boundary Rules

1. **AAP provisions the VM only** — it creates the KubeVirt manifest, attaches storage and network, and powers on the VM
2. **AAP NEVER queries `osac.*` endpoints** — it has no need to reach `api.osac.pool.se-lab...` during installation
3. **Agent installer operates autonomously** after VM boot — no external orchestrator interaction required
4. **CoreDNS patch is deferred to post-install** — the host CoreDNS does not need to know about `osac.*` during the install phase; a conditional forward zone can be added later if cross-cluster resolution is needed

---

## 10. Laptop Access

### 10.1 Network Access via sshuttle

The `192.168.100.0/24` network is not routable from the corporate LAN. Use sshuttle to create a transparent proxy through the host node:

```bash
sshuttle -r <username>@10.6.76.15 192.168.100.0/24
```

This routes all traffic for `192.168.100.0/24` through an SSH tunnel to the host, which has direct access to the `br-sno` bridge.

### 10.2 Local /etc/hosts

Add these entries to your laptop's `/etc/hosts` to resolve the SNO cluster endpoints:

```
# SNO-VM (osac) cluster on sno-n45-u19
192.168.100.10  api.osac.pool.se-lab.eng.rdu2.dc.redhat.com
192.168.100.10  console-openshift-console.apps.osac.pool.se-lab.eng.rdu2.dc.redhat.com
192.168.100.10  oauth-openshift.apps.osac.pool.se-lab.eng.rdu2.dc.redhat.com
```

### 10.3 Verification

```bash
# With sshuttle running and /etc/hosts configured:
oc login https://api.osac.pool.se-lab.eng.rdu2.dc.redhat.com:6443 \
  --username kubeadmin --password <from install dir>

# Open console in browser:
# https://console-openshift-console.apps.osac.pool.se-lab.eng.rdu2.dc.redhat.com
```

---

## 11. Validation Results

### 11.1 Pre-Deployment Simulation (6 Tests)

All tests executed in a workspace simulation environment before live deployment:

| # | Test | Description | Result |
|---|---|---|---|
| 1 | dnsmasq zones | Verify dnsmasq config resolves api, api-int, *.apps, and registry to correct IPs | ✅ PASS |
| 2 | Wildcard hijack proof | Confirm Option A domains are hijacked by host wildcard; Option B domains are not | ✅ PASS |
| 3 | CoreDNS forwarding | Verify CoreDNS on host can forward to corporate DNS without interfering with guest | ✅ PASS |
| 4 | Bastion /etc/hosts | Confirm bastion resolves api.osac... and api-int.osac... via /etc/hosts for wait-for | ✅ PASS |
| 5 | no-resolv loop prevention | Verify that with no-resolv, dnsmasq does not read /etc/resolv.conf (preventing DNS loop) | ✅ PASS |
| 6 | Manifest bundle | Validate install-config.yaml and agent-config.yaml are syntactically correct and consistent | ✅ PASS |

### 11.2 Live Deployment Verification

| Check | Method | Result |
|---|---|---|
| Node status | `oc get nodes` | ✅ 1 node Ready |
| ClusterOperators | `oc get co` | ✅ All healthy (Available=True, Degraded=False) |
| API endpoint | `curl -k https://api.osac.pool.se-lab...:6443/healthz` | ✅ Responding |
| Registry access | `curl -k https://192.168.100.3:8443/v2/_catalog` (from SNO-VM) | ✅ Reachable |
| DNS resolution | `dig api.osac.pool.se-lab... @192.168.100.2` (from SNO-VM) | ✅ Resolves to 192.168.100.10 |
| Laptop login | `oc login` via sshuttle | ✅ Successful |
| OCP version | `oc get clusterversion` | ✅ 4.22.6 |

---

## 12. Known Issues & Caveats

### 12.1 OSAC-5005: macvlan on OVS br-ex Incompatibility

- **Issue:** macvlan interfaces on OVS-managed bridges (br-ex) are unreliable in OCP due to OVN-Kubernetes flow rules
- **Impact on this deployment:** **NONE** — our solution uses a standalone Linux bridge (`br-sno`) with the `bridge` CNI plugin, not macvlan on OVS
- **Action:** No action required. This caveat is documented to prevent future confusion if someone suggests switching to macvlan

### 12.2 OSAC-5002: DNS Resolution Gap in InfraEnv Ignition

- **Issue:** Agent-based installer ignition can fail if DNS is not available at the exact moment the agent needs to resolve API endpoints
- **Impact on this deployment:** **Mitigated** — the bastion dnsmasq is running before the SNO-VM boots, and the nmstate config points the VM's resolver directly at bastion
- **Action:** Ensure bastion-vm is running and dnsmasq is healthy before starting osac-sno-vm

### 12.3 OSAC-3828: OVN EVPN Long-Term Fix

- **Issue:** Long-term plan for native EVPN-based cross-cluster networking in OVN-Kubernetes
- **Impact on this deployment:** **Irrelevant** — this is a future architectural direction, not applicable to manual lab deployments
- **Action:** None. When EVPN support lands, it may obsolete the manual bridge approach for production

### 12.4 OSAC-5239: Linux Bridge + Multus Confirmation

- **Issue/Outcome:** Confirmed that standard Linux bridge with Multus secondary NIC works reliably for KubeVirt VM networking
- **Impact:** **Positive** — validates our chosen architecture

### 12.5 OSAC-4882: Agent-Based KubeVirt VM Deployment Spike

- **Issue/Outcome:** Original spike that explored and validated the Agent-based Installer approach for deploying OCP on KubeVirt VMs
- **Impact:** This document is the networking deliverable from that spike

### 12.6 General Caveats

| Caveat | Detail |
|---|---|
| NMState Operator | Absent on host — all bridge configuration is manual (NNCP not available) |
| No DHCP on br-sno | All IPs are static; there is no DHCP server on the bridge |
| No outbound NAT for SNO-VM | The SNO-VM has no masquerade NIC; it cannot reach the internet directly. All external traffic (DNS, NTP, etc.) must go through bastion or be pre-mirrored in the registry |
| MAC must match | agent-config MAC must exactly match VM spec MAC — any mismatch causes the agent to fail to identify the NIC |
| Boot order matters | First boot: ISO (bootOrder: 2 means "try disk first, fall through to ISO" — the disk is blank so ISO boots). After install: disk boots first (RHCOS) |
| Storage Class matters | Host SNO should have Read-Write StorageClass devoid of the 'WaitForFirstConsumer' . Else, it blocks the standard method of ISO file import as well as VM Snapshotting|

---

## 13. Deployed Configuration Files

### 13.1 dnsmasq Configuration

**File:** `/etc/dnsmasq.d/osac-sno.conf` on bastion-vm

```ini
interface=eth1
listen-address=127.0.0.1,192.168.100.2
no-resolv

address=/api.osac.pool.se-lab.eng.rdu2.dc.redhat.com/192.168.100.10
address=/api-int.osac.pool.se-lab.eng.rdu2.dc.redhat.com/192.168.100.10
address=/.apps.osac.pool.se-lab.eng.rdu2.dc.redhat.com/192.168.100.10
address=/registry-vm-svc.aap-portal-lab.svc.cluster.local/192.168.100.3

server=10.6.73.2
```

### 13.2 Network Attachment Definition (NAD)

```yaml
apiVersion: k8s.cni.cncf.io/v1
kind: NetworkAttachmentDefinition
metadata:
  name: br-lab-nad
  namespace: aap-portal-lab
spec:
  config: |
    {
      "cniVersion": "0.3.1",
      "name": "br-lab",
      "type": "bridge",
      "bridge": "br-sno",
      "macspoofchk": false,
      "promiscMode": true,
      "ipam": {}
    }
```

**Key NAD settings:**
- `macspoofchk: false` — allows VMs to use custom MAC addresses (required for static MAC assignment)
- `promiscMode: true` — allows the bridge to forward traffic for all MACs (required for L2 adjacency between VMs)
- `ipam: {}` — no IPAM; all IP addresses are statically configured via nmstate in agent-config

### 13.3 Bastion /etc/hosts

```
# /etc/hosts on bastion-vm
127.0.0.1   localhost localhost.localdomain
::1         localhost localhost.localdomain

# SNO-VM API endpoints (for openshift-install agent wait-for)
192.168.100.10  api.osac.pool.se-lab.eng.rdu2.dc.redhat.com
192.168.100.10  api-int.osac.pool.se-lab.eng.rdu2.dc.redhat.com
```

### 13.4 CoreDNS Forward Zone Patch (Post-Install, Optional)

To enable resolution of `osac.*` endpoints from the host cluster's pods, apply this CoreDNS ConfigMap patch:

```bash
oc edit configmap dns-default -n openshift-dns
```

Add this server block to the Corefile:

```
osac.pool.se-lab.eng.rdu2.dc.redhat.com:5353 {
    forward . 192.168.100.2
    errors
    bufsize 1232
}
```

> **Note:** This patch is **deferred to post-install**. It is NOT required during the Agent-based installation. Apply it only when cross-cluster service resolution is needed from host-cluster pods.

### 13.5 Linux Bridge Creation on Host (One-Time Setup)

```bash
# Create the bridge (survives reboot via NetworkManager)
nmcli connection add type bridge ifname br-sno con-name br-sno \
  ipv4.addresses 192.168.100.1/24 ipv4.method manual \
  ipv6.method disabled

nmcli connection up br-sno
```

> **Note:** No physical NIC is slaved to this bridge. It is a software-only L2 segment used exclusively by KubeVirt VMs via Multus.

---

