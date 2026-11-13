-- Optional rollback script, deliberately outside the service's up-only catalog.
drop index project_membership_subjects_by_tenant_user;
drop index archived_tenants_by_name;
