-- ##### FILE: 000005_master_data.up.sql #######################################
CREATE TABLE product_categories (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code         VARCHAR(40) NOT NULL,
  name         VARCHAR(160) NOT NULL,
  parent_id    UUID REFERENCES product_categories(id) ON DELETE SET NULL,
  description  TEXT,
  sort_order   INTEGER NOT NULL DEFAULT 0,
  is_active    BOOLEAN NOT NULL DEFAULT TRUE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at   TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_product_categories_code ON product_categories(code) WHERE deleted_at IS NULL;
CREATE INDEX        ix_product_categories_parent ON product_categories(parent_id);
CREATE TRIGGER tr_product_categories_upd BEFORE UPDATE ON product_categories
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE routes (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code         VARCHAR(40) NOT NULL,
  name         VARCHAR(120) NOT NULL,
  description  TEXT,
  is_active    BOOLEAN NOT NULL DEFAULT TRUE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at   TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_routes_code ON routes(code) WHERE deleted_at IS NULL;
CREATE TRIGGER tr_routes_upd BEFORE UPDATE ON routes FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE dosage_forms (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code         VARCHAR(40) NOT NULL,
  name         VARCHAR(120) NOT NULL,
  route_id     UUID REFERENCES routes(id) ON DELETE SET NULL,
  description  TEXT,
  is_active    BOOLEAN NOT NULL DEFAULT TRUE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at   TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_dosage_forms_code ON dosage_forms(code) WHERE deleted_at IS NULL;
CREATE TRIGGER tr_dosage_forms_upd BEFORE UPDATE ON dosage_forms FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE package_types (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code         VARCHAR(40) NOT NULL,
  name         VARCHAR(120) NOT NULL,
  description  TEXT,
  is_active    BOOLEAN NOT NULL DEFAULT TRUE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at   TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_package_types_code ON package_types(code) WHERE deleted_at IS NULL;
CREATE TRIGGER tr_package_types_upd BEFORE UPDATE ON package_types FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE unit_types (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code              VARCHAR(40) NOT NULL,
  name              VARCHAR(120) NOT NULL,
  symbol            VARCHAR(20),
  base_unit_id      UUID REFERENCES unit_types(id) ON DELETE SET NULL,
  conversion_factor NUMERIC(15,6),
  is_active         BOOLEAN NOT NULL DEFAULT TRUE,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at        TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_unit_types_code ON unit_types(code) WHERE deleted_at IS NULL;
CREATE TRIGGER tr_unit_types_upd BEFORE UPDATE ON unit_types FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE storage_conditions (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code              VARCHAR(40) NOT NULL,
  name              VARCHAR(120) NOT NULL,
  min_temp_celsius  NUMERIC(5,2),
  max_temp_celsius  NUMERIC(5,2),
  description       TEXT,
  is_active         BOOLEAN NOT NULL DEFAULT TRUE,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at        TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_storage_conditions_code ON storage_conditions(code) WHERE deleted_at IS NULL;
CREATE TRIGGER tr_storage_conditions_upd BEFORE UPDATE ON storage_conditions FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE tax_rates (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code         VARCHAR(40) NOT NULL,
  name         VARCHAR(120) NOT NULL,
  rate_percent NUMERIC(6,3) NOT NULL,
  is_default   BOOLEAN NOT NULL DEFAULT FALSE,
  is_active    BOOLEAN NOT NULL DEFAULT TRUE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at   TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_tax_rates_code ON tax_rates(code) WHERE deleted_at IS NULL;
CREATE TRIGGER tr_tax_rates_upd BEFORE UPDATE ON tax_rates FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE manufacturers (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code            VARCHAR(40) NOT NULL,
  name            VARCHAR(200) NOT NULL,
  country         VARCHAR(100),
  address         VARCHAR(500),
  contact_person  VARCHAR(120),
  phone           VARCHAR(30),
  email           CITEXT,
  website         VARCHAR(255),
  drug_license_no VARCHAR(80),
  is_active       BOOLEAN NOT NULL DEFAULT TRUE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at      TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_manufacturers_code ON manufacturers(code) WHERE deleted_at IS NULL;
CREATE INDEX        ix_manufacturers_name ON manufacturers USING gin (name gin_trgm_ops);
CREATE TRIGGER tr_manufacturers_upd BEFORE UPDATE ON manufacturers FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE brands (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code               VARCHAR(40) NOT NULL,
  name               VARCHAR(200) NOT NULL,
  manufacturer_id    UUID NOT NULL REFERENCES manufacturers(id) ON DELETE RESTRICT,
  country_of_origin  VARCHAR(100),
  description        TEXT,
  is_active          BOOLEAN NOT NULL DEFAULT TRUE,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at         TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_brands_code ON brands(code) WHERE deleted_at IS NULL;
CREATE INDEX        ix_brands_manufacturer ON brands(manufacturer_id);
CREATE INDEX        ix_brands_name ON brands USING gin (name gin_trgm_ops);
CREATE TRIGGER tr_brands_upd BEFORE UPDATE ON brands FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE suppliers (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code                VARCHAR(40) NOT NULL,
  name                VARCHAR(200) NOT NULL,
  contact_person      VARCHAR(120),
  phone               VARCHAR(30),
  email               CITEXT,
  address             VARCHAR(500),
  city                VARCHAR(100),
  country             VARCHAR(100),
  drug_license_no     VARCHAR(80),
  tin                 VARCHAR(80),
  credit_limit        NUMERIC(15,4),
  credit_period_days  INTEGER,
  opening_balance     NUMERIC(15,4) NOT NULL DEFAULT 0,
  current_balance     NUMERIC(15,4) NOT NULL DEFAULT 0,
  is_active           BOOLEAN NOT NULL DEFAULT TRUE,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at          TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_suppliers_code ON suppliers(code) WHERE deleted_at IS NULL;
CREATE INDEX        ix_suppliers_name ON suppliers USING gin (name gin_trgm_ops);
CREATE TRIGGER tr_suppliers_upd BEFORE UPDATE ON suppliers FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE drug_groups (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code         VARCHAR(40) NOT NULL,
  name         VARCHAR(160) NOT NULL,
  description  TEXT,
  is_active    BOOLEAN NOT NULL DEFAULT TRUE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at   TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_drug_groups_code ON drug_groups(code) WHERE deleted_at IS NULL;
CREATE TRIGGER tr_drug_groups_upd BEFORE UPDATE ON drug_groups FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE drug_classes (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code          VARCHAR(40) NOT NULL,
  name          VARCHAR(160) NOT NULL,
  drug_group_id UUID REFERENCES drug_groups(id) ON DELETE SET NULL,
  description   TEXT,
  is_active     BOOLEAN NOT NULL DEFAULT TRUE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_drug_classes_code ON drug_classes(code) WHERE deleted_at IS NULL;
CREATE INDEX        ix_drug_classes_group ON drug_classes(drug_group_id);
CREATE TRIGGER tr_drug_classes_upd BEFORE UPDATE ON drug_classes FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE therapeutic_classes (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code          VARCHAR(40) NOT NULL,
  name          VARCHAR(160) NOT NULL,
  drug_class_id UUID REFERENCES drug_classes(id) ON DELETE SET NULL,
  description   TEXT,
  is_active     BOOLEAN NOT NULL DEFAULT TRUE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_therapeutic_classes_code ON therapeutic_classes(code) WHERE deleted_at IS NULL;
CREATE INDEX        ix_therapeutic_classes_class ON therapeutic_classes(drug_class_id);
CREATE TRIGGER tr_therapeutic_classes_upd BEFORE UPDATE ON therapeutic_classes FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE generic_medicines (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code                  VARCHAR(40) NOT NULL,
  name                  VARCHAR(200) NOT NULL,
  description           TEXT,
  therapeutic_class_id  UUID REFERENCES therapeutic_classes(id) ON DELETE SET NULL,
  is_controlled         BOOLEAN NOT NULL DEFAULT FALSE,
  is_active             BOOLEAN NOT NULL DEFAULT TRUE,
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at            TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_generic_medicines_code ON generic_medicines(code) WHERE deleted_at IS NULL;
CREATE INDEX        ix_generic_medicines_name ON generic_medicines USING gin (name gin_trgm_ops);
CREATE INDEX        ix_generic_medicines_tc   ON generic_medicines(therapeutic_class_id);
CREATE TRIGGER tr_generic_medicines_upd BEFORE UPDATE ON generic_medicines FOR EACH ROW EXECUTE FUNCTION set_updated_at();