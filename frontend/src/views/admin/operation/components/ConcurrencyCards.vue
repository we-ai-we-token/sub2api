<template>
  <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
    <!-- Category cards: openai_oauth / adobe / gemini -->
    <div
      v-for="card in cards"
      :key="card.key"
      class="rounded-2xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700"
    >
      <div class="mb-3 text-xs font-bold tracking-wider text-gray-500 dark:text-gray-400">
        {{ cardLabel(card.key) }}
      </div>
      <div class="flex items-end justify-between">
        <div>
          <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.operation.imageReport.currentConcurrency') }}</div>
          <div class="mt-1 text-2xl font-bold" :class="card.available ? 'text-gray-900 dark:text-white' : 'text-gray-400 dark:text-gray-500'">
            {{ card.available ? card.current_concurrency : t('admin.operation.imageReport.unavailable') }}
          </div>
        </div>
        <div class="text-right">
          <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.operation.imageReport.totalConcurrency') }}</div>
          <div class="mt-1 text-lg font-semibold text-gray-700 dark:text-gray-300">{{ card.total_concurrency }}</div>
        </div>
      </div>
    </div>

    <!-- Alert card -->
    <div
      v-if="alert"
      class="rounded-2xl bg-amber-50 p-5 shadow-sm ring-1 ring-amber-200 dark:bg-amber-900/20 dark:ring-amber-700/50"
    >
      <div class="mb-3 text-xs font-bold tracking-wider text-amber-600 dark:text-amber-400">
        {{ t('admin.operation.imageReport.alertConcurrency') }}
      </div>
      <div class="flex items-end justify-between">
        <div>
          <div class="text-xs text-amber-600/70 dark:text-amber-400/70">{{ t('admin.operation.imageReport.currentConcurrency') }}</div>
          <div class="mt-1 text-2xl font-bold" :class="alert.available ? 'text-amber-700 dark:text-amber-300' : 'text-gray-400 dark:text-gray-500'">
            {{ alert.available ? alert.current_concurrency : t('admin.operation.imageReport.unavailable') }}
          </div>
        </div>
        <div class="text-right">
          <div class="text-xs text-amber-600/70 dark:text-amber-400/70">{{ t('admin.operation.imageReport.totalConcurrency') }}</div>
          <div class="mt-1 text-lg font-semibold text-amber-700 dark:text-amber-300">{{ alert.total_concurrency }}</div>
        </div>
      </div>
      <div class="mt-3 text-xs text-amber-600/80 dark:text-amber-400/80">
        {{ t('admin.operation.imageReport.alertHint') }}
        <span class="ml-1 font-semibold">({{ alert.account_count }})</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ConcurrencyOverview } from '@/api/admin/operationImageReport'

const props = defineProps<{ overview: ConcurrencyOverview | null }>()
const { t } = useI18n()

const cards = computed(() => props.overview?.cards ?? [])
const alert = computed(() => props.overview?.alert ?? null)

const cardLabel = (key: string): string => {
  const map: Record<string, string> = {
    openai_oauth: t('admin.operation.imageReport.cardOpenaiOauth'),
    adobe: t('admin.operation.imageReport.cardAdobe'),
    gemini: t('admin.operation.imageReport.cardGemini')
  }
  return map[key] ?? key
}
</script>
