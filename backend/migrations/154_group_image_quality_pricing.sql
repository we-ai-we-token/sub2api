-- 按 OpenAI 返回的 quality（low/medium/high）计费的分组配置，仅 openai 平台使用。
-- image_quality_billing 为 true 时改用 quality 单价计费，否则维持 1K/2K/4K 计费。

ALTER TABLE groups ADD COLUMN IF NOT EXISTS image_quality_billing BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS image_price_low DECIMAL(20,8);
ALTER TABLE groups ADD COLUMN IF NOT EXISTS image_price_medium DECIMAL(20,8);
ALTER TABLE groups ADD COLUMN IF NOT EXISTS image_price_high DECIMAL(20,8);

COMMENT ON COLUMN groups.image_quality_billing IS '是否按 OpenAI 返回的 quality（low/medium/high）计费，仅 openai 平台使用；false 时按 1K/2K/4K 计费';
COMMENT ON COLUMN groups.image_price_low IS 'quality=low 图片生成单价 (USD)，留空则回退模型默认图片价';
COMMENT ON COLUMN groups.image_price_medium IS 'quality=medium 图片生成单价 (USD)，留空则回退模型默认图片价';
COMMENT ON COLUMN groups.image_price_high IS 'quality=high 图片生成单价 (USD)，留空则回退模型默认图片价';

-- 记录实际计费使用的 quality，便于后续按 quality 维度统计。
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS image_quality VARCHAR(16);

COMMENT ON COLUMN usage_logs.image_quality IS 'OpenAI 生图实际计费 quality（low/medium/high），按 quality 计费时记录';
