<template>
  <div class="card p-6">
    <div class="mb-4 flex flex-wrap items-center justify-between gap-2">
      <h3 class="text-sm font-bold text-gray-900 dark:text-white">{{ t('admin.operation.imageReport.today') }}</h3>
      <div class="flex gap-1 rounded-lg bg-gray-100 p-0.5 dark:bg-dark-700">
        <button
          v-for="tab in tabs"
          :key="tab"
          type="button"
          class="rounded-md px-3 py-1 text-xs font-medium transition-colors"
          :class="activeTab === tab
            ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-800 dark:text-white'
            : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'"
          @click="activeTab = tab"
        >
          {{ t('admin.operation.imageReport.' + tab) }}
        </button>
      </div>
    </div>
    <div class="h-72">
      <Bar v-if="chartData" :data="chartData" :options="options" />
      <div v-else class="flex h-full items-center justify-center text-sm text-gray-400">{{ t('common.noData') }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Bar } from 'vue-chartjs'
import { Chart as ChartJS, BarElement, CategoryScale, LinearScale, Tooltip, Legend } from 'chart.js'
import type { TodayItem } from '@/api/admin/operationImageReport'

ChartJS.register(BarElement, CategoryScale, LinearScale, Tooltip, Legend)

const props = defineProps<{ items: TodayItem[] }>()
const { t } = useI18n()

const tabs = ['platform', 'model', 'group'] as const
type Tab = (typeof tabs)[number]
const activeTab = ref<Tab>('platform')

const current = computed(() => props.items.filter((i) => i.dimension === activeTab.value))

const chartData = computed(() => {
  if (!current.value.length) return null
  return {
    labels: current.value.map((i) => i.key),
    datasets: [
      {
        label: t('admin.operation.imageReport.success'),
        data: current.value.map((i) => i.success),
        backgroundColor: '#10b981',
        borderRadius: 4,
        stack: 'today'
      },
      {
        label: t('admin.operation.imageReport.failure'),
        data: current.value.map((i) => i.failure),
        backgroundColor: '#ef4444',
        borderRadius: 4,
        stack: 'today'
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
      tooltip: { enabled: true }
    },
    scales: {
      x: { stacked: true, grid: { display: false }, ticks: { color: c.text } },
      y: { stacked: true, beginAtZero: true, grid: { color: c.grid }, ticks: { color: c.text, precision: 0 } }
    }
  }
})
</script>
