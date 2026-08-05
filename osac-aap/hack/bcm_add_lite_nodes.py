#!/usr/bin/env python3
"""Add Lite Nodes to BCM from a YAML inventory file.

Usage:
    bcm_add_lite_nodes.py --url https://bcm-head:8081 \
        --cert /path/to/cert.pem --key /path/to/key.pem \
        --inventory nodes.yml

See samples/bcm_lite_nodes_inventory.yml for inventory file format.
"""

import argparse
import json
import ssl
import sys
import uuid
from urllib.request import Request, urlopen

import yaml


def make_ssl_context(cert, key, *, verify=True):
    ctx = ssl.create_default_context()
    ctx.load_cert_chain(cert, key)
    if not verify:
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
    return ctx


def bcm_call(url, ssl_ctx, service, call, args=None):
    payload = json.dumps({
        "service": service,
        "call": call,
        "minify": True,
        "args": args or [],
    }).encode()

    req = Request(url + "/json", data=payload)
    req.add_header("Content-Type", "application/json")

    resp = urlopen(req, context=ssl_ctx, timeout=30)
    return json.loads(resp.read())


def get_devices(url, ssl_ctx):
    """Fetches all devices, returning (partition_uuid, hostname_to_device_map)."""
    devices = bcm_call(url, ssl_ctx, "cmdevice", "getDevices")
    if not isinstance(devices, list):
        msg = devices.get("errormessage", str(devices)) if isinstance(devices, dict) else str(devices)
        raise RuntimeError(f"BCM getDevices failed: {msg}")
    partition = None
    existing = {}
    for d in devices:
        if d.get("partition") and not partition:
            partition = d["partition"]
        existing[d.get("hostname", d.get("uuid", "unknown"))] = d
    if not partition:
        raise RuntimeError("No partition found in BCM — is the cluster initialized?")
    return partition, existing


def get_networks(url, ssl_ctx):
    nets = bcm_call(url, ssl_ctx, "cmnet", "getNetworks")
    if not isinstance(nets, list):
        msg = nets.get("errormessage", str(nets)) if isinstance(nets, dict) else str(nets)
        raise RuntimeError(f"BCM getNetworks failed: {msg}")
    return {n["name"]: n["uuid"] for n in nets}


def build_notes(node):
    """Builds key=value notes string. Inverse of parse_notes() in bcm.py filter."""
    parts = []
    if node.get("resource_class"):
        parts.append(f"resource_class={node['resource_class']}")
    return "\n".join(parts)


def add_lite_node(url, ssl_ctx, node, partition, network_uuid=None):
    device = {
        "baseType": "Device",
        "childType": "LiteNode",
        "hostname": node["hostname"],
        "mac": node["mac"],
        "uuid": str(uuid.uuid4()),
        "partition": partition,
    }

    notes = build_notes(node)
    if notes:
        device["notes"] = notes

    interfaces = []
    if node.get("ip") and network_uuid:
        interfaces.append({
            "baseType": "NetworkInterface",
            "childType": "NetworkPhysicalInterface",
            "name": "BOOTIF",
            "ip": node["ip"],
            "network": network_uuid,
            "uuid": str(uuid.uuid4()),
        })

    if node.get("bmc"):
        bmc = node["bmc"]
        if bmc.get("username") and bmc.get("password"):
            device["bmcSettings"] = {
                "baseType": "BMCSettings",
                "userName": bmc["username"],
                "password": bmc["password"],
                "userID": bmc.get("user_id", 2),
                "uuid": str(uuid.uuid4()),
            }
        if network_uuid and bmc.get("ip"):
            interfaces.append({
                "baseType": "NetworkInterface",
                "childType": "NetworkBmcInterface",
                "name": bmc.get("interface_name", "rf0"),
                "ip": bmc["ip"],
                "network": network_uuid,
                "uuid": str(uuid.uuid4()),
            })

    if interfaces:
        device["interfaces"] = interfaces

    result = bcm_call(url, ssl_ctx, "cmdevice", "addLiteNode", [device])

    if result.get("success") is False:
        errors = [v.get("message", str(v)) for v in result.get("validation", [])]
        error_msg = "; ".join(errors) if errors else result.get("errormessage", "unknown error")
        print(f"  Failed to add {node['hostname']}: {error_msg}", file=sys.stderr)
        return None

    bmc_info = ""
    if node.get("bmc", {}).get("ip"):
        bmc_info = f", bmc={node['bmc']['ip']}"
    rc_info = ""
    if node.get("resource_class"):
        rc_info = f", resource_class={node['resource_class']}"
    print(f"  Added {node['hostname']} (uuid: {device['uuid']}{bmc_info}{rc_info})")
    return device["uuid"]


def main():
    parser = argparse.ArgumentParser(description="Add Lite Nodes to BCM")
    parser.add_argument("--url", required=True, help="BCM API URL (e.g. https://bcm-head:8081)")
    parser.add_argument("--cert", required=True, help="Client certificate path")
    parser.add_argument("--key", required=True, help="Client key path")
    parser.add_argument("--inventory", required=True, help="YAML inventory file")
    parser.add_argument("--no-verify-certs", action="store_true",
                        help="Disable TLS certificate verification (insecure, for lab/debug only)")
    args = parser.parse_args()

    if not args.url.startswith("https://"):
        print("Error: --url must use https://", file=sys.stderr)
        sys.exit(1)

    ssl_ctx = make_ssl_context(args.cert, args.key, verify=not args.no_verify_certs)

    with open(args.inventory) as f:
        inventory = yaml.safe_load(f)

    nodes = inventory.get("nodes", [])
    if not nodes:
        print("No nodes in inventory file", file=sys.stderr)
        sys.exit(1)

    print(f"Loading BCM state from {args.url}...")
    partition, existing = get_devices(args.url, ssl_ctx)
    networks = get_networks(args.url, ssl_ctx)

    print(f"Partition: {partition}")
    print(f"Networks: {', '.join(networks.keys())}")
    print(f"Existing devices: {', '.join(existing.keys())}")
    print()

    network_name = inventory.get("network", "internalnet")
    network_uuid = networks.get(network_name)
    if not network_uuid:
        print(f"Warning: network '{network_name}' not found, BMC interfaces will be skipped",
              file=sys.stderr)

    for node in nodes:
        hostname = node["hostname"]
        if hostname in existing:
            print(f"Skipping {hostname} — already exists as {existing[hostname].get('childType')}")
            continue

        print(f"Adding {hostname}...")
        add_lite_node(args.url, ssl_ctx, node, partition, network_uuid)

    print("\nDone.")


if __name__ == "__main__":
    main()
