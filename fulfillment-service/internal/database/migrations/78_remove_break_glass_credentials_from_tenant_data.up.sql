-- Remove break-glass credentials (username + plaintext password) that were
-- inadvertently persisted in the tenant status JSONB.  Only the
-- break_glass_user_id should be stored; the password lives in Keycloak.

update tenants
set data = data #- '{status,break_glass_credentials}'
where data -> 'status' ? 'break_glass_credentials';

update archived_tenants
set data = data #- '{status,break_glass_credentials}'
where data -> 'status' ? 'break_glass_credentials';
