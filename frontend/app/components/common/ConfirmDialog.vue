<script setup lang="ts">
const props = defineProps<{
  open:     boolean
  title:    string
  message?: string
  danger?:  boolean
}>()
const emit = defineEmits<{ confirm: []; cancel: [] }>()

// Close on Escape
onMounted(() => {
  if (import.meta.client) {
    document.addEventListener('keydown', onKey)
  }
})
onUnmounted(() => {
  if (import.meta.client) {
    document.removeEventListener('keydown', onKey)
  }
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
          class="relative z-10 bg-white dark:bg-gray-800 rounded-xl shadow-2xl p-6 w-full max-w-sm mx-4"
        >
          <h3 class="text-lg font-semibold text-gray-900 dark:text-white">{{ title }}</h3>
          <p v-if="message" class="mt-2 text-sm text-gray-600 dark:text-gray-400">{{ message }}</p>
          <div class="mt-5 flex justify-end gap-3">
            <button
              class="px-4 py-2 text-sm rounded-lg bg-gray-100 hover:bg-gray-200 dark:bg-gray-700 dark:hover:bg-gray-600 transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-gray-400"
              @click="emit('cancel')"
            >Cancel</button>
            <button
              autofocus
              :class="['px-4 py-2 text-sm rounded-lg text-white font-medium transition focus-visible:outline-none focus-visible:ring-2', danger ? 'bg-red-600 hover:bg-red-700 focus-visible:ring-red-400' : 'bg-blue-600 hover:bg-blue-700 focus-visible:ring-blue-400']"
              @click="emit('confirm')"
            >Confirm</button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
