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

-- Convert compute instance and template run_strategy from short string to protojson enum name.
-- The ComputeInstanceRunStrategy enum PR changed the run_strategy field from optional string to
-- optional ComputeInstanceRunStrategy enum. protojson serializes enum values using the full name:
--   "Always"  → "COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS"
--   "Halted"  → "COMPUTE_INSTANCE_RUN_STRATEGY_HALTED"
-- Without this migration, protojson.Unmarshal fails when reading existing rows from the DB.

-- Compute instances
update compute_instances
set data = jsonb_set(
  data,
  '{spec,run_strategy}',
  case data->'spec'->>'run_strategy'
    when 'Always' then '"COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS"'::jsonb
    when 'Halted' then '"COMPUTE_INSTANCE_RUN_STRATEGY_HALTED"'::jsonb
    else data->'spec'->'run_strategy'
  end
)
where data->'spec' ? 'run_strategy'
  and data->'spec'->>'run_strategy' in ('Always', 'Halted');

-- Compute instance templates (spec_defaults)
update compute_instance_templates
set data = jsonb_set(
  data,
  '{spec_defaults,run_strategy}',
  case data->'spec_defaults'->>'run_strategy'
    when 'Always' then '"COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS"'::jsonb
    when 'Halted' then '"COMPUTE_INSTANCE_RUN_STRATEGY_HALTED"'::jsonb
    else data->'spec_defaults'->'run_strategy'
  end
)
where data->'spec_defaults' ? 'run_strategy'
  and data->'spec_defaults'->>'run_strategy' in ('Always', 'Halted');
