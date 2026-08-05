============================
osac.templates Release Notes
============================

.. contents:: Topics

v0.0.1
======

Release Summary
---------------

Initial release of the osac.templates Ansible collection for OSAC (Open Sovereign AI Cloud)
Fulfillment Services. This collection provides templates for provisioning OpenShift clusters
and virtual machines on OpenShift Virtualization through the OSAC orchestration system.

New Roles
---------

Cluster Templates
~~~~~~~~~~~~~~~~~

- osac.templates.ocp_small - Deploy minimal OpenShift cluster (2 nodes, fc430 resource class)
- osac.templates.ocp_4_17_small - (removed, replaced by ocp_small)
- osac.templates.ocp_4_17_small_github - (removed, replaced by ocp_small)

VM Templates
~~~~~~~~~~~~

- osac.templates.ocp_virt_vm - VM template for OpenShift Virtualization with configurable CPU, memory, disk, and cloud-init support

Features
--------

- Template metadata system using ``meta/osac.yaml`` for OSAC integration
- Parameter validation via Ansible ``argument_specs``
- Support for floating IP assignment and port forwarding for external access
- Cloud-init integration for VM initialization
- SSH key injection for secure VM access
- Configurable resource specifications (CPU, memory, disk)
- Automated lifecycle management (create/delete operations)

Known Issues
------------

- Templates require OSAC fulfillment service and cannot be run directly with ansible-playbook
- VM templates require OpenShift Virtualization operator to be installed
- Cluster templates depend on ``osac.service`` collection
