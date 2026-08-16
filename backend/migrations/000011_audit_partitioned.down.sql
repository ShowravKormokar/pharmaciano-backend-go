-- ##### FILE: 000011_audit_partitioned.down.sql ###############################
DROP TABLE IF EXISTS audit_log_archive;
DROP TABLE IF EXISTS audit_logs_default;
DROP TABLE IF EXISTS audit_logs;