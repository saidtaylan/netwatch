<script setup lang="ts">
const ui = useUIStore()

const typeStyles: Record<string, string> = {
  success: 'bg-green-50 border-green-400 text-green-800 dark:bg-green-900/30 dark:text-green-300',
  error:   'bg-red-50   border-red-400   text-red-800   dark:bg-red-900/30   dark:text-red-300',
  warning: 'bg-yellow-50 border-yellow-400 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-300',
  info:    'bg-blue-50  border-blue-400  text-blue-800  dark:bg-blue-900/30  dark:text-blue-300',
}
</script>

<template>
  <Teleport to="body">
    <div class="fixed bottom-4 right-4 z-50 flex flex-col gap-2 w-80">
      <TransitionGroup name="toast">
        <div
          v-for="t in ui.toasts"
          :key="t.id"
          :class="['flex items-start gap-2 px-3 py-2.5 rounded-lg border text-sm shadow-lg', typeStyles[t.type]]"
        >
          <span class="flex-1">{{ t.message }}</span>
          <button class="opacity-60 hover:opacity-100" @click="ui.removeToast(t.id)">✕</button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.toast-enter-active, .toast-leave-active { transition: all 0.2s; }
.toast-enter-from  { opacity: 0; transform: translateX(100%); }
.toast-leave-to    { opacity: 0; transform: translateX(100%); }
</style>
