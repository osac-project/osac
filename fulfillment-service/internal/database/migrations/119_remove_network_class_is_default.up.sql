-- NetworkClass is a deployment singleton; the legacy is_default flag is no
-- longer part of the protobuf contract. Remove it from persisted JSON and drop
-- the obsolete uniqueness index created for default swapping.
update network_classes
set data = data - 'is_default'
where data ? 'is_default';

drop index if exists network_classes_single_default;
