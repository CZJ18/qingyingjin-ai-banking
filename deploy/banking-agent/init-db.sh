#!/bin/sh
set -eu

: "${POSTGRES_APP_PASSWORD:?runtime role password is required}"

# getenv keeps the password out of process arguments; literal quoting and format
# prevent a password from becoming SQL. Do not enable psql query echoing here.
psql --no-psqlrc --quiet --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set=ON_ERROR_STOP=1 <<'SQL'
\getenv app_password POSTGRES_APP_PASSWORD
SELECT format(
  'CREATE ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS PASSWORD %L',
  'banking_agent_app', :'app_password'
) \gexec
REVOKE ALL ON DATABASE qingrongjin_agent FROM PUBLIC;
GRANT CONNECT ON DATABASE qingrongjin_agent TO banking_agent_app;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO banking_agent_app;
SQL
