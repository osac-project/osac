network-runner
==============
This role implements network device abstraction for Ansible Networking modules

Requirements
------------
* Ansible 2.5+

Role Variables
--------------
* vlan_id
* vlan_name (default depends on provider)

Dependencies
------------

A list of other roles hosted on Galaxy should go here, plus any details in
regards to parameters that may need to be set for other roles, or variables
that are used from other roles.

Example Playbook
----------------

```
---
- hosts: all

  tasks:
    - name: do create_vlan
      import_role:
        name: network-runner
        tasks_from: create_vlan
      vars:
        vlan_name: v101
        vlan_id: 101

    - name: do delete_vlan
      import_role:
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
