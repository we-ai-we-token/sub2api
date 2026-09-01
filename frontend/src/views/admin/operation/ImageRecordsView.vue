<template>
  <AppLayout>
    <div class="space-y-6 pb-12">
      <div>
        <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ t('admin.operation.imageRecords.title') }}</h1>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.operation.imageRecords.description') }}</p>
      </div>

      <!-- Filter bar -->
      <div class="flex flex-wrap items-center gap-3 rounded-2xl bg-white p-4 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700">
        <UserSearchSelect v-model="filters.userId" @change="applyFilters" />

        <div class="flex items-center gap-2">
          <label class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.operation.imageRecords.filterTimeRange') }}</label>
          <select v-model="filters.rangeHours" class="filter-select" @change="applyFilters">
            <option :value="1">{{ t('admin.operation.imageRecords.lastHour') }}</option>
            <option :value="6">{{ t('admin.operation.imageRecords.last6Hours') }}</option>
            <option :value="24">{{ t('admin.operation.imageRecords.last24Hours') }}</option>
            <option :value="168">{{ t('admin.operation.imageRecords.last7Days') }}</option>
          </select>
        </div>

        <div class="flex items-center gap-2">
          <label class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.operation.imageRecords.filterPlatform') }}</label>
          <select v-model="filters.platform" class="filter-select" @change="applyFilters">
            <option value="">{{ t('admin.operation.imageRecords.all') }}</option>
            <option value="openai">OpenAI</option>
            <option value="gemini">Gemini</option>
          </select>
        </div>

        <div class="flex items-center gap-2">
          <label class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.operation.imageRecords.filterStatus') }}</label>
          <select v-model="filters.success" class="filter-select" @change="applyFilters">
            <option value="">{{ t('admin.operation.imageRecords.all') }}</option>
            <option value="true">{{ t('admin.operation.imageRecords.success') }}</option>
            <option value="false">{{ t('admin.operation.imageRecords.failure') }}</option>
          </select>
        </div>

        <div class="flex items-center gap-2">
          <label class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.operation.imageRecords.filterModel') }}</label>
          <input
            v-model.trim="filters.model"
            type="text"
            class="filter-select w-40"
            placeholder="gpt-image-2"
            @keyup.enter="applyFilters"
          />
        </div>

        <div class="flex items-center gap-2">
          <label class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.operation.imageRecords.filterMinTotal') }}</label>
          <input
            v-model.number="filters.minTotalMs"
            type="number"
            min="0"
            class="filter-select w-28"
            placeholder="0"
            @keyup.enter="applyFilters"
          />
        </div>

        <button
          class="ml-auto rounded-lg bg-primary-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-primary-700 disabled:opacity-50"
          :disabled="loading"
          @click="applyFilters"
        >
          {{ t('admin.operation.imageRecords.refresh') }}
        </button>
      </div>

      <!-- Table -->
      <div class="overflow-x-auto rounded-2xl bg-white shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700">
        <table class="w-full min-w-max text-left text-sm">
          <thead class="bg-gray-50/80 dark:bg-dark-800/80">
            <tr>
              <th class="th-cell">{{ t('admin.operation.imageRecords.colTime') }}</th>
              <th class="th-cell">{{ t('admin.operation.imageRecords.colModel') }}</th>
              <th class="th-cell">{{ t('admin.operation.imageRecords.colStatus') }}</th>
              <th class="th-cell text-right">{{ t('admin.operation.imageRecords.colTotal') }}</th>
              <th class="th-cell text-right">{{ t('admin.operation.imageRecords.colUpstream') }}</th>
              <th class="th-cell text-right">{{ t('admin.operation.imageRecords.colResponse') }}</th>
              <th class="th-cell text-right">{{ t('admin.operation.imageRecords.colQueue') }}</th>
              <th class="th-cell text-center">{{ t('admin.operation.imageRecords.colRetries') }}</th>
              <th class="th-cell">{{ t('admin.operation.imageRecords.colImages') }}</th>
              <th class="th-cell text-right">{{ t('admin.operation.imageRecords.colUser') }}</th>
              <th class="th-cell text-right">{{ t('admin.operation.imageRecords.colAccount') }}</th>
              <th class="th-cell"></th>
            </tr>
          </thead>
          <tbody>
            <template v-for="record in records" :key="record.id">
              <tr class="border-t border-gray-100 hover:bg-gray-50/60 dark:border-dark-700 dark:hover:bg-dark-700/40">
                <td class="td-cell whitespace-nowrap">{{ formatTime(record.created_at) }}</td>
                <td class="td-cell">
                  <div class="font-medium text-gray-900 dark:text-white">{{ record.model }}</div>
                  <div class="text-xs text-gray-400">{{ record.platform }} · {{ record.endpoint }}</div>
                </td>
                <td class="td-cell">
                  <span
                    class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium"
                    :class="record.success
                      ? 'bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-400'
                      : 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-400'"
                  >
                    {{ record.success ? t('admin.operation.imageRecords.success') : t('admin.operation.imageRecords.failure') }}
                  </span>
                  <div v-if="!record.success" class="mt-0.5 text-xs text-gray-400">
                    {{ record.status_code }}<span v-if="record.error_type"> · {{ record.error_type }}</span>
                  </div>
                </td>
                <td class="td-cell text-right font-mono">{{ formatMs(record.total_ms) }}</td>
                <td class="td-cell text-right font-mono">{{ formatMs(record.upstream_ms) }}</td>
                <td
                  class="td-cell text-right font-mono"
                  :class="{ 'font-semibold text-red-600 dark:text-red-400': (record.response_ms ?? 0) >= 60_000 }"
                >
                  {{ formatMs(record.response_ms) }}
                </td>
                <td class="td-cell text-right font-mono">{{ formatMs(queueWaitMs(record)) }}</td>
                <td class="td-cell text-center">
                  <span :class="{ 'font-semibold text-amber-600 dark:text-amber-400': record.account_switches > 0 || record.attempts > 1 }">
                    {{ record.attempts }} / {{ record.account_switches }}
                  </span>
                </td>
                <td class="td-cell whitespace-nowrap">
                  <span v-if="record.image_count > 0">{{ record.image_count }}<span v-if="record.image_size"> × {{ record.image_size }}</span></span>
                  <span v-else class="text-gray-300 dark:text-gray-600">—</span>
                </td>
                <td class="td-cell text-right font-mono">{{ record.user_id ?? '—' }}</td>
                <td class="td-cell text-right font-mono">{{ record.account_id ?? '—' }}</td>
                <td class="td-cell text-right">
                  <button class="text-xs font-medium text-primary-600 hover:text-primary-700 dark:text-primary-400" @click="toggleExpand(record.id)">
                    {{ expandedId === record.id ? t('admin.operation.imageRecords.collapse') : t('admin.operation.imageRecords.expand') }}
                  </button>
                </td>
              </tr>
              <tr v-if="expandedId === record.id" class="border-t border-gray-100 bg-gray-50/50 dark:border-dark-700 dark:bg-dark-900/30">
                <td class="td-cell" colspan="12">
                  <div class="space-y-3 py-1">
                    <div>
                      <div class="mb-1 text-xs font-semibold text-gray-500 dark:text-gray-400">{{ t('admin.operation.imageRecords.detailTimings') }}</div>
                      <div class="grid grid-cols-2 gap-x-8 gap-y-1 sm:grid-cols-4 lg:grid-cols-7">
                        <div v-for="seg in segmentTimings(record)" :key="seg.label" class="text-xs">
                          <span class="text-gray-400">{{ seg.label }}</span>
                          <span class="ml-1 font-mono text-gray-700 dark:text-gray-300">{{ seg.value ?? '—' }}</span>
                        </div>
                      </div>
                    </div>
                    <div class="grid grid-cols-1 gap-1 text-xs sm:grid-cols-2">
                      <div>
                        <span class="text-gray-400">{{ t('admin.operation.imageRecords.requestId') }}</span>
                        <span class="ml-1 font-mono">{{ record.request_id || '—' }}</span>
                      </div>
                      <div>
                        <span class="text-gray-400">{{ t('admin.operation.imageRecords.upstreamRequestId') }}</span>
                        <span class="ml-1 font-mono">{{ record.upstream_request_id || '—' }}</span>
                      </div>
                    </div>
                    <div v-if="record.upstream_error_message" class="text-xs">
                      <span class="text-gray-400">{{ t('admin.operation.imageRecords.upstreamError') }}</span>
                      <span class="ml-1 break-all text-red-600 dark:text-red-400">
                        <template v-if="record.upstream_status_code">[{{ record.upstream_status_code }}] </template>{{ record.upstream_error_message }}
                      </span>
                    </div>
                    <div v-if="record.attempts_detail?.length">
                      <div class="mb-1 text-xs font-semibold text-gray-500 dark:text-gray-400">{{ t('admin.operation.imageRecords.attemptsDetail') }}</div>
                      <table class="w-full text-xs">
                        <thead>
                          <tr class="text-gray-400">
                            <th class="py-1 pr-4 text-left font-normal">{{ t('admin.operation.imageRecords.attemptTime') }}</th>
                            <th class="py-1 pr-4 text-left font-normal">{{ t('admin.operation.imageRecords.attemptAccount') }}</th>
                            <th class="py-1 pr-4 text-left font-normal">{{ t('admin.operation.imageRecords.attemptStatus') }}</th>
                            <th class="py-1 pr-4 text-left font-normal">{{ t('admin.operation.imageRecords.attemptKind') }}</th>
                            <th class="py-1 text-left font-normal">{{ t('admin.operation.imageRecords.attemptMessage') }}</th>
                          </tr>
                        </thead>
                        <tbody>
                          <tr v-for="(attempt, idx) in record.attempts_detail" :key="idx" class="border-t border-gray-100 dark:border-dark-700">
                            <td class="py-1 pr-4 font-mono whitespace-nowrap">{{ attempt.at_unix_ms ? formatTime(new Date(attempt.at_unix_ms).toISOString()) : '—' }}</td>
                            <td class="py-1 pr-4">{{ attempt.account_name || attempt.account_id || '—' }}</td>
                            <td class="py-1 pr-4 font-mono">{{ attempt.upstream_status_code || '—' }}</td>
                            <td class="py-1 pr-4">{{ attempt.kind || '—' }}</td>
                            <td class="py-1 break-all">{{ attempt.message || '—' }}</td>
                          </tr>
                        </tbody>
                      </table>
                    </div>
                  </div>
                </td>
              </tr>
            </template>
            <tr v-if="!loading && records.length === 0">
              <td class="td-cell py-10 text-center text-gray-400" colspan="12">{{ t('admin.operation.imageRecords.noData') }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- Pagination -->
      <Pagination
        v-if="pagination.total > 0"
        :page="pagination.page"
        :total="pagination.total"
        :page-size="pagination.pageSize"
        @update:page="handlePageChange"
        @update:pageSize="handlePageSizeChange"
      />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { reactive, ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import UserSearchSelect from './components/UserSearchSelect.vue'
import imageGenerationRecordsAPI from '@/api/admin/imageGenerationRecords'
import type { ImageGenerationRecord, ImageGenerationRecordListParams } from '@/api/admin/imageGenerationRecords'

const { t } = useI18n()

const filters = reactive<{
  rangeHours: number
  platform: string
  success: '' | 'true' | 'false'
  model: string
  minTotalMs: number | ''
  userId: number | undefined
}>({
  rangeHours: 24,
  platform: '',
  success: '',
  model: '',
  minTotalMs: '',
  userId: undefined
})

const pagination = reactive({ page: 1, pageSize: 20, total: 0 })
const records = ref<ImageGenerationRecord[]>([])
const loading = ref(false)
const expandedId = ref<number | null>(null)

function buildParams(): ImageGenerationRecordListParams {
  const params: ImageGenerationRecordListParams = {
    page: pagination.page,
    page_size: pagination.pageSize,
    start_time: new Date(Date.now() - filters.rangeHours * 3600 * 1000).toISOString()
  }
  if (filters.platform) params.platform = filters.platform
  if (filters.success) params.success = filters.success === 'true'
  if (filters.model) params.model = filters.model
  if (typeof filters.minTotalMs === 'number' && filters.minTotalMs > 0) params.min_total_ms = filters.minTotalMs
  if (filters.userId !== undefined) params.user_id = filters.userId
  return params
}

async function fetchRecords() {
  loading.value = true
  try {
    const data = await imageGenerationRecordsAPI.list(buildParams())
    records.value = data.items
    pagination.total = data.total
  } finally {
    loading.value = false
  }
}

function applyFilters() {
  pagination.page = 1
  expandedId.value = null
  fetchRecords()
}

function handlePageChange(page: number) {
  pagination.page = page
  expandedId.value = null
  fetchRecords()
}

function handlePageSizeChange(pageSize: number) {
  pagination.pageSize = pageSize
  pagination.page = 1
  expandedId.value = null
  fetchRecords()
}

function toggleExpand(id: number) {
  expandedId.value = expandedId.value === id ? null : id
}

function queueWaitMs(record: ImageGenerationRecord): number {
  return (record.image_slot_wait_ms ?? 0) + (record.user_slot_wait_ms ?? 0) + (record.account_slot_wait_ms ?? 0)
}

function segmentTimings(record: ImageGenerationRecord) {
  return [
    { label: t('admin.operation.imageRecords.authMs'), value: record.auth_ms },
    { label: t('admin.operation.imageRecords.imageSlotWaitMs'), value: record.image_slot_wait_ms },
    { label: t('admin.operation.imageRecords.userSlotWaitMs'), value: record.user_slot_wait_ms },
    { label: t('admin.operation.imageRecords.accountSlotWaitMs'), value: record.account_slot_wait_ms },
    { label: t('admin.operation.imageRecords.routingMs'), value: record.routing_ms },
    { label: t('admin.operation.imageRecords.upstreamMs'), value: record.upstream_ms },
    { label: t('admin.operation.imageRecords.responseMs'), value: record.response_ms }
  ]
}

function formatMs(ms: number | undefined | null): string {
  if (ms === undefined || ms === null) return '—'
  if (ms < 1000) return `${ms}ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`
  const minutes = Math.floor(ms / 60_000)
  const seconds = Math.round((ms % 60_000) / 1000)
  return `${minutes}m${String(seconds).padStart(2, '0')}s`
}

function formatTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString(undefined, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false
  })
}

onMounted(fetchRecords)
</script>

<style scoped>
.filter-select {
  @apply rounded-lg border border-gray-200 bg-gray-50 px-2 py-1 text-sm text-gray-700 focus:outline-none dark:border-dark-600 dark:bg-dark-700 dark:text-gray-300;
}
.th-cell {
  @apply px-4 py-3 text-xs font-medium text-gray-500 dark:text-gray-400;
}
.td-cell {
  @apply px-4 py-3 align-top text-gray-700 dark:text-gray-300;
}
</style>
