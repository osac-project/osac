--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
-- the License. You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
-- an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
-- specific language governing permissions and limitations under the License.
--

-- Create the table for discovered billing accounts (OSAC-6883). billing_accounts is a flat,
-- provider-global read model (design.md 3784 Sec 4.2/4.3: "account... read models may be
-- flat"), not the generic Metadata/Spec/Status object shape: no tenant, no data jsonb column,
-- no archive/active companion tables.
create table billing_accounts (
  id text not null primary key,
  provider_key text not null check (provider_key <> ''),
  -- Provider-native account id used for backend calls. Never returned by any API.
  provider_account_reference text not null check (provider_account_reference <> ''),
  display_name text not null default '',
  -- ISO-4217 currency, empty when the provider does not supply one (e.g. M360).
  base_currency text not null default '',
  -- OSAC's provider-agnostic attribution id. Nullable: unset until discovery or
  -- assignment-time resolution (story 2.03/OSAC-6885) establishes one.
  correlation_id text,
  -- Normalized state: ACTIVE, INACTIVE, or UNKNOWN. Never the provider's raw status text.
  state text not null default 'UNKNOWN',
  status_observed_at timestamp with time zone not null default now()
);

-- One row per provider account: a provider key together with its native account reference
-- identifies exactly one billing_accounts row.
create unique index billing_accounts_unique_provider_account
  on billing_accounts (provider_key, provider_account_reference);

-- At most one account may hold a given correlation_id. This backs story 2.03/OSAC-6885's
-- ACCOUNT_AMBIGUOUS/duplicate-correlation check; partial so multiple NULLs (not yet
-- established) are allowed.
create unique index billing_accounts_unique_correlation_id
  on billing_accounts (correlation_id)
  where correlation_id is not null;
