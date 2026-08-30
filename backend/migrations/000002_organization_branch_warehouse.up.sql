-- ---------------------------------------------------------------------------
-- Prerequisites
-- ---------------------------------------------------------------------------
-- Enable the citext extension (used for case‑insensitive text columns).
CREATE EXTENSION IF NOT EXISTS citext;

-- The trigger function used by all tables to update `updated_at` on every row
-- update. (Define it once here; in a real project this would usually live in
-- migration 000001 or a shared file.)
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- ---------------------------------------------------------------------------
-- Table: organizations
-- ---------------------------------------------------------------------------
CREATE TABLE organizations (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name                  VARCHAR(200) NOT NULL,
  slug                  VARCHAR(80)  NOT NULL,
  trade_license_no      VARCHAR(80),
  drug_license_no       VARCHAR(80),
  vat_registration_no   VARCHAR(80),
  tin                   VARCHAR(80),
  subscription_plan     VARCHAR(20)  NOT NULL DEFAULT 'free'
                          CHECK (subscription_plan IN ('free','pro','enterprise')),
  is_active             BOOLEAN      NOT NULL DEFAULT TRUE,
  contact_phone         VARCHAR(30),
  contact_email         CITEXT,
  website               VARCHAR(255),
  logo_url              VARCHAR(500),
  address_line1         VARCHAR(255),
  address_line2         VARCHAR(255),
  city                  VARCHAR(100),
  state                 VARCHAR(100),
  postal_code           VARCHAR(20),
  country               VARCHAR(100),
  currency              CHAR(3)      NOT NULL DEFAULT 'BDT',
  timezone              VARCHAR(64)  NOT NULL DEFAULT 'Asia/Dhaka',
  created_at            TIMESTAMPTZ  NOT NULL DEFAULT now(),
  updated_at            TIMESTAMPTZ  NOT NULL DEFAULT now(),
  deleted_at            TIMESTAMPTZ
);

CREATE UNIQUE INDEX ux_organizations_slug ON organizations(slug) WHERE deleted_at IS NULL;
CREATE INDEX        ix_organizations_active ON organizations(is_active) WHERE deleted_at IS NULL;

CREATE TRIGGER tr_organizations_upd BEFORE UPDATE ON organizations
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- Table: branches
-- ---------------------------------------------------------------------------
CREATE TABLE branches (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  code             VARCHAR(40)  NOT NULL,
  name             VARCHAR(200) NOT NULL,
  is_active        BOOLEAN      NOT NULL DEFAULT TRUE,
  is_default       BOOLEAN      NOT NULL DEFAULT FALSE,
  address          VARCHAR(500),
  city             VARCHAR(100),
  state            VARCHAR(100),
  postal_code      VARCHAR(20),
  country          VARCHAR(100),
  email            CITEXT,
  phone            VARCHAR(30),
  latitude         DOUBLE PRECISION,
  longitude        DOUBLE PRECISION,
  open_time        VARCHAR(5),
  close_time       VARCHAR(5),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at       TIMESTAMPTZ
);

CREATE UNIQUE INDEX ux_branches_org_code ON branches(organization_id, code) WHERE deleted_at IS NULL;
CREATE INDEX        ix_branches_org      ON branches(organization_id)      WHERE deleted_at IS NULL;
CREATE INDEX        ix_branches_active   ON branches(organization_id, is_active) WHERE deleted_at IS NULL;

CREATE TRIGGER tr_branches_upd BEFORE UPDATE ON branches
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- Table: warehouses
-- ---------------------------------------------------------------------------
CREATE TABLE warehouses (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  branch_id        UUID NOT NULL REFERENCES branches(id)      ON DELETE CASCADE,
  code             VARCHAR(40)  NOT NULL,
  name             VARCHAR(200) NOT NULL,
  location         VARCHAR(255),
  capacity         INTEGER,
  is_active        BOOLEAN      NOT NULL DEFAULT TRUE,
  is_main          BOOLEAN      NOT NULL DEFAULT FALSE,
  created_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
  deleted_at       TIMESTAMPTZ
);

CREATE UNIQUE INDEX ux_warehouses_branch_code ON warehouses(branch_id, code) WHERE deleted_at IS NULL;
CREATE INDEX        ix_warehouses_branch      ON warehouses(branch_id)      WHERE deleted_at IS NULL;

-- Enforce at most one main warehouse per branch (considering only non‑deleted rows).
CREATE UNIQUE INDEX ux_warehouses_main_per_branch ON warehouses(branch_id)
  WHERE is_main = TRUE AND deleted_at IS NULL;

CREATE TRIGGER tr_warehouses_upd BEFORE UPDATE ON warehouses
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();