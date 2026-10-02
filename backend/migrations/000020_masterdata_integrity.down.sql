-- ##### FILE: 000020_masterdata_integrity.down.sql ##########################
-- Reverses 000020_masterdata_integrity.up.sql in the exact reverse order:
-- the eleven `code` trigram indexes, then the ten `name` trigram indexes, then
-- the two foreign-key indexes, then the three check constraints. Every object is
-- dropped by name, and the constraints use IF EXISTS so the rollback cannot fail
-- part-way on an already-clean database.
--
-- This restores the 000005 state for the eleven MasterData resources. It does
-- not touch the manufacturers/brands/suppliers tables, which 000005 also
-- created and which 000006/000007/000008 reference. It also does not drop
-- ix_generic_medicines_name, which 000005 created rather than 000020.
-- ---------------------------------------------------------------------------

DROP INDEX IF EXISTS ix_generic_medicines_code;
DROP INDEX IF EXISTS ix_therapeutic_classes_code;
DROP INDEX IF EXISTS ix_drug_classes_code;
DROP INDEX IF EXISTS ix_drug_groups_code;
DROP INDEX IF EXISTS ix_tax_rates_code;
DROP INDEX IF EXISTS ix_storage_conditions_code;
DROP INDEX IF EXISTS ix_unit_types_code;
DROP INDEX IF EXISTS ix_package_types_code;
DROP INDEX IF EXISTS ix_routes_code;
DROP INDEX IF EXISTS ix_dosage_forms_code;
DROP INDEX IF EXISTS ix_product_categories_code;

DROP INDEX IF EXISTS ix_therapeutic_classes_name;
DROP INDEX IF EXISTS ix_drug_classes_name;
DROP INDEX IF EXISTS ix_drug_groups_name;
DROP INDEX IF EXISTS ix_tax_rates_name;
DROP INDEX IF EXISTS ix_storage_conditions_name;
DROP INDEX IF EXISTS ix_unit_types_name;
DROP INDEX IF EXISTS ix_package_types_name;
DROP INDEX IF EXISTS ix_routes_name;
DROP INDEX IF EXISTS ix_dosage_forms_name;
DROP INDEX IF EXISTS ix_product_categories_name;

DROP INDEX IF EXISTS ix_unit_types_base_unit;
DROP INDEX IF EXISTS ix_dosage_forms_route;

ALTER TABLE storage_conditions
  DROP CONSTRAINT IF EXISTS ck_storage_conditions_temp_range;

ALTER TABLE unit_types
  DROP CONSTRAINT IF EXISTS ck_unit_types_base_unit_not_self;

ALTER TABLE product_categories
  DROP CONSTRAINT IF EXISTS ck_product_categories_parent_not_self;
