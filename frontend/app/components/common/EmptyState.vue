<script setup lang="ts">
defineProps<{
  title:       string
  description?: string
  icon?:        string
}>()
</script>

<template>
  <div class="flex flex-col items-center justify-center py-16 text-center text-gray-400 dark:text-gray-600">
    <div v-if="icon" class="text-5xl mb-3">{{ icon }}</div>
    <p class="text-base font-medium text-gray-500 dark:text-gray-400">{{ title }}</p>
    <p v-if="description" class="text-sm mt-1 max-w-sm">{{ description }}</p>
    <slot />
  </div>
</template>
