DROP TRIGGER IF EXISTS tr_report_jobs_upd ON report_jobs;
DROP FUNCTION IF EXISTS set_updated_at();

DROP TABLE IF EXISTS report_job_attempts;
DROP TABLE IF EXISTS report_run_provenance;
DROP TABLE IF EXISTS report_artifacts;
DROP TABLE IF EXISTS report_idempotency;
DROP TABLE IF EXISTS report_jobs;
