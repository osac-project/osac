================================
agentless_net.steps Release Notes
================================

.. contents:: Topics

v1.0.0
======

Release Summary
---------------
Initial release of the agentless_net.steps collection providing
cluster_infra and external_access step implementations for the
agentless network backend.

New Roles
---------
- ``cluster_infra`` - VLAN allocation, switch configuration, L3 router
  namespace, and SNAT for CaaS cluster provisioning.
- ``external_access`` - Public IP allocation, DNAT for API and ingress
  endpoints, DNS records, and MetalLB configuration.
