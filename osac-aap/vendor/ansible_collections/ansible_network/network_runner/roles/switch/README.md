network-runner switch role
==========================

This role implements network device abstraction for Ansible Networking
modules.

Requirements
------------
* Ansible 2.5+

Role Variables
--------------
* vlan_id
* vlan_name (default depends on provider)
* port_name
* port_description
* guard_scope, guard_key (optional deletion guard, see below)

Actions
-------

Every action is included through ``roles/switch/tasks/<action>.yaml`` and
dispatches to ``providers/<ansible_network_os>/<action>.yaml``. A provider that
does not implement an action fails with a clear message instead of silently
doing nothing.

Mutation actions: ``create_vlan``, ``delete_vlan``, ``conf_trunk_port``,
``add_trunk_vlan``, ``delete_trunk_vlan``, ``conf_access_port``, ``delete_port``.

Normalized read and guard actions: ``list_vlans``, ``get_port_conf``,
``set_deletion_guard``. Their results are provider-neutral and are the only
shape callers should consume; callers must never parse provider command output.

``list_vlans``
    Sets ``network_runner_vlans``::

        interfaces:
          - name: swp1
            vlan_ids: [100, 101, 102]
            tagged_vlan_ids: [100, 101, 102]
            untagged_vlan_ids: []
            pvid: null
        used_vlan_ids: [1, 100, 101, 102]
        reserved_vlan_ids: [3700, 3701, 3702, 4000]

    ``used_vlan_ids`` holds every VLAN ID present on a bridge interface and
    ``reserved_vlan_ids`` the VLAN IDs the device reserves for internal and
    L3-VNI use. VLAN ranges are expanded.

``get_port_conf``
    Requires ``port_name`` and sets ``network_runner_port``::

        name: swp1
        present: true
        vlan_ids: [100]
        tagged_vlan_ids: []
        untagged_vlan_ids: [100]
        pvid: 100

    ``present`` is false when the device reports no bridge state for the port,
    in which case every list is empty and ``pvid`` is null. ``pvid`` is the
    untagged ingress VLAN, or null when the port has no PVID membership.

``set_deletion_guard``
    Requires ``guard_scope`` and ``guard_key`` and records, on the device, that
    the owner identified by them was deleted. The guard is written under the
    same configuration lock the provider uses for port changes, so a port
    change cannot run between the guard check and the configuration apply.

    ``conf_access_port`` and ``delete_port`` accept the same ``guard_scope`` and
    ``guard_key``. When both are set, the provider refuses the port change with
    exit code 73 if the guard exists, and leaves the device unchanged. Callers
    use this to let a delete win over a concurrent apply for the same owner.

Example Playbook
----------------

```
---
- hosts: switches

  tasks:
    - name: read the switch VLAN state
      include_role:
        name: network-runner
        tasks_from: list_vlans
      vars:
        ansible_network_os: cumulus

    - name: do create_vlan
      include_role:
        name: network-runner
        tasks_from: create_vlan
      vars:
        vlan_name: v101
        vlan_id: 101

    - name: do delete_vlan
      include_role:
        name: network-runner
        tasks_from: delete_vlan
      vars:
        vlan_id: 101
        vlan_name: v101
```


License
-------
Apache

Author Information
------------------
Ansible
