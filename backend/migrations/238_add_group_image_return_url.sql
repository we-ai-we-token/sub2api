-- 生图返回 URL 开关（仅 openai / gemini 平台使用）：
--   false(默认) -> 行为完全不变，图片按上游原样返回（gpt-image-* 默认是 b64_json）
--   true        -> 客户端显式传 response_format=url 时，网关把图片转存对象存储，
--                  响应中 data[i].url 返回对象存储短链接（预签名，默认 24h 过期），
--                  并删除 data[i].b64_json；客户端未要求 url 时行为仍然不变。
-- 默认 false，保证既有分组升级后行为零变化（老客户端不受影响）。
ALTER TABLE groups ADD COLUMN IF NOT EXISTS image_return_url BOOLEAN NOT NULL DEFAULT FALSE;
