import { describe, it, expect, vi, beforeEach } from 'vitest'
import operationImageReportAPI from '../operationImageReport'
import { apiClient } from '../../client'

vi.mock('../../client', () => ({
  apiClient: { get: vi.fn() }
}))

describe('operationImageReportAPI', () => {
  beforeEach(() => vi.clearAllMocks())

  it('requests concurrency endpoint and unwraps data', async () => {
    (apiClient.get as any).mockResolvedValue({ data: { cards: [], alert: {} } })
    const res = await operationImageReportAPI.concurrency()
    expect(apiClient.get).toHaveBeenCalledWith('/admin/operation/image-report/concurrency')
    expect(res).toEqual({ cards: [], alert: {} })
  })

  it('passes series params through', async () => {
    (apiClient.get as any).mockResolvedValue({ data: { buckets: [] } })
    await operationImageReportAPI.requestSeries({ platform: 'openai', bucket: '5m', tz: 'UTC' })
    expect(apiClient.get).toHaveBeenCalledWith('/admin/operation/image-report/request-series', {
      params: { platform: 'openai', bucket: '5m', tz: 'UTC' }
    })
  })
})
