# (c) 2026 Red Hat Inc.
#
# This file is part of Ansible
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

from unittest.mock import MagicMock
from unittest.mock import patch

from ansible_collections.amazon.aws.plugins.module_utils.rds import AnsibleRDSError
from ansible_collections.amazon.aws.plugins.modules import rds_global_cluster_info
from ansible_collections.amazon.aws.plugins.modules.rds_global_cluster_info import global_cluster_info

mod_name = "ansible_collections.amazon.aws.plugins.modules.rds_global_cluster_info"


@patch(mod_name + ".describe_global_clusters")
def test_global_cluster_info_one_cluster(m_describe):
    conn = MagicMock()
    module = MagicMock()
    m_describe.return_value = [
        {
            "GlobalClusterIdentifier": "my-global-cluster",
            "GlobalClusterArn": "arn:aws:rds::123456789012:global-cluster:my-global-cluster",
            "Engine": "aurora-postgresql",
            "EngineVersion": "14.8",
            "Status": "available",
        }
    ]

    result = global_cluster_info(conn, module, "my-global-cluster")

    assert len(result) == 1
    assert result[0]["global_cluster_identifier"] == "my-global-cluster"
    assert result[0]["engine"] == "aurora-postgresql"
    m_describe.assert_called_with(conn, GlobalClusterIdentifier="my-global-cluster")


@patch(mod_name + ".describe_global_clusters")
def test_global_cluster_info_no_results(m_describe):
    conn = MagicMock()
    module = MagicMock()
    m_describe.return_value = []

    result = global_cluster_info(conn, module, "nonexistent")

    assert result == []
    m_describe.assert_called_with(conn, GlobalClusterIdentifier="nonexistent")


@patch(mod_name + ".describe_global_clusters")
def test_global_cluster_info_all(m_describe):
    conn = MagicMock()
    module = MagicMock()
    m_describe.return_value = [
        {"GlobalClusterIdentifier": "cluster-1", "Engine": "aurora-mysql"},
        {"GlobalClusterIdentifier": "cluster-2", "Engine": "aurora-postgresql"},
    ]

    result = global_cluster_info(conn, module, None)

    assert len(result) == 2
    assert result[0]["global_cluster_identifier"] == "cluster-1"
    assert result[1]["global_cluster_identifier"] == "cluster-2"
    m_describe.assert_called_with(conn)


@patch(mod_name + ".AnsibleAWSModule")
def test_main_success(m_AnsibleAWSModule):
    m_module = MagicMock()
    m_AnsibleAWSModule.return_value = m_module
    m_module.params = {"global_cluster_identifier": None}

    rds_global_cluster_info.main()

    m_module.client.assert_called_with("rds")
    call_kwargs = m_module.exit_json.call_args[1]
    assert call_kwargs["changed"] is False
    assert "global_clusters" in call_kwargs


@patch(mod_name + ".describe_global_clusters")
@patch(mod_name + ".AnsibleAWSModule")
def test_main_failure(m_AnsibleAWSModule, m_describe):
    m_module = MagicMock()
    m_AnsibleAWSModule.return_value = m_module
    m_module.params = {"global_cluster_identifier": None}
    e = AnsibleRDSError()
    m_describe.side_effect = e

    rds_global_cluster_info.main()

    m_module.client.assert_called_with("rds")
    m_module.fail_json_aws.assert_called_with(e, msg="Could not describe global clusters.")
