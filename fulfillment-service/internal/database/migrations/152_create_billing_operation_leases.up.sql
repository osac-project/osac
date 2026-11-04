-- Committed, expiring guards for billing operations that perform provider I/O
-- outside a database transaction. Keys are SHA-256 digests, not provider data.
create table billing_operation_leases (
    operation_key text primary key,
    owner text not null,
    expires_at timestamptz not null,
    constraint billing_operation_leases_key_digest check (operation_key ~ '^[0-9a-f]{64}$')
);
