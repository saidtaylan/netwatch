<script setup lang="ts">
import { fmtRelative } from '~/utils/format'

const auth = useAuthStore()
const { windows, active, create, cancel } = useMaintenance()

// Form state
const showForm   = ref(false)
const form       = reactive({ targetId: '', durationMs: 3600000, reason: '', createdBy: 'ui-admin' })
const submitting = ref(false)
const confirmId  = ref<string | null>(null)

const durationOptions = [
  { label: '30 minutes', ms: 30 * 60 * 1000 },
  { label: '1 hour',     ms: 60 * 60 * 1000 },
  { label: '2 hours',    ms: 2 * 60 * 60 * 1000 },
  { label: '4 hours',    ms: 4 * 60 * 60 * 1000 },
  { label: '8 hours',    ms: 8 * 60 * 60 * 1000 },
]

async function submit() {
  if (!form.targetId.trim() || !form.reason.trim()) return
  submitting.value = true
  try {
    await create(form.targetId.trim(), form.durationMs, form.reason.trim(), form.createdBy)
    showForm.value = false
    Object.assign(form, { targetId: '', reason: '' })
  } finally {
    submitting.value = false
  }
}

async function confirmCancel() {
  if (!confirmId.value) return
  await cancel(confirmId.value)
  confirmId.value = null
}

function endsIn(endsAt: string): string {
  const diff = new Date(endsAt).getTime() - Date.now()
  if (diff <= 0) return 'expired'
  const h = Math.floor(diff / 3600000)
  const m = Math.floor((diff % 3600000) / 60000)
  return h > 0 ? `${h}h ${m}m remaining` : `${m}m remaining`
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <div>
        <h2 class="text-xl font-bold text-gray-900 dark:text-white">Maintenance Windows</h2>
        <p class="text-sm text-gray-500 mt-0.5">Suppress alerts for targets during scheduled maintenance.</p>
      </div>
      <button
        v-if="auth.isAuthenticated"
        @click="showForm = !showForm"
        class="text-sm px-3 py-1.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg transition"
      >
        {{ showForm ? 'Cancel' : '+ New Window' }}
      </button>
    </div>

    <!-- Create form -->
    <div v-if="showForm" class="bg-white dark:bg-gray-800 rounded-xl border border-blue-200 dark:border-blue-700 shadow-sm p-5">
      <h3 class="text-sm font-semibold mb-4 text-gray-800 dark:text-gray-200">New Maintenance Window</h3>
      <div class="space-y-3">
        <div>
          <label class="block text-xs font-medium text-gray-600 dark:text-gray-400 mb-1">Target ID or Name</label>
          <input v-model="form.targetId" type="text" placeholder="e.g. db-primary"
            class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm px-3 py-2 focus:outline-none focus:ring-2 focus:ring-blue-500" />
        </div>
        <div>
          <label class="block text-xs font-medium text-gray-600 dark:text-gray-400 mb-1">Duration</label>
          <select v-model="form.durationMs"
            class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm px-3 py-2 focus:outline-none">
            <option v-for="opt in durationOptions" :key="opt.ms" :value="opt.ms">{{ opt.label }}</option>
          </select>
        </div>
        <div>
          <label class="block text-xs font-medium text-gray-600 dark:text-gray-400 mb-1">Reason</label>
          <input v-model="form.reason" type="text" placeholder="e.g. Scheduled DB upgrade"
            class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm px-3 py-2 focus:outline-none focus:ring-2 focus:ring-blue-500" />
        </div>
        <div class="flex justify-end gap-2 pt-1">
          <button @click="showForm = false" class="text-sm px-3 py-1.5 rounded-lg bg-gray-100 hover:bg-gray-200 dark:bg-gray-700 transition">Cancel</button>
          <button @click="submit" :disabled="submitting || !form.targetId || !form.reason"
            class="text-sm px-4 py-1.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg transition disabled:opacity-60">
            {{ submitting ? 'Setting…' : 'Set Maintenance' }}
          </button>
        </div>
      </div>
    </div>

    <!-- Active windows -->
    <div v-if="active.length" class="space-y-2">
      <h3 class="text-xs font-semibold text-gray-500 uppercase tracking-wide">Active ({{ active.length }})</h3>
      <div v-for="w in active" :key="w.id" class="bg-white dark:bg-gray-800 rounded-xl border border-orange-200 dark:border-orange-800 shadow-sm px-4 py-3 flex items-start justify-between gap-3">
        <div class="space-y-1">
          <div class="flex items-center gap-2">
            <span class="text-sm font-semibold text-gray-900 dark:text-white">{{ w.target_id }}</span>
            <span class="text-xs text-orange-600 bg-orange-50 dark:bg-orange-900/20 rounded-full px-2 py-0.5">🔧 {{ endsIn(w.ends_at) }}</span>
          </div>
          <p class="text-xs text-gray-500">{{ w.reason }}</p>
          <p class="text-xs text-gray-400">by {{ w.created_by }} · started {{ fmtRelative(w.started_at) }}</p>
        </div>
        <button
          v-if="auth.isAuthenticated"
          @click="confirmId = w.id"
          class="text-xs text-red-500 hover:text-red-700 hover:underline flex-shrink-0"
        >Cancel</button>
      </div>
    </div>

    <!-- Past/all windows -->
    <div v-if="(windows.data.value?.length ?? 0) > active.length" class="space-y-2">
      <h3 class="text-xs font-semibold text-gray-500 uppercase tracking-wide">Expired</h3>
      <div v-for="w in windows.data.value?.filter(w => new Date(w.ends_at) <= new Date())" :key="w.id"
        class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm px-4 py-3 opacity-60">
        <div class="flex items-center gap-2">
          <span class="text-sm text-gray-700 dark:text-gray-300">{{ w.target_id }}</span>
          <span class="text-xs text-gray-400">{{ w.reason }}</span>
        </div>
        <p class="text-xs text-gray-400">ended {{ fmtRelative(w.ends_at) }}</p>
      </div>
    </div>

    <EmptyState
      v-if="!windows.loading.value && !(windows.data.value?.length)"
      title="No maintenance windows"
      description="Create a window to suppress alerts during planned outages."
      icon="🔧"
    />
  </div>

  <!-- Confirm cancel -->
  <ConfirmDialog
    :open="!!confirmId"
    title="Cancel maintenance window?"
    message="Alerts will resume immediately for this target."
    danger
    @confirm="confirmCancel"
    @cancel="confirmId = null"
  />
</template>
