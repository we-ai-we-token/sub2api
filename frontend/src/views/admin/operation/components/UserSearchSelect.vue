<template>
  <div class="flex items-center gap-2">
    <label class="whitespace-nowrap text-xs font-medium text-gray-600 dark:text-gray-400">
      {{ t('admin.usage.userFilter') }}
    </label>
    <div ref="rootRef" class="relative">
      <input
        v-model="keyword"
        type="text"
        class="w-48 rounded-lg border border-gray-200 bg-gray-50 px-2 py-1 pr-7 text-sm text-gray-700 focus:outline-none dark:border-dark-600 dark:bg-dark-700 dark:text-gray-300"
        :placeholder="t('admin.usage.searchUserPlaceholder')"
        @input="debounceSearch"
        @focus="showDropdown = true"
      />
      <button
        v-if="modelValue !== undefined"
        type="button"
        class="absolute right-2 top-1 text-gray-400 hover:text-gray-600 dark:hover:text-gray-200"
        aria-label="Clear user filter"
        @click="clearUser"
      >
        ✕
      </button>
      <div
        v-if="showDropdown && (results.length > 0 || keyword)"
        class="absolute z-50 mt-1 max-h-60 w-72 overflow-auto rounded-lg border border-gray-200 bg-white shadow-lg dark:border-dark-600 dark:bg-dark-800"
      >
        <button
          v-for="u in results"
          :key="u.id"
          type="button"
          class="w-full px-3 py-2 text-left text-sm hover:bg-gray-100 dark:hover:bg-dark-700"
          @click="selectUser(u)"
        >
          <span class="text-gray-700 dark:text-gray-300">
            {{ u.email
            }}<span v-if="u.deleted" class="ml-1 text-xs text-gray-400">（{{ t('admin.usage.userDeletedBadge') }}）</span>
          </span>
          <span class="ml-2 text-xs text-gray-400">#{{ u.id }}</span>
        </button>
        <div v-if="results.length === 0" class="px-3 py-2 text-sm text-gray-400">
          {{ t('common.noData') }}
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { SimpleUser } from '@/api/admin/usage'

const props = defineProps<{ modelValue: number | undefined }>()
const emit = defineEmits<{
  (e: 'update:modelValue', value: number | undefined): void
  (e: 'change'): void
}>()

const { t } = useI18n()

const rootRef = ref<HTMLElement | null>(null)
const keyword = ref('')
const results = ref<SimpleUser[]>([])
const showDropdown = ref(false)

let searchTimeout: ReturnType<typeof setTimeout> | null = null
// 每次输入都递增；只有序号仍匹配的响应才允许写回，避免慢的旧请求覆盖新结果。
let searchSequence = 0

const clearPendingSearch = () => {
  if (searchTimeout) {
    clearTimeout(searchTimeout)
    searchTimeout = null
  }
  searchSequence += 1
}

const debounceSearch = () => {
  clearPendingSearch()
  const query = keyword.value.trim()
  if (!query) {
    results.value = []
    return
  }

  const sequence = searchSequence
  searchTimeout = setTimeout(async () => {
    searchTimeout = null
    try {
      const found = await adminAPI.usage.searchUsers(query)
      if (sequence === searchSequence) {
        results.value = found.sort((a, b) => Number(a.deleted) - Number(b.deleted))
      }
    } catch {
      if (sequence === searchSequence) {
        results.value = []
      }
    }
  }, 300)
}

const selectUser = (u: SimpleUser) => {
  clearPendingSearch()
  keyword.value = u.email
  showDropdown.value = false
  emit('update:modelValue', u.id)
  emit('change')
}

const clearUser = () => {
  clearPendingSearch()
  keyword.value = ''
  results.value = []
  showDropdown.value = false
  if (props.modelValue === undefined) return
  emit('update:modelValue', undefined)
  emit('change')
}

const onDocumentClick = (e: MouseEvent) => {
  const target = e.target as Node | null
  if (!target) return
  if (!(rootRef.value?.contains(target) ?? false)) {
    showDropdown.value = false
  }
}

onMounted(() => document.addEventListener('click', onDocumentClick))
onUnmounted(() => {
  clearPendingSearch()
  document.removeEventListener('click', onDocumentClick)
})
</script>
