#!/usr/bin/env python3
"""Generate packaged copies of the protected Netris input contract."""

from __future__ import annotations

import argparse
import json
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "osac-aap/contracts/netris-protected-inputs.json"
DESTINATIONS = (
    ROOT
    / "osac-aap/collections/ansible_collections/netris/controller/plugins/module_utils/netris_protected_inputs.json",
    ROOT / "osac-aap/charts/aap/files/netris-protected-inputs.json",
    ROOT / "osac-installer/charts/osac/files/netris-protected-inputs.json",
    ROOT / "osac-operator/pkg/aap/netris_protected_inputs_contract.json",
)


def rendered_contract() -> str:
    contract = json.loads(SOURCE.read_text())
    return json.dumps(contract, indent=2, sort_keys=True) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()

    expected = rendered_contract()
    mismatches = []
    for destination in DESTINATIONS:
        if args.check:
            if not destination.exists() or destination.read_text() != expected:
                mismatches.append(destination)
            continue
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text(expected)

    if mismatches:
        for destination in mismatches:
            print(f"generated contract is out of date: {destination}")
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
