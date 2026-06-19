import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import LatencyChart from '../LatencyChart.vue'

vi.mock('chart.js', () => ({
  Chart: { register: vi.fn() },
  CategoryScale: {},
  Legend: {},
  LineElement: {},
  LinearScale: {},
  PointElement: {},
  Tooltip: {},
}))

vi.mock('vue-chartjs', async () => {
  const { defineComponent } = await import('vue')
  return {
    Line: defineComponent({
      name: 'LineChartStub',
      props: {
        data: { type: Object, required: true },
        options: { type: Object, default: () => ({}) },
      },
      template: '<div class="mock-line" />',
    }),
  }
})

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

describe('LatencyChart', () => {
  it('shows no-data when empty', () => {
    const w = mount(LatencyChart, { props: { buckets: [] } })
    expect(w.find('.mock-line').exists()).toBe(false)
  })

  it('renders chart when buckets present', () => {
    const buckets = [
      {
        bucket_start: '2025-01-01T00:00:00Z',
        count: 1,
        min_ms: 1,
        p25_ms: 2,
        p50_ms: 3,
        p75_ms: 4,
        max_ms: 5,
        avg_ms: 3
      }
    ]
    const w = mount(LatencyChart, { props: { buckets } })
    expect(w.find('.mock-line').exists()).toBe(true)
  })
})
