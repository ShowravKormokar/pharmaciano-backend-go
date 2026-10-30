-- Posted financial evidence must never be dropped as part of an application
-- rollback. Roll forward with a corrective migration instead.
DO $$
BEGIN
  RAISE EXCEPTION 'ledger contract migration is forward-only; preserve financial evidence and roll forward';
END;
$$;
