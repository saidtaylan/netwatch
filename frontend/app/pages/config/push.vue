<script setup lang="ts">
import type { SharedConfig, ConfigPushResult } from '~/types/api'
import { fmtRelative } from '~/utils/format'

const api = useApi()
const ui  = useUIStore()

const form = reactive<SharedConfig>({
  timeout:             undefined,
  max_retries:         undefined,
  retry_interval_sec:  undefined,
  ticker_interval_sec: undefined,
  probe_interval_sec:  undefined,
  reload_interval_sec: undefined,
  watchdog_threshold_sec: undefined,
  recovery_probes:     undefined,
})

const submitting = ref(false)
const result     = ref<ConfigPushResult | null>(null)

// Strip undefined/empty fields before sending
function buildPayload(): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(form)) {
    if (v !== undefined && v !== null && v !== '') out[k] = Number(v)
  }
  return out
}

async function push() {
  const payload = buildPayload()
  if (Object.keys(payload).length === 0) {
    ui.addToast('warning', 'No fields to push — fill in at least one value.')
    return
  }
  submitting.value = true
  try {
    result.value = await api.put<ConfigPushResult>('/cluster/config', payload)
    ui.addToast('success', `Config pushed to ${result.value.broadcast_to.length} peer(s)`)
  } catch (e: any) {
    ui.addToast('error', `Push failed: ${e?.message}`)
  } finally {
    submitting.value = false
  }
}

interface Field { key: keyof SharedConfig; label: string; hint: string }
const fields: Field[] = [
  { key: 'timeout',              label: 'Timeout (s)',              hint: 'Probe timeout seconds' },
  { key: 'max_retries',          label: 'Max Retries',              hint: 'Retries before hard_down' },
  { key: 'retry_interval_sec',   label: 'Retry Interval (s)',        hint: '≥5' },
  { key: 'ticker_interval_sec',  label: 'Ticker Interval (s)',       hint: 'Main loop tick' },
  { key: 'probe_interval_sec',   label: 'Probe Interval (s)',        hint: 'Default probe interval' },
  { key: 'reload_interval_sec',  label: 'Reload Interval (s)',       hint: '0 = disabled' },
  { key: 'watchdog_threshold_sec', label: 'Watchdog Threshold (s)', hint: '0 = disabled' },
  { key: 'recovery_probes',      label: 'Recovery Probes',          hint: 'Probes before soft_up→up' },
]
</script>

<template>
  <div class="max-w-lg space-y-5">
    <div class="flex items-center gap-3">
      <NuxtLink :to="{ name: 'config' }" class="text-gray-400 hover:text-gray-600 text-sm">← Config</NuxtLink>
      <h2 class="text-xl font-bold text-gray-900 dark:text-white">Push Config</h2>
    </div>
    <p class="text-sm text-gray-500">Push shared config fields to all cluster nodes. Leave fields empty to skip them.</p>

    <div class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm p-5 space-y-4">
      <div v-for="f in fields" :key="f.key" class="flex items-center gap-3">
        <label class="w-48 text-sm text-gray-700 dark:text-gray-300 flex-shrink-0">{{ f.label }}</label>
        <input
          v-model="(form as any)[f.key]"
          type="number"
          :placeholder="f.hint"
          class="flex-1 rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm px-3 py-1.5 focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
      </div>

      <div class="pt-2 flex justify-end">
        <button @click="push" :disabled="submitting"
          class="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white text-sm font-medium rounded-lg transition disabled:opacity-60">
          {{ submitting ? 'Pushing…' : 'Push to all nodes' }}
        </button>
      </div>
    </div>

    <!-- Result -->
    <div v-if="result" class="bg-white dark:bg-gray-800 rounded-xl border border-green-200 dark:border-green-700 shadow-sm p-4 space-y-2">
      <h3 class="text-sm font-semibold text-green-700 dark:text-green-400">Push complete</h3>
      <p class="text-xs text-gray-600 dark:text-gray-400">Fields: {{ result.fields_applied.join(', ') }}</p>
      <p class="text-xs text-gray-600 dark:text-gray-400">Broadcast to: {{ result.broadcast_to.join(', ') || 'none (standalone)' }}</p>
      <div v-if="Object.keys(result.failed_nodes ?? {}).length" class="text-xs text-red-600">
        Failed: {{ Object.keys(result.failed_nodes).join(', ') }}
      </div>
      <p class="text-xs text-gray-400">{{ fmtRelative(result.pushed_at) }}</p>
    </div>
  </div>
</template>
