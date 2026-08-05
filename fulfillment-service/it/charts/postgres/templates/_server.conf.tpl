{{/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/}}

{{- define "database.server.conf" -}}

# Enable TLS:
ssl = on
{{- if .Values.certs.caBundle.configMap }}
ssl_ca_file = '/config/ca/bundle.pem'
{{- else }}
ssl_ca_file = '/secrets/cert/ca.crt'
{{- end }}
ssl_cert_file = '/secrets/cert/tls.crt'
ssl_key_file = '/secrets/cert/tls.key'

# Use our custom access file:
hba_file = '/config/access/access.conf'

# Disable fsync to improve performance in development environments. This skips flushing data to
# disk, which is faster but means data can be lost on crash. Acceptable for development where data
# doesn't need to survive restarts.
fsync = off

# Don't wait for WAL writes to complete before reporting transaction committed. This reduces commit
# latency significantly but means recent transactions may be lost on crash.
synchronous_commit = off

# Skip writing full page images to WAL after checkpoint. This reduces WAL volume and I/O, and is
# safe when fsync is off since we're already accepting data loss risk.
full_page_writes = off

{{- end -}}
