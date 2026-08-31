-- ##### FILE: 000007_inventory_batch.up.sql ###################################
CREATE TABLE inventory_batches (
  id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  branch_id                UUID NOT NULL REFERENCES branches(id) ON DELETE CASCADE,
  warehouse_id             UUID REFERENCES warehouses(id) ON DELETE SET NULL,
  medicine_id              UUID NOT NULL REFERENCES medicines(id) ON DELETE RESTRICT,
  variant_id               UUID REFERENCES medicine_variants(id) ON DELETE SET NULL,
  purchase_id              UUID,
  purchase_item_id         UUID,
  supplier_id              UUID REFERENCES suppliers(id) ON DELETE SET NULL,
  batch_no                 VARCHAR(80) NOT NULL,
  mfg_date                 DATE,
  expiry_date              DATE NOT NULL,
  barcode                  VARCHAR(60),
  quantity_received        INTEGER NOT NULL DEFAULT 0,
  quantity_available       INTEGER NOT NULL DEFAULT 0,
  quantity_reserved        INTEGER NOT NULL DEFAULT 0,
  quantity_sold            INTEGER NOT NULL DEFAULT 0,
  quantity_returned        INTEGER NOT NULL DEFAULT 0,
  quantity_damaged         INTEGER NOT NULL DEFAULT 0,
  purchase_price_per_unit  NUMERIC(15,4) NOT NULL,
  mrp_per_unit             NUMERIC(15,4),
  sale_price_per_unit      NUMERIC(15,4) NOT NULL,
  sale_price_per_strip     NUMERIC(15,4),
  tax_rate_percent         NUMERIC(6,3),
  discount_percent         NUMERIC(6,3),
  status                   VARCHAR(20) NOT NULL DEFAULT 'active'
                             CHECK (status IN ('active','inactive','expired','recalled','exhausted')),
  notes                    TEXT,
  created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at               TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_inventory_batches_branch_med_batch
  ON inventory_batches(branch_id, medicine_id, batch_no, COALESCE(variant_id, '00000000-0000-0000-0000-000000000000'::uuid))
  WHERE deleted_at IS NULL;
CREATE INDEX ix_inventory_batches_org       ON inventory_batches(organization_id);
CREATE INDEX ix_inventory_batches_branch    ON inventory_batches(branch_id);
CREATE INDEX ix_inventory_batches_medicine  ON inventory_batches(medicine_id);
CREATE INDEX ix_inventory_batches_expiry    ON inventory_batches(expiry_date) WHERE deleted_at IS NULL AND status != 'exhausted';
CREATE INDEX ix_inventory_batches_available ON inventory_batches(quantity_available) WHERE quantity_available > 0;
CREATE TRIGGER tr_inventory_batches_upd BEFORE UPDATE ON inventory_batches
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE stock_movements (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  branch_id        UUID NOT NULL REFERENCES branches(id) ON DELETE CASCADE,
  warehouse_id     UUID REFERENCES warehouses(id) ON DELETE SET NULL,
  medicine_id      UUID NOT NULL REFERENCES medicines(id) ON DELETE RESTRICT,
  batch_id         UUID NOT NULL REFERENCES inventory_batches(id) ON DELETE CASCADE,
  movement_type    VARCHAR(30) NOT NULL
                     CHECK (movement_type IN ('purchase','sale','sale_return','transfer_in','transfer_out',
                                              'adjustment_in','adjustment_out','damage','expiry')),
  reference_type   VARCHAR(30),
  reference_id     UUID,
  quantity_delta   INTEGER NOT NULL,
  quantity_after   INTEGER NOT NULL,
  unit_cost        NUMERIC(15,4),
  reason           TEXT,
  performed_by     UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at       TIMESTAMPTZ
);
CREATE INDEX ix_stock_movements_batch     ON stock_movements(batch_id);
CREATE INDEX ix_stock_movements_medicine  ON stock_movements(medicine_id);
CREATE INDEX ix_stock_movements_reference ON stock_movements(reference_type, reference_id);
CREATE INDEX ix_stock_movements_created   ON stock_movements(created_at DESC);
CREATE TRIGGER tr_stock_movements_upd BEFORE UPDATE ON stock_movements
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE warehouse_transfers (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id    UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  transfer_no        VARCHAR(40) NOT NULL,
  from_branch_id     UUID NOT NULL REFERENCES branches(id) ON DELETE RESTRICT,
  to_branch_id       UUID NOT NULL REFERENCES branches(id) ON DELETE RESTRICT,
  from_warehouse_id  UUID REFERENCES warehouses(id) ON DELETE SET NULL,
  to_warehouse_id    UUID REFERENCES warehouses(id) ON DELETE SET NULL,
  status             VARCHAR(20) NOT NULL DEFAULT 'draft'
                       CHECK (status IN ('draft','dispatched','in_transit','received','cancelled')),
  requested_by       UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  approved_by        UUID REFERENCES users(id) ON DELETE SET NULL,
  dispatched_by      UUID REFERENCES users(id) ON DELETE SET NULL,
  received_by        UUID REFERENCES users(id) ON DELETE SET NULL,
  dispatched_at      TIMESTAMPTZ,
  received_at        TIMESTAMPTZ,
  notes              TEXT,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at         TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_warehouse_transfers_no ON warehouse_transfers(transfer_no) WHERE deleted_at IS NULL;
CREATE INDEX ix_warehouse_transfers_from      ON warehouse_transfers(from_branch_id);
CREATE INDEX ix_warehouse_transfers_to        ON warehouse_transfers(to_branch_id);
CREATE INDEX ix_warehouse_transfers_status    ON warehouse_transfers(status);
CREATE TRIGGER tr_warehouse_transfers_upd BEFORE UPDATE ON warehouse_transfers
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE warehouse_transfer_items (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  transfer_id  UUID NOT NULL REFERENCES warehouse_transfers(id) ON DELETE CASCADE,
  medicine_id  UUID NOT NULL REFERENCES medicines(id) ON DELETE RESTRICT,
  batch_id     UUID NOT NULL REFERENCES inventory_batches(id) ON DELETE RESTRICT,
  batch_no     VARCHAR(80) NOT NULL,
  expiry_date  DATE NOT NULL,
  quantity     INTEGER NOT NULL,
  unit_cost    NUMERIC(15,4) NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at   TIMESTAMPTZ
);
CREATE INDEX ix_warehouse_transfer_items_transfer ON warehouse_transfer_items(transfer_id);
CREATE TRIGGER tr_warehouse_transfer_items_upd BEFORE UPDATE ON warehouse_transfer_items
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();