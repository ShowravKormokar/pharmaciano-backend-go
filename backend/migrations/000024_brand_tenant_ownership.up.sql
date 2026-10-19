-- Brand is organization-owned. The existing catalog tables were global, so
-- refuse to guess ownership for any rows already present.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM brands) OR EXISTS (SELECT 1 FROM manufacturers) THEN
    RAISE EXCEPTION
      'brand tenant migration requires explicit organization_id mapping for existing brands/manufacturers';
  END IF;
END;
$$;

ALTER TABLE manufacturers
  ADD COLUMN organization_id UUID NOT NULL
    REFERENCES organizations(id) ON DELETE RESTRICT;

ALTER TABLE brands
  ADD COLUMN organization_id UUID NOT NULL
    REFERENCES organizations(id) ON DELETE RESTRICT;

CREATE UNIQUE INDEX ux_manufacturers_org_id_id
  ON manufacturers(organization_id, id);

ALTER TABLE brands
  DROP CONSTRAINT brands_manufacturer_id_fkey,
  ADD CONSTRAINT brands_org_manufacturer_fkey
    FOREIGN KEY (organization_id, manufacturer_id)
    REFERENCES manufacturers(organization_id, id)
    ON DELETE RESTRICT;

DROP INDEX ux_brands_code;
CREATE UNIQUE INDEX ux_brands_org_code
  ON brands(organization_id, code)
  WHERE deleted_at IS NULL;
CREATE INDEX ix_brands_org_manufacturer
  ON brands(organization_id, manufacturer_id)
  WHERE deleted_at IS NULL;
CREATE INDEX ix_brands_org_active_code
  ON brands(organization_id, is_active, code, id)
  WHERE deleted_at IS NULL;

CREATE INDEX ix_manufacturers_org
  ON manufacturers(organization_id)
  WHERE deleted_at IS NULL;

-- Serialize medicine creation/reassignment with Brand deactivation/deletion.
-- The lock makes the service's reference-aware soft-delete check race-safe.
CREATE FUNCTION require_active_medicine_brand()
RETURNS TRIGGER AS $$
DECLARE
  brand_is_active BOOLEAN;
BEGIN
  SELECT is_active
    INTO brand_is_active
    FROM brands
   WHERE id = NEW.brand_id
     AND deleted_at IS NULL
   FOR UPDATE;

  IF NOT FOUND OR NOT brand_is_active THEN
    RAISE EXCEPTION 'medicine brand must exist and be active'
      USING ERRCODE = '23503',
            CONSTRAINT = 'medicines_brand_active_fkey';
  END IF;

  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER tr_medicines_require_active_brand
  BEFORE INSERT OR UPDATE OF brand_id ON medicines
  FOR EACH ROW EXECUTE FUNCTION require_active_medicine_brand();
