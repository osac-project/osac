{{/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/}}

{{/*
Return the hostname for the fulfillment API. Fails if 'externalHostname' is not set.
*/}}
{{- define "fulfillment-api.hostname" -}}
{{- required "externalHostname is required" .Values.externalHostname -}}
{{- end -}}

{{/*
Generate the token issuer URL for JWT signing and JWKS discovery.

On OpenShift a Route provides TLS passthrough on port 443, so the URL omits the port. In the kind
variant traffic goes directly to the Kubernetes Service on port 8000.
*/}}
{{- define "fulfillment-api.tokenIssuerUrl" -}}
{{- if eq .Values.variant "openshift" -}}
https://{{ include "fulfillment-api.hostname" . }}
{{- else -}}
https://{{ include "fulfillment-api.hostname" . }}:8000
{{- end -}}
{{- end -}}

{{/*
Return the hostname for the fulfillment internal API. Fails if 'internalHostname' is not set.
*/}}
{{- define "fulfillment-internal-api.hostname" -}}
{{- required "internalHostname is required" .Values.internalHostname -}}
{{- end -}}
