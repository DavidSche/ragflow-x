-- Run as the PostgreSQL migration/bootstrap role:
--   psql -v ON_ERROR_STOP=1 \
--     -v runtime_user='ragflow_x_runtime' \
--     -f scripts/postgres/configure-runtime-role.sql
--
-- The runtime role must already exist and must not own any RAGFlow-X tables.
-- Migrations are executed with a separate high-privilege role; this account
-- receives only the DML required by the application. Row-level security,
-- added by migration v64, prevents it from deleting the platform tenant.

SELECT format('GRANT CONNECT ON DATABASE %I TO %I', current_database(), :'runtime_user'::name) \gexec
SELECT format('GRANT USAGE ON SCHEMA %I TO %I', current_schema(), :'runtime_user'::name) \gexec
SELECT format('REVOKE CREATE ON SCHEMA %I FROM %I', current_schema(), :'runtime_user'::name) \gexec

SELECT format(
    'GRANT SELECT, INSERT, UPDATE, DELETE ON %I.%I TO %I',
    schemaname, tablename, :'runtime_user'::name)
FROM pg_tables
WHERE schemaname = current_schema() AND tablename LIKE 'rgx\_%'
  AND tablename <> 'rgx_schema_version' \gexec

SELECT format(
    'REVOKE ALL PRIVILEGES ON %I.%I FROM %I',
    current_schema(), 'rgx_schema_version', :'runtime_user'::name) \gexec

SELECT format(
    'GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %I TO %I',
    current_schema(), :'runtime_user'::name) \gexec

SELECT format(
    'ALTER DEFAULT PRIVILEGES IN SCHEMA %I GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %I',
    current_schema(), :'runtime_user'::name) \gexec

SELECT format(
    'ALTER DEFAULT PRIVILEGES IN SCHEMA %I GRANT USAGE, SELECT ON SEQUENCES TO %I',
    current_schema(), :'runtime_user'::name) \gexec
