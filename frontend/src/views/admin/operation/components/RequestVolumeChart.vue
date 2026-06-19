<template>
  <div class="card p-6">
    <h3 class="mb-4 text-sm font-bold text-gray-900 dark:text-white">{{ t('admin.operation.imageReport.requestVolume') }}</h3>
    <div class="h-72">
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

ChartJS.register(LineElement, PointElement, CategoryScale, LinearScale, Tooltip, Legend)

const props = defineProps<{ buckets: RequestBucket[] }>()
const { t } = useI18n()

const chartData = computed(() => {
  if (!props.buckets?.length) return null
  return {
    labels: props.buckets.map((b) => b.bucket_start),
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
    plugins: { legend: { display: true, position: 'top' as const } },
    scales: {
      x: {
        grid: { color: c.grid },
        ticks: { color: c.text }
      },
      y: {
        beginAtZero: true,
        grid: { color: c.grid },
        ticks: { color: c.text }
      }
    }
  }
})
</script>
