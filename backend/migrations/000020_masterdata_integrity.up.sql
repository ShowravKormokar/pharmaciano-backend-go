-- ##### FILE: 000020_masterdata_integrity.up.sql ############################
-- Integrity constraints and query indexes for the eleven central MasterData
-- resources created by 000005_master_data.up.sql.
--
-- This migration is strictly additive. It creates no table, drops no table and
-- alters no column type, so it is safe to apply to a database that already
-- carries data. Nothing here changes the meaning of an existing row.
--
-- 1. Three CHECK constraints. All three facts are row-level, so the database
--    can refuse them no matter which client wrote the row. Phase 03 still
--    rejects them in the request validator and answers 400; this is the
--    backstop for writes that never pass through a handler (the seeders, psql,
--    a future importer).
--
--      * A category may not be its own parent, and a unit may not be its own
--        base unit. Multi-node cycles in those two self-referencing tables are
--        NOT row-level facts and remain a service-layer concern (Phase 06).
--      * A storage band may omit either end, but if both ends are supplied they
--        must not be inverted.
--
-- 2. Indexes.
--      * dosage_forms.route_id and unit_types.base_unit_id are foreign keys that
--        000005 created with no index on the referencing column. Without one,
--        every parent-row UPDATE/DELETE and every FK existence check scans the
--        whole child table.
--      * The GIN trigram indexes close the `?q=` gap recorded as C-07.
--        docs/api/API.md §2.6 contracts `?q=` as "trigram text search", and
--        API.md §7.7 puts it on the /master list endpoints, but 000005 created
--        trigram indexes only on manufacturers, brands, suppliers and
--        generic_medicines. On the other ten tables a search was a sequential
--        scan. pg_trgm is installed by 000001.
--
-- Naming follows the conventions already in the repository: `ck_<table>_<rule>`
-- for check constraints (000015, 000016), `ix_<table>_<column>` for indexes
-- (000005). Index and constraint names share one namespace per schema, so
-- none of these may collide with 000005.
--
-- Before applying to a populated database, confirm the temperature check will
-- validate, since ADD CONSTRAINT verifies every existing row:
--
--     SELECT id, code FROM storage_conditions
--      WHERE min_temp_celsius IS NOT NULL AND max_temp_celsius IS NOT NULL
--        AND min_temp_celsius > max_temp_celsius;
--
-- The bundled seed data (seed/storage_conditions.yaml) is already clean.
--
-- Deliberately NOT constrained, because no document in the repository settles
-- the rule and choosing a bound would be a business decision, not a schema
-- decision (contract §11.2):
--
--   * unit_types.conversion_factor bounds. seed/unit_types.yaml:25 calls the
--     factor a "default; overridden per medicine", so it is not an invariant
--     on this row. Whether 0 or negative is legal, and whether a base unit may
--     carry a factor at all, is undecided.
--   * tax_rates.rate_percent bounds. NUMERIC(6,3) admits -999.999. No document
--     states a valid interval, and this table is a rate catalogue rather than a
--     tax line, so a sign rule cannot be inferred (C-08).
--   * Exactly one is_default tax rate. Neither the schema nor any code
--     enforces it today, and no rule says which row wins a conflict (C-08).
--   * product_categories.sort_order uniqueness or contiguity. seed/categories.yaml
--     uses 999 as a "Miscellaneous" sentinel, so gaps are expected by design.
--   * Case-insensitive code uniqueness. The partial unique indexes from 000005
--     are case-sensitive, and nothing states that "Tab" and "tab" must collide.
--
-- MasterData tables are small reference catalogues, so the ACCESS EXCLUSIVE
-- locks and the sequential scans taken by ADD CONSTRAINT and CREATE INDEX are
-- acceptable without CONCURRENTLY. Revisit if a catalogue ever grows large.
-- ---------------------------------------------------------------------------

-- A category cannot parent itself.
ALTER TABLE product_categories
  ADD CONSTRAINT ck_product_categories_parent_not_self
  CHECK (parent_id IS DISTINCT FROM id);

-- A unit cannot be its own base unit.
ALTER TABLE unit_types
  ADD CONSTRAINT ck_unit_types_base_unit_not_self
  CHECK (base_unit_id IS DISTINCT FROM id);

-- A storage band is optional at either end, but must not be inverted.
ALTER TABLE storage_conditions
  ADD CONSTRAINT ck_storage_conditions_temp_range
  CHECK (min_temp_celsius IS NULL OR max_temp_celsius IS NULL
         OR min_temp_celsius <= max_temp_celsius);

-- Foreign keys introduced by 000005 with no index on the referencing column.
CREATE INDEX ix_dosage_forms_route   ON dosage_forms(route_id);
CREATE INDEX ix_unit_types_base_unit ON unit_types(base_unit_id);

-- Trigram indexes for `?q=` (C-07).
--
-- `?q=` searches BOTH `name` and `code`, as
--   (name ILIKE $1 OR code ILIKE $1)
-- An OR can only be served by a BitmapOr when BOTH arms are indexable, so indexing
-- `name` alone leaves the whole predicate with no index plan and PostgreSQL
-- sequentially scans the table. `code` therefore needs a trigram index too --
-- the partial unique indexes on `code` are btree and can never serve a
-- leading-wildcard ILIKE.
--
-- All eleven tables are covered, including generic_medicines, whose `name` index
-- already exists from 000005 but whose `code` is unindexed.
CREATE INDEX ix_product_categories_name  ON product_categories  USING gin (name gin_trgm_ops);
CREATE INDEX ix_dosage_forms_name        ON dosage_forms        USING gin (name gin_trgm_ops);
CREATE INDEX ix_routes_name              ON routes              USING gin (name gin_trgm_ops);
CREATE INDEX ix_package_types_name       ON package_types       USING gin (name gin_trgm_ops);
CREATE INDEX ix_unit_types_name          ON unit_types          USING gin (name gin_trgm_ops);
CREATE INDEX ix_storage_conditions_name  ON storage_conditions  USING gin (name gin_trgm_ops);
CREATE INDEX ix_tax_rates_name           ON tax_rates           USING gin (name gin_trgm_ops);
CREATE INDEX ix_drug_groups_name         ON drug_groups         USING gin (name gin_trgm_ops);
CREATE INDEX ix_drug_classes_name        ON drug_classes        USING gin (name gin_trgm_ops);
CREATE INDEX ix_therapeutic_classes_name ON therapeutic_classes USING gin (name gin_trgm_ops);

CREATE INDEX ix_product_categories_code  ON product_categories  USING gin (code gin_trgm_ops);
CREATE INDEX ix_dosage_forms_code        ON dosage_forms        USING gin (code gin_trgm_ops);
CREATE INDEX ix_routes_code              ON routes              USING gin (code gin_trgm_ops);
CREATE INDEX ix_package_types_code       ON package_types       USING gin (code gin_trgm_ops);
CREATE INDEX ix_unit_types_code          ON unit_types          USING gin (code gin_trgm_ops);
CREATE INDEX ix_storage_conditions_code  ON storage_conditions  USING gin (code gin_trgm_ops);
CREATE INDEX ix_tax_rates_code           ON tax_rates           USING gin (code gin_trgm_ops);
CREATE INDEX ix_drug_groups_code         ON drug_groups         USING gin (code gin_trgm_ops);
CREATE INDEX ix_drug_classes_code        ON drug_classes        USING gin (code gin_trgm_ops);
CREATE INDEX ix_therapeutic_classes_code ON therapeutic_classes USING gin (code gin_trgm_ops);
CREATE INDEX ix_generic_medicines_code   ON generic_medicines   USING gin (code gin_trgm_ops);
