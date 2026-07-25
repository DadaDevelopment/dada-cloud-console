CREATE TABLE payments (
  id              UUID PRIMARY KEY,
  org_id          TEXT NOT NULL,
  plan            TEXT NOT NULL,
  amount_value    NUMERIC(10,2) NOT NULL,
  currency        TEXT NOT NULL DEFAULT 'RUB',
  status          TEXT NOT NULL DEFAULT 'pending',
  yk_payment_id   TEXT UNIQUE,
  confirmation_url TEXT,
  customer_email  TEXT,
  created_by_sub  TEXT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  paid_at         TIMESTAMPTZ
);

CREATE INDEX payments_org_idx ON payments(org_id, created_at DESC);
