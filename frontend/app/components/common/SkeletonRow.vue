<!-- Generic skeleton loader row — use inside a list/table while data is loading -->
<script setup lang="ts">
const props = defineProps<{ cols?: number; rows?: number }>()
const colCount = computed(() => Math.max(1, props.cols ?? 3))
const rowCount = computed(() => Math.max(1, props.rows ?? 4))
</script>

<template>
  <div class="space-y-2 p-4" role="status" aria-label="Loading…" aria-busy="true">
    <div
      v-for="r in rowCount"
      :key="r"
      class="flex items-center gap-3"
    >
      <div
        v-for="c in colCount"
        :key="c"
        :class="['h-4 bg-gray-200 dark:bg-gray-700 rounded animate-pulse',
          c === 1 ? 'w-2 h-2 rounded-full flex-shrink-0' :
          c === 2 ? 'flex-1' : 'w-16']"
      />
    </div>
    <span class="sr-only">Loading…</span>
  </div>
</template>
