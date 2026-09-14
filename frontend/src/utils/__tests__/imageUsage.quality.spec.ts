import { describe, expect, it } from 'vitest'

import { formatImageQuality, formatImageBillingTier } from '@/utils/imageUsage'

// gpt-image-2.5-flare / gpt-image-2.5-sunburst 新增 xhigh / max 两档质量。
// 后端如实记录之后，前端这份白名单漏了同样会显示「未记录」——这个用例守住第二半。
const t = ((key: string) => key) as unknown as Parameters<typeof formatImageQuality>[1]

describe('formatImageQuality', () => {
  it('renders every known quality, including the new xhigh/max tiers', () => {
    for (const quality of ['low', 'medium', 'high', 'xhigh', 'max']) {
      expect(formatImageQuality({ image_quality: quality } as never, t)).toBe(quality)
    }
  })

  it('lower-cases and trims before matching', () => {
    expect(formatImageQuality({ image_quality: '  XHIGH ' } as never, t)).toBe('xhigh')
    expect(formatImageQuality({ image_quality: 'Max' } as never, t)).toBe('max')
  })

  it('still reports genuinely unrecorded values', () => {
    expect(formatImageQuality({ image_quality: '' } as never, t)).toBe('usage.imageQualityNotRecorded')
    expect(formatImageQuality({ image_quality: null } as never, t)).toBe('usage.imageQualityNotRecorded')
    expect(formatImageQuality(null, t)).toBe('usage.imageQualityNotRecorded')
  })
})

describe('formatImageBillingTier', () => {
  // 计费档在后端已收敛到 high，但历史行只要落了 xhigh/max 也要能正常显示。
  it('renders quality tiers including xhigh/max', () => {
    for (const tier of ['low', 'medium', 'high', 'xhigh', 'max']) {
      expect(formatImageBillingTier({ billing_tier: tier } as never, t)).toBe(tier)
    }
  })

  it('still echoes size tiers', () => {
    expect(formatImageBillingTier({ billing_tier: '2K' } as never, t)).toBe('2K')
  })
})
