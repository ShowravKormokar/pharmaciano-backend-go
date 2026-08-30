-- The order of DROP statements must respect foreign key dependencies:
-- warehouses depends on branches and organizations, branches depends on organizations.
DROP TABLE IF EXISTS warehouses;
DROP TABLE IF EXISTS branches;
DROP TABLE IF EXISTS organizations;

-- Optionally drop the trigger function if it was created only for this migration.
-- In a real project, you would keep it for subsequent migrations.
-- DROP FUNCTION IF EXISTS set_updated_at();

-- Do NOT drop the citext extension here; it may be used by other tables.