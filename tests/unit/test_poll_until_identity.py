"""Every poll_until call in the test tree names the resource it waits on.

poll_until's resource_kind and resource_id are required keyword parameters,
so a call that forgets them fails at runtime with a TypeError. This test
catches the same mistake at collection time, in every file under tests/,
and also rejects plain string literals for resource_kind: the kind must
come from WaitResourceKind so a typo or a stale literal cannot slip in
unnoticed.

Calls that forward their arguments with ``**`` (the fast-polling shims in
unit tests) cannot be checked statically; the required-parameter TypeError
covers them at runtime.
"""

from __future__ import annotations

import ast
from pathlib import Path

# Wrappers with the same required keyword pair: they call poll_until and
# every one of their callers must declare the identity too.
WAIT_CALLS = frozenset({"poll_until", "poll_or_fail", "wait_owned_gone"})


def call_name(call: ast.Call) -> str:
    fn = call.func
    return fn.id if isinstance(fn, ast.Name) else (fn.attr if isinstance(fn, ast.Attribute) else "<call>")


def wait_calls(tree: ast.AST) -> list[ast.Call]:
    return [node for node in ast.walk(tree) if isinstance(node, ast.Call) and call_name(node) in WAIT_CALLS]


def test_every_wait_call_declares_resource_identity() -> None:
    tests_root = Path(__file__).resolve().parents[1]
    problems: list[str] = []
    checked = 0

    for path in sorted(tests_root.rglob("*.py")):
        tree = ast.parse(path.read_text(), filename=str(path))
        for call in wait_calls(tree):
            checked += 1
            location = f"{path.relative_to(tests_root.parent)}:{call.lineno}"
            keywords = {kw.arg for kw in call.keywords if kw.arg is not None}
            forwards_kwargs = any(kw.arg is None for kw in call.keywords)
            if not forwards_kwargs:
                for param in ("resource_kind", "resource_id"):
                    if param not in keywords:
                        problems.append(f"{location}: {call_name(call)}() missing {param}")
            kind = next((kw.value for kw in call.keywords if kw.arg == "resource_kind"), None)
            if kind is not None and not isinstance(kind, (ast.Name, ast.Attribute)):
                problems.append(f"{location}: resource_kind must reference WaitResourceKind, got {ast.dump(kind)}")

    assert checked > 200, f"expected to scan the whole tests/ tree, found only {checked} wait calls"
    assert not problems, "wait calls without resource identity:\n" + "\n".join(problems)
