-- ##### FILE: 000013_ai_forecast.up.sql #######################################
CREATE TABLE ai_forecasts (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id    UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  branch_id          UUID REFERENCES branches(id) ON DELETE SET NULL,
  medicine_id        UUID REFERENCES medicines(id) ON DELETE SET NULL,
  forecast_type      VARCHAR(30) NOT NULL
                       CHECK (forecast_type IN ('demand','restock','business_summary')),
  scope              VARCHAR(20) NOT NULL CHECK (scope IN ('org','branch','medicine')),
  period_from        TIMESTAMPTZ NOT NULL,
  period_to          TIMESTAMPTZ NOT NULL,
  horizon_days       INTEGER NOT NULL,
  request_payload    JSONB NOT NULL,
  response_payload   JSONB,
  predicted_units    NUMERIC(15,4),
  predicted_revenue  NUMERIC(15,4),
  confidence         NUMERIC(4,3),  -- 0..1
  model_name         VARCHAR(80) NOT NULL,
  tokens_used        INTEGER,
  cost_usd           NUMERIC(10,6),
  requested_by       UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  expires_at         TIMESTAMPTZ NOT NULL,
  status             VARCHAR(20) NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','success','failed')),
  error_message      TEXT,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at         TIMESTAMPTZ
);
CREATE INDEX ix_ai_forecasts_org_branch ON ai_forecasts(organization_id, branch_id);
CREATE INDEX ix_ai_forecasts_medicine ON ai_forecasts(medicine_id);
CREATE INDEX ix_ai_forecasts_period ON ai_forecasts(period_from, period_to);
CREATE INDEX ix_ai_forecasts_status ON ai_forecasts(status);
CREATE TRIGGER tr_ai_forecasts_upd BEFORE UPDATE ON ai_forecasts
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

