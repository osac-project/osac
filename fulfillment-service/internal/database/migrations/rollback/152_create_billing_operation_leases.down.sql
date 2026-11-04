-- Optional rollback script, deliberately outside the service's up-only catalog.
drop table billing_operation_leases;
