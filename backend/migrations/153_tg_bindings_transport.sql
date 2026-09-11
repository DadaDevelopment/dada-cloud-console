-- One binding can speak Telegram either as a bot (Bot API, BotFather token)
-- or as a real account (MTProto user session). The poller and everything
-- behind it are transport-agnostic; the column only tells tg-gateway which
-- client to hand the row to. Existing rows are bots.
ALTER TABLE tg_bindings ADD COLUMN IF NOT EXISTS transport TEXT NOT NULL DEFAULT 'bot';
ALTER TABLE tg_bindings DROP CONSTRAINT IF EXISTS tg_bindings_transport_check;
ALTER TABLE tg_bindings ADD CONSTRAINT tg_bindings_transport_check CHECK (transport IN ('bot', 'user'));
