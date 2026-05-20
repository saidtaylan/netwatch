<script setup lang="ts">
defineProps<{
  healthy: boolean | null
  isolated: boolean
  memberCount: number
  expectedCount?: number
}>()
</script>

<template>
  <div class="flex items-center gap-2">
    <template v-if="isolated">
      <span class="inline-flex items-center gap-1.5 text-xs font-medium text-orange-600 bg-orange-50 dark:bg-orange-900/20 ring-1 ring-orange-400 rounded-full px-2.5 py-1">
        <span class="w-1.5 h-1.5 rounded-full bg-orange-500 animate-pulse" />
        Isolated Mode
      </span>
    </template>
    <template v-else-if="healthy === true">
      <span class="inline-flex items-center gap-1.5 text-xs font-medium text-green-700 bg-green-50 dark:bg-green-900/20 ring-1 ring-green-400 rounded-full px-2.5 py-1">
        <span class="w-1.5 h-1.5 rounded-full bg-green-500" />
        Quorum healthy
        <span v-if="expectedCount" class="opacity-70">({{ memberCount }}/{{ expectedCount }})</span>
      </span>
    </template>
    <template v-else-if="healthy === false">
      <span class="inline-flex items-center gap-1.5 text-xs font-medium text-red-700 bg-red-50 dark:bg-red-900/20 ring-1 ring-red-400 rounded-full px-2.5 py-1">
        <span class="w-1.5 h-1.5 rounded-full bg-red-500 animate-pulse" />
        Quorum lost
        <span v-if="expectedCount" class="opacity-70">({{ memberCount }}/{{ expectedCount }})</span>
      </span>
    </template>
    <template v-else>
      <span class="inline-flex items-center gap-1.5 text-xs font-medium text-gray-500 bg-gray-50 dark:bg-gray-800 ring-1 ring-gray-300 rounded-full px-2.5 py-1">
        <span class="w-1.5 h-1.5 rounded-full bg-gray-400" />
        Standalone
      </span>
    </template>
  </div>
</template>
