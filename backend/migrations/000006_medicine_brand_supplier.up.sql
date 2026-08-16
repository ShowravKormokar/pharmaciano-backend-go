-- ##### FILE: 000006_medicine_brand_supplier.up.sql ###########################
CREATE TABLE medicines (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  sku                   VARCHAR(60)  NOT NULL,
  barcode               VARCHAR(60),
  name                  VARCHAR(255) NOT NULL,
  display_name          VARCHAR(255),
  description           TEXT,
  generic_medicine_id   UUID REFERENCES generic_medicines(id)    ON DELETE SET NULL,
  brand_id              UUID NOT NULL REFERENCES brands(id)      ON DELETE RESTRICT,
  category_id           UUID NOT NULL REFERENCES product_categories(id) ON DELETE RESTRICT,
  dosage_form_id        UUID REFERENCES dosage_forms(id)         ON DELETE SET NULL,
  route_id              UUID REFERENCES routes(id)               ON DELETE SET NULL,
  manufacturer_id       UUID REFERENCES manufacturers(id)        ON DELETE SET NULL,
  storage_condition_id  UUID REFERENCES storage_conditions(id)   ON DELETE SET NULL,
  strength              VARCHAR(60),
  pack_size             VARCHAR(60),
  units_per_pack        INTEGER,
  units_per_strip       INTEGER,
  package_type_id       UUID REFERENCES package_types(id)        ON DELETE SET NULL,
  sales_unit_id         UUID REFERENCES unit_types(id)           ON DELETE SET NULL,
  requires_prescription BOOLEAN NOT NULL DEFAULT FALSE,
  is_controlled         BOOLEAN NOT NULL DEFAULT FALSE,
  is_refrigerated       BOOLEAN NOT NULL DEFAULT FALSE,
  is_imported           BOOLEAN NOT NULL DEFAULT FALSE,
  country_of_origin     VARCHAR(100),
  importer_name         VARCHAR(200),
  import_license_no     VARCHAR(80),
  mrp                   NUMERIC(15,4),
  default_cost          NUMERIC(15,4),
  tax_rate_id           UUID REFERENCES tax_rates(id) ON DELETE SET NULL,
  hsn_code              VARCHAR(40),
  shelf_life_months     INTEGER,
  min_stock_threshold   INTEGER,
  max_stock_threshold   INTEGER,
  image_url             VARCHAR(500),
  is_active             BOOLEAN NOT NULL DEFAULT TRUE,
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at            TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_medicines_sku      ON medicines(sku)     WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX ux_medicines_barcode  ON medicines(barcode) WHERE deleted_at IS NULL AND barcode IS NOT NULL;
CREATE INDEX        ix_medicines_brand    ON medicines(brand_id);
CREATE INDEX        ix_medicines_category ON medicines(category_id);
CREATE INDEX        ix_medicines_generic  ON medicines(generic_medicine_id);
CREATE INDEX        ix_medicines_active   ON medicines(is_active) WHERE deleted_at IS NULL;
CREATE INDEX        ix_medicines_name_trgm    ON medicines USING gin (name gin_trgm_ops);
CREATE INDEX        ix_medicines_display_trgm ON medicines USING gin (display_name gin_trgm_ops);
CREATE TRIGGER tr_medicines_upd BEFORE UPDATE ON medicines FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE medicine_variants (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  medicine_id    UUID NOT NULL REFERENCES medicines(id) ON DELETE CASCADE,
  name           VARCHAR(200) NOT NULL,
  sku            VARCHAR(60)  NOT NULL,
  barcode        VARCHAR(60),
  strength       VARCHAR(60),
  pack_size      VARCHAR(60),
  units_per_pack INTEGER,
  mrp            NUMERIC(15,4),
  is_active      BOOLEAN NOT NULL DEFAULT TRUE,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at     TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_medicine_variants_sku    ON medicine_variants(sku) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX ux_medicine_variants_barcode ON medicine_variants(barcode) WHERE deleted_at IS NULL AND barcode IS NOT NULL;
CREATE INDEX        ix_medicine_variants_med    ON medicine_variants(medicine_id);
CREATE TRIGGER tr_medicine_variants_upd BEFORE UPDATE ON medicine_variants FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE medicine_barcodes (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  medicine_id  UUID NOT NULL REFERENCES medicines(id)         ON DELETE CASCADE,
  variant_id   UUID REFERENCES medicine_variants(id)          ON DELETE CASCADE,
  barcode      VARCHAR(60) NOT NULL,
  barcode_type VARCHAR(20) NOT NULL DEFAULT 'EAN13'
                CHECK (barcode_type IN ('EAN13','EAN8','UPC','CODE128','QR','OTHER')),
  is_primary   BOOLEAN NOT NULL DEFAULT FALSE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at   TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_medicine_barcodes_bc ON medicine_barcodes(barcode) WHERE deleted_at IS NULL;
CREATE INDEX        ix_medicine_barcodes_med ON medicine_barcodes(medicine_id);
CREATE TRIGGER tr_medicine_barcodes_upd BEFORE UPDATE ON medicine_barcodes FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE medicine_price_history (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  medicine_id     UUID NOT NULL REFERENCES medicines(id) ON DELETE CASCADE,
  price_type      VARCHAR(20) NOT NULL CHECK (price_type IN ('mrp','wholesale','retail','cost')),
  old_price       NUMERIC(15,4),
  new_price       NUMERIC(15,4) NOT NULL,
  changed_by      UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  effective_from  TIMESTAMPTZ NOT NULL DEFAULT now(),
  reason          VARCHAR(255),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at      TIMESTAMPTZ
);
CREATE INDEX ix_medicine_price_history_med ON medicine_price_history(medicine_id, effective_from DESC);
CREATE TRIGGER tr_medicine_price_history_upd BEFORE UPDATE ON medicine_price_history FOR EACH ROW EXECUTE FUNCTION set_updated_at();