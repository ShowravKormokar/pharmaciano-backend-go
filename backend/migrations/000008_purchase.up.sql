-- ##### FILE: 000008_purchase.up.sql ##########################################
-- Note: purchases table references purchase_items but purchase_items references purchases;
-- we need to create purchases first, then purchase_items, purchase_payments, purchase_receipts.
CREATE TABLE purchases (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  branch_id            UUID NOT NULL REFERENCES branches(id) ON DELETE CASCADE,
  supplier_id          UUID NOT NULL REFERENCES suppliers(id) ON DELETE RESTRICT,
  purchase_no          VARCHAR(40) NOT NULL,
  reference_no         VARCHAR(80),
  status               VARCHAR(20) NOT NULL DEFAULT 'draft'
                         CHECK (status IN ('draft','pending_approval','approved','rejected','dispatched',
                                           'partially_received','received','completed','cancelled')),
  payment_status       VARCHAR(20) NOT NULL DEFAULT 'unpaid'
                         CHECK (payment_status IN ('unpaid','partial','paid')),
  subtotal             NUMERIC(15,4) NOT NULL DEFAULT 0,
  discount_amount      NUMERIC(15,4) NOT NULL DEFAULT 0,
  tax_amount           NUMERIC(15,4) NOT NULL DEFAULT 0,
  other_charges        NUMERIC(15,4) NOT NULL DEFAULT 0,
  total_amount         NUMERIC(15,4) NOT NULL DEFAULT 0,
  paid_amount          NUMERIC(15,4) NOT NULL DEFAULT 0,
  due_amount           NUMERIC(15,4) NOT NULL DEFAULT 0,
  requested_by         UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  approved_by          UUID REFERENCES users(id) ON DELETE SET NULL,
  approved_at          TIMESTAMPTZ,
  rejected_by          UUID REFERENCES users(id) ON DELETE SET NULL,
  rejected_at          TIMESTAMPTZ,
  rejection_reason     TEXT,
  received_at          TIMESTAMPTZ,
  expected_delivery_at TIMESTAMPTZ,
  completed_at         TIMESTAMPTZ,
  notes                TEXT,
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at           TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_purchases_no ON purchases(purchase_no) WHERE deleted_at IS NULL;
CREATE INDEX ix_purchases_supplier ON purchases(supplier_id);
CREATE INDEX ix_purchases_branch   ON purchases(branch_id);
CREATE INDEX ix_purchases_status   ON purchases(status);
CREATE INDEX ix_purchases_payment  ON purchases(payment_status);
CREATE TRIGGER tr_purchases_upd BEFORE UPDATE ON purchases
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE purchase_items (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  purchase_id       UUID NOT NULL REFERENCES purchases(id) ON DELETE CASCADE,
  medicine_id       UUID NOT NULL REFERENCES medicines(id) ON DELETE RESTRICT,
  variant_id        UUID REFERENCES medicine_variants(id) ON DELETE SET NULL,
  batch_no          VARCHAR(80) NOT NULL,
  mfg_date          DATE,
  expiry_date       DATE NOT NULL,
  quantity_ordered  INTEGER NOT NULL,
  quantity_received INTEGER NOT NULL DEFAULT 0,
  unit_cost         NUMERIC(15,4) NOT NULL,
  discount_percent  NUMERIC(6,3) NOT NULL DEFAULT 0,
  tax_rate_percent  NUMERIC(6,3) NOT NULL DEFAULT 0,
  line_total        NUMERIC(15,4) NOT NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at        TIMESTAMPTZ
);
CREATE INDEX ix_purchase_items_purchase ON purchase_items(purchase_id);
CREATE INDEX ix_purchase_items_medicine ON purchase_items(medicine_id);
CREATE TRIGGER tr_purchase_items_upd BEFORE UPDATE ON purchase_items
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE purchase_payments (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  purchase_id   UUID NOT NULL REFERENCES purchases(id) ON DELETE CASCADE,
  payment_no    VARCHAR(40) NOT NULL,
  amount        NUMERIC(15,4) NOT NULL,
  method        VARCHAR(20) NOT NULL CHECK (method IN ('cash','bank','mobile','cheque','adjustment')),
  reference_no  VARCHAR(80),
  paid_on       TIMESTAMPTZ NOT NULL DEFAULT now(),
  paid_by       UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  notes         TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_purchase_payments_no ON purchase_payments(payment_no) WHERE deleted_at IS NULL;
CREATE INDEX ix_purchase_payments_purchase ON purchase_payments(purchase_id);
CREATE TRIGGER tr_purchase_payments_upd BEFORE UPDATE ON purchase_payments
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE purchase_receipts (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  purchase_id          UUID NOT NULL REFERENCES purchases(id) ON DELETE CASCADE,
  receipt_no           VARCHAR(40) NOT NULL,
  delivery_challan_no  VARCHAR(80),
  received_by          UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  received_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  notes                TEXT,
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at           TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_purchase_receipts_no ON purchase_receipts(receipt_no) WHERE deleted_at IS NULL;
CREATE INDEX ix_purchase_receipts_purchase ON purchase_receipts(purchase_id);
CREATE TRIGGER tr_purchase_receipts_upd BEFORE UPDATE ON purchase_receipts
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE inventory_batches
  ADD CONSTRAINT fk_inventory_batches_purchase
  FOREIGN KEY (purchase_id) REFERENCES purchases(id) ON DELETE SET NULL,
  ADD CONSTRAINT fk_inventory_batches_purchase_item
  FOREIGN KEY (purchase_item_id) REFERENCES purchase_items(id) ON DELETE SET NULL;