<!-- Skeleton for dashboard stat cards -->
<script setup lang="ts">
defineProps<{ count?: number }>()
</script>

<template>
  <div
    v-for="i in (count ?? 4)"
    :key="i"
    class="bg-white dark:bg-gray-800 rounded-xl p-4 shadow-sm border border-gray-100 dark:border-gray-700"
    role="status"
    aria-busy="true"
  >
    <div class="h-3 w-24 bg-gray-200 dark:bg-gray-700 rounded animate-pulse mb-3" />
    <div class="h-7 w-12 bg-gray-200 dark:bg-gray-700 rounded animate-pulse mb-2" />
    <div class="h-2.5 w-20 bg-gray-100 dark:bg-gray-800 rounded animate-pulse" />
    <span class="sr-only">Loading…</span>
  </div>
</template>
