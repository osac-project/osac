#!/bin/bash
set -euo pipefail

echo "Creating per-instance databases for prefix '${INSTANCE_PREFIX}'..."

SERVICE_DB="${INSTANCE_PREFIX}_service"
METERING_DB="${INSTANCE_PREFIX}_metering"

for db in "$SERVICE_DB" "$METERING_DB"; do
    echo "Ensuring role and database: ${db}"
    psql -v ON_ERROR_STOP=1 -d postgres <<SQL
DO \$\$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='${db}') THEN
    CREATE ROLE ${db} LOGIN;
    RAISE NOTICE 'Created role ${db}';
  ELSE
    RAISE NOTICE 'Role ${db} already exists';
  END IF;
END \$\$;

SELECT 'CREATE DATABASE ${db} OWNER ${db}'
  WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname='${db}')
\gexec
SQL
done

echo "Database initialization complete."
