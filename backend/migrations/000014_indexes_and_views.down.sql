-- ##### FILE: 000014_indexes_and_views.down.sql #############################
DROP VIEW IF EXISTS view_purchase_outstandings;
DROP VIEW IF EXISTS view_daily_sales;
DROP VIEW IF EXISTS view_stock_summary;

-- Dropping indexes is optional; we can drop them if needed.
DROP INDEX IF EXISTS ix_journal_lines_journal_order;
DROP INDEX IF EXISTS ix_journal_lines_account_br;
DROP INDEX IF EXISTS ix_stock_movements_ref_id;
DROP INDEX IF EXISTS ix_purchases_reference_no;
DROP INDEX IF EXISTS ix_inventory_batches_batch_no;
DROP INDEX IF EXISTS ix_medicines_barcode_trgm;
DROP INDEX IF EXISTS ix_medicines_sku_trgm;