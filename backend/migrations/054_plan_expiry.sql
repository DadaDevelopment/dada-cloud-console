ALTER TABLE billing_accounts ADD COLUMN IF NOT EXISTS plan_expires_at TIMESTAMPTZ;
ALTER TABLE billing_accounts ADD COLUMN IF NOT EXISTS expiry_notified_at TIMESTAMPTZ;
