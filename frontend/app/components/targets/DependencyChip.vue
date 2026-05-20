<script setup lang="ts">
defineProps<{
  id:    string
  label: string
  role:  'root_cause' | 'dependency' | 'impact'
}>()

const styles = {
  root_cause: 'bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-300 ring-red-300',
  dependency: 'bg-orange-100 dark:bg-orange-900/30 text-orange-700 dark:text-orange-300 ring-orange-300',
  impact:     'bg-yellow-100 dark:bg-yellow-900/30 text-yellow-700 dark:text-yellow-300 ring-yellow-300',
}

const prefixes = {
  root_cause: '⚠ Root:',
  dependency: '↑ Dep:',
  impact:     '↓ Impact:',
}
</script>

<template>
  <NuxtLink
    :to="`/targets/${encodeURIComponent(id)}`"
    :class="['inline-flex items-center gap-1 text-xs font-medium rounded-full px-2 py-0.5 ring-1 ring-inset transition hover:opacity-80', styles[role]]"
  >
    <span class="opacity-70">{{ prefixes[role] }}</span>
    <span>{{ label }}</span>
  </NuxtLink>
</template>
