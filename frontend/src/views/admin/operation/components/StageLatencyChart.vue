<template>
  <div class="card p-6">
    <h3 class="mb-1 text-sm font-bold text-gray-900 dark:text-white">
      {{ t('admin.operation.imageReport.stageLatency') }}
      <span class="ml-1 text-xs font-normal text-gray-400">({{ t('admin.operation.imageReport.latencyUnit') }})</span>
    </h3>
    <p class="mb-4 text-xs text-gray-400 dark:text-gray-500">
      {{ t('admin.operation.imageReport.stageLatencyHint') }}
    </p>
    <div class="h-80">
      <Line v-if="chartData" :data="chartData" :options="options" />
      <div v-else class="flex h-full items-center justify-center text-sm text-gray-400">
        {{ t('common.noData') }}
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Line } from 'vue-chartjs'
import {
  Chart as ChartJS,
  LineElement,
  PointElement,
  CategoryScale,
  LinearScale,
  Tooltip,
  Legend
} from 'chart.js'
import type { StageLatencyBucket } from '@/api/admin/operationImageReport'
import { formatBucketLabel } from './bucketLabel'

ChartJS.register(LineElement, PointElement, CategoryScale, LinearScale, Tooltip, Legend)

const props = defineProps<{ buckets: StageLatencyBucket[]; bucket: '5m' | '1h' }>()
const { t } = useI18n()

// 上游用暖色、回传用冷色：一眼能看出长尾是哪一段贡献的。
const series: { key: keyof StageLatencyBucket; label: string; color: string; dash?: number[] }[] = [
  { key: 'upstream_p50_ms', label: 'upstream p50', color: '#f59e0b' },
  { key: 'upstream_p90_ms', label: 'upstream p90', color: '#f59e0b', dash: [4, 3] },
  { key: 'upstream_p95_ms', label: 'upstream p95', color: '#b45309', dash: [2, 2] },
  { key: 'response_p50_ms', label: 'response p50', color: '#3b82f6' },
  { key: 'response_p90_ms', label: 'response p90', color: '#3b82f6', dash: [4, 3] },
  { key: 'response_p95_ms', label: 'response p95', color: '#1d4ed8', dash: [2, 2] }
]

const toSeconds = (v: number | null): number | null => (v == null ? null : v / 1000)

const chartData = computed(() => {
  if (!props.buckets?.length) return null
  return {
    labels: props.buckets.map((b) => formatBucketLabel(b.bucket_start, props.bucket)),
    datasets: series.map((s) => ({
      label: s.label,
      data: props.buckets.map((b) => toSeconds(b[s.key] as number | null)),
      borderColor: s.color,
      borderDash: s.dash ?? [],
      borderWidth: 2,
      pointRadius: 0,
      fill: false,
      tension: 0.3
    }))
  }
})

const isDarkMode = computed(() => document.documentElement.classList.contains('dark'))
const colors = computed(() => ({
  grid: isDarkMode.value ? '#374151' : '#f3f4f6',
  text: isDarkMode.value ? '#9ca3af' : '#6b7280'
}))

const options = computed(() => {
  const c = colors.value
  return {
    responsive: true,
    maintainAspectRatio: false,
    interaction: { mode: 'index' as const, intersect: false },
    plugins: {
      legend: { display: true, position: 'top' as const },
      tooltip: {
        enabled: true,
        callbacks: {
          label: (ctx: { dataset: { label?: string }; parsed: { y: number | null } }) => {
            const v = ctx.parsed.y
            return `${ctx.dataset.label}: ${v == null ? '-' : v.toFixed(2)}s`
          }
        }
      }
    },
    scales: {
      x: { grid: { color: c.grid }, ticks: { color: c.text, maxRotation: 0, autoSkip: true } },
      y: {
        beginAtZero: true,
        grid: { color: c.grid },
        ticks: { color: c.text, callback: (v: number | string) => `${v}s` }
      }
    }
  }
})
</script>
