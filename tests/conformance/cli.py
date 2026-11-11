"""Command-line entry point for the OSAC conformance suite."""

from __future__ import annotations

import argparse
import sys
from collections.abc import Sequence
from pathlib import Path

from tests.conformance.config import Config, load_config
from tests.conformance.errors import CommandLineError, ConfigError

_DEFAULT_AREAS = Path(__file__).with_name("areas")


class _Parser(argparse.ArgumentParser):
    def error(self, message: str) -> None:
        raise CommandLineError(message)


def _parser() -> argparse.ArgumentParser:
    parent = _Parser(add_help=False)
    parent.add_argument("--config", default="/config/conformance.yaml")
    parent.add_argument("--results", default="/results")
    parent.add_argument("--areas", default=str(_DEFAULT_AREAS))

    parser = _Parser(prog="osac-conformance", add_help=True)
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("list", parents=[parent], add_help=True)
    run = sub.add_parser("run", parents=[parent], add_help=True)
    run.add_argument("--plan")
    run.add_argument("--keep", action="store_true")
    return parser


def _load(args: argparse.Namespace) -> Config:
    from tests.conformance.area import load_areas

    areas = load_areas(Path(args.areas))
    return load_config(Path(args.config), area_names=areas)


def main(argv: Sequence[str] | None = None) -> int:
    parser = _parser()
    try:
        args = parser.parse_args(argv)
    except CommandLineError as exc:
        print(f"osac-conformance: {exc}", file=sys.stderr)
        return 2

    try:
        _load(args)
    except ConfigError as exc:
        for check in exc.checks:
            print(f"osac-conformance: {check}", file=sys.stderr)
        return 2

    print(f"osac-conformance: {args.command} is not implemented yet", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
