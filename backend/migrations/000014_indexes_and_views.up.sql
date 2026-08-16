-- ##### FILE: 000014_indexes_and_views.up.sql #################################
-- Additional indexes not covered by previous migrations, plus useful views.

-- ----- Indexes for common search and performance -----
-- medicine name trigram already created in 000006, but we add more.
CREATE INDEX IF NOT EXISTS ix_medicines_sku_trgm ON medicines USING gin (sku gin_trgm_ops);
CREATE INDEX IF NOT EXISTS ix_medicines_barcode_trgm ON medicines USING gin (barcode gin_trgm_ops);

-- Inventory batch: search by batch_no
CREATE INDEX IF NOT EXISTS ix_inventory_batches_batch_no ON inventory_batches(batch_no) WHERE deleted_at IS NULL;

-- Purchase: search by reference_no
CREATE INDEX IF NOT EXISTS ix_purchases_reference_no ON purchases(reference_no) WHERE reference_no IS NOT NULL;

-- Sales: search by customer name (via customer table)
-- Already have customer name trigram.

-- Stock movements: by reference_id quickly
CREATE INDEX IF NOT EXISTS ix_stock_movements_ref_id ON stock_movements(reference_id) WHERE reference_id IS NOT NULL;

-- Audit logs: created_at for range queries
-- Already have partition and index on created_at.

-- Journal lines: composite for balances
CREATE INDEX IF NOT EXISTS ix_journal_lines_account_br ON journal_lines(account_id, branch_id);
CREATE INDEX IF NOT EXISTS ix_journal_lines_journal_order ON journal_lines(journal_id, line_order);

-- Account balances: quick lookup for closing
CREATE INDEX IF NOT EXISTS ix_account_balances_org_acc ON account_balances(organization_id, account_id);

-- ----- Views -----
-- 1) Current stock summary per medicine/branch
CREATE OR REPLACE VIEW view_stock_summary AS
SELECT
  ib.organization_id,
  ib.branch_id,
  ib.medicine_id,
  m.name AS medicine_name,
  m.sku,
  SUM(ib.quantity_available) AS total_available,
  SUM(ib.quantity_reserved)  AS total_reserved,
  SUM(ib.quantity_sold)      AS total_sold,
  SUM(ib.quantity_damaged)   AS total_damaged,
  MIN(ib.expiry_date)        AS earliest_expiry,
  COUNT(ib.id)               AS batch_count
FROM inventory_batches ib
JOIN medicines m ON m.id = ib.medicine_id
WHERE ib.deleted_at IS NULL AND ib.status != 'exhausted' AND ib.status != 'expired'
GROUP BY ib.organization_id, ib.branch_id, ib.medicine_id, m.name, m.sku;

-- 2) Sales ledger summary per day
CREATE OR REPLACE VIEW view_daily_sales AS
SELECT
  s.organization_id,
  s.branch_id,
  DATE(s.sold_at) AS sale_date,
  COUNT(s.id) AS transaction_count,
  SUM(s.total_amount) AS total_revenue,
  SUM(s.discount_amount) AS total_discount,
  SUM(s.tax_amount) AS total_tax,
  SUM(s.paid_amount) AS total_paid,
  SUM(s.change_amount) AS total_change
FROM sales s
WHERE s.deleted_at IS NULL AND s.status = 'completed'
GROUP BY s.organization_id, s.branch_id, DATE(s.sold_at)
ORDER BY sale_date DESC;

-- 3) Purchase outstanding balances
CREATE OR REPLACE VIEW view_purchase_outstandings AS
SELECT
  p.id AS purchase_id,
  p.purchase_no,
  p.supplier_id,
  s.name AS supplier_name,
  p.total_amount,
  p.paid_amount,
  p.due_amount,
  p.status,
  p.payment_status,
  p.created_at
FROM purchases p
JOIN suppliers s ON s.id = p.supplier_id
WHERE p.deleted_at IS NULL AND p.due_amount > 0
ORDER BY p.due_amount DESC;
