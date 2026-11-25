#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""Unit tests for the framework-agnostic name_resolver and its lookup wrappers."""

import pytest
from ansible.errors import AnsibleError
from ansible_collections.vastdata.vms.plugins.lookup import vast_name_to_id as vast_name_to_id_lookup
from ansible_collections.vastdata.vms.plugins.lookup import vast_target_cluster_to_remote_target_id as vast_target_lookup
from ansible_collections.vastdata.vms.plugins.module_utils.vast import name_resolver
from ansible_collections.vastdata.vms.plugins.module_utils.vast.errors import VastError


class FakeEndpoint:
    def __init__(self, endpoint):
        self.endpoint = endpoint

    def get(self, **params):
        FakeClient.endpoint = self.endpoint
        FakeClient.params = params
        return FakeClient.results


class FakeApi:
    def __getitem__(self, endpoint):
        return FakeEndpoint(endpoint)


class FakeClient:
    results = []
    endpoint = None
    params = None
    connection = None

    def __init__(self, connection):
        FakeClient.connection = connection
        self.api = FakeApi()


# --- name_to_id ------------------------------------------------------------


def test_resolves_tenant_name_to_id(monkeypatch):
    monkeypatch.setattr(name_resolver, "VastClient", FakeClient)
    FakeClient.results = [{"id": 7, "name": "default"}]

    result = name_resolver.name_to_id(
        "default",
        {"host": "vms.example.com", "token": "secret"},
        "tenants",
    )

    assert result == 7
    assert FakeClient.endpoint == "tenants"
    assert FakeClient.params == {"name": "default"}


def test_resolves_custom_id_field(monkeypatch):
    monkeypatch.setattr(name_resolver, "VastClient", FakeClient)
    FakeClient.results = [{"guid": "tenant-guid", "name": "default"}]

    result = name_resolver.name_to_id(
        "default",
        {"host": "vms.example.com", "token": "secret"},
        "tenants",
        id_field="guid",
    )

    assert result == "tenant-guid"


def test_accepts_dataprotection_vms_aliases(monkeypatch):
    monkeypatch.setattr(name_resolver, "VastClient", FakeClient)
    FakeClient.results = [{"id": 3, "name": "default"}]

    result = name_resolver.name_to_id(
        "default",
        {
            "vms_host": "v3115",
            "vms_username": "admin",
            "vms_password": "password",
            "validate_certs": False,
        },
        "tenants",
    )

    assert result == 3
    assert FakeClient.connection.host == "v3115"
    assert FakeClient.connection.username == "admin"
    assert FakeClient.connection.password == "password"
    assert FakeClient.connection.validate_certs is False


def test_fails_on_missing_auth():
    with pytest.raises(VastError):
        name_resolver.name_to_id("default", {"host": "vms.example.com"}, "tenants")


def test_fails_on_no_matches(monkeypatch):
    monkeypatch.setattr(name_resolver, "VastClient", FakeClient)
    FakeClient.results = []

    with pytest.raises(VastError):
        name_resolver.name_to_id(
            "missing",
            {"host": "vms.example.com", "token": "secret"},
            "tenants",
        )


def test_fails_on_multiple_matches(monkeypatch):
    monkeypatch.setattr(name_resolver, "VastClient", FakeClient)
    FakeClient.results = [
        {"id": 1, "name": "default"},
        {"id": 2, "name": "default"},
    ]

    with pytest.raises(VastError):
        name_resolver.name_to_id(
            "default",
            {"host": "vms.example.com", "token": "secret"},
            "tenants",
        )


def test_non_strict_returns_none_on_no_match(monkeypatch):
    monkeypatch.setattr(name_resolver, "VastClient", FakeClient)
    FakeClient.results = []

    result = name_resolver.name_to_id(
        "missing",
        {"host": "vms.example.com", "token": "secret"},
        "tenants",
        strict=False,
    )
    assert result is None


# --- target_cluster_to_remote_target_id ------------------------------------


def test_resolves_target_cluster_to_remote_target_id(monkeypatch):
    monkeypatch.setattr(name_resolver, "VastClient", FakeClient)
    FakeClient.results = [{"id": 42, "name": "v3115_v151"}]

    result = name_resolver.target_cluster_to_remote_target_id(
        "vast3115-var",
        "vast151-var",
        {"host": "vms.example.com", "token": "secret"},
        [{"cluster": "vast3115-var", "name": "v3115_v151", "leading_vip": "172.27.151.100"}],
        [{"cluster": "vast151-var", "role": "REPLICATION", "ip_ranges": [["172.27.151.100", "172.27.151.104"]]}],
    )

    assert result == 42
    assert FakeClient.endpoint == "nativereplicationremotetargets"
    assert FakeClient.params == {"name": "v3115_v151"}


def test_resolves_remote_target_id_via_explicit_target_cluster(monkeypatch):
    """A peer naming target_cluster directly matches without needing VIP ranges."""
    monkeypatch.setattr(name_resolver, "VastClient", FakeClient)
    FakeClient.results = [{"id": 7, "name": "src_dst"}]

    result = name_resolver.target_cluster_to_remote_target_id(
        "src",
        "dst",
        {"host": "vms.example.com", "token": "secret"},
        [{"cluster": "src", "name": "src_dst", "target_cluster": "dst"}],
        [],
    )

    assert result == 7


def test_remote_target_id_fails_when_no_peer_matches():
    with pytest.raises(VastError):
        name_resolver.target_cluster_to_remote_target_id(
            "src",
            "dst",
            {"host": "vms.example.com", "token": "secret"},
            [{"cluster": "src", "name": "src_other", "target_cluster": "elsewhere"}],
            [],
        )


def test_remote_target_id_fails_when_multiple_peers_match():
    """Ambiguous peers must error rather than silently pick one."""
    with pytest.raises(VastError):
        name_resolver.target_cluster_to_remote_target_id(
            "src",
            "dst",
            {"host": "vms.example.com", "token": "secret"},
            [
                {"cluster": "src", "name": "peer1", "target_cluster": "dst"},
                {"cluster": "src", "name": "peer2", "target_cluster": "dst"},
            ],
            [],
        )


def test_remote_target_id_requires_source_and_target():
    with pytest.raises(VastError):
        name_resolver.target_cluster_to_remote_target_id(
            "",
            "dst",
            {"host": "vms.example.com", "token": "secret"},
            [],
            [],
        )


class TestMatchingReplicationPeers:
    """Unit coverage for the stream/standby remote-target peer selection logic."""

    def test_matches_on_explicit_target_cluster(self):
        peers = [{"cluster": "src", "name": "p", "target_cluster": "dst"}]
        matches = name_resolver._matching_replication_peers("src", "dst", peers, [])
        assert matches == peers

    def test_matches_on_vip_range_membership(self):
        peers = [{"cluster": "src", "name": "p", "leading_vip": "10.0.0.5"}]
        vippools = [{"cluster": "dst", "role": "REPLICATION", "ip_ranges": [["10.0.0.1", "10.0.0.10"]]}]
        matches = name_resolver._matching_replication_peers("src", "dst", peers, vippools)
        assert matches == peers

    def test_skips_peers_from_other_source_cluster(self):
        peers = [{"cluster": "other", "name": "p", "target_cluster": "dst"}]
        assert name_resolver._matching_replication_peers("src", "dst", peers, []) == []

    def test_skips_vip_out_of_range(self):
        peers = [{"cluster": "src", "name": "p", "leading_vip": "10.0.1.5"}]
        vippools = [{"cluster": "dst", "role": "REPLICATION", "ip_ranges": [["10.0.0.1", "10.0.0.10"]]}]
        assert name_resolver._matching_replication_peers("src", "dst", peers, vippools) == []


class TestTargetClusterIpRanges:
    def test_collects_only_target_cluster_replication_pools(self):
        vippools = [
            {"cluster": "dst", "role": "REPLICATION", "ip_ranges": [["10.0.0.1", "10.0.0.10"]]},
            {"cluster": "dst", "role": "PROTOCOLS", "ip_ranges": [["10.0.1.1", "10.0.1.10"]]},
            {"cluster": "other", "role": "REPLICATION", "ip_ranges": [["10.0.2.1", "10.0.2.10"]]},
        ]
        ranges = name_resolver._target_cluster_ip_ranges("dst", vippools)
        assert ranges == [["10.0.0.1", "10.0.0.10"]]

    def test_includes_pools_without_role(self):
        vippools = [{"cluster": "dst", "ip_ranges": [["10.0.0.1", "10.0.0.10"]]}]
        assert name_resolver._target_cluster_ip_ranges("dst", vippools) == [["10.0.0.1", "10.0.0.10"]]


class TestIpInRanges:
    def test_matches_inside_range(self):
        assert name_resolver._ip_in_ranges("10.0.0.5", [["10.0.0.1", "10.0.0.10"]]) is True

    def test_rejects_outside_range(self):
        assert name_resolver._ip_in_ranges("10.0.0.11", [["10.0.0.1", "10.0.0.10"]]) is False

    def test_rejects_invalid_ip(self):
        assert name_resolver._ip_in_ranges("not-an-ip", [["10.0.0.1", "10.0.0.10"]]) is False

    def test_ignores_malformed_range(self):
        assert name_resolver._ip_in_ranges("10.0.0.5", [["10.0.0.1"]]) is False


# --- lookup plugin wrappers ------------------------------------------------


class TestVastNameToIdLookup:
    def test_passes_terms_and_kwargs_through(self, monkeypatch):
        captured = {}

        def fake_name_to_id(name, vms, endpoint, **kwargs):
            captured["args"] = (name, vms, endpoint)
            captured["kwargs"] = kwargs
            return 11

        monkeypatch.setattr(vast_name_to_id_lookup, "name_to_id", fake_name_to_id)

        result = vast_name_to_id_lookup.LookupModule().run(
            ["default", {"host": "h", "token": "t"}, "tenants"],
            id_field="guid",
        )

        assert result == [11]
        assert captured["args"] == ("default", {"host": "h", "token": "t"}, "tenants")
        assert captured["kwargs"]["id_field"] == "guid"

    def test_rejects_wrong_term_count(self):
        with pytest.raises(AnsibleError):
            vast_name_to_id_lookup.LookupModule().run(["only", "two"])

    def test_translates_vast_error(self, monkeypatch):
        def boom(*args, **kwargs):
            raise VastError("nope")

        monkeypatch.setattr(vast_name_to_id_lookup, "name_to_id", boom)

        with pytest.raises(AnsibleError):
            vast_name_to_id_lookup.LookupModule().run(["x", {}, "tenants"])


class TestVastTargetClusterLookup:
    def test_passes_terms_through(self, monkeypatch):
        captured = {}

        def fake_resolve(source, target, vms, peers, pools):
            captured["args"] = (source, target, vms, peers, pools)
            return 99

        monkeypatch.setattr(vast_target_lookup, "target_cluster_to_remote_target_id", fake_resolve)

        result = vast_target_lookup.LookupModule().run(["src", "dst", {"host": "h"}, [], []])

        assert result == [99]
        assert captured["args"] == ("src", "dst", {"host": "h"}, [], [])

    def test_rejects_wrong_term_count(self):
        with pytest.raises(AnsibleError):
            vast_target_lookup.LookupModule().run(["src", "dst"])

    def test_translates_vast_error(self, monkeypatch):
        def boom(*args, **kwargs):
            raise VastError("nope")

        monkeypatch.setattr(vast_target_lookup, "target_cluster_to_remote_target_id", boom)

        with pytest.raises(AnsibleError):
            vast_target_lookup.LookupModule().run(["src", "dst", {}, [], []])
