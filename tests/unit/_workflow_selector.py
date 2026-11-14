"""Safe evaluator for the GitHub Actions selectors checked by workflow tests."""

from __future__ import annotations

import ast
from collections.abc import Mapping


def _evaluate(node: ast.AST) -> object:
    """Evaluate the selector's string, comparison, and short-circuit operators only."""
    if isinstance(node, ast.Expression):
        return _evaluate(node.body)
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        return node.value
    if isinstance(node, ast.BoolOp) and isinstance(node.op, (ast.And, ast.Or)):
        stop_on = isinstance(node.op, ast.Or)
        value: object = None
        for operand in node.values:
            value = _evaluate(operand)
            if bool(value) is stop_on:
                return value
        return value
    if isinstance(node, ast.Compare) and len(node.ops) == 1 and isinstance(node.ops[0], (ast.Eq, ast.NotEq)):
        same = _evaluate(node.left) == _evaluate(node.comparators[0])
        return same if isinstance(node.ops[0], ast.Eq) else not same
    raise ValueError(f"unsupported in a selector: {ast.dump(node)[:80]}")


def evaluate_selector(expression: str, values: Mapping[str, str]) -> str:
    """Substitute workflow contexts and evaluate a selector without executing YAML text."""
    code = expression.removeprefix("${{").removesuffix("}}").strip()
    for name, value in values.items():
        code = code.replace(name, repr(value))
    code = " ".join(code.replace("&&", " and ").replace("||", " or ").split())
    return str(_evaluate(ast.parse(code, mode="eval")))
