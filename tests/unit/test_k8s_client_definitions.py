from __future__ import annotations

import ast
import inspect
import textwrap

from tests.e2e.core.k8s_client import K8sClient


def test_cluster_order_status_has_single_implementation() -> None:
    class_definition = ast.parse(textwrap.dedent(inspect.getsource(K8sClient))).body[0]
    implementations = [
        node
        for node in class_definition.body
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name == "get_cluster_order_status"
    ]
    assert len(implementations) == 1, "Duplicate methods silently override the earlier implementation"
