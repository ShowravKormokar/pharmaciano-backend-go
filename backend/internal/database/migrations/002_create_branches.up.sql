CREATE TABLE IF NOT EXISTS branches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    name TEXT NOT NULL,
    address TEXT,
    email TEXT,
    phone TEXT,
    is_active BOOLEAN DEFAULT true
);
CREATE INDEX IF NOT EXISTS idx_branches_org ON branches(organization_id);