-- ##### FILE: 000008_purchase.down.sql ########################################
ALTER TABLE inventory_batches
	DROP CONSTRAINT IF EXISTS fk_inventory_batches_purchase,
	DROP CONSTRAINT IF EXISTS fk_inventory_batches_purchase_item;
DROP TABLE IF EXISTS purchase_receipts;
DROP TABLE IF EXISTS purchase_payments;
DROP TABLE IF EXISTS purchase_items;
DROP TABLE IF EXISTS purchases;