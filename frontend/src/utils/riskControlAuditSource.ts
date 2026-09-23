// 二开：风控日志详情里「审计来源」一栏，本地规则产生的记录没有 engine_meta，也不是 API 审计，应显示 '-'。
// 上游只认 cyber_policy / keyword_block / hash_block 三种拦截动作；二开的 keyword_observe
// （非 pre_block 模式下关键词命中只记录）落的是 action=allow + highest_category=keyword，
// 不补这一条会被标成「OpenAI（未记录审计模型版本）」。
const LOCAL_RULE_ACTIONS = new Set(['cyber_policy', 'keyword_block', 'hash_block'])
const KEYWORD_CATEGORY = 'keyword'

export function isLocalRuleModerationRow(row: { action: string; highest_category?: string | null }): boolean {
  return LOCAL_RULE_ACTIONS.has(row.action) || row.highest_category === KEYWORD_CATEGORY
}
