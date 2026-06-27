<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- 第一行：3 个卡片 -->
      <div class="grid grid-cols-1 gap-4 md:grid-cols-3">
        <!-- 卡片 1：分组选择 -->
        <div class="card flex flex-col gap-2 p-5">
          <span class="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
            {{ t('modelPlaza.cards.group') }}
          </span>
          <select
            v-model="selectedGroupId"
            class="input"
            :disabled="loading || groups.length === 0"
            @change="onGroupChange"
          >
            <option v-for="g in groups" :key="g.id" :value="g.id">
              {{ g.name }}
            </option>
          </select>
          <span v-if="selectedGroup" class="inline-flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400">
            <PlatformIcon :platform="selectedGroup.platform as GroupPlatform" size="xs" />
            {{ selectedGroup.platform }}
            <span
              v-if="selectedGroup.is_exclusive"
              class="ml-1 rounded bg-purple-100 px-1.5 py-0.5 text-[10px] font-medium uppercase text-purple-600 dark:bg-purple-900/40 dark:text-purple-300"
            >
              {{ t('modelPlaza.exclusive') }}
            </span>
          </span>
        </div>

        <!-- 卡片 2：分组倍率 -->
        <div class="card flex flex-col gap-1 p-5">
          <span class="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
            {{ t('modelPlaza.cards.rate') }}
          </span>
          <span class="text-3xl font-bold text-primary-600 dark:text-primary-400">
            {{ effectiveRate != null ? `${formatRate(effectiveRate)}x` : '-' }}
          </span>
          <span
            v-if="hasUserRate"
            class="text-xs text-amber-600 dark:text-amber-400"
            :title="t('modelPlaza.userRateHint')"
          >
            {{ t('modelPlaza.userRateBadge', { rate: formatRate(selectedGroup!.rate_multiplier) }) }}
          </span>
        </div>

        <!-- 卡片 3：支持模型数量 -->
        <div class="card flex flex-col gap-1 p-5">
          <span class="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
            {{ t('modelPlaza.cards.modelCount') }}
          </span>
          <span class="text-3xl font-bold text-gray-900 dark:text-white">
            {{ loading ? '-' : models.length }}
          </span>
          <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('modelPlaza.cards.modelCountUnit') }}</span>
        </div>
      </div>

      <!-- 第二行：提示文案 -->
      <div
        class="flex items-start gap-2 rounded-lg border border-blue-100 bg-blue-50/60 px-4 py-3 text-sm text-blue-700 dark:border-blue-900/40 dark:bg-blue-900/20 dark:text-blue-300"
      >
        <Icon name="infoCircle" size="md" class="mt-0.5 flex-shrink-0" />
        <span>{{ t('modelPlaza.exchangeHint') }}</span>
      </div>
      <p class="-mt-3 px-1 text-xs text-gray-400 dark:text-gray-500">
        {{ t('modelPlaza.priceUnitNote') }}
      </p>

      <!-- 第三部分：模型列表 -->
      <div class="card overflow-hidden">
        <div class="flex items-center justify-between border-b border-gray-100 px-4 py-3 dark:border-dark-700">
          <div class="relative w-full sm:w-72">
            <Icon
              name="search"
              size="md"
              class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-gray-500"
            />
            <input
              v-model="searchQuery"
              type="text"
              :placeholder="t('modelPlaza.searchPlaceholder')"
              class="input pl-10"
            />
          </div>
          <button
            @click="reload"
            :disabled="loading"
            class="btn btn-secondary ml-3 flex-shrink-0"
            :title="t('common.refresh', 'Refresh')"
          >
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          </button>
        </div>

        <table class="w-full border-collapse text-sm">
          <thead>
            <tr
              class="border-b border-gray-100 bg-gray-50/50 text-xs font-medium uppercase tracking-wide text-gray-500 dark:border-dark-700 dark:bg-dark-800/50 dark:text-gray-400"
            >
              <th class="px-4 py-3 text-left">{{ t('modelPlaza.columns.model') }}</th>
              <th class="px-4 py-3 text-left">{{ t('modelPlaza.columns.platform') }}</th>
              <th class="px-4 py-3 text-right">{{ t('modelPlaza.columns.input') }}</th>
              <th class="px-4 py-3 text-right">{{ t('modelPlaza.columns.output') }}</th>
              <th class="px-4 py-3 text-right">{{ t('modelPlaza.columns.cacheWrite') }}</th>
              <th class="px-4 py-3 text-right">{{ t('modelPlaza.columns.cacheRead') }}</th>
              <th class="px-4 py-3 text-right">{{ t('modelPlaza.columns.imageOutput') }}</th>
            </tr>
          </thead>
          <tbody v-if="loading">
            <tr>
              <td colspan="7" class="py-10 text-center">
                <Icon name="refresh" size="lg" class="inline-block animate-spin text-gray-400" />
              </td>
            </tr>
          </tbody>
          <tbody v-else-if="filteredModels.length === 0">
            <tr>
              <td colspan="7" class="py-12 text-center">
                <Icon name="inbox" size="xl" class="mx-auto mb-3 h-12 w-12 text-gray-400" />
                <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('modelPlaza.empty') }}</p>
              </td>
            </tr>
          </tbody>
          <tbody v-else>
            <tr
              v-for="m in filteredModels"
              :key="`${m.platform}-${m.name}`"
              class="border-b border-gray-50 transition-colors last:border-b-0 hover:bg-gray-50/40 dark:border-dark-700/50 dark:hover:bg-dark-800/40"
            >
              <td class="px-4 py-3 font-medium text-gray-900 dark:text-white">{{ m.name }}</td>
              <td class="px-4 py-3">
                <span
                  :class="[
                    'inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[11px] font-medium uppercase',
                    platformBadgeClass(m.platform),
                  ]"
                >
                  <PlatformIcon :platform="m.platform as GroupPlatform" size="xs" />
                  {{ m.platform }}
                </span>
              </td>

              <!-- 按次/图片计费：合并后续列，直接展示 1K/2K/4K 价格 -->
              <td
                v-if="isPerRequest(m)"
                colspan="5"
                class="px-4 py-3 text-gray-700 dark:text-gray-300"
              >
                {{ perRequestText(m) }}
              </td>

              <!-- token 计费：逐列展示「原价划线 + 折算后金额」（每百万 token） -->
              <template v-else>
                <td
                  v-for="(val, ci) in tokenCells(m)"
                  :key="ci"
                  class="px-4 py-3 text-right tabular-nums"
                >
                  <div class="flex items-center justify-end gap-1.5">
                    <span
                      v-if="showStrike && val != null"
                      class="text-xs text-gray-400 line-through dark:text-gray-500"
                    >
                      {{ basePerMillion(val) }}
                    </span>
                    <span class="text-sm font-semibold text-gray-900 dark:text-white">
                      {{ foldedPerMillion(val) }}
                    </span>
                  </div>
                </td>
              </template>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import modelPlazaAPI, {
  type ModelPlazaGroup,
  type ModelPlazaModel,
} from '@/api/modelPlaza'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatScaled } from '@/utils/pricing'
import { platformBadgeClass } from '@/utils/platformColors'
import { BILLING_MODE_TOKEN } from '@/constants/channel'
import type { GroupPlatform } from '@/types'

const { t } = useI18n()
const appStore = useAppStore()

const groups = ref<ModelPlazaGroup[]>([])
const models = ref<ModelPlazaModel[]>([])
const selectedGroupId = ref<number | null>(null)
const loading = ref(false)
const searchQuery = ref('')
/** 选中分组的有效倍率（后端返回，已含用户专属覆盖）。 */
const tokenMultiplier = ref(1)
const imageMultiplier = ref(1)

/** 每百万 token 换算系数。 */
const PER_MILLION = 1_000_000

const selectedGroup = computed<ModelPlazaGroup | null>(
  () => groups.value.find((g) => g.id === selectedGroupId.value) ?? null,
)

const effectiveRate = computed<number | null>(() => {
  const g = selectedGroup.value
  if (!g) return null
  return g.user_rate_multiplier ?? g.rate_multiplier
})

const hasUserRate = computed(() => selectedGroup.value?.user_rate_multiplier != null)

const filteredModels = computed(() => {
  const q = searchQuery.value.trim().toLowerCase()
  if (!q) return models.value
  return models.value.filter(
    (m) => m.name.toLowerCase().includes(q) || m.platform.toLowerCase().includes(q),
  )
})

function isPerRequest(m: ModelPlazaModel): boolean {
  return m.billing_mode !== BILLING_MODE_TOKEN
}

/** 倍率非 1 时才展示原价划线对比（倍率为 1 时折后价 == 原价，无需重复）。 */
const showStrike = computed(() => tokenMultiplier.value !== 1)

/** token 行 5 列原价：输入 / 输出 / 缓存写 / 缓存读 / 图片输出。 */
function tokenCells(m: ModelPlazaModel): (number | null)[] {
  return [
    m.input_price,
    m.output_price,
    m.cache_write_price,
    m.cache_read_price,
    m.image_output_price,
  ]
}

/** 原价（每百万 token）。 */
function basePerMillion(value: number | null): string {
  return formatScaled(value, PER_MILLION)
}

/** 折算后金额（原价 × token 倍率，每百万 token）。 */
function foldedPerMillion(value: number | null): string {
  return formatScaled(value == null ? null : value * tokenMultiplier.value, PER_MILLION)
}

/** 把倍率格式化为简洁字符串（去掉多余的尾随 0）。 */
function formatRate(rate: number): string {
  return Number(rate.toPrecision(6)).toString()
}

/**
 * 生图按次模型一行文字（价格已按图片倍率折算）：统一始终展示 1K/2K/4K 三档，
 * 无论各档价格是否相同、来源是渠道定价还是分组分辨率定价。
 *  - image_tiers 有任一档价格 → "1K: x   2K: y   4K: z"（缺失的档位显示 "-"）；
 *  - 仅有 flat 按次价（理论上 resolveImageTier 已回落填满三档，这里兜底）→ 三档同价；
 *  - 都没有 → 未配置定价。
 */
function perRequestText(m: ModelPlazaModel): string {
  const fold = (v: number | null): string => formatScaled(v == null ? null : v * imageMultiplier.value, 1)
  const tiers = m.image_tiers
  const hasTier =
    tiers != null && (tiers.price_1k != null || tiers.price_2k != null || tiers.price_4k != null)
  if (hasTier) {
    return [
      `1K: ${fold(tiers!.price_1k)}`,
      `2K: ${fold(tiers!.price_2k)}`,
      `4K: ${fold(tiers!.price_4k)}`,
    ].join('   ')
  }
  if (m.per_request_price != null) {
    const v = fold(m.per_request_price)
    return [`1K: ${v}`, `2K: ${v}`, `4K: ${v}`].join('   ')
  }
  return t('modelPlaza.noPricing')
}

async function load(groupId?: number) {
  loading.value = true
  try {
    const data = await modelPlazaAPI.getModelPlaza(groupId)
    groups.value = data.groups
    models.value = data.models
    tokenMultiplier.value = data.token_multiplier
    imageMultiplier.value = data.image_multiplier
    // 后端已回退到 id 最小的分组；以其返回值为准同步选择框。
    selectedGroupId.value = data.selected_group_id || data.groups[0]?.id || null
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    loading.value = false
  }
}

function onGroupChange() {
  if (selectedGroupId.value != null) {
    load(selectedGroupId.value)
  }
}

function reload() {
  load(selectedGroupId.value ?? undefined)
}

onMounted(() => load())
</script>
