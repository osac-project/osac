--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
-- the License. You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--

-- Audit records are immutable event data, not lifecycle-managed OSAC objects.
create table audit_records (
  id text not null primary key,
  creation_timestamp timestamp with time zone not null default now(),
  tenant text not null,
  data jsonb not null
);
