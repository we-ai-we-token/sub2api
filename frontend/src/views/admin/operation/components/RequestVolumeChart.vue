<template>
  <div class="card p-6">
    <h3 class="mb-4 text-sm font-bold text-gray-900 dark:text-white">{{ t('admin.operation.imageReport.requestVolume') }}</h3>
    <div class="h-80">
      <Line v-if="chartData" :data="chartData" :options="options" />
      <div v-else class="flex h-full items-center justify-center text-sm text-gray-400">{{ t('common.noData') }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Line } from 'vue-chartjs'
import { Chart as ChartJS, LineElement, PointElement, CategoryScale, LinearScale, Tooltip, Legend } from 'chart.js'
import type { RequestBucket } from '@/api/admin/operationImageReport'
import { formatBucketLabel } from './bucketLabel'

ChartJS.register(LineElement, PointElement, CategoryScale, LinearScale, Tooltip, Legend)

const props = defineProps<{ buckets: RequestBucket[]; bucket: '5m' | '1h' }>()
const { t } = useI18n()

const chartData = computed(() => {
  if (!props.buckets?.length) return null
  return {
    labels: props.buckets.map((b) => formatBucketLabel(b.bucket_start, props.bucket)),
    datasets: [
      {
        label: t('admin.operation.imageReport.success'),
        data: props.buckets.map((b) => b.success_count),
        borderColor: '#10b981',
        borderWidth: 2,
        pointRadius: 0,
        fill: false,
        tension: 0.3
      },
      {
        label: t('admin.operation.imageReport.failure'),
        data: props.buckets.map((b) => b.failure_count),
        borderColor: '#ef4444',
        borderWidth: 2,
        pointRadius: 0,
        fill: false,
        tension: 0.3
      }
    ]
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
          afterBody: (items: { dataIndex: number }[]) => {
            const idx = items[0]?.dataIndex
            if (idx == null) return ''
            const b = props.buckets[idx]
            if (!b) return ''
            const rate = (b.success_rate * 100).toFixed(1)
            return `${t('admin.operation.imageReport.successRate')}: ${rate}%`
          }
        }
      }
    },
    scales: {
      x: { grid: { color: c.grid }, ticks: { color: c.text, maxRotation: 0, autoSkip: true } },
      y: { beginAtZero: true, grid: { color: c.grid }, ticks: { color: c.text, precision: 0 } }
    }
  }
})
</script>
