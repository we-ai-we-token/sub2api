-- OpenAI image quality billing.
-- Online deployments may already have these columns; keep this migration idempotent.

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS image_quality_billing BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS image_price_low DECIMAL(20,8);

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS image_price_medium DECIMAL(20,8);

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS image_price_high DECIMAL(20,8);

ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS image_quality VARCHAR(16);

COMMENT ON COLUMN groups.image_quality_billing IS 'OpenAI image billing mode: false uses size tiers (1K/2K/4K), true uses usage quality tiers (low/medium/high)';
COMMENT ON COLUMN groups.image_price_low IS 'OpenAI image low quality unit price (USD)';
COMMENT ON COLUMN groups.image_price_medium IS 'OpenAI image medium quality unit price (USD)';
COMMENT ON COLUMN groups.image_price_high IS 'OpenAI image high quality unit price (USD)';
COMMENT ON COLUMN usage_logs.image_quality IS 'OpenAI image quality from upstream usage; normalized to low/medium/high when used for billing';
