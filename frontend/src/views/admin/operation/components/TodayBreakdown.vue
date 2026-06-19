<template>
  <div class="rounded-2xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700">
    <h3 class="mb-4 text-sm font-bold text-gray-900 dark:text-white">{{ t('admin.operation.imageReport.today') }}</h3>
    <div v-if="!items.length" class="text-sm text-gray-400">{{ t('common.noData') }}</div>
    <div v-else class="space-y-6">
      <!-- Platform group -->
      <div v-if="byPlatform.length">
        <div class="mb-2 text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">
          {{ t('admin.operation.imageReport.platform') }}
        </div>
        <table class="w-full text-sm">
          <thead>
            <tr class="text-xs text-gray-500 dark:text-gray-400">
              <th class="pb-1 text-left font-normal"></th>
              <th class="pb-1 text-right font-normal">{{ t('admin.operation.imageReport.success') }}</th>
              <th class="pb-1 text-right font-normal">{{ t('admin.operation.imageReport.failure') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in byPlatform" :key="item.key" class="border-t border-gray-100 dark:border-dark-700">
              <td class="py-1.5 font-medium text-gray-700 dark:text-gray-300">{{ item.key }}</td>
              <td class="py-1.5 text-right text-green-600 dark:text-green-400">{{ item.success }}</td>
              <td class="py-1.5 text-right text-red-500 dark:text-red-400">{{ item.failure }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- Model group -->
      <div v-if="byModel.length">
        <div class="mb-2 text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">
          {{ t('admin.operation.imageReport.model') }}
        </div>
        <table class="w-full text-sm">
          <thead>
            <tr class="text-xs text-gray-500 dark:text-gray-400">
              <th class="pb-1 text-left font-normal"></th>
              <th class="pb-1 text-right font-normal">{{ t('admin.operation.imageReport.success') }}</th>
              <th class="pb-1 text-right font-normal">{{ t('admin.operation.imageReport.failure') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in byModel" :key="item.key" class="border-t border-gray-100 dark:border-dark-700">
              <td class="py-1.5 font-medium text-gray-700 dark:text-gray-300">{{ item.key }}</td>
              <td class="py-1.5 text-right text-green-600 dark:text-green-400">{{ item.success }}</td>
              <td class="py-1.5 text-right text-red-500 dark:text-red-400">{{ item.failure }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- Group group -->
      <div v-if="byGroup.length">
        <div class="mb-2 text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">
          {{ t('admin.operation.imageReport.group') }}
        </div>
        <table class="w-full text-sm">
          <thead>
            <tr class="text-xs text-gray-500 dark:text-gray-400">
              <th class="pb-1 text-left font-normal"></th>
              <th class="pb-1 text-right font-normal">{{ t('admin.operation.imageReport.success') }}</th>
              <th class="pb-1 text-right font-normal">{{ t('admin.operation.imageReport.failure') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in byGroup" :key="item.key" class="border-t border-gray-100 dark:border-dark-700">
              <td class="py-1.5 font-medium text-gray-700 dark:text-gray-300">{{ item.key }}</td>
              <td class="py-1.5 text-right text-green-600 dark:text-green-400">{{ item.success }}</td>
              <td class="py-1.5 text-right text-red-500 dark:text-red-400">{{ item.failure }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TodayItem } from '@/api/admin/operationImageReport'

const props = defineProps<{ items: TodayItem[] }>()
const { t } = useI18n()

const byPlatform = computed(() => props.items.filter((i) => i.dimension === 'platform'))
const byModel = computed(() => props.items.filter((i) => i.dimension === 'model'))
const byGroup = computed(() => props.items.filter((i) => i.dimension === 'group'))
</script>
