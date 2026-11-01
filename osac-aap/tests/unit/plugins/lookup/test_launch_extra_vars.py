from __future__ import annotations

import json

from ansible import context
from ansible.module_utils.common.collections import ImmutableDict
from ansible.parsing.dataloader import DataLoader
from ansible.utils import vars as ansible_vars

import launch_extra_vars as plugin


def _names(monkeypatch, *extra_vars):
    monkeypatch.setattr(context, "CLIARGS", ImmutableDict(extra_vars=tuple(extra_vars)))
    # ansible-core caches the parsed extra vars on the function per process.
    monkeypatch.setattr(ansible_vars.load_extra_vars, "extra_vars", None, raising=False)
    lookup = plugin.LookupModule(loader=DataLoader())
    return lookup.run([])


def test_lists_only_names_sorted(monkeypatch, tmp_path):
    job = tmp_path / "extravars"
    job.write_text(json.dumps({"osac_job_vars": {"task": "create_subnet"}, "awx_job_id": 7}))
    assert _names(monkeypatch, f"@{job}") == ["awx_job_id", "osac_job_vars"]


def test_merges_every_extra_vars_argument(monkeypatch, tmp_path):
    job = tmp_path / "extravars"
    job.write_text(json.dumps({"osac_job_vars": {}}))
    names = _names(monkeypatch, f"@{job}", "netris_url=https://example.invalid")
    assert names == ["netris_url", "osac_job_vars"]


def test_no_extra_vars(monkeypatch):
    assert _names(monkeypatch) == []
