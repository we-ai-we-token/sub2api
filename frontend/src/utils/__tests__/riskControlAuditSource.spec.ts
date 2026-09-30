import { describe, expect, it } from 'vitest'
import { isLocalRuleModerationRow } from '../riskControlAuditSource'

describe('isLocalRuleModerationRow', () => {
  it('treats local block actions as not API-audited', () => {
    for (const action of ['cyber_policy', 'keyword_block', 'hash_block']) {
      expect(isLocalRuleModerationRow({ action, highest_category: '' })).toBe(true)
    }
  })

  it('treats fork keyword_observe hits (allow + keyword category) as not API-audited', () => {
    expect(isLocalRuleModerationRow({ action: 'allow', highest_category: 'keyword' })).toBe(true)
  })

  it('treats allowlisted users\' rewritten hash hits (allow + hash category) as not API-audited', () => {
    expect(isLocalRuleModerationRow({ action: 'allow', highest_category: 'hash' })).toBe(true)
  })

  it('keeps API-audited rows on the legacy audit source label', () => {
    expect(isLocalRuleModerationRow({ action: 'allow', highest_category: 'sexual' })).toBe(false)
    expect(isLocalRuleModerationRow({ action: 'block', highest_category: 'violence' })).toBe(false)
  })
})
