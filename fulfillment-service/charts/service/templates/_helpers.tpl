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
{{- if eq .Values.variant "kind" -}}
https://{{ include "fulfillment-api.hostname" . }}:8000
{{- else -}}
https://{{ include "fulfillment-api.hostname" . }}
{{- end -}}
{{- end -}}

{{/*
Return the hostname for the fulfillment internal API. Fails if 'internalHostname' is not set.
*/}}
{{- define "fulfillment-internal-api.hostname" -}}
{{- required "internalHostname is required" .Values.internalHostname -}}
{{- end -}}

{{- define "fulfillment-mcp-server.hostname" -}}
{{- $hostname := required "mcp.externalHostname is required when mcp.enabled=true" .Values.mcp.externalHostname -}}
{{- if eq .Values.variant "kind" -}}
{{- $expected := printf "mcp.%s.localhost" .Release.Namespace -}}
{{- if ne $hostname $expected -}}
{{- fail (printf "mcp.externalHostname must be %s for the kind variant" $expected) -}}
{{- end -}}
{{- end -}}
{{- $hostname -}}
{{- end -}}

{{- define "fulfillment-mcp-server.externalPort" -}}
{{- $port := int .Values.mcp.externalPort -}}
{{- if and (eq .Values.variant "openshift") (ne $port 443) -}}
{{- fail "mcp.externalPort must be 443 for the openshift variant" -}}
{{- end -}}
{{- $port -}}
{{- end -}}

{{/*
Check if a service tier is enabled via global.services.<key>.enabled.
Args: list of [context, serviceKey]
Falls back to true when global.services is not set.
Returns non-empty string for enabled, empty for disabled (for use in {{- if include ... }}).
*/}}
{{- define "fulfillment-service.serviceEnabled" -}}
{{- $ctx := index . 0 -}}
{{- $svcKey := index . 1 -}}
{{- $enabled := true -}}
{{- if $ctx.Values.global -}}
{{- if $ctx.Values.global.services -}}
{{- $svc := index $ctx.Values.global.services $svcKey -}}
{{- if $svc -}}
{{- if hasKey $svc "enabled" -}}
{{- $enabled = $svc.enabled -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- $enabled -}}
{{- end }}

{{/*
Networking installation settings, from global.networking:
- provisioningEnabled (default true): whether OSAC runs provider networking jobs. It can't change after installation.
- excludedIpv4Cidrs (default empty): canonical IPv4 CIDRs that the provider's infrastructure uses.
Renders the command line arguments shared by the gRPC server and the controllers.
*/}}
{{- define "fulfillment-service.networkingArgs" -}}
{{- $networking := dict -}}
{{- if .Values.global -}}
{{- $networking = .Values.global.networking | default dict -}}
{{- end -}}
{{- $enabled := true -}}
{{- if hasKey $networking "provisioningEnabled" -}}
{{- $enabled = $networking.provisioningEnabled -}}
{{- end }}
- --networking-provisioning-enabled={{ $enabled }}
{{- with $networking.excludedIpv4Cidrs }}
- --networking-excluded-ipv4-cidrs={{ join "," . }}
{{- end }}
{{- end }}
