<script setup lang="ts">
const props = defineProps<{
  error:     Error | null | undefined
  title?:    string
  retrying?: boolean
}>()

const emit = defineEmits<{ retry: [] }>()

// Don't show 503 as an error — pages handle it as "feature disabled"
const show = computed(() =>
  !!props.error && !props.error.message?.includes('503')
)

function friendly(err: Error): string {
  if (err.message?.includes('Failed to fetch') || err.message?.includes('ECONNREFUSED')) {
    return 'Could not reach the backend. Check your connection.'
  }
  if (err.message?.includes('401')) return 'Authentication failed. Check your token.'
  if (err.message?.includes('403')) return 'Permission denied.'
  return err.message ?? 'An unexpected error occurred.'
}
</script>

<template>
  <div
    v-if="show"
    class="flex items-start gap-3 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-xl px-4 py-3"
    role="alert"
  >
    <span class="text-red-500 flex-shrink-0 mt-0.5" aria-hidden="true">⚠</span>
    <div class="flex-1 min-w-0">
      <p class="text-sm font-medium text-red-800 dark:text-red-300">
        {{ title ?? 'Failed to load data' }}
      </p>
      <p class="text-xs text-red-600 dark:text-red-400 mt-0.5">
        {{ friendly(error!) }}
      </p>
    </div>
    <button
      class="flex-shrink-0 text-xs text-red-600 hover:text-red-800 dark:text-red-400 hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-red-400 rounded"
      :disabled="retrying"
      @click="emit('retry')"
    >
      {{ retrying ? 'Retrying…' : 'Retry' }}
    </button>
  </div>
</template>
