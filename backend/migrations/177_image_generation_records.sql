-- 生图记录表：每次 Images API 生图请求（成功 + 失败）落一行，记录分段耗时与重试/切号明细，
-- 用于分析生图瓶颈（上游生成 vs 网关排队 vs 回传）。
-- 口径与运营生图报表一致：/v1/images/generations|edits（OpenAI 与 Gemini 分组）。

CREATE TABLE IF NOT EXISTS image_generation_records (
    id BIGSERIAL PRIMARY KEY,

    -- 关联标识
    request_id VARCHAR(64),
    client_request_id VARCHAR(64),
    user_id BIGINT,
    api_key_id BIGINT,
    account_id BIGINT,
    group_id BIGINT,

    -- 请求维度
    platform VARCHAR(32) NOT NULL DEFAULT '',
    endpoint VARCHAR(32) NOT NULL DEFAULT '',
    model VARCHAR(128) NOT NULL DEFAULT '',
    upstream_model VARCHAR(128),
    stream BOOLEAN NOT NULL DEFAULT FALSE,

    -- 分段耗时（毫秒）
    auth_ms BIGINT,
    routing_ms BIGINT,
    image_slot_wait_ms BIGINT,
    user_slot_wait_ms BIGINT,
    account_slot_wait_ms BIGINT,
    upstream_ms BIGINT,
    response_ms BIGINT,
    first_token_ms BIGINT,
    total_ms BIGINT NOT NULL DEFAULT 0,

    -- 重试与切号
    attempts INT NOT NULL DEFAULT 0,
    account_switches INT NOT NULL DEFAULT 0,
    same_account_retries INT NOT NULL DEFAULT 0,
    attempts_detail JSONB,

    -- 上游信息
    upstream_status_code INT,
    upstream_request_id VARCHAR(128),
    upstream_error_message TEXT,

    -- 生图元数据
    image_count INT NOT NULL DEFAULT 0,
    image_size VARCHAR(32),
    image_quality VARCHAR(32),

    -- 结果
    success BOOLEAN NOT NULL DEFAULT FALSE,
    status_code INT,
    error_type VARCHAR(64),

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE image_generation_records IS '生图请求耗时记录（成功+失败），用于瓶颈分析';
COMMENT ON COLUMN image_generation_records.upstream_ms IS '最后一次尝试：发上游请求到收到响应头的耗时';
COMMENT ON COLUMN image_generation_records.response_ms IS '最后一次尝试：转发总时长减上游头部时间（读上游 body + 写回客户端）';
COMMENT ON COLUMN image_generation_records.routing_ms IS '选号阶段耗时（包含用户槽与账号槽等待）';
COMMENT ON COLUMN image_generation_records.image_slot_wait_ms IS '等待全局生图并发槽的耗时';
COMMENT ON COLUMN image_generation_records.user_slot_wait_ms IS '等待用户级并发槽的耗时';
COMMENT ON COLUMN image_generation_records.account_slot_wait_ms IS '等待账号并发槽的耗时（跨所有尝试累计）';
COMMENT ON COLUMN image_generation_records.attempts IS '实际发起上游转发的次数';
COMMENT ON COLUMN image_generation_records.account_switches IS '切换账号次数';
COMMENT ON COLUMN image_generation_records.same_account_retries IS '同账号重试总次数';
COMMENT ON COLUMN image_generation_records.attempts_detail IS '失败的上游尝试明细 JSON 数组（账号/状态码/时间），最终成功的尝试不在其中';

CREATE INDEX IF NOT EXISTS idx_image_gen_records_created_at
    ON image_generation_records (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_image_gen_records_platform_time
    ON image_generation_records (platform, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_image_gen_records_model_time
    ON image_generation_records (model, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_image_gen_records_account_time
    ON image_generation_records (account_id, created_at DESC)
    WHERE account_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_image_gen_records_user_time
    ON image_generation_records (user_id, created_at DESC)
    WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_image_gen_records_success_time
    ON image_generation_records (success, created_at DESC);
