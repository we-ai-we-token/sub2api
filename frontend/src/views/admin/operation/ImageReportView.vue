<template>
  <AppLayout>
    <div class="space-y-6 pb-12">
      <div>
        <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ t('admin.operation.imageReport.title') }}</h1>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.operation.imageReport.description') }}</p>
      </div>

      <!-- Concurrency cards -->
      <ConcurrencyCards :overview="overview" />

      <!-- Today breakdown -->
      <TodayBreakdown :items="todayItems" />

      <!-- Filter bar -->
      <div class="flex flex-wrap items-center gap-3 rounded-2xl bg-white p-4 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700">
        <!-- User -->
        <UserSearchSelect v-model="filters.user_id" @change="onFilterChange" />

        <!-- Platform -->
        <div class="flex items-center gap-2">
          <label class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.operation.imageReport.platform') }}</label>
          <select
            v-model="filters.platform"
            class="rounded-lg border border-gray-200 bg-gray-50 px-2 py-1 text-sm text-gray-700 focus:outline-none dark:border-dark-600 dark:bg-dark-700 dark:text-gray-300"
            @change="onPlatformChange"
          >
            <option value="openai">OpenAI</option>
            <option value="gemini">Gemini</option>
          </select>
        </div>

        <!-- Model -->
        <div class="flex items-center gap-2">
          <label class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.operation.imageReport.model') }}</label>
          <select
            v-model="filters.model"
            class="rounded-lg border border-gray-200 bg-gray-50 px-2 py-1 text-sm text-gray-700 focus:outline-none dark:border-dark-600 dark:bg-dark-700 dark:text-gray-300"
            @change="onFilterChange"
          >
            <option value="">—</option>
            <option v-for="m in filterOptions.models" :key="m" :value="m">{{ m }}</option>
          </select>
        </div>

        <!-- Group -->
        <div class="flex items-center gap-2">
          <label class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.operation.imageReport.group') }}</label>
          <select
            v-model="filters.group_id"
            class="rounded-lg border border-gray-200 bg-gray-50 px-2 py-1 text-sm text-gray-700 focus:outline-none dark:border-dark-600 dark:bg-dark-700 dark:text-gray-300"
            @change="onFilterChange"
          >
            <option :value="undefined">—</option>
            <option v-for="g in filterOptions.groups" :key="g.id" :value="g.id">{{ g.name }}</option>
          </select>
        </div>

        <!-- Granularity -->
        <div class="flex items-center gap-2">
          <label class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.operation.imageReport.granularity') }}</label>
          <select
            v-model="filters.bucket"
            class="rounded-lg border border-gray-200 bg-gray-50 px-2 py-1 text-sm text-gray-700 focus:outline-none dark:border-dark-600 dark:bg-dark-700 dark:text-gray-300"
            @change="onFilterChange"
          >
            <option value="5m">5m</option>
            <option value="1h">1h</option>
          </select>
        </div>
      </div>

      <!-- Charts: each full-width on its own row -->
      <div class="space-y-6">
        <RequestVolumeChart :buckets="requestBuckets" :bucket="filters.bucket" />
        <StageLatencyChart :buckets="stageBuckets" :bucket="filters.bucket" />
        <LatencyChart :buckets="latencyBuckets" :bucket="filters.bucket" />
      </div>

      <!-- Footer note -->
      <p class="text-xs text-gray-400 dark:text-gray-500">{{ t('admin.operation.imageReport.successRateNote') }}</p>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import operationImageReportAPI from '@/api/admin/operationImageReport'
import type {
  ConcurrencyOverview,
  LatencyBucket,
  StageLatencyBucket,
  RequestBucket,
  TodayItem,
  FilterOptions
} from '@/api/admin/operationImageReport'
import ConcurrencyCards from './components/ConcurrencyCards.vue'
import TodayBreakdown from './components/TodayBreakdown.vue'
import LatencyChart from './components/LatencyChart.vue'
import StageLatencyChart from './components/StageLatencyChart.vue'
import RequestVolumeChart from './components/RequestVolumeChart.vue'
import UserSearchSelect from './components/UserSearchSelect.vue'

const { t } = useI18n()

const tz = Intl.DateTimeFormat().resolvedOptions().timeZone

const filters = reactive<{
  platform: string
  model: string
  group_id: number | undefined
  user_id: number | undefined
  bucket: '5m' | '1h'
}>({
  platform: 'openai',
  model: '',
  group_id: undefined,
  user_id: undefined,
  bucket: '1h'
})

const overview = ref<ConcurrencyOverview | null>(null)
const todayItems = ref<TodayItem[]>([])
const latencyBuckets = ref<LatencyBucket[]>([])
const stageBuckets = ref<StageLatencyBucket[]>([])
const requestBuckets = ref<RequestBucket[]>([])
const filterOptions = ref<FilterOptions>({ models: [], groups: [] })

function buildSeriesParams() {
  const params: Record<string, string | number | undefined> = { tz }
  if (filters.platform) params.platform = filters.platform
  if (filters.model) params.model = filters.model
  if (filters.group_id !== undefined) params.group_id = filters.group_id
  if (filters.user_id !== undefined) params.user_id = filters.user_id
  if (filters.bucket) params.bucket = filters.bucket
  return params as Parameters<typeof operationImageReportAPI.latencySeries>[0]
}

async function fetchSeries() {
  const params = buildSeriesParams()
  const [latency, stage, request, ov] = await Promise.all([
    operationImageReportAPI.latencySeries(params),
    operationImageReportAPI.stageLatencySeries(params),
    operationImageReportAPI.requestSeries(params),
    operationImageReportAPI.overview(tz)
  ])
  latencyBuckets.value = latency.buckets
  stageBuckets.value = stage.buckets
  requestBuckets.value = request.buckets
  todayItems.value = ov.today
}

async function fetchInitial() {
  const [conc, ov, fo, latency, stage, request] = await Promise.all([
    operationImageReportAPI.concurrency(),
    operationImageReportAPI.overview(tz),
    operationImageReportAPI.filters(filters.platform),
    operationImageReportAPI.latencySeries(buildSeriesParams()),
    operationImageReportAPI.stageLatencySeries(buildSeriesParams()),
    operationImageReportAPI.requestSeries(buildSeriesParams())
  ])
  overview.value = conc
  todayItems.value = ov.today
  filterOptions.value = fo
  latencyBuckets.value = latency.buckets
  stageBuckets.value = stage.buckets
  requestBuckets.value = request.buckets
}

async function onPlatformChange() {
  // Reset platform-specific filters to avoid stale values from previous platform.
  // user_id 不清：按用户排查时通常要在 openai/gemini 之间来回切着看。
  filters.model = ''
  filters.group_id = undefined
  filterOptions.value = await operationImageReportAPI.filters(filters.platform)
  await fetchSeries()
}

async function onFilterChange() {
  await fetchSeries()
}

onMounted(fetchInitial)
</script>
