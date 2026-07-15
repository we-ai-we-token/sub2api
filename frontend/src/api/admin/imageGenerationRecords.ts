/**
 * Admin 运营管理 - 生图记录 API
 * 每次 Images API 生图请求（成功+失败）一行，含分段耗时与重试/切号明细。
 */
import { apiClient } from '../client'
import type { PaginatedResponse } from '@/types'

export interface ImageGenerationAttempt {
  at_unix_ms?: number
  account_id?: number
  account_name?: string
  upstream_status_code?: number
  upstream_request_id?: string
  kind?: string
  message?: string
}

export interface ImageGenerationRecord {
  id: number
  request_id?: string
  client_request_id?: string
  user_id?: number
  api_key_id?: number
  account_id?: number
  group_id?: number
  platform: string
  endpoint: string
  model: string
  upstream_model?: string
  stream: boolean
  auth_ms?: number
  routing_ms?: number
  image_slot_wait_ms?: number
  user_slot_wait_ms?: number
  account_slot_wait_ms?: number
  upstream_ms?: number
  response_ms?: number
  first_token_ms?: number
  total_ms: number
  attempts: number
  account_switches: number
  same_account_retries: number
  attempts_detail?: ImageGenerationAttempt[]
  upstream_status_code?: number
  upstream_request_id?: string
  upstream_error_message?: string
  image_count: number
  image_size?: string
  image_quality?: string
  success: boolean
  status_code?: number
  error_type?: string
  created_at: string
}

export interface ImageGenerationRecordListParams {
  page?: number
  page_size?: number
  /** RFC3339，缺省最近 24 小时 */
  start_time?: string
  end_time?: string
  platform?: string
  /** 前缀匹配 */
  model?: string
  success?: boolean
  user_id?: number
  account_id?: number
  group_id?: number
  min_total_ms?: number
}

const base = '/admin/operation/image-records'

const imageGenerationRecordsAPI = {
  async list(
    params: ImageGenerationRecordListParams
  ): Promise<PaginatedResponse<ImageGenerationRecord>> {
    const { data } = await apiClient.get<PaginatedResponse<ImageGenerationRecord>>(base, { params })
    return data
  }
}

export default imageGenerationRecordsAPI
