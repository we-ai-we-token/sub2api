// 生图返回 URL 开关（后端字段 image_return_url）仅对 openai / gemini 平台有意义：
// 其余平台要么不走 /v1/images/* 链路，要么响应里没有 data[].url 这个位置。
// 与后端 service.groupSupportsImageReturnURL 保持一致，改一处必须同步另一处。
export function supportsGroupImageReturnUrl(platform: string): boolean {
  return platform === "openai" || platform === "gemini";
}

// 提交前归一化：平台被改成不支持的平台时把开关压回 false，
// 避免在库里留下一个永远不生效的 true（后端 sanitizeGroupImageReturnURL 也会兜一次）。
export function normalizeGroupImageReturnUrl(
  platform: string,
  enabled: boolean | undefined,
): boolean {
  return supportsGroupImageReturnUrl(platform) && enabled === true;
}
