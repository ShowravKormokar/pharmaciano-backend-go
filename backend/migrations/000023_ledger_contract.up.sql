-- Evolve the original ledger schema in place. The original migration stores
-- BDT major units as NUMERIC(15,4); the current application uses integer poisha.
-- Abort before changing anything if legacy data cannot be represented exactly.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM organizations o
    JOIN journals j ON j.organization_id = o.id
    WHERE o.currency <> 'BDT'
  ) OR EXISTS (
    SELECT 1 FROM organizations o
    JOIN account_balances b ON b.organization_id = o.id
    WHERE o.currency <> 'BDT'
  ) THEN
    RAISE EXCEPTION 'ledger migration supports existing BDT journals only';
  END IF;

  IF EXISTS (
    SELECT 1 FROM journals
    WHERE total_debit <> round(total_debit, 2)
       OR total_credit <> round(total_credit, 2)
  ) OR EXISTS (
    SELECT 1 FROM journal_lines
    WHERE debit <> round(debit, 2) OR credit <> round(credit, 2)
  ) OR EXISTS (
    SELECT 1 FROM account_balances
    WHERE opening_debit <> round(opening_debit, 2)
       OR opening_credit <> round(opening_credit, 2)
       OR debit_amount <> round(debit_amount, 2)
       OR credit_amount <> round(credit_amount, 2)
       OR closing_debit <> round(closing_debit, 2)
       OR closing_credit <> round(closing_credit, 2)
  ) THEN
    RAISE EXCEPTION 'ledger migration found amounts more precise than BDT poisha';
  END IF;

  IF EXISTS (
    SELECT j.id
    FROM journals j
    LEFT JOIN journal_lines l ON l.journal_id = j.id AND l.deleted_at IS NULL
    WHERE j.status IN ('posted', 'reversed')
    GROUP BY j.id, j.total_debit, j.total_credit
    HAVING count(l.id) < 2
       OR coalesce(sum(l.debit), 0) <> j.total_debit
       OR coalesce(sum(l.credit), 0) <> j.total_credit
  ) THEN
    RAISE EXCEPTION 'ledger migration found a posted journal with invalid lines';
  END IF;
END;
$$;

ALTER TABLE chart_of_accounts RENAME TO ledger_accounts;
ALTER TABLE journals RENAME TO ledger_journals;
ALTER TABLE journal_lines RENAME TO ledger_lines;
ALTER TABLE account_balances RENAME TO ledger_account_balances;

ALTER TABLE ledger_accounts
  ADD COLUMN is_postable BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN deactivation_reason VARCHAR(500),
  ADD COLUMN version BIGINT NOT NULL DEFAULT 1;
UPDATE ledger_accounts a
SET is_postable = NOT EXISTS (
  SELECT 1 FROM ledger_accounts child
  WHERE child.organization_id = a.organization_id
    AND child.parent_id = a.id
    AND child.deleted_at IS NULL
);
CREATE UNIQUE INDEX ux_ledger_accounts_org_id ON ledger_accounts(organization_id, id);
CREATE INDEX ix_ledger_accounts_org_parent_code
  ON ledger_accounts(organization_id, parent_id, code)
  WHERE deleted_at IS NULL;

CREATE TABLE ledger_periods (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  name            VARCHAR(100) NOT NULL,
  start_date      DATE NOT NULL,
  end_date        DATE NOT NULL,
  status          VARCHAR(20) NOT NULL DEFAULT 'open'
                    CHECK (status IN ('open', 'closed')),
  closed_at       TIMESTAMPTZ,
  closed_by       UUID REFERENCES users(id) ON DELETE RESTRICT,
  closed_reason   VARCHAR(500),
  reopened_at     TIMESTAMPTZ,
  reopened_by     UUID REFERENCES users(id) ON DELETE RESTRICT,
  reopened_reason VARCHAR(500),
  version         BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at      TIMESTAMPTZ,
  CHECK (start_date <= end_date),
  CHECK (status = 'open' OR (status = 'closed' AND closed_at IS NOT NULL)),
  UNIQUE (organization_id, id),
  EXCLUDE USING gist (
    organization_id WITH =,
    daterange(start_date, end_date, '[]') WITH &&
  )
);
CREATE INDEX ix_ledger_periods_org_status_start
  ON ledger_periods(organization_id, status, start_date DESC)
  WHERE deleted_at IS NULL;

-- Preserve posted legacy entries by assigning each to its calendar-year period.
INSERT INTO ledger_periods (organization_id, name, start_date, end_date, status, closed_at)
SELECT
  j.organization_id,
  extract(year FROM j.journal_date)::int::text,
  make_date(extract(year FROM j.journal_date)::int, 1, 1),
  make_date(extract(year FROM j.journal_date)::int, 12, 31),
  CASE WHEN extract(year FROM j.journal_date) < extract(year FROM current_date)
       THEN 'closed' ELSE 'open' END,
  CASE WHEN extract(year FROM j.journal_date) < extract(year FROM current_date)
       THEN now() ELSE NULL END
FROM ledger_journals j
GROUP BY j.organization_id, extract(year FROM j.journal_date);

ALTER TABLE ledger_journals
  RENAME COLUMN total_debit TO total_debit_minor;
ALTER TABLE ledger_journals
  RENAME COLUMN total_credit TO total_credit_minor;
ALTER TABLE ledger_journals
  ALTER COLUMN total_debit_minor TYPE BIGINT
    USING (total_debit_minor * 100)::BIGINT,
  ALTER COLUMN total_credit_minor TYPE BIGINT
    USING (total_credit_minor * 100)::BIGINT,
  ALTER COLUMN posted_by DROP NOT NULL,
  ALTER COLUMN posted_at DROP NOT NULL,
  ALTER COLUMN status SET DEFAULT 'draft',
  ADD COLUMN idempotency_key VARCHAR(200),
  ADD COLUMN period_id UUID,
  ADD COLUMN currency_code CHAR(3) NOT NULL DEFAULT 'BDT',
  ADD COLUMN reversal_of_journal_id UUID,
  ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0);

UPDATE ledger_journals
SET status = CASE WHEN status = 'reversed' THEN 'posted' ELSE status END,
    posted_by = CASE WHEN status = 'draft' THEN NULL ELSE posted_by END,
    posted_at = CASE WHEN status = 'draft' THEN NULL ELSE coalesce(posted_at, created_at) END,
    period_id = p.id,
    currency_code = o.currency
FROM ledger_periods p
JOIN organizations o ON o.id = p.organization_id
WHERE p.organization_id = ledger_journals.organization_id
  AND ledger_journals.journal_date::date BETWEEN p.start_date AND p.end_date
  ;

-- A historical posted row must have an actor. If the legacy user was removed,
-- the migration stops instead of inventing or discarding financial evidence.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM ledger_journals
    WHERE status IN ('posted', 'reversed') AND posted_by IS NULL
  ) THEN
    RAISE EXCEPTION 'ledger migration cannot identify the actor for a posted journal';
  END IF;
END;
$$;

UPDATE ledger_journals
SET period_id = p.id,
    currency_code = o.currency
FROM ledger_periods p
JOIN organizations o ON o.id = p.organization_id
WHERE p.organization_id = ledger_journals.organization_id
  AND ledger_journals.journal_date::date BETWEEN p.start_date AND p.end_date
  ;

ALTER TABLE ledger_journals
  ALTER COLUMN period_id SET NOT NULL,
  ADD CONSTRAINT fk_ledger_journals_period
    FOREIGN KEY (organization_id, period_id)
    REFERENCES ledger_periods(organization_id, id) ON DELETE RESTRICT,
  ADD CONSTRAINT uq_ledger_journals_org_id UNIQUE (organization_id, id),
  ADD CONSTRAINT ck_ledger_journals_totals
    CHECK (total_debit_minor >= 0 AND total_credit_minor >= 0),
  ADD CONSTRAINT ck_ledger_journals_posting_state
    CHECK (
      (status = 'draft' AND posted_at IS NULL AND posted_by IS NULL)
      OR (status IN ('posted', 'reversed') AND posted_at IS NOT NULL AND posted_by IS NOT NULL)
    ),
  ADD CONSTRAINT ck_ledger_journals_not_self_reversal
    CHECK (reversed_by_journal_id IS NULL OR reversed_by_journal_id <> id);

CREATE SEQUENCE ledger_journal_number_seq;
SELECT setval(
  'ledger_journal_number_seq',
  greatest(coalesce((SELECT max(regexp_replace(journal_no, '\D', '', 'g')::BIGINT)
                     FROM ledger_journals
                     WHERE journal_no ~ '[0-9]'), 0), 1),
  true
);
CREATE UNIQUE INDEX ux_ledger_journals_org_idempotency
  ON ledger_journals(organization_id, idempotency_key)
  WHERE idempotency_key IS NOT NULL;
CREATE INDEX ix_ledger_journals_org_date_id
  ON ledger_journals(organization_id, journal_date DESC, id DESC)
  WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX ux_ledger_journals_org_reversal
  ON ledger_journals(organization_id, reversal_of_journal_id)
  WHERE reversal_of_journal_id IS NOT NULL;
CREATE INDEX ix_ledger_journals_org_period_date
  ON ledger_journals(organization_id, period_id, journal_date, id)
  WHERE deleted_at IS NULL;
CREATE INDEX ix_ledger_journals_org_source
  ON ledger_journals(organization_id, source_module, source_id)
  WHERE source_id IS NOT NULL;

ALTER TABLE ledger_lines
  RENAME COLUMN debit TO debit_minor;
ALTER TABLE ledger_lines
  RENAME COLUMN credit TO credit_minor;
ALTER TABLE ledger_lines
  ALTER COLUMN debit_minor TYPE BIGINT USING (debit_minor * 100)::BIGINT,
  ALTER COLUMN credit_minor TYPE BIGINT USING (credit_minor * 100)::BIGINT,
  ADD COLUMN organization_id UUID;
UPDATE ledger_lines l
SET organization_id = j.organization_id
FROM ledger_journals j
WHERE j.id = l.journal_id;
ALTER TABLE ledger_lines
  ALTER COLUMN organization_id SET NOT NULL,
  ADD CONSTRAINT fk_ledger_lines_journal_org
    FOREIGN KEY (organization_id, journal_id)
    REFERENCES ledger_journals(organization_id, id) ON DELETE RESTRICT,
  ADD CONSTRAINT fk_ledger_lines_account_org
    FOREIGN KEY (organization_id, account_id)
    REFERENCES ledger_accounts(organization_id, id) ON DELETE RESTRICT,
  ADD CONSTRAINT ck_ledger_lines_amounts
    CHECK ((debit_minor > 0 AND credit_minor = 0)
        OR (credit_minor > 0 AND debit_minor = 0)),
  ADD CONSTRAINT uq_ledger_lines_org_journal_order
    UNIQUE (organization_id, journal_id, line_order);
CREATE INDEX ix_ledger_lines_org_account_journal
  ON ledger_lines(organization_id, account_id, journal_id);
CREATE INDEX ix_ledger_lines_org_journal_order
  ON ledger_lines(organization_id, journal_id, line_order);

ALTER TABLE ledger_account_balances
  RENAME COLUMN opening_debit TO opening_debit_minor;
ALTER TABLE ledger_account_balances
  RENAME COLUMN opening_credit TO opening_credit_minor;
ALTER TABLE ledger_account_balances
  RENAME COLUMN debit_amount TO debit_amount_minor;
ALTER TABLE ledger_account_balances
  RENAME COLUMN credit_amount TO credit_amount_minor;
ALTER TABLE ledger_account_balances
  RENAME COLUMN closing_debit TO closing_debit_minor;
ALTER TABLE ledger_account_balances
  RENAME COLUMN closing_credit TO closing_credit_minor;
ALTER TABLE ledger_account_balances
  ALTER COLUMN opening_debit_minor TYPE BIGINT USING (opening_debit_minor * 100)::BIGINT,
  ALTER COLUMN opening_credit_minor TYPE BIGINT USING (opening_credit_minor * 100)::BIGINT,
  ALTER COLUMN debit_amount_minor TYPE BIGINT USING (debit_amount_minor * 100)::BIGINT,
  ALTER COLUMN credit_amount_minor TYPE BIGINT USING (credit_amount_minor * 100)::BIGINT,
  ALTER COLUMN closing_debit_minor TYPE BIGINT USING (closing_debit_minor * 100)::BIGINT,
  ALTER COLUMN closing_credit_minor TYPE BIGINT USING (closing_credit_minor * 100)::BIGINT;

CREATE TABLE ledger_source_postings (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id     UUID NOT NULL,
  source_type         VARCHAR(40) NOT NULL,
  source_operation_id UUID NOT NULL,
  posting_kind        VARCHAR(60) NOT NULL,
  journal_id          UUID NOT NULL,
  payload_hash        CHAR(64) NOT NULL,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT uq_ledger_source_identity
    UNIQUE (organization_id, source_type, source_operation_id, posting_kind),
  CONSTRAINT fk_ledger_source_journal
    FOREIGN KEY (organization_id, journal_id)
    REFERENCES ledger_journals(organization_id, id) ON DELETE RESTRICT
);

CREATE TABLE ledger_subledger_accounts (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  party_type      VARCHAR(20) NOT NULL CHECK (party_type IN ('customer', 'supplier')),
  party_id        UUID NOT NULL,
  currency_code   CHAR(3) NOT NULL DEFAULT 'BDT',
  is_active       BOOLEAN NOT NULL DEFAULT TRUE,
  version         BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at      TIMESTAMPTZ,
  UNIQUE (organization_id, id),
  UNIQUE (organization_id, party_type, party_id, currency_code)
);

CREATE TABLE ledger_subledger_balances (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id     UUID NOT NULL,
  subledger_account_id UUID NOT NULL,
  outstanding_minor   BIGINT NOT NULL DEFAULT 0,
  reserved_minor      BIGINT NOT NULL DEFAULT 0 CHECK (reserved_minor >= 0),
  version             BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, subledger_account_id),
  FOREIGN KEY (organization_id, subledger_account_id)
    REFERENCES ledger_subledger_accounts(organization_id, id) ON DELETE RESTRICT
);

CREATE OR REPLACE FUNCTION ledger_require_balanced_posting()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
  target_id UUID;
  journal_status VARCHAR(20);
  expected_debit BIGINT;
  expected_credit BIGINT;
  actual_debit BIGINT;
  actual_credit BIGINT;
  line_count BIGINT;
BEGIN
  target_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END;
  IF TG_TABLE_NAME = 'ledger_lines' THEN
    target_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.journal_id ELSE NEW.journal_id END;
  END IF;

  SELECT status, total_debit_minor, total_credit_minor
    INTO journal_status, expected_debit, expected_credit
    FROM ledger_journals WHERE id = target_id;
  IF NOT FOUND OR journal_status = 'draft' THEN
    RETURN NULL;
  END IF;

  SELECT count(*), coalesce(sum(debit_minor), 0), coalesce(sum(credit_minor), 0)
    INTO line_count, actual_debit, actual_credit
    FROM ledger_lines
    WHERE journal_id = target_id AND deleted_at IS NULL;
  IF line_count < 2
     OR actual_debit <> actual_credit
     OR actual_debit <> expected_debit
     OR actual_credit <> expected_credit THEN
    RAISE EXCEPTION 'posted ledger journal % is not balanced', target_id
      USING ERRCODE = '23514';
  END IF;
  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER tr_ledger_journals_balanced
AFTER INSERT OR UPDATE ON ledger_journals
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION ledger_require_balanced_posting();
CREATE CONSTRAINT TRIGGER tr_ledger_lines_balanced
AFTER INSERT OR UPDATE OR DELETE ON ledger_lines
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION ledger_require_balanced_posting();

CREATE OR REPLACE FUNCTION ledger_protect_posted_journal()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.status <> 'draft' THEN
      RAISE EXCEPTION 'posted ledger journals are immutable' USING ERRCODE = '23514';
    END IF;
    RETURN OLD;
  END IF;

  IF OLD.status <> 'draft' AND (
    NEW.status <> OLD.status
    OR (to_jsonb(NEW) - 'reversed_by_journal_id' - 'updated_at' - 'is_reversed')
       <> (to_jsonb(OLD) - 'reversed_by_journal_id' - 'updated_at' - 'is_reversed')
    OR (OLD.reversed_by_journal_id IS NOT NULL
        AND NEW.reversed_by_journal_id IS DISTINCT FROM OLD.reversed_by_journal_id)
  ) THEN
    RAISE EXCEPTION 'posted ledger journals are immutable' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER tr_ledger_journals_immutable
BEFORE UPDATE OR DELETE ON ledger_journals
FOR EACH ROW EXECUTE FUNCTION ledger_protect_posted_journal();

CREATE OR REPLACE FUNCTION ledger_protect_posted_lines()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
  target_journal_id UUID;
  target_status VARCHAR(20);
BEGIN
  target_journal_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.journal_id ELSE NEW.journal_id END;
  SELECT status INTO target_status FROM ledger_journals WHERE id = target_journal_id;
  IF target_status IS DISTINCT FROM 'draft' THEN
    RAISE EXCEPTION 'posted ledger lines are immutable' USING ERRCODE = '23514';
  END IF;
  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER tr_ledger_lines_immutable
BEFORE INSERT OR UPDATE OR DELETE ON ledger_lines
FOR EACH ROW EXECUTE FUNCTION ledger_protect_posted_lines();

-- The exclusion constraint above protects concurrent period creation.
-- Add tenant-safe branch/account references to the original branch table.
CREATE UNIQUE INDEX IF NOT EXISTS ux_branches_org_id_id ON branches(organization_id, id);

ALTER TABLE ledger_accounts
  DROP CONSTRAINT IF EXISTS chart_of_accounts_organization_id_fkey,
  DROP CONSTRAINT IF EXISTS chart_of_accounts_parent_id_fkey;
ALTER TABLE ledger_journals
  DROP CONSTRAINT IF EXISTS journals_organization_id_fkey,
  DROP CONSTRAINT IF EXISTS journals_branch_id_fkey,
  DROP CONSTRAINT IF EXISTS journals_reversed_by_journal_id_fkey;
ALTER TABLE ledger_lines
  DROP CONSTRAINT IF EXISTS journal_lines_journal_id_fkey,
  DROP CONSTRAINT IF EXISTS journal_lines_account_id_fkey,
  DROP CONSTRAINT IF EXISTS journal_lines_branch_id_fkey;

ALTER TABLE ledger_accounts
  ADD CONSTRAINT fk_ledger_accounts_org
    FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE RESTRICT;
ALTER TABLE ledger_journals
  ADD CONSTRAINT fk_ledger_journals_org
    FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE RESTRICT;
ALTER TABLE ledger_journals
  ADD CONSTRAINT fk_ledger_journals_reversed_by_org
    FOREIGN KEY (organization_id, reversed_by_journal_id)
    REFERENCES ledger_journals(organization_id, id) ON DELETE RESTRICT,
  ADD CONSTRAINT fk_ledger_journals_reversal_of_org
    FOREIGN KEY (organization_id, reversal_of_journal_id)
    REFERENCES ledger_journals(organization_id, id) ON DELETE RESTRICT;
ALTER TABLE ledger_accounts
  ADD CONSTRAINT fk_ledger_accounts_parent_org
    FOREIGN KEY (organization_id, parent_id)
    REFERENCES ledger_accounts(organization_id, id) ON DELETE RESTRICT;
ALTER TABLE ledger_journals
  ADD CONSTRAINT fk_ledger_journals_branch_org
    FOREIGN KEY (organization_id, branch_id)
    REFERENCES branches(organization_id, id) ON DELETE RESTRICT;
ALTER TABLE ledger_lines
  ADD CONSTRAINT fk_ledger_lines_branch_org
    FOREIGN KEY (organization_id, branch_id)
    REFERENCES branches(organization_id, id) ON DELETE RESTRICT;
