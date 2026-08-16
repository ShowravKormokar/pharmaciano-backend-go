-- ##### FILE: 000001_init_extensions.down.sql #################################
DROP FUNCTION IF EXISTS prevent_write();
DROP FUNCTION IF EXISTS set_updated_at();
-- Extensions are usually kept; drop only if this DB is dedicated.
-- DROP EXTENSION IF EXISTS unaccent;
-- DROP EXTENSION IF EXISTS btree_gist;
-- DROP EXTENSION IF EXISTS btree_gin;
-- DROP EXTENSION IF EXISTS pg_trgm;
-- DROP EXTENSION IF EXISTS citext;
-- DROP EXTENSION IF EXISTS pgcrypto;
