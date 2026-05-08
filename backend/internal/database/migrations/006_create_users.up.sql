CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    branch_id UUID REFERENCES branches(id),
    role_id UUID NOT NULL REFERENCES roles(id),
    name TEXT NOT NULL,
    email TEXT UNIQUE NOT NULL,
    phone VARCHAR(20),
    password_hash TEXT NOT NULL,
    status VARCHAR(20) DEFAULT 'active',
    last_login_at TIMESTAMPTZ,
    joining_date TIMESTAMPTZ NOT NULL,
    n_id TEXT,
    present_address TEXT,
    permanent_address TEXT,
    educational_bg TEXT,
    failed_attempts INTEGER DEFAULT 0,
    locked_until TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_users_org ON users(organization_id);
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role_id);