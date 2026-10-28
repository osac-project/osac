# OSAC Personas

These are the essential personas that OSAC must target for an MVP. Many
additional personas can be identified within the solution space, some of which
will be added to this document over time as the project is ready to address
their unique use cases.

Personas describe the work people need to do. Roles define what they are allowed
to do in OSAC. People represented by the same persona may need different levels
of access.

For example, Cloud Provider Admin responsibilities might be split between
someone who manages tenant accounts and someone who only reviews audit logs.
The auditor needs access to the logs, but should not need permission to change
resources or manage users. The same applies within a tenant: someone reviewing
audit logs should be able to access their own tenant's records without receiving
administrative permissions.

A role such as Auditor or Read Only does not automatically require a new
persona. We add a persona when we need to describe distinct user goals or
workflows, rather than a different set of permissions.

## Cloud Provider Admin

* works for the Cloud Provider
* handles or facilitates tenant onboarding
* sets quotas for tenant organizations
* uses the OSAC interfaces to manage tenant organizations
* uses the OSAC interfaces to manage resource allocation
* can work across tenant organizations, with access limited by their assigned permissions
* manages any global catalogs of templates (VM templates, cluster templates, etc)
* ensures that all global templates work with the local infrastructure
* works with internal infrastructure owners (Cloud Infrastructure Admins) as necessary

## Cloud Infrastructure Admin

* works for the Cloud Provider
* manages a part of core infrastructure, possibly including network, firewall, compute resources, storage, etc.
* ensures that cloud services are running, including the cloud control plane
* integrates the cloud control plane with local infrastructure including specific compute hardware, DNS systems, storage solutions, etc.

## Tenant Admin

* works for the Tenant Organization
* typically does not have any relationship to other tenants of the cloud provider
* manages their organization's configuration in OSAC, including IDP
* manages users within their organization
* manages quotas within their organization
* can only see users and resources associated with their own organization
* manages organization-specific catalogs of templates
* controls which global templates are visible to tenant users

## Tenant User

* works for the Tenant Organization
* can utilize templates from global and organization-specific catalogs
* self-service provisions cloud resources for use by themself or their team
* manages the full lifecycle of cloud resourses
* can see quota utilization that would apply to them
* may prefer click-ops, but wants an API and CLI for optional automation
