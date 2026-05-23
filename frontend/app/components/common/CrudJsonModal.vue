<script setup lang="ts">
/**
 * Generic JSON-edit modal for CRUD operations against the storage-backed
 * domains (apps, channels, targets, silences, slo targets). The form is
 * a single JSON textarea — pragmatic for power users + scales across all
 * domains without per-entity Vue forms.
 *
 * Usage:
 *   <CrudJsonModal
 *     :open="modal.open"
 *     :title="modal.title"
 *     :initialJson="modal.initialJson"
 *     :submitLabel="modal.submitLabel"
 *     @submit="onSubmit"
 *     @cancel="modal.open = false"
 *   />
 */
const props = defineProps<{
  open: boolean
  title: string
  initialJson: string                  // pre-filled JSON body
  submitLabel?: string                 // default: "Save"
  hint?: string                        // small help text under textarea
}>()

const emit = defineEmits<{ submit: [json: string]; cancel: [] }>()

const editing = ref('')
const parseError = ref<string | null>(null)
const submitting = ref(false)

watch(() => props.open, (open) => {
  if (open) {
    editing.value = props.initialJson
    parseError.value = null
    submitting.value = false
  }
})

function onSubmit() {
  // Validate JSON shape client-side so backend doesn't get garbage.
  try {
    JSON.parse(editing.value)
  } catch (e: unknown) {
    parseError.value = (e as Error).message
    return
  }
  parseError.value = null
  submitting.value = true
  emit('submit', editing.value)
}

// Allow caller to re-enable the submit button after an upstream error.
defineExpose({
  reset: () => { submitting.value = false },
})

onMounted(() => {
  if (import.meta.client) document.addEventListener('keydown', onKey)
})
onUnmounted(() => {
  if (import.meta.client) document.removeEventListener('keydown', onKey)
})
function onKey(e: KeyboardEvent) {
  if (props.open && e.key === 'Escape') emit('cancel')
}
</script>

<template>
  <Teleport to="body">
    <Transition name="fade">
      <div v-if="open" class="fixed inset-0 z-50 flex items-center justify-center">
        <div class="absolute inset-0 bg-black/40" @click="emit('cancel')" />
        <div
          role="dialog"
          :aria-label="title"
          aria-modal="true"
          class="relative z-10 bg-white dark:bg-gray-800 rounded-xl shadow-2xl p-6 w-full max-w-2xl mx-4"
        >
          <h3 class="text-lg font-semibold text-gray-900 dark:text-white">{{ title }}</h3>

          <textarea
            v-model="editing"
            spellcheck="false"
            rows="12"
            class="mt-4 w-full font-mono text-xs rounded-lg border border-gray-300 dark:border-gray-600 bg-gray-50 dark:bg-gray-900 text-gray-900 dark:text-gray-100 px-3 py-2 focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
          <p v-if="parseError" class="mt-2 text-xs text-red-600">JSON: {{ parseError }}</p>
          <p v-else-if="hint" class="mt-2 text-xs text-gray-500">{{ hint }}</p>

          <div class="mt-5 flex justify-end gap-3">
            <button
              type="button"
              class="px-4 py-2 text-sm rounded-lg bg-gray-100 hover:bg-gray-200 dark:bg-gray-700 dark:hover:bg-gray-600 transition"
              @click="emit('cancel')"
            >Cancel</button>
            <button
              type="button"
              :disabled="submitting"
              class="px-4 py-2 text-sm rounded-lg text-white font-medium transition bg-blue-600 hover:bg-blue-700 disabled:opacity-60 disabled:cursor-not-allowed"
              @click="onSubmit"
            >{{ submitting ? 'Saving…' : (submitLabel ?? 'Save') }}</button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
