-- OAuth 生图链路开关（仅 openai 平台使用）：
--   true(默认)  -> 走上游 Responses(image_generation 工具) 链路，发往 /backend-api/codex/responses
--   false       -> 走二开专用 codex images 端点链路（/backend-api/codex/images/*）
-- 默认 true，保证所有既有分组升级后默认使用 Responses 链路。
ALTER TABLE groups ADD COLUMN IF NOT EXISTS image_use_responses_api BOOLEAN NOT NULL DEFAULT TRUE;
