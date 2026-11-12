#!/bin/bash
#
# Copyright (c) 2026 Red Hat, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
# the License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.
#

# Generates the Terraform plugin framework schema and model code for a public API service from its proto
# definitions. See README.md in this directory for the full description of the pipeline and its caveats.
#
# The first step of the pipeline converts the protos into an OpenAPI document. Three plugins were
# evaluated for that step, all run with the original proto field names:
#
#   github.com/google/gnostic/cmd/protoc-gen-openapi
#     Emits OpenAPI 3 directly, and maps OUTPUT_ONLY field behavior to readOnly. Rejected because it
#     honors the `body` option of the google.api.http annotations but not `response_body`: the
#     responses of operations with `response_body: "object"` reference the whole RPC response
#     message, so the merged Terraform schemas contain the envelope attributes, the generated Go
#     code has duplicate type names and doesn't compile, and enums are emitted as numbers instead
#     of strings.
#
#   connectrpc.com/protoc-gen-connect-openapi
#     Emits OpenAPI 3.1 directly, honors `response_body`, and maps field behavior (OUTPUT_ONLY to
#     readOnly, REQUIRED to required). Rejected because it represents optional fields as
#     `oneOf: [<schema>, null]`, and tfplugingen-openapi doesn't look through that wrapper, so the
#     enums lose their generated OneOf validators: invalid values would only be rejected by the
#     server at apply time instead of by the plan. It is the better OpenAPI document for other
#     consumers (documentation, client generation), so it could be run alongside this pipeline.
#
#   github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2   (used here)
#     The plugin of the REST gateway itself, so the document matches the contract the gateway
#     actually serves, including `body` and `response_body`, and enums are emitted as strings with
#     their values, which become OneOf validators. Its two costs are accepted: it emits Swagger
#     2.0, requiring the swagger2openapi conversion below, and it ignores google.api.field_behavior,
#     so output-only and immutable semantics stay in the provider glue.
#
# Usage:
#
#   ./generate.sh <service>
#
# For example:
#
#   ./generate.sh baremetal_instances_service
#
# Requirements:
#
#   go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest
#   go install github.com/hashicorp/terraform-plugin-codegen-openapi/cmd/tfplugingen-openapi@latest
#   go install github.com/hashicorp/terraform-plugin-codegen-framework/cmd/tfplugingen-framework@latest
#   node with npx (for the swagger2openapi conversion)

set -euo pipefail

service="${1:?usage: generate.sh <service>}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
provider_dir="$(cd "${script_dir}/../.." && pwd)"
repo_dir="$(cd "${provider_dir}/.." && pwd)"
out_dir="${script_dir}/out"

# Generate the OpenAPI 2 spec of the service from the protos, using the grpc-gateway plugin so that the `body` and
# `response_body` options of the HTTP annotations are honored:
rm -rf "${out_dir}"
mkdir -p "${out_dir}"
cd "${repo_dir}/proto"
PATH="${HOME}/go/bin:${PATH}" buf generate \
    --template "${script_dir}/buf.gen.openapi.yaml" \
    --path "public/osac/public/v1/${service}.proto"

# Convert it to OpenAPI 3, which is what the HashiCorp generator accepts:
swagger="$(find "${out_dir}" -name '*.json' | head -1)"
npx -y swagger2openapi "${swagger}" -o "${out_dir}/openapi3.json" >/dev/null

# Convert the OpenAPI spec into a provider code specification and then into framework code.
# Each service has its own generator configuration, listing only the paths that exist
# in that service's spec. Services that only generate data sources skip the provider
# and resource generation, so that those files always reflect the full configuration:
cd "${script_dir}"
config="${script_dir}/configs/${service}.yml"
"${HOME}/go/bin/tfplugingen-openapi" generate \
    --config "${config}" \
    --output "${out_dir}/provider_code_spec.json" \
    "${out_dir}/openapi3.json"
command="data-sources"
if grep -q "^resources:" "${config}"; then
    command="all"
fi
"${HOME}/go/bin/tfplugingen-framework" generate ${command} \
    --input "${out_dir}/provider_code_spec.json" \
    --output "${provider_dir}/internal/gen"

echo "Generated code written to ${provider_dir}/internal/gen"
