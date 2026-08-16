-- ##### FILE: 000010_ledger.up.sql ############################################
CREATE TABLE chart_of_accounts (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  code             VARCHAR(40) NOT NULL,
  name             VARCHAR(200) NOT NULL,
  account_type     VARCHAR(30) NOT NULL
                     CHECK (account_type IN ('asset','liability','equity','revenue','expense')),
  sub_type         VARCHAR(30),
  parent_id        UUID REFERENCES chart_of_accounts(id) ON DELETE SET NULL,
  normal_side      VARCHAR(10) NOT NULL CHECK (normal_side IN ('debit','credit')),
  is_system        BOOLEAN NOT NULL DEFAULT FALSE,
  is_active        BOOLEAN NOT NULL DEFAULT TRUE,
  description      TEXT,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at       TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_chart_of_accounts_org_code ON chart_of_accounts(organization_id, code) WHERE deleted_at IS NULL;
CREATE INDEX ix_chart_of_accounts_parent ON chart_of_accounts(parent_id);
CREATE INDEX ix_chart_of_accounts_type ON chart_of_accounts(account_type);
CREATE TRIGGER tr_chart_of_accounts_upd BEFORE UPDATE ON chart_of_accounts
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE journals (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  branch_id             UUID REFERENCES branches(id) ON DELETE SET NULL,
  journal_no            VARCHAR(40) NOT NULL,
  journal_date          TIMESTAMPTZ NOT NULL DEFAULT now(),
  source_module         VARCHAR(40) NOT NULL,
  source_id             UUID,
  description           TEXT NOT NULL,
  total_debit           NUMERIC(15,4) NOT NULL,
  total_credit          NUMERIC(15,4) NOT NULL,
  posted_by             UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  posted_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  is_reversed           BOOLEAN NOT NULL DEFAULT FALSE,
  reversed_by_journal_id UUID REFERENCES journals(id) ON DELETE SET NULL,
  status                VARCHAR(20) NOT NULL DEFAULT 'posted'
                          CHECK (status IN ('draft','posted','reversed')),
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at            TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_journals_org_no ON journals(organization_id, journal_no) WHERE deleted_at IS NULL;
CREATE INDEX ix_journals_branch ON journals(branch_id);
CREATE INDEX ix_journals_source ON journals(source_module, source_id);
CREATE INDEX ix_journals_date ON journals(journal_date DESC);
CREATE TRIGGER tr_journals_upd BEFORE UPDATE ON journals
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE journal_lines (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  journal_id   UUID NOT NULL REFERENCES journals(id) ON DELETE CASCADE,
  account_id   UUID NOT NULL REFERENCES chart_of_accounts(id) ON DELETE RESTRICT,
  branch_id    UUID REFERENCES branches(id) ON DELETE SET NULL,
  debit        NUMERIC(15,4) NOT NULL DEFAULT 0,
  credit       NUMERIC(15,4) NOT NULL DEFAULT 0,
  description  TEXT,
  line_order   INTEGER NOT NULL DEFAULT 0,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at   TIMESTAMPTZ,
  CHECK (debit >= 0 AND credit >= 0),
  CHECK ( (debit > 0 AND credit = 0) OR (credit > 0 AND debit = 0) )
);
CREATE INDEX ix_journal_lines_journal ON journal_lines(journal_id);
CREATE INDEX ix_journal_lines_account ON journal_lines(account_id);
CREATE TRIGGER tr_journal_lines_upd BEFORE UPDATE ON journal_lines
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE account_balances (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  branch_id        UUID REFERENCES branches(id) ON DELETE SET NULL,
  account_id       UUID NOT NULL REFERENCES chart_of_accounts(id) ON DELETE RESTRICT,
  period_year      INTEGER NOT NULL,
  period_month     INTEGER NOT NULL CHECK (period_month BETWEEN 1 AND 12),
  opening_debit    NUMERIC(15,4) NOT NULL DEFAULT 0,
  opening_credit   NUMERIC(15,4) NOT NULL DEFAULT 0,
  debit_amount     NUMERIC(15,4) NOT NULL DEFAULT 0,
  credit_amount    NUMERIC(15,4) NOT NULL DEFAULT 0,
  closing_debit    NUMERIC(15,4) NOT NULL DEFAULT 0,
  closing_credit   NUMERIC(15,4) NOT NULL DEFAULT 0,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at       TIMESTAMPTZ,
  UNIQUE(organization_id, branch_id, account_id, period_year, period_month)
);
CREATE INDEX ix_account_balances_account ON account_balances(account_id);
CREATE INDEX ix_account_balances_period  ON account_balances(period_year, period_month);
CREATE TRIGGER tr_account_balances_upd BEFORE UPDATE ON account_balances
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE targets (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  branch_id        UUID REFERENCES branches(id) ON DELETE SET NULL,
  name             VARCHAR(200) NOT NULL,
  target_type      VARCHAR(20) NOT NULL CHECK (target_type IN ('revenue','units','profit')),
  scope            VARCHAR(20) NOT NULL CHECK (scope IN ('org','branch')),
  period_type      VARCHAR(20) NOT NULL CHECK (period_type IN ('monthly','quarterly','yearly')),
  period_year      INTEGER NOT NULL,
  period_month     INTEGER CHECK (period_month BETWEEN 1 AND 12),
  period_quarter   INTEGER CHECK (period_quarter BETWEEN 1 AND 4),
  amount           NUMERIC(15,4) NOT NULL,
  created_by       UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  notes            TEXT,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at       TIMESTAMPTZ
);
CREATE INDEX ix_targets_org_branch ON targets(organization_id, branch_id);
CREATE INDEX ix_targets_period ON targets(period_year, period_month, period_quarter);
CREATE TRIGGER tr_targets_upd BEFORE UPDATE ON targets
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE target_progress (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  target_id        UUID NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
  achieved_amount  NUMERIC(15,4) NOT NULL DEFAULT 0,
  achieved_percent NUMERIC(6,3) NOT NULL DEFAULT 0,
  as_of_date       TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at       TIMESTAMPTZ,
  UNIQUE(target_id, as_of_date)
);
CREATE INDEX ix_target_progress_target ON target_progress(target_id);
CREATE TRIGGER tr_target_progress_upd BEFORE UPDATE ON target_progress
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();