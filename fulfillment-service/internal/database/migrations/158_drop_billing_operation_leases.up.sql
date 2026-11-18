-- Billing provider operations now use the ordinary request transaction and read-only provider evidence.
-- No runtime consumer uses these guards; even populated, unexpired rows are obsolete. Do not cascade to
-- unexpected dependencies or change billing objects, identity markers or interval history.
drop table billing_operation_leases;
