-- Drop all tables in reverse order (respecting foreign keys)

DROP TABLE IF EXISTS system_settings;
DROP TABLE IF EXISTS journal_entries;
DROP TABLE IF EXISTS ai_insights;
DROP TABLE IF EXISTS backup_logs;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS reports;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS inventory_batches;
DROP TABLE IF EXISTS return_items;
DROP TABLE IF EXISTS sales_returns;
DROP TABLE IF EXISTS sale_items;
DROP TABLE IF EXISTS sales;
DROP TABLE IF EXISTS customers;
DROP TABLE IF EXISTS purchase_items;
DROP TABLE IF EXISTS purchases;
DROP TABLE IF EXISTS suppliers;
DROP TABLE IF EXISTS medicines;
DROP TABLE IF EXISTS brands;
DROP TABLE IF EXISTS categories;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS permissions;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS warehouses;
DROP TABLE IF EXISTS branches;
DROP TABLE IF EXISTS organizations;

-- Note: pgcrypto extension is not dropped as it may be used elsewhere