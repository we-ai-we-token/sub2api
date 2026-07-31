<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- 第一行：3 个卡片 -->
      <div class="grid grid-cols-1 gap-4 md:grid-cols-3">
        <!-- 卡片 1：分组选择 -->
        <div class="card flex flex-col gap-2 p-5">
          <span class="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
            {{ t('modelPlazaLegacy.cards.group') }}
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
          <span v-if="selectedGroup" class="inline-flex items-center gap-2 text-base font-semibold text-gray-700 dark:text-gray-200">
            <PlatformIcon :platform="selectedGroup.platform as GroupPlatform" size="md" />
            {{ selectedGroup.platform }}
          </span>
        </div>

        <!-- 卡片 2：分组倍率 -->
        <div class="card flex flex-col gap-1 p-5">
          <span class="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
            {{ t('modelPlazaLegacy.cards.rate') }}
          </span>
          <span class="text-3xl font-bold text-primary-600 dark:text-primary-400">
            {{ effectiveRate != null ? `${formatRate(effectiveRate)}x` : '-' }}
          </span>
          <span
            v-if="hasUserRate"
            class="text-xs text-amber-600 dark:text-amber-400"
            :title="t('modelPlazaLegacy.userRateHint')"
          >
            {{ t('modelPlazaLegacy.userRateBadge', { rate: formatRate(selectedGroup!.rate_multiplier) }) }}
          </span>
        </div>

        <!-- 卡片 3：支持模型数量 -->
        <div class="card flex flex-col gap-1 p-5">
          <span class="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
            {{ t('modelPlazaLegacy.cards.modelCount') }}
          </span>
          <span class="text-3xl font-bold text-gray-900 dark:text-white">
            {{ loading ? '-' : models.length }}
          </span>
          <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('modelPlazaLegacy.cards.modelCountUnit') }}</span>
        </div>
      </div>

      <!-- 第二行：提示文案 -->
      <div
        class="flex items-start gap-2 rounded-lg border border-blue-100 bg-blue-50/60 px-4 py-3 text-sm text-blue-700 dark:border-blue-900/40 dark:bg-blue-900/20 dark:text-blue-300"
      >
        <Icon name="infoCircle" size="md" class="mt-0.5 flex-shrink-0" />
        <span>{{ t('modelPlazaLegacy.exchangeHint') }}</span>
      </div>
      <p class="-mt-3 px-1 text-xs text-gray-400 dark:text-gray-500">
        {{ t('modelPlazaLegacy.priceUnitNote') }}
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
              :placeholder="t('modelPlazaLegacy.searchPlaceholder')"
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
              <th class="px-4 py-3 text-left">{{ t('modelPlazaLegacy.columns.model') }}</th>
              <th class="px-4 py-3 text-left">{{ t('modelPlazaLegacy.columns.platform') }}</th>
              <th class="px-4 py-3 text-right">{{ t('modelPlazaLegacy.columns.input') }}</th>
              <th class="px-4 py-3 text-right">{{ t('modelPlazaLegacy.columns.output') }}</th>
              <th class="px-4 py-3 text-right">{{ t('modelPlazaLegacy.columns.cacheWrite') }}</th>
              <th class="px-4 py-3 text-right">{{ t('modelPlazaLegacy.columns.cacheRead') }}</th>
              <th class="px-4 py-3 text-right">{{ t('modelPlazaLegacy.columns.imageOutput') }}</th>
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
                <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('modelPlazaLegacy.empty') }}</p>
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
                <div class="flex flex-wrap items-center gap-1.5">
                  <span
                    :class="[
                      'inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[11px] font-medium uppercase',
                      platformBadgeClass(m.platform),
                    ]"
                  >
                    <PlatformIcon :platform="m.platform as GroupPlatform" size="xs" />
                    {{ m.platform }}
                  </span>
                  <span
                    v-if="isPerRequest(m)"
                    class="inline-flex items-center rounded-md border border-pink-200 bg-pink-50 px-2 py-0.5 text-[11px] font-medium text-pink-700 dark:border-pink-900/50 dark:bg-pink-900/20 dark:text-pink-300"
                  >
                    {{ t('modelPlazaLegacy.imagePerRequestTag') }}
                  </span>
                </div>
              </td>

              <!-- 按次/图片计费：合并后续列，1K/2K/4K 彩色标签清晰区分各分辨率单价 -->
              <td v-if="isPerRequest(m)" colspan="5" class="px-4 py-3">
                <div v-if="perRequestTiers(m).length > 0" class="flex flex-wrap items-center gap-2">
                  <span
                    v-for="tier in perRequestTiers(m)"
                    :key="tier.label"
                    :class="[
                      'inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1',
                      tierBadgeClass(tier.label),
                    ]"
                  >
                    <span class="text-[10px] font-bold uppercase opacity-70">{{ tier.label }}</span>
                    <span class="text-sm font-semibold tabular-nums">{{ tier.price }}</span>
                  </span>
                </div>
                <span v-else class="text-gray-400">{{ t('modelPlazaLegacy.noPricing') }}</span>
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
} from '@/api/modelPlazaLegacy'
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
/** 选中分组是否按生图质量（low/medium/high）计费；false 时按尺寸（1K/2K/4K）。 */
const imageQualityBilling = ref(false)

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
 * 生图按次模型的档位（价格已按图片倍率折算），用于渲染彩色标签。
 *
 * 档位口径由后端逐模型决定（同组内不同模型可不同）：质量计费分组里「渠道未配价」
 * 的模型填 low/medium/high，其余填 1K/2K/4K。这里按后端实际填充的档位渲染：
 *  - image_tiers 对应档位有任一价格 → 三档（缺失档位显示 "-"）；
 *  - 仅有 flat 按次价（resolveImageTier 通常已回落填满三档，这里兜底）→ 三档同价，
 *    档位标签跟随分组质量计费标志；
 *  - 都没有 → 空数组（模板显示"未配置定价"）。
 */
function perRequestTiers(m: ModelPlazaModel): { label: string; price: string }[] {
  const fold = (v: number | null): string => formatScaled(v == null ? null : v * imageMultiplier.value, 1)
  const tiers = m.image_tiers
  const hasQuality =
    tiers != null &&
    (tiers.price_low != null || tiers.price_medium != null || tiers.price_high != null)
  if (hasQuality) {
    return [
      { label: 'low', price: fold(tiers!.price_low) },
      { label: 'medium', price: fold(tiers!.price_medium) },
      { label: 'high', price: fold(tiers!.price_high) },
    ]
  }
  const hasSize =
    tiers != null && (tiers.price_1k != null || tiers.price_2k != null || tiers.price_4k != null)
  if (hasSize) {
    return [
      { label: '1K', price: fold(tiers!.price_1k) },
      { label: '2K', price: fold(tiers!.price_2k) },
      { label: '4K', price: fold(tiers!.price_4k) },
    ]
  }
  if (m.per_request_price != null) {
    const v = fold(m.per_request_price)
    // 无任何档价的 flat 兜底：标签跟随分组是否质量计费。
    const labels = imageQualityBilling.value
      ? (['low', 'medium', 'high'] as const)
      : (['1K', '2K', '4K'] as const)
    return labels.map((label) => ({ label, price: v }))
  }
  return []
}

/** 每档一种颜色，便于一眼区分不同分辨率/质量单价。 */
function tierBadgeClass(label: string): string {
  switch (label) {
    case '1K':
    case 'low':
      return 'border-sky-200 bg-sky-50 text-sky-700 dark:border-sky-900/50 dark:bg-sky-900/20 dark:text-sky-300'
    case '2K':
    case 'medium':
      return 'border-violet-200 bg-violet-50 text-violet-700 dark:border-violet-900/50 dark:bg-violet-900/20 dark:text-violet-300'
    case '4K':
    case 'high':
      return 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900/50 dark:bg-amber-900/20 dark:text-amber-300'
    default:
      return 'border-gray-200 bg-gray-50 text-gray-700 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300'
  }
}

async function load(groupId?: number) {
  loading.value = true
  try {
    const data = await modelPlazaAPI.getModelPlaza(groupId)
    groups.value = data.groups
    models.value = data.models
    tokenMultiplier.value = data.token_multiplier
    imageMultiplier.value = data.image_multiplier
    imageQualityBilling.value = data.image_quality_billing
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
