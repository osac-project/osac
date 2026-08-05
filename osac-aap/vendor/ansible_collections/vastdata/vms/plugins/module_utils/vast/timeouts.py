"""Centralized timeout constants for async VAST operations.

Values sourced from Orion's battle-tested system_consts
(comet/cluster_config.py) to ensure Ansible modules use
realistic timeouts for real-cluster operations.
"""

# Task polling default (used by BaseResource._wait_for_task)
DEFAULT_TASK_TIMEOUT = 300

# CNode operations
CNODE_STATE_CHANGE_TIMEOUT = 600
CNODE_REPLACEMENT_TIMEOUT = 3600

# DNode operations
DNODE_STATE_CHANGE_TIMEOUT = 600
DNODE_REPLACEMENT_TIMEOUT = 3600
