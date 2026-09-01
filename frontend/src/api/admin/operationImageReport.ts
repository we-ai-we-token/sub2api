/**
 * Admin 运营管理 - 生图报表 API
 * 全部只读 GET。
 */
import { apiClient } from '../client'

export interface ConcurrencyCard {
  key: string // "openai_oauth" | "adobe" | "gemini"
  current_concurrency: number
  total_concurrency: number
  available: boolean
}
export interface AlertConcurrency {
  account_count: number
  current_concurrency: number
  total_concurrency: number
  available: boolean
}
export interface ConcurrencyOverview {
  cards: ConcurrencyCard[]
  alert: AlertConcurrency
}
export interface LatencyBucket {
  bucket_start: string
  count: number
  min_ms: number | null
  p25_ms: number | null
  p50_ms: number | null
  p75_ms: number | null
  max_ms: number | null
  avg_ms: number | null
}
/** 「上游生成 vs 回传客户端」分段耗时分位数（单位 ms，无数据为 null）。 */
export interface StageLatencyBucket {
  bucket_start: string
  count: number
  upstream_p50_ms: number | null
  upstream_p90_ms: number | null
  upstream_p95_ms: number | null
  response_p50_ms: number | null
  response_p90_ms: number | null
  response_p95_ms: number | null
}
export interface RequestBucket {
  bucket_start: string
  success_count: number
  failure_count: number
  success_rate: number
}
export interface TodayItem {
  dimension: 'platform' | 'model' | 'group'
  key: string
  group_id?: number
  success: number
  failure: number
}
export interface FilterOptions {
  models: string[]
  groups: { id: number; name: string }[]
}

export interface SeriesParams {
  platform?: string
  model?: string
  group_id?: number
  user_id?: number
  bucket?: '5m' | '1h'
  tz?: string
}

const base = '/admin/operation/image-report'

const operationImageReportAPI = {
  async concurrency(): Promise<ConcurrencyOverview> {
    const { data } = await apiClient.get<ConcurrencyOverview>(`${base}/concurrency`)
    return data
  },
  async overview(tz: string): Promise<{ today: TodayItem[] }> {
    const { data } = await apiClient.get<{ today: TodayItem[] }>(`${base}/overview`, { params: { tz } })
    return data
  },
  async latencySeries(params: SeriesParams): Promise<{ buckets: LatencyBucket[] }> {
    const { data } = await apiClient.get<{ buckets: LatencyBucket[] }>(`${base}/latency-series`, { params })
    return data
  },
  async stageLatencySeries(params: SeriesParams): Promise<{ buckets: StageLatencyBucket[] }> {
    const { data } = await apiClient.get<{ buckets: StageLatencyBucket[] }>(`${base}/stage-latency-series`, { params })
    return data
  },
  async requestSeries(params: SeriesParams): Promise<{ buckets: RequestBucket[] }> {
    const { data } = await apiClient.get<{ buckets: RequestBucket[] }>(`${base}/request-series`, { params })
    return data
  },
  async filters(platform: string): Promise<FilterOptions> {
    const { data } = await apiClient.get<FilterOptions>(`${base}/filters`, { params: { platform } })
    return data
  }
}

export default operationImageReportAPI
