"""Merge per-phase Ginkgo JUnit reports into the single junit.xml of a CI log bundle.

The integration-test jobs run one Ginkgo invocation per test phase (or per
suite) and let Ginkgo write one JUnit file per phase through the make
targets' JUNIT_REPORT_DIR. This script concatenates those files into one
document so the bundle holds exactly one junit.xml at its root: every
<testsuite> of every phase file becomes a child of a fresh <testsuites>
root whose count attributes are the sums of the merged suites. Each merged
suite's name is prefixed with its source file's stem, because the phases of
one suite share Ginkgo's suite description and the file names are the only
record of which phase produced which results.

A missing or empty reports directory means the tests never ran: the script
prints a notice and exits successfully without writing the output file, so
a run that failed before its tests has no junit.xml for monitors to read. A
report file that does not parse (a killed run can leave a truncated one
behind) is skipped with a warning for the same reason: one broken phase
must not make the whole merged file unreadable.
"""

import argparse
import contextlib
import sys
import xml.etree.ElementTree as ET
from pathlib import Path

# Ginkgo writes these attributes on both <testsuites> and <testsuite>; the
# merged root keeps the sums of the merged suites.
COUNT_ATTRIBUTES = ("tests", "disabled", "skipped", "errors", "failures")
TIME_ATTRIBUTE = "time"


def phase_suites(report: Path) -> list[ET.Element] | None:
    """The <testsuite> elements of one report file, or None when unusable.

    Ginkgo writes a <testsuites> root with <testsuite> children; a file
    whose root is itself a <testsuite> (the shape some other reporters
    write) contributes that root."""
    try:
        root = ET.fromstring(report.read_text())
    except (OSError, ET.ParseError, UnicodeDecodeError) as error:
        print(f"warning: skipping unreadable JUnit report {report.name}: {error}", file=sys.stderr)
        return None
    if root.tag == "testsuite":
        return [root]
    if root.tag == "testsuites":
        suites = [child for child in root if child.tag == "testsuite"]
        if suites:
            return suites
    print(f"warning: skipping JUnit report {report.name} with no testsuite element", file=sys.stderr)
    return None


def merge_reports(reports: list[Path]) -> ET.Element:
    """One <testsuites> document holding every report's suites."""
    merged = ET.Element("testsuites")
    totals = {attribute: 0 for attribute in COUNT_ATTRIBUTES}
    total_time = 0.0
    for report in reports:
        suites = phase_suites(report)
        if suites is None:
            continue
        for suite in suites:
            suite.set("name", f"{report.stem} {suite.get('name', '')}".strip())
            for attribute in COUNT_ATTRIBUTES:
                with contextlib.suppress(ValueError):
                    totals[attribute] += int(suite.get(attribute) or 0)
            with contextlib.suppress(ValueError):
                total_time += float(suite.get(TIME_ATTRIBUTE) or 0)
            merged.append(suite)
    for attribute, count in totals.items():
        merged.set(attribute, str(count))
    merged.set(TIME_ATTRIBUTE, str(round(total_time, 3)))
    return merged


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("reports", type=Path, help="Directory holding the per-phase JUnit .xml files")
    parser.add_argument("output", type=Path, help="Path of the merged junit.xml to write")
    args = parser.parse_args()

    reports = sorted(args.reports.glob("*.xml")) if args.reports.is_dir() else []
    if not reports:
        print(f"No JUnit reports in {args.reports}: the tests did not run, no {args.output.name} written.")
        return 0
    merged = merge_reports(reports)
    if len(merged) == 0:
        print(f"No readable JUnit reports in {args.reports}: no {args.output.name} written.", file=sys.stderr)
        return 0
    args.output.parent.mkdir(parents=True, exist_ok=True)
    ET.indent(merged)
    ET.ElementTree(merged).write(args.output, encoding="utf-8", xml_declaration=True)
    print(f"Merged {len(merged)} JUnit suites from {args.reports} into {args.output}.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
