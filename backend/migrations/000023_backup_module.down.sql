DROP TRIGGER IF EXISTS tr_system_backup_verifications_immutable ON system_backup_verifications;
DROP FUNCTION IF EXISTS reject_completed_backup_verification_mutation();
DROP TABLE IF EXISTS system_backup_verifications;

DROP TRIGGER IF EXISTS tr_system_backup_runs_upd ON system_backup_runs;
DROP TABLE IF EXISTS system_backup_runs;
