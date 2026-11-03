#!/usr/bin/env python3
"""Assign fixture addresses to MetalLB v1 Services in the Kind test cluster."""

import argparse
import json
import os
import subprocess
import time
from pathlib import Path


def kubectl(*args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["kubectl", *args],
        check=True,
        capture_output=True,
        text=True,
        timeout=10,
    )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--address", required=True)
    parser.add_argument("--stop-file", required=True, type=Path)
    parser.add_argument("--external-ip-uuid", required=True)
    parser.add_argument(
        "--service-name",
        default="",
        help="Optional exact Service name filter; UUID label match is always required.",
    )
    args = parser.parse_args()

    kubeconfig = os.environ.get("KUBECONFIG", "")
    expected_kubeconfig = Path(__file__).resolve().parents[3] / "kubeconfig-osac-test"
    if not kubeconfig or Path(kubeconfig).resolve() != expected_kubeconfig:
        raise SystemExit("MetalLB status fixture requires the dedicated osac-test kubeconfig")
    context = kubectl("config", "current-context").stdout.strip()
    if context != "kind-osac-test":
        raise SystemExit("MetalLB status fixture requires the kind-osac-test context")

    while not args.stop_file.exists():
        try:
            result = kubectl("get", "services", "-n", args.namespace, "-o", "json")
            services = json.loads(result.stdout).get("items", [])
            for service in services:
                metadata = service.get("metadata", {})
                labels = metadata.get("labels", {})
                ingress = service.get("status", {}).get("loadBalancer", {}).get("ingress", [])
                name_ok = not args.service_name or metadata.get("name") == args.service_name
                if (
                    name_ok
                    and labels.get("osac.openshift.io/externalip-uuid") == args.external_ip_uuid
                    and not ingress
                ):
                    patch = json.dumps(
                        {"status": {"loadBalancer": {"ingress": [{"ip": args.address}]}}}
                    )
                    kubectl(
                        "patch",
                        "service",
                        metadata["name"],
                        "-n",
                        args.namespace,
                        "--subresource=status",
                        "--type=merge",
                        "-p",
                        patch,
                    )
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired, json.JSONDecodeError):
            pass
        time.sleep(0.2)


if __name__ == "__main__":
    main()
