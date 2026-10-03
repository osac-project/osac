-- Add non-partial indexes for child references so ExternalIP cleanup can find
-- consumers that are still being deleted as well as active consumers.
create index external_ip_attachments_external_ip_ref_id_idx
  on external_ip_attachments ((data -> 'spec' -> 'external_ip' ->> 'id'));

create index external_ip_attachments_external_ip_ref_name_idx
  on external_ip_attachments ((data -> 'spec' -> 'external_ip' ->> 'name'));

create index nat_gateways_external_ip_ref_id_idx
  on nat_gateways ((data -> 'spec' -> 'external_ip' ->> 'id'));

create index nat_gateways_external_ip_ref_name_idx
  on nat_gateways ((data -> 'spec' -> 'external_ip' ->> 'name'));
