======================================
Network-Runner: Ansible Networking API
======================================

Overview
--------
Network-Runner is a python library that abstracts management and
interaction with switching hardware to Ansible Networking. This library is not
tested with all the modules included with Ansible Networking. In theory it
should work with any switch that has compatible modules included with Ansible
Networking if the provider tasks are added to this library's Ansible role.
See the contributor documentation for more information
about adding support for an Ansible Networking module to this library.

* Free software: Apache license
* Documentation: https://network-runner.readthedocs.io/en/latest/
* Source: https://github.com/ansible-network/network-runner/

Components
----------
The Network-Runner library consists of the following components:

``Python API``
  Imported directly by python.

``Ansible Role``
  Used by Ansible during Ansible invocation.

Use Cases
---------
``Python API``

Any python application could need the ability to communicate with a switch
to perform a task that network-runner is able to complete. The interaction
with ansible is designed in a library style that will allow direct import and
invocation in python independent of a running OpenStack deployment.

API Features
------------
The following matrix indicates which features have been implmented.

+--------------------+-------------+-------+------+-----+------+---------+----------+-----+
|                    | openvswitch | junos | nxos | eos | enos | cumulus | dellos10 | fos |
+--------------------+-------------+-------+------+-----+------+---------+----------+-----+
| Create VLAN        |     N/A     |   Y   |  Y   |  Y  |  Y   |    Y    |    Y     |  Y  |
+--------------------+-------------+-------+------+-----+------+---------+----------+-----+
| Delete VLAN        |     N/A     |   Y   |  Y   |  Y  |  Y   |    Y    |    Y     |  Y  |
+--------------------+-------------+-------+------+-----+------+---------+----------+-----+
| Delete Port        |      Y      |   Y   |  Y   |  Y  |  Y   |    Y    |    Y     |  Y  |
+--------------------+-------------+-------+------+-----+------+---------+----------+-----+
| Config Access Port |      Y      |   Y   |  Y   |  Y  |  Y   |    Y    |    Y     |  Y  |
+--------------------+-------------+-------+------+-----+------+---------+----------+-----+
| Config Trunk Port  |      N      |   Y   |  N   |  Y  |  Y   |    Y    |    N     |  Y  |
+--------------------+-------------+-------+------+-----+------+---------+----------+-----+
| List VLANs         |      N      |   N   |  N   |  N  |  N   |    N    |    N     |  N  |
+--------------------+-------------+-------+------+-----+------+---------+----------+-----+
| Add Trunk VLAN     |      N      |   N   |  N   |  N  |  N   |    N    |    N     |  Y  |
+--------------------+-------------+-------+------+-----+------+---------+----------+-----+
| Delete Trunk VLAN  |      N      |   N   |  N   |  N  |  N   |    N    |    N     |  Y  |
+--------------------+-------------+-------+------+-----+------+---------+----------+-----+
| Get Port Conf      |      N      |   N   |  N   |  N  |  N   |    N    |    N     |  Y  |
+--------------------+-------------+-------+------+-----+------+---------+----------+-----+
