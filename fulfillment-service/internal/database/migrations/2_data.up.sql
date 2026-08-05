--
-- Copyright (c) 2025 Red Hat Inc.
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

insert into cluster_templates (id, data) values
(
  'ocp_4_17_small',
  '{
    "id": "ocp_4_17_small",
    "title": "OpenShift 4.17 small",
    "description": "OpenShift 4.17 with `small` instances as worker nodes.",
    "parameters": [
      {
        "type": "type.googleapis.com/google.protobuf.BoolValue",
        "name": "my_bool",
        "default": {
          "@type": "type.googleapis.com/google.protobuf.BoolValue",
          "value": true
        }
      },
      {
        "type": "type.googleapis.com/google.protobuf.Int32Value",
        "name": "my_int",
        "default": {
          "@type": "type.googleapis.com/google.protobuf.Int32Value",
          "value": 42
        }
      },
      {
        "type": "type.googleapis.com/google.protobuf.StringValue",
        "name": "my_string",
        "default": {
          "@type": "type.googleapis.com/google.protobuf.StringValue",
          "value": "my_value"
        }
      }
    ]
  }'
);
