-- The original 000007 schema is still referenced by legacy Purchase/Sale
-- tables. Keep it intact; the normalized model is introduced additively.
CREATE UNIQUE INDEX IF NOT EXISTS ux_branches_org_id
  ON branches (organization_id, id);
CREATE UNIQUE INDEX IF NOT EXISTS ux_warehouses_org_branch_id
  ON warehouses (organization_id, branch_id, id);
CREATE UNIQUE INDEX IF NOT EXISTS ux_inventory_batches_org_id
  ON inventory_batches (organization_id, id);

CREATE TABLE inventory_batch_identities (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id    UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  variant_id         UUID NOT NULL REFERENCES medicine_variants(id) ON DELETE RESTRICT,
  normalized_lot_no  VARCHAR(80) NOT NULL,
  display_lot_no     VARCHAR(80) NOT NULL,
  manufacturer_id    UUID,
  manufactured_on    DATE,
  expires_on         DATE NOT NULL,
  expiry_precision   VARCHAR(8) NOT NULL CHECK (expiry_precision IN ('day', 'month')),
  recall_status      VARCHAR(16) NOT NULL DEFAULT 'clear'
                       CHECK (recall_status IN ('clear', 'recalled')),
  status             VARCHAR(16) NOT NULL DEFAULT 'active'
                       CHECK (status IN ('active', 'inactive', 'exhausted', 'recalled')),
  source_metadata    JSONB NOT NULL DEFAULT '{}'::jsonb
                       CHECK (jsonb_typeof(source_metadata) = 'object'),
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, id),
  UNIQUE (organization_id, id, variant_id),
  UNIQUE (organization_id, variant_id, normalized_lot_no),
  CHECK (normalized_lot_no = lower(btrim(normalized_lot_no))),
  CHECK (length(normalized_lot_no) > 0),
  CHECK (length(display_lot_no) > 0),
  CHECK (manufactured_on IS NULL OR manufactured_on <= expires_on)
);
CREATE INDEX ix_inventory_batch_expiry
  ON inventory_batch_identities (organization_id, variant_id, expires_on, id)
  WHERE status = 'active' AND recall_status = 'clear';
CREATE INDEX ix_inventory_batch_lot_prefix
  ON inventory_batch_identities (organization_id, normalized_lot_no text_pattern_ops);

CREATE TABLE inventory_legacy_batch_links (
  organization_id       UUID NOT NULL,
  legacy_batch_id       UUID NOT NULL REFERENCES inventory_batches(id) ON DELETE RESTRICT,
  batch_identity_id     UUID NOT NULL,
  linked_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (organization_id, legacy_batch_id),
  FOREIGN KEY (organization_id, legacy_batch_id)
    REFERENCES inventory_batches (organization_id, id) ON DELETE RESTRICT,
  FOREIGN KEY (organization_id, batch_identity_id)
    REFERENCES inventory_batch_identities (organization_id, id) ON DELETE RESTRICT
);
CREATE INDEX ix_inventory_legacy_batch_identity
  ON inventory_legacy_batch_links (organization_id, batch_identity_id);

CREATE TABLE inventory_balances (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id    UUID NOT NULL,
  branch_id          UUID NOT NULL,
  warehouse_id       UUID NOT NULL,
  batch_id           UUID NOT NULL,
  condition          VARCHAR(16) NOT NULL
                       CHECK (condition IN ('sellable', 'quarantined', 'damaged', 'expired', 'recalled', 'in_transit')),
  on_hand_quantity   BIGINT NOT NULL DEFAULT 0 CHECK (on_hand_quantity >= 0),
  reserved_quantity  BIGINT NOT NULL DEFAULT 0 CHECK (reserved_quantity >= 0 AND reserved_quantity <= on_hand_quantity),
  version            BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, id),
  UNIQUE (organization_id, id, batch_id),
  UNIQUE (organization_id, warehouse_id, batch_id, condition),
  FOREIGN KEY (organization_id, branch_id)
    REFERENCES branches (organization_id, id) ON DELETE RESTRICT,
  FOREIGN KEY (organization_id, branch_id, warehouse_id)
    REFERENCES warehouses (organization_id, branch_id, id) ON DELETE RESTRICT,
  FOREIGN KEY (organization_id, batch_id)
    REFERENCES inventory_batch_identities (organization_id, id) ON DELETE RESTRICT,
  CHECK (condition = 'sellable' OR reserved_quantity = 0)
);
CREATE INDEX ix_inventory_balances_fefo
  ON inventory_balances (organization_id, warehouse_id, batch_id)
  WHERE condition = 'sellable' AND on_hand_quantity > reserved_quantity;
CREATE INDEX ix_inventory_balances_branch_variant
  ON inventory_balances (organization_id, branch_id, warehouse_id, batch_id, condition);

CREATE TABLE inventory_movements (
  id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id          UUID NOT NULL,
  branch_id                UUID NOT NULL,
  warehouse_id             UUID NOT NULL,
  batch_id                 UUID NOT NULL,
  condition                VARCHAR(16) NOT NULL
                             CHECK (condition IN ('sellable', 'quarantined', 'damaged', 'expired', 'recalled', 'in_transit')),
  movement_kind            VARCHAR(32) NOT NULL
                             CHECK (movement_kind IN ('receipt', 'sale_consumption', 'sale_return', 'purchase_return', 'adjustment_increase', 'adjustment_decrease', 'damage', 'expiry_write_off', 'transfer_dispatch', 'transfer_receipt', 'transfer_loss', 'opening_balance', 'correction', 'reversal')),
  quantity_delta           BIGINT NOT NULL CHECK (quantity_delta <> 0),
  quantity_after           BIGINT NOT NULL CHECK (quantity_after >= 0),
  balance_version          BIGINT NOT NULL CHECK (balance_version > 0),
  operation_id             UUID NOT NULL,
  operation_line_id        UUID NOT NULL,
  line_number              INTEGER NOT NULL CHECK (line_number > 0),
  source_module            VARCHAR(24) NOT NULL
                             CHECK (source_module IN ('purchase', 'sale', 'inventory', 'adjustment', 'transfer', 'stock_count')),
  source_type              VARCHAR(48),
  source_id                UUID,
  value_delta_minor        BIGINT,
  currency_code            CHAR(3),
  performed_by             UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  occurred_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
  reversal_of_movement_id  UUID REFERENCES inventory_movements(id) ON DELETE RESTRICT,
  reason                   VARCHAR(1000),
  created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, operation_id, operation_line_id),
  UNIQUE (organization_id, operation_id, line_number),
  UNIQUE (organization_id, id),
  FOREIGN KEY (organization_id, branch_id)
    REFERENCES branches (organization_id, id) ON DELETE RESTRICT,
  FOREIGN KEY (organization_id, branch_id, warehouse_id)
    REFERENCES warehouses (organization_id, branch_id, id) ON DELETE RESTRICT,
  FOREIGN KEY (organization_id, batch_id)
    REFERENCES inventory_batch_identities (organization_id, id) ON DELETE RESTRICT,
  CHECK ((value_delta_minor IS NULL AND currency_code IS NULL) OR
         (value_delta_minor IS NOT NULL AND currency_code = 'BDT'))
);
CREATE INDEX ix_inventory_movements_history
  ON inventory_movements (organization_id, warehouse_id, occurred_at DESC, id DESC);
CREATE INDEX ix_inventory_movements_operation
  ON inventory_movements (organization_id, operation_id);
CREATE INDEX ix_inventory_movements_batch_history
  ON inventory_movements (organization_id, batch_id, occurred_at DESC, id DESC);

CREATE FUNCTION inventory_reject_immutable_change() RETURNS TRIGGER AS $$
BEGIN
  RAISE EXCEPTION 'inventory evidence is append-only' USING ERRCODE = '55000';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER tr_inventory_movements_immutable
  BEFORE UPDATE OR DELETE ON inventory_movements
  FOR EACH ROW EXECUTE FUNCTION inventory_reject_immutable_change();

CREATE TABLE inventory_reservations (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  UUID NOT NULL,
  branch_id        UUID NOT NULL,
  warehouse_id     UUID NOT NULL,
  operation_id     UUID NOT NULL,
  owner_type       VARCHAR(16) NOT NULL CHECK (owner_type IN ('sale', 'checkout', 'order')),
  owner_id         UUID NOT NULL,
  status           VARCHAR(16) NOT NULL DEFAULT 'active'
                     CHECK (status IN ('active', 'consumed', 'released', 'expired', 'cancelled')),
  expires_at       TIMESTAMPTZ NOT NULL,
  consumed_at      TIMESTAMPTZ,
  released_at      TIMESTAMPTZ,
  cancelled_at     TIMESTAMPTZ,
  created_by       UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  version          BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, operation_id),
  UNIQUE (organization_id, id),
  FOREIGN KEY (organization_id, branch_id, warehouse_id)
    REFERENCES warehouses (organization_id, branch_id, id) ON DELETE RESTRICT,
  CHECK ((status = 'active' AND consumed_at IS NULL AND released_at IS NULL AND cancelled_at IS NULL) OR
         (status = 'consumed' AND consumed_at IS NOT NULL) OR
         (status = 'released' AND released_at IS NOT NULL) OR
         (status = 'expired' AND released_at IS NOT NULL) OR
         (status = 'cancelled' AND cancelled_at IS NOT NULL))
);
CREATE INDEX ix_inventory_reservations_expiry
  ON inventory_reservations (organization_id, expires_at, id) WHERE status = 'active';
CREATE INDEX ix_inventory_reservations_owner
  ON inventory_reservations (organization_id, owner_type, owner_id);

CREATE TABLE inventory_reservation_lines (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id     UUID NOT NULL,
  reservation_id      UUID NOT NULL,
  balance_id          UUID NOT NULL,
  batch_id            UUID NOT NULL,
  line_number         INTEGER NOT NULL CHECK (line_number > 0),
  reserved_quantity   BIGINT NOT NULL CHECK (reserved_quantity > 0),
  consumed_quantity   BIGINT NOT NULL DEFAULT 0 CHECK (consumed_quantity >= 0),
  released_quantity   BIGINT NOT NULL DEFAULT 0 CHECK (released_quantity >= 0),
  balance_version     BIGINT NOT NULL CHECK (balance_version > 0),
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, reservation_id, line_number),
  FOREIGN KEY (organization_id, reservation_id)
    REFERENCES inventory_reservations (organization_id, id) ON DELETE RESTRICT,
  FOREIGN KEY (organization_id, balance_id, batch_id)
    REFERENCES inventory_balances (organization_id, id, batch_id) ON DELETE RESTRICT,
  FOREIGN KEY (organization_id, batch_id)
    REFERENCES inventory_batch_identities (organization_id, id) ON DELETE RESTRICT,
  CHECK (consumed_quantity + released_quantity <= reserved_quantity)
);
CREATE INDEX ix_inventory_reservation_lines_balance
  ON inventory_reservation_lines (organization_id, balance_id);

CREATE TABLE inventory_cost_layers (
  id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id          UUID NOT NULL,
  branch_id                UUID NOT NULL,
  warehouse_id             UUID NOT NULL,
  batch_id                 UUID NOT NULL,
  origin                   VARCHAR(20) NOT NULL
                             CHECK (origin IN ('purchase', 'opening_balance', 'adjustment', 'transfer')),
  origin_operation_id      UUID NOT NULL,
  origin_operation_line_id UUID NOT NULL,
  original_quantity        BIGINT NOT NULL CHECK (original_quantity > 0),
  remaining_quantity       BIGINT NOT NULL CHECK (remaining_quantity >= 0 AND remaining_quantity <= original_quantity),
  unit_cost_minor          BIGINT NOT NULL CHECK (unit_cost_minor >= 0),
  currency_code            CHAR(3) NOT NULL CHECK (currency_code = 'BDT'),
  acquired_at              TIMESTAMPTZ NOT NULL,
  created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, id),
  UNIQUE (organization_id, origin_operation_id, origin_operation_line_id),
  FOREIGN KEY (organization_id, branch_id, warehouse_id)
    REFERENCES warehouses (organization_id, branch_id, id) ON DELETE RESTRICT,
  FOREIGN KEY (organization_id, batch_id)
    REFERENCES inventory_batch_identities (organization_id, id) ON DELETE RESTRICT
);
CREATE INDEX ix_inventory_cost_layers_fifo
  ON inventory_cost_layers (organization_id, warehouse_id, batch_id, acquired_at, id)
  WHERE remaining_quantity > 0;

CREATE TABLE inventory_cost_allocations (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id       UUID NOT NULL,
  cost_layer_id         UUID NOT NULL,
  movement_id           UUID NOT NULL,
  operation_id          UUID NOT NULL,
  operation_line_id     UUID NOT NULL,
  allocation_kind       VARCHAR(20) NOT NULL
                          CHECK (allocation_kind IN ('sale', 'sale_return', 'purchase_return', 'write_off', 'transfer', 'adjustment')),
  allocated_quantity    BIGINT NOT NULL CHECK (allocated_quantity > 0),
  allocated_value_minor BIGINT NOT NULL CHECK (allocated_value_minor >= 0),
  currency_code         CHAR(3) NOT NULL CHECK (currency_code = 'BDT'),
  allocated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, operation_id, operation_line_id, cost_layer_id, allocation_kind),
  FOREIGN KEY (organization_id, cost_layer_id)
    REFERENCES inventory_cost_layers (organization_id, id) ON DELETE RESTRICT,
  FOREIGN KEY (organization_id, movement_id)
    REFERENCES inventory_movements (organization_id, id) ON DELETE RESTRICT
);
CREATE INDEX ix_inventory_cost_allocations_layer
  ON inventory_cost_allocations (organization_id, cost_layer_id);
CREATE TRIGGER tr_inventory_cost_allocations_immutable
  BEFORE UPDATE OR DELETE ON inventory_cost_allocations
  FOR EACH ROW EXECUTE FUNCTION inventory_reject_immutable_change();

CREATE TABLE inventory_adjustments (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  UUID NOT NULL,
  branch_id        UUID NOT NULL,
  warehouse_id     UUID NOT NULL,
  operation_id     UUID NOT NULL,
  adjustment_no    VARCHAR(40) NOT NULL,
  adjustment_kind  VARCHAR(24) NOT NULL
                     CHECK (adjustment_kind IN ('count_variance', 'damage', 'expiry_write_off', 'opening_balance', 'correction', 'other_approved')),
  status           VARCHAR(16) NOT NULL DEFAULT 'draft'
                     CHECK (status IN ('draft', 'submitted', 'approved', 'rejected', 'posted', 'cancelled')),
  reason           VARCHAR(1000) NOT NULL,
  evidence_ref     VARCHAR(500),
  requested_by     UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  requested_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  approved_by      UUID REFERENCES users(id) ON DELETE RESTRICT,
  approved_at      TIMESTAMPTZ,
  rejected_by      UUID REFERENCES users(id) ON DELETE RESTRICT,
  rejected_at      TIMESTAMPTZ,
  reject_reason    VARCHAR(1000),
  posted_by        UUID REFERENCES users(id) ON DELETE RESTRICT,
  posted_at        TIMESTAMPTZ,
  cancelled_by     UUID REFERENCES users(id) ON DELETE RESTRICT,
  cancelled_at     TIMESTAMPTZ,
  version          BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, operation_id),
  UNIQUE (organization_id, adjustment_no),
  UNIQUE (organization_id, id),
  FOREIGN KEY (organization_id, branch_id, warehouse_id)
    REFERENCES warehouses (organization_id, branch_id, id) ON DELETE RESTRICT,
  CHECK ((status <> 'approved') OR (approved_by IS NOT NULL AND approved_at IS NOT NULL)),
  CHECK ((status <> 'posted') OR (posted_by IS NOT NULL AND posted_at IS NOT NULL)),
  CHECK ((status <> 'rejected') OR (rejected_by IS NOT NULL AND rejected_at IS NOT NULL AND reject_reason IS NOT NULL))
);
CREATE INDEX ix_inventory_adjustments_queue
  ON inventory_adjustments (organization_id, status, created_at, id);

CREATE TABLE inventory_adjustment_lines (
  id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id          UUID NOT NULL,
  adjustment_id            UUID NOT NULL,
  line_number              INTEGER NOT NULL CHECK (line_number > 0),
  batch_id                 UUID NOT NULL,
  condition                VARCHAR(16) NOT NULL
                             CHECK (condition IN ('sellable', 'quarantined', 'damaged', 'expired', 'recalled', 'in_transit')),
  quantity_delta           BIGINT NOT NULL CHECK (quantity_delta <> 0),
  expected_balance_version BIGINT NOT NULL CHECK (expected_balance_version >= 0),
  reason                   VARCHAR(1000) NOT NULL,
  evidence_ref             VARCHAR(500),
  unit_cost_minor          BIGINT CHECK (unit_cost_minor >= 0),
  currency_code            CHAR(3) CHECK (currency_code IS NULL OR currency_code = 'BDT'),
  created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, adjustment_id, line_number),
  FOREIGN KEY (organization_id, adjustment_id)
    REFERENCES inventory_adjustments (organization_id, id) ON DELETE RESTRICT,
  FOREIGN KEY (organization_id, batch_id)
    REFERENCES inventory_batch_identities (organization_id, id) ON DELETE RESTRICT,
  CHECK ((unit_cost_minor IS NULL AND currency_code IS NULL) OR
         (unit_cost_minor IS NOT NULL AND currency_code = 'BDT'))
);

CREATE TABLE inventory_transfers (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  operation_id        UUID NOT NULL,
  transfer_no         VARCHAR(40) NOT NULL,
  from_branch_id      UUID NOT NULL,
  from_warehouse_id   UUID NOT NULL,
  to_branch_id        UUID NOT NULL,
  to_warehouse_id     UUID NOT NULL,
  status              VARCHAR(24) NOT NULL DEFAULT 'draft'
                        CHECK (status IN ('draft', 'approved', 'dispatched', 'partially_received', 'received', 'cancelled')),
  requested_by        UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  requested_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  approved_by         UUID REFERENCES users(id) ON DELETE RESTRICT,
  approved_at         TIMESTAMPTZ,
  dispatched_by       UUID REFERENCES users(id) ON DELETE RESTRICT,
  dispatched_at       TIMESTAMPTZ,
  received_by         UUID REFERENCES users(id) ON DELETE RESTRICT,
  received_at         TIMESTAMPTZ,
  cancelled_by        UUID REFERENCES users(id) ON DELETE RESTRICT,
  cancelled_at        TIMESTAMPTZ,
  notes               VARCHAR(1000),
  version             BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, operation_id),
  UNIQUE (organization_id, transfer_no),
  UNIQUE (organization_id, id),
  FOREIGN KEY (organization_id, from_branch_id, from_warehouse_id)
    REFERENCES warehouses (organization_id, branch_id, id) ON DELETE RESTRICT,
  FOREIGN KEY (organization_id, to_branch_id, to_warehouse_id)
    REFERENCES warehouses (organization_id, branch_id, id) ON DELETE RESTRICT,
  CHECK (from_warehouse_id <> to_warehouse_id),
  CHECK ((status <> 'dispatched' AND status <> 'partially_received' AND status <> 'received') OR
         (dispatched_by IS NOT NULL AND dispatched_at IS NOT NULL))
);
CREATE INDEX ix_inventory_transfers_queue
  ON inventory_transfers (organization_id, status, created_at, id);
CREATE INDEX ix_inventory_transfers_source
  ON inventory_transfers (organization_id, from_warehouse_id, status, created_at DESC);
CREATE INDEX ix_inventory_transfers_destination
  ON inventory_transfers (organization_id, to_warehouse_id, status, created_at DESC);

CREATE TABLE inventory_transfer_lines (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id     UUID NOT NULL,
  transfer_id         UUID NOT NULL,
  line_number         INTEGER NOT NULL CHECK (line_number > 0),
  variant_id          UUID NOT NULL REFERENCES medicine_variants(id) ON DELETE RESTRICT,
  batch_id            UUID NOT NULL,
  requested_quantity  BIGINT NOT NULL CHECK (requested_quantity > 0),
  dispatched_quantity BIGINT NOT NULL DEFAULT 0 CHECK (dispatched_quantity >= 0),
  received_quantity   BIGINT NOT NULL DEFAULT 0 CHECK (received_quantity >= 0),
  lost_quantity       BIGINT NOT NULL DEFAULT 0 CHECK (lost_quantity >= 0),
  status              VARCHAR(24) NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'dispatched', 'partially_received', 'received', 'disputed', 'resolved')),
  currency_code       CHAR(3) CHECK (currency_code IS NULL OR currency_code = 'BDT'),
  notes               VARCHAR(1000),
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, transfer_id, line_number),
  FOREIGN KEY (organization_id, transfer_id)
    REFERENCES inventory_transfers (organization_id, id) ON DELETE RESTRICT,
  FOREIGN KEY (organization_id, batch_id, variant_id)
    REFERENCES inventory_batch_identities (organization_id, id, variant_id) ON DELETE RESTRICT,
  FOREIGN KEY (organization_id, batch_id)
    REFERENCES inventory_batch_identities (organization_id, id) ON DELETE RESTRICT,
  CHECK (received_quantity + lost_quantity <= dispatched_quantity),
  CHECK (dispatched_quantity <= requested_quantity)
);

CREATE TABLE inventory_recalls (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  variant_id       UUID REFERENCES medicine_variants(id) ON DELETE RESTRICT,
  batch_id         UUID,
  recall_no        VARCHAR(40) NOT NULL,
  status           VARCHAR(12) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'closed')),
  reason           VARCHAR(1000) NOT NULL,
  source_ref       VARCHAR(500),
  evidence_ref     VARCHAR(500),
  effective_at     TIMESTAMPTZ NOT NULL,
  activated_by     UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  activated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  closed_by        UUID REFERENCES users(id) ON DELETE RESTRICT,
  closed_at        TIMESTAMPTZ,
  close_reason     VARCHAR(1000),
  version          BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, recall_no),
  UNIQUE (organization_id, id),
  FOREIGN KEY (organization_id, batch_id)
    REFERENCES inventory_batch_identities (organization_id, id) ON DELETE RESTRICT,
  FOREIGN KEY (organization_id, batch_id, variant_id)
    REFERENCES inventory_batch_identities (organization_id, id, variant_id) ON DELETE RESTRICT,
  CHECK (variant_id IS NOT NULL OR batch_id IS NOT NULL),
  CHECK ((status = 'active' AND closed_at IS NULL AND closed_by IS NULL) OR
         (status = 'closed' AND closed_at IS NOT NULL AND closed_by IS NOT NULL))
);
CREATE INDEX ix_inventory_recalls_active
  ON inventory_recalls (organization_id, effective_at, id) WHERE status = 'active';
CREATE INDEX ix_inventory_recalls_variant
  ON inventory_recalls (organization_id, variant_id) WHERE status = 'active';
CREATE INDEX ix_inventory_recalls_batch
  ON inventory_recalls (organization_id, batch_id) WHERE status = 'active' AND batch_id IS NOT NULL;

CREATE TABLE inventory_stock_counts (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id   UUID NOT NULL,
  branch_id         UUID NOT NULL,
  warehouse_id      UUID NOT NULL,
  count_no          VARCHAR(40) NOT NULL,
  status            VARCHAR(16) NOT NULL DEFAULT 'draft'
                      CHECK (status IN ('draft', 'counting', 'submitted', 'approved', 'posted', 'cancelled')),
  cutoff_strategy   VARCHAR(20) NOT NULL DEFAULT 'version_snapshot'
                      CHECK (cutoff_strategy IN ('version_snapshot', 'freeze')),
  opened_by         UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  opened_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  submitted_by      UUID REFERENCES users(id) ON DELETE RESTRICT,
  submitted_at      TIMESTAMPTZ,
  approved_by       UUID REFERENCES users(id) ON DELETE RESTRICT,
  approved_at       TIMESTAMPTZ,
  posted_by         UUID REFERENCES users(id) ON DELETE RESTRICT,
  posted_at         TIMESTAMPTZ,
  cancelled_by      UUID REFERENCES users(id) ON DELETE RESTRICT,
  cancelled_at      TIMESTAMPTZ,
  notes             VARCHAR(1000),
  version           BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, count_no),
  UNIQUE (organization_id, id),
  FOREIGN KEY (organization_id, branch_id, warehouse_id)
    REFERENCES warehouses (organization_id, branch_id, id) ON DELETE RESTRICT,
  CHECK ((status NOT IN ('submitted', 'approved', 'posted')) OR
         (submitted_by IS NOT NULL AND submitted_at IS NOT NULL)),
  CHECK ((status <> 'approved' AND status <> 'posted') OR
         (approved_by IS NOT NULL AND approved_at IS NOT NULL)),
  CHECK ((status <> 'posted') OR (posted_by IS NOT NULL AND posted_at IS NOT NULL))
);
CREATE INDEX ix_inventory_stock_counts_queue
  ON inventory_stock_counts (organization_id, status, created_at, id);

CREATE TABLE inventory_stock_count_lines (
  id                         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id            UUID NOT NULL,
  stock_count_id             UUID NOT NULL,
  line_number                INTEGER NOT NULL CHECK (line_number > 0),
  balance_id                 UUID NOT NULL,
  batch_id                   UUID NOT NULL,
  condition                  VARCHAR(16) NOT NULL
                               CHECK (condition IN ('sellable', 'quarantined', 'damaged', 'expired', 'recalled', 'in_transit')),
  expected_quantity          BIGINT NOT NULL CHECK (expected_quantity >= 0),
  observed_quantity          BIGINT CHECK (observed_quantity IS NULL OR observed_quantity >= 0),
  expected_balance_version   BIGINT NOT NULL CHECK (expected_balance_version > 0),
  observed_by                UUID REFERENCES users(id) ON DELETE RESTRICT,
  observed_at                TIMESTAMPTZ,
  variance                   BIGINT NOT NULL DEFAULT 0,
  reason                     VARCHAR(1000),
  evidence_ref               VARCHAR(500),
  created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, stock_count_id, line_number),
  UNIQUE (organization_id, stock_count_id, balance_id),
  FOREIGN KEY (organization_id, stock_count_id)
    REFERENCES inventory_stock_counts (organization_id, id) ON DELETE RESTRICT,
  FOREIGN KEY (organization_id, balance_id)
    REFERENCES inventory_balances (organization_id, id) ON DELETE RESTRICT,
  FOREIGN KEY (organization_id, batch_id)
    REFERENCES inventory_batch_identities (organization_id, id) ON DELETE RESTRICT,
  CHECK ((observed_quantity IS NULL AND observed_at IS NULL) OR
         (observed_quantity IS NOT NULL AND observed_by IS NOT NULL AND observed_at IS NOT NULL))
);

CREATE TABLE inventory_reorder_policies (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id   UUID NOT NULL,
  branch_id         UUID NOT NULL,
  warehouse_id      UUID NOT NULL,
  variant_id        UUID NOT NULL REFERENCES medicine_variants(id) ON DELETE RESTRICT,
  minimum_quantity  BIGINT NOT NULL CHECK (minimum_quantity >= 0),
  target_quantity   BIGINT NOT NULL,
  safety_quantity   BIGINT NOT NULL DEFAULT 0 CHECK (safety_quantity >= 0),
  lead_time_days    INTEGER NOT NULL DEFAULT 0 CHECK (lead_time_days BETWEEN 0 AND 365),
  is_active         BOOLEAN NOT NULL DEFAULT TRUE,
  effective_from    DATE NOT NULL DEFAULT CURRENT_DATE,
  effective_to      DATE,
  version           BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, warehouse_id, variant_id),
  FOREIGN KEY (organization_id, branch_id, warehouse_id)
    REFERENCES warehouses (organization_id, branch_id, id) ON DELETE RESTRICT,
  CHECK (target_quantity >= minimum_quantity),
  CHECK (effective_to IS NULL OR effective_to >= effective_from)
);
CREATE INDEX ix_inventory_reorder_active
  ON inventory_reorder_policies (organization_id, warehouse_id, variant_id)
  WHERE is_active;

CREATE TRIGGER tr_inventory_batch_identities_upd BEFORE UPDATE ON inventory_batch_identities
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER tr_inventory_balances_upd BEFORE UPDATE ON inventory_balances
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER tr_inventory_reservations_upd BEFORE UPDATE ON inventory_reservations
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER tr_inventory_cost_layers_upd BEFORE UPDATE ON inventory_cost_layers
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER tr_inventory_adjustments_upd BEFORE UPDATE ON inventory_adjustments
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER tr_inventory_transfers_upd BEFORE UPDATE ON inventory_transfers
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER tr_inventory_transfer_lines_upd BEFORE UPDATE ON inventory_transfer_lines
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER tr_inventory_recalls_upd BEFORE UPDATE ON inventory_recalls
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER tr_inventory_stock_counts_upd BEFORE UPDATE ON inventory_stock_counts
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER tr_inventory_reorder_policies_upd BEFORE UPDATE ON inventory_reorder_policies
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();