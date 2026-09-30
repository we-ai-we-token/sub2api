// 二开：风控日志详情里「审计来源」一栏，本地规则产生的记录没有 engine_meta，也不是 API 审计，应显示 '-'。
// 上游只认 cyber_policy / keyword_block / hash_block 三种拦截动作；以下两类落的是 action=allow，
// 只能靠 highest_category 识别，不补会被标成「OpenAI（未记录审计模型版本）」：
// - 二开的 keyword_observe（非 pre_block 模式下关键词命中只记录）：highest_category=keyword；
// - 上游 v0.2.10 风控白名单用户（mode=risk_control_log_only）：buildLog 把关键词/哈希拦截
//   统一改写成 allow，highest_category 仍是 keyword / hash。
const LOCAL_RULE_ACTIONS = new Set(['cyber_policy', 'keyword_block', 'hash_block'])
const LOCAL_RULE_CATEGORIES = new Set(['keyword', 'hash'])

export function isLocalRuleModerationRow(row: { action: string; highest_category?: string | null }): boolean {
  return LOCAL_RULE_ACTIONS.has(row.action) || LOCAL_RULE_CATEGORIES.has(row.highest_category ?? '')
}
