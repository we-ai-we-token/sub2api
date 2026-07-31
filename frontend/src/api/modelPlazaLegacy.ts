/**
 * 模型广场 API（用户侧，只读）
 *
 * 以「分组」为中心聚合该分组支持的模型与定价：
 *  - groups：用户可选分组（已按 id 升序），用于选择框与卡片；
 *  - selected_group_id：本次返回所对应的分组；
 *  - models：选中分组支持的模型清单，价格已按分组「有效倍率」折算后返回。
 *
 * 价格单位：token 计费字段为「每 token」（前端 ×1,000,000 展示为每百万 token）；
 * 生图按次字段（image_tiers / per_request_price）为「每次」单价。
 */

import { apiClient } from './client'
import type { BillingMode } from '@/constants/channel'

export interface ModelPlazaGroup {
  id: number
  name: string
  platform: string
  /** 'standard' | 'subscription' */
  subscription_type: string
  is_exclusive: boolean
  /** 分组默认倍率。 */
  rate_multiplier: number
  /** 用户专属倍率（覆盖默认值），无专属配置时为 null。 */
  user_rate_multiplier: number | null
}

/**
 * 生图（按次）模型的档位价格（已折算，单位：每次）。
 *
 * 分组按尺寸计费时用 price_1k/2k/4k；OpenAI 分组开启质量计费时改用
 * price_low/medium/high。两组互斥，未启用的一组为 null。
 */
export interface ModelPlazaImageTiers {
  price_1k: number | null
  price_2k: number | null
  price_4k: number | null
  price_low: number | null
  price_medium: number | null
  price_high: number | null
}

export interface ModelPlazaModel {
  name: string
  platform: string
  billing_mode: BillingMode
  /** token 计费：每 token 原价（前端 ×1,000,000，再按倍率折算展示）。 */
  input_price: number | null
  output_price: number | null
  cache_write_price: number | null
  cache_read_price: number | null
  image_output_price: number | null
  /** 按次计费：默认每次原价（无档位时使用）。 */
  per_request_price: number | null
  /** 按次计费：1K/2K/4K 档位原价。 */
  image_tiers: ModelPlazaImageTiers | null
}

export interface ModelPlazaResponse {
  groups: ModelPlazaGroup[]
  selected_group_id: number
  /** 选中分组的 token 有效倍率（含用户专属覆盖），前端用于折算 token 价。 */
  token_multiplier: number
  /** 选中分组的图片有效倍率，前端用于折算生图按次价。 */
  image_multiplier: number
  /** 选中分组是否按生图质量（low/medium/high）计费；false 时按尺寸（1K/2K/4K）。 */
  image_quality_billing: boolean
  models: ModelPlazaModel[]
}

/** 获取模型广场数据；groupId 省略时后端默认选中 id 最小的分组。 */
export async function getModelPlaza(
  groupId?: number,
  options?: { signal?: AbortSignal }
): Promise<ModelPlazaResponse> {
  const { data } = await apiClient.get<ModelPlazaResponse>('/model-plaza-legacy/models', {
    params: groupId != null ? { group_id: groupId } : undefined,
    signal: options?.signal
  })
  return data
}

export const modelPlazaAPI = { getModelPlaza }

export default modelPlazaAPI
