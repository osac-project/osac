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
Generates the shell script that writes the Keycloak configuration file and starts the server. This is used instead of
environment variables so that tools like `kc.sh` work correctly when executed via `kubectl exec`.
*/}}
{{- define "keycloak.start" -}}
#!/bin/bash

# Directory containing the database connection parameters:
param_dir="/opt/keycloak/db"

# Regular expression to match the URL and extract the components:
url_regex="^postgres://(([^:@]+)(:([^@]+))?@)?([^:/]+)(:([0-9]+))?/(.*)$"

# Default values for database connection parameters:
db_host="localhost"
db_port="5432"
db_name="postgres"
declare -A db_params

# Load actual values from the files:
for param_path in "${param_dir}"/*; do
  param_name=$(basename "${param_path}")
  param_value=$(tr -d '\0' < "${param_path}")
  case "${param_name}" in
    "url")
      if [[ ! "${param_value}" =~ ${url_regex} ]]; then
        echo "URL from file '${param_path}' is invalid, it doesn't match regular expression '${url_regex}'"
        exit 1
      fi
      [[ -n "${BASH_REMATCH[2]}" ]] && db_user="${BASH_REMATCH[2]}"
      [[ -n "${BASH_REMATCH[4]}" ]] && db_password="${BASH_REMATCH[4]}"
      [[ -n "${BASH_REMATCH[5]}" ]] && db_host="${BASH_REMATCH[5]}"
      [[ -n "${BASH_REMATCH[7]}" ]] && db_port="${BASH_REMATCH[7]}"
      [[ -n "${BASH_REMATCH[8]}" ]] && db_name="${BASH_REMATCH[8]}"
      ;;
    "user")
      db_user="${param_value}"
      ;;
    "password")
      db_password="${param_value}"
      ;;
    "host")
      db_host="${param_value}"
      ;;
    "port")
      db_port="${param_value}"
      ;;
    "dbname")
      db_name="${param_value}"
      ;;
    "sslcert" | "sslkey" | "sslrootcert")
      db_params["${param_name}"]="${param_path}"
      ;;
    *)
      db_params["${param_name}"]="${param_value}"
      ;;
  esac
done

# Rebuild the URL without the user and password, as they are stored in separate properties:
db_url="jdbc:postgresql://${db_host}:${db_port}/${db_name}"
index="0"
for param_name in "${!db_params[@]}"; do
  if [[ "${index}" == "0" ]]; then
    db_url+="?"
  else
    db_url+="&"
  fi
  param_value="${db_params[${param_name}]}"
  db_url+="${param_name}=${param_value}"
  ((index++))
done

# Write the Keycloak configuration file:
conf_file="/opt/keycloak/conf/keycloak.conf"
cat > "${conf_file}" <<.
db=postgres
db-url=${db_url}
db-username=${db_user}
db-password=${db_password}
hostname={{ include "keycloak.hostname" . }}
hostname-port=8000
hostname-strict=true
hostname-strict-https=true
http-enabled=false
https-enabled=true
https-port=8000
https-certificate-file=/opt/keycloak/tls/tls.crt
https-certificate-key-file=/opt/keycloak/tls/tls.key
{{ if .Values.certs.caBundle.configMap }}
truststore-paths=/opt/keycloak/cas
{{ end }}
.
chmod 600 "${conf_file}"

# Start Keycloak:
exec /opt/keycloak/bin/kc.sh start --import-realm
{{- end -}}
