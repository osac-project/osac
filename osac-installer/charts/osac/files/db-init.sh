#!/bin/bash
set -euo pipefail

echo "Creating per-instance databases for prefix '${INSTANCE_PREFIX}'..."

SERVICE_DB="${INSTANCE_PREFIX}_service"
METERING_DB="${INSTANCE_PREFIX}_metering"

# A run ID may contain hyphens, which an unquoted PostgreSQL identifier
# cannot carry. format's %I quotes an identifier only when it needs it, so
# the plain names this script has always created produce the same SQL
# statements as before.
for db in "$SERVICE_DB" "$METERING_DB"; do
    echo "Ensuring role and database: ${db}"
    psql -v ON_ERROR_STOP=1 -d postgres <<SQL
DO \$\$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='${db}') THEN
    EXECUTE format('CREATE ROLE %I LOGIN', '${db}');
    RAISE NOTICE 'Created role ${db}';
  ELSE
    RAISE NOTICE 'Role ${db} already exists';
  END IF;
END \$\$;

SELECT format('CREATE DATABASE %I OWNER %I', '${db}', '${db}')
  WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname='${db}')
\gexec
SQL
done

echo "Database initialization complete."
