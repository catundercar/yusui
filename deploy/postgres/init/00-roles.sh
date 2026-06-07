#!/bin/bash
# Runs once at Postgres cluster init (docker-entrypoint-initdb.d).
# Creates the two YuSui roles; the migration grants table privileges.
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
	CREATE ROLE yusui_migrate LOGIN PASSWORD '${YUSUI_MIGRATE_PASSWORD}';
	CREATE ROLE yusui_app     LOGIN PASSWORD '${YUSUI_APP_PASSWORD}';
	GRANT CREATE, CONNECT, TEMPORARY ON DATABASE "$POSTGRES_DB" TO yusui_migrate;
	GRANT CONNECT ON DATABASE "$POSTGRES_DB" TO yusui_app;
	-- goose keeps its version table in public; PG16 revoked CREATE-on-public.
	GRANT CREATE ON SCHEMA public TO yusui_migrate;
EOSQL
