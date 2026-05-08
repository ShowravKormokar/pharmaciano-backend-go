CREATE TABLE IF NOT EXISTS audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    user_id UUID,
    action VARCHAR(50) NOT NULL,
    module VARCHAR(50) NOT NULL,
    ip VARCHAR(45),
    user_agent VARCHAR(255),
    details TEXT,
    browser VARCHAR(100),
    os VARCHAR(100),
    device VARCHAR(150),
    location VARCHAR(150)
);
CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_logs(user_id);