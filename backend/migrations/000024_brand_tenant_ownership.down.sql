DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM brands) OR EXISTS (SELECT 1 FROM manufacturers) THEN
    RAISE EXCEPTION
      'brand tenant rollback would discard organization ownership; preserve rows and roll forward instead';
  END IF;
END;
$$;

DROP TRIGGER tr_medicines_require_active_brand ON medicines;
DROP FUNCTION require_active_medicine_brand();

ALTER TABLE brands
  DROP CONSTRAINT brands_org_manufacturer_fkey,
  ADD CONSTRAINT brands_manufacturer_id_fkey
    FOREIGN KEY (manufacturer_id)
    REFERENCES manufacturers(id)
    ON DELETE RESTRICT;

DROP INDEX ix_manufacturers_org;
DROP INDEX ix_brands_org_active_code;
DROP INDEX ix_brands_org_manufacturer;
DROP INDEX ux_brands_org_code;
DROP INDEX ux_manufacturers_org_id_id;

ALTER TABLE brands DROP COLUMN organization_id;
ALTER TABLE manufacturers DROP COLUMN organization_id;

CREATE UNIQUE INDEX ux_brands_code
  ON brands(code)
  WHERE deleted_at IS NULL;
