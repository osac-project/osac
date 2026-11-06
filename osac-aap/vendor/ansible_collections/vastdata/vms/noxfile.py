"""Nox sessions for the vastdata.vms Ansible collection.

Run from the collection root (``ansible_collections/vastdata/vms``). The
collection uses a self-contained REST client, so the only runtime Python
dependency is ``requests``; tests additionally use ``requests-mock``.
"""

import os

import nox

PYTHON_VERSIONS = ["3.11", "3.12"]
ANSIBLE_VERSIONS = ["2.16", "2.17", "2.18"]


def _namespace_pythonpath():
    """Return the path that makes ``ansible_collections.vastdata.vms`` importable."""
    cwd = os.path.abspath(".")
    namespace_root = os.path.abspath(os.path.join(cwd, os.pardir, os.pardir, os.pardir))
    if os.path.isdir(os.path.join(namespace_root, "ansible_collections")):
        return namespace_root
    return None


@nox.session(python=PYTHON_VERSIONS)
def lint(session):
    """Run linting checks."""
    session.install("yamllint", "ruff", "ansible-lint", "ansible-core>=2.16")
    session.run("yamllint", "-c", ".yamllint", ".")
    session.run("ruff", "check", "plugins/")
    session.run("ansible-lint")


@nox.session(python=PYTHON_VERSIONS)
def unit(session):
    """Run unit tests with pytest."""
    session.install("pytest", "pytest-cov", "ansible-core>=2.16", "requests", "requests-mock")
    env = {}
    namespace_root = _namespace_pythonpath()
    if namespace_root:
        env["PYTHONPATH"] = namespace_root
    session.run(
        "pytest",
        "tests/unit/",
        "-v",
        "--tb=short",
        "--cov=plugins",
        "--cov-report=term-missing",
        *session.posargs,
        env=env,
    )


@nox.session(python=PYTHON_VERSIONS)
@nox.parametrize("ansible", ANSIBLE_VERSIONS)
def sanity(session, ansible):
    """Run ansible-test sanity checks against multiple ansible-core versions."""
    session.install(f"ansible-core~={ansible}.0")
    session.run(
        "ansible-test",
        "sanity",
        "--python",
        session.python,
        "--color",
        "yes",
        "-v",
    )
