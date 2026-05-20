<!-- B2 — ready for when backend adds severity field -->
<script setup lang="ts">
import type { Severity } from '~/types/api'

const props = defineProps<{ severity?: Severity }>()

const styles: Record<Severity, { label: string; classes: string }> = {
  critical: { label: 'Critical', classes: 'bg-red-50 text-red-700 ring-red-500  dark:bg-red-900/20' },
  warning:  { label: 'Warning',  classes: 'bg-yellow-50 text-yellow-700 ring-yellow-500 dark:bg-yellow-900/20' },
  info:     { label: 'Info',     classes: 'bg-blue-50 text-blue-700 ring-blue-400 dark:bg-blue-900/20' },
}

const style = computed(() => styles[props.severity ?? 'info'])
</script>

<template>
  <span :class="['inline-flex items-center text-xs font-medium px-1.5 py-0.5 rounded-full ring-1 ring-inset', style.classes]">
    {{ style.label }}
  </span>
</template>
