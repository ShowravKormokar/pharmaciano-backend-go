-- Index the Medicine catalogue for the queries the module actually runs.
--
-- Two facts shape these indexes:
--
--   * The catalogue is read through the brand join. medicines has no
--     organization_id, so the tenant of a product is the organization of its
--     brand (brands.organization_id, added by 000024). A scoped read
--     therefore filters on brands.organization_id and reaches the catalogue
--     through brands.id, and the useful entry point into medicines is
--     brand_id.
--
--   * The list endpoints are keyset ("seek") paginated: the page boundary is
--     the (sort key, id) of the last row of the previous page. A keyset
--     query only stays fast while an index matches the ORDER BY, so the sort
--     column and the id tie-breaker have to be indexed together. Without
--     these indexes PostgreSQL sorts the whole tenant catalogue for every
--     page, which is exactly the cost keyset pagination exists to avoid.
--
-- The migration is additive: no column, constraint or existing index changes,
-- and no row is rewritten. The down migration removes only what this file
-- created.

-- The default order of the catalogue: name, then id as the tie-breaker.
CREATE INDEX ix_medicines_name_id
  ON medicines (name, id) WHERE deleted_at IS NULL;

-- The catalogue is also browsed by its display label and by its SKU. The
-- display label is nullable, so the index and the keyset predicate have to
-- use the same expression over it: a row-wise comparison against NULL is
-- NULL rather than true, and the index has to match the expression the
-- ORDER BY uses.
CREATE INDEX ix_medicines_display_key_id
  ON medicines (COALESCE(display_name, ''), id) WHERE deleted_at IS NULL;

CREATE INDEX ix_medicines_sku_id
  ON medicines (sku, id) WHERE deleted_at IS NULL;

-- The other orders the API allows. created_at and updated_at are descending,
-- because the newest rows are the ones a caller asks for first.
CREATE INDEX ix_medicines_created_id
  ON medicines (created_at DESC, id DESC) WHERE deleted_at IS NULL;

CREATE INDEX ix_medicines_updated_id
  ON medicines (updated_at DESC, id DESC) WHERE deleted_at IS NULL;

-- The filtered lists: by brand (the tenant of a product), by category, by
-- generic ingredient and by catalogue state.
CREATE INDEX ix_medicines_brand_name_id
  ON medicines (brand_id, name, id) WHERE deleted_at IS NULL;

CREATE INDEX ix_medicines_category_name_id
  ON medicines (category_id, name, id) WHERE deleted_at IS NULL;

CREATE INDEX ix_medicines_generic_name_id
  ON medicines (generic_medicine_id, name, id)
  WHERE deleted_at IS NULL AND generic_medicine_id IS NOT NULL;

CREATE INDEX ix_medicines_active_name_id
  ON medicines (is_active, name, id) WHERE deleted_at IS NULL;

-- The package variants of a medicine and the identifiers bound to it.
CREATE INDEX ix_medicine_variants_med_name_id
  ON medicine_variants (medicine_id, name, id) WHERE deleted_at IS NULL;

CREATE INDEX ix_medicine_barcodes_med_barcode_id
  ON medicine_barcodes (medicine_id, barcode, id) WHERE deleted_at IS NULL;

-- The Medicine module checks whether a variant is still referenced before it
-- allows a package change (contract §5.2, §10). The referencing columns carry
-- no index of their own, so without these the check is a sequential scan of
-- three operational tables on every package update.
CREATE INDEX ix_inventory_batches_variant
  ON inventory_batches (variant_id) WHERE variant_id IS NOT NULL;

CREATE INDEX ix_purchase_items_variant
  ON purchase_items (variant_id) WHERE variant_id IS NOT NULL;

CREATE INDEX ix_sale_items_variant
  ON sale_items (variant_id) WHERE variant_id IS NOT NULL;
