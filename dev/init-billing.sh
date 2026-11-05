# Локальные данные, только для разработки. Повторный запуск безопасен.
psql -v ON_ERROR_STOP=1 -U pvz -d postgres <<'SQL'
SELECT 'CREATE ROLE pvz_billing LOGIN PASSWORD ''pvz_billing'''
 WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname='pvz_billing') \gexec
SELECT 'CREATE DATABASE pvz_billing OWNER pvz_billing'
 WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname='pvz_billing') \gexec
REVOKE ALL ON DATABASE pvz_billing FROM PUBLIC;
SQL
