<script setup lang="ts">
import type { SLOSnapshot, SLOTargetResult } from '~/types/api'
import { fmtPercent, fmtDurationSec, fmtRelative } from '~/utils/format'

interface SLOTargetConfig {
  id: string
  target_uptime: number     // 0..1 (e.g. 0.999)
  window: string            // "30d", "7d", "24h", "1h"
}

const api  = useApi()
const ui   = useUIStore()
const auth = useAuthStore()

const { data: slo, error, loading, refresh } = usePolling<SLOSnapshot>(
  () => api.get<SLOSnapshot>('/slo'),
  { intervalMs: 60000 }
)

// Independent poller for the CRUD-managed target list. Faster cadence so
// changes from this UI feel snappy without waiting for the heavyweight
// /slo recompute.
const cfgs = usePolling<SLOTargetConfig[]>(() => api.get<SLOTargetConfig[]>('/slo/targets'), { intervalMs: 10000 })

// Backend returns targets as Record<id, SLOResult> — convert to sorted array
const targetList = computed<SLOTargetResult[]>(() => {
  if (!slo.value?.targets) return []
  return Object.values(slo.value.targets).sort((a, b) => {
    if (a.slo_breached !== b.slo_breached) return a.slo_breached ? -1 : 1
    return a.remaining_budget_sec - b.remaining_budget_sec
  })
})

// Config-only targets (defined but no result yet, e.g. just-added)
const cfgOnlyTargets = computed<SLOTargetConfig[]>(() => {
  const known = new Set(targetList.value.map(t => t.target_id))
  return (cfgs.data.value ?? []).filter(t => !known.has(t.id))
})

const cfgById = computed<Record<string, SLOTargetConfig>>(() => {
  const m: Record<string, SLOTargetConfig> = {}
  for (const c of cfgs.data.value ?? []) m[c.id] = c
  return m
})

// ── CRUD ───────────────────────────────────────────────────────────────
const modal = reactive({
  open: false,
  mode: 'create' as 'create' | 'edit',
  title: '',
  initialJson: '',
  editingId: '' as string,
})

function openCreate() {
  modal.mode = 'create'
  modal.title = 'New SLO Target'
  modal.editingId = ''
  modal.initialJson = JSON.stringify({
    id: 'target-id',
    target_uptime: 0.999,
    window: '30d',
  }, null, 2)
  modal.open = true
}

function openEdit(c: SLOTargetConfig) {
  modal.mode = 'edit'
  modal.title = `Edit SLO for ${c.id}`
  modal.editingId = c.id
  modal.initialJson = JSON.stringify(c, null, 2)
  modal.open = true
}

async function onSubmit(json: string) {
  try {
    const body = JSON.parse(json)
    const id = modal.mode === 'edit' ? modal.editingId : body.id
    if (!id) { ui.addToast('error', 'SLO target needs an id'); modal.open = false; return }
    if (typeof body.target_uptime !== 'number' || body.target_uptime <= 0 || body.target_uptime >= 1) {
      ui.addToast('error', 'target_uptime must be a number between 0 and 1 (exclusive)')
      modal.open = false
      return
    }
    await api.put(`/slo/targets/${encodeURIComponent(id)}`, body)
    ui.addToast('success', modal.mode === 'create' ? `SLO target ${id} created` : `SLO target ${id} updated`)
    modal.open = false
    await cfgs.refresh()
    await refresh()
  } catch (err: any) {
    ui.addToast('error', `Save failed: ${err?.data?.error ?? err?.message ?? err}`)
    modal.open = false
  }
}

async function onDelete(id: string) {
  if (!confirm(`Delete SLO target "${id}"? Incident history is preserved.`)) return
  try {
    await api.del(`/slo/targets/${encodeURIComponent(id)}`)
    ui.addToast('success', `SLO target ${id} deleted`)
    await cfgs.refresh()
    await refresh()
  } catch (err: any) {
    ui.addToast('error', `Delete failed: ${err?.data?.error ?? err?.message ?? err}`)
  }
}

const sloDisabled = computed(() => (error.value as any)?.response?.status === 503 || (error.value as any)?.message?.includes('503'))
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between flex-wrap gap-3">
      <div>
        <h2 class="text-xl font-bold text-gray-900 dark:text-white">SLO Dashboard</h2>
        <p v-if="slo?.computed_at" class="text-xs text-gray-400 mt-0.5">
          Computed {{ fmtRelative(slo.computed_at) }}
        </p>
      </div>
      <div class="flex gap-2">
        <button v-if="auth.token" @click="openCreate"
          class="text-sm px-3 py-1.5 rounded-lg bg-blue-600 hover:bg-blue-700 text-white font-medium transition">
          + New SLO Target
        </button>
        <button @click="refresh" class="text-xs text-blue-500 hover:underline">Refresh</button>
      </div>
    </div>

    <ErrorBanner v-if="!sloDisabled" :error="error" title="Failed to load SLO data" @retry="refresh()" />

    <div v-if="sloDisabled" class="bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-300 dark:border-yellow-700 rounded-xl p-4">
      <p class="text-sm text-yellow-700 dark:text-yellow-300">
        SLO tracking is disabled. Set <code>slo.enabled: true</code> in config.yaml on every node and restart.
      </p>
    </div>

    <div v-else-if="targetList.length || cfgOnlyTargets.length" class="space-y-3">
      <!-- Targets with computed metrics -->
      <div
        v-for="t in targetList"
        :key="t.target_id"
        class="bg-white dark:bg-gray-800 rounded-xl border shadow-sm overflow-hidden"
        :class="t.slo_breached ? 'border-red-300 dark:border-red-700' : 'border-gray-100 dark:border-gray-700'"
      >
        <!-- Header -->
        <div class="flex items-center justify-between px-4 py-3 border-b border-gray-100 dark:border-gray-700">
          <div class="flex items-center gap-2">
            <NuxtLink :to="{ name: 'targets-id', params: { id: t.target_id } }"
              class="font-semibold text-gray-900 dark:text-white hover:underline text-sm">
              {{ t.target_id }}
            </NuxtLink>
            <span class="text-xs text-gray-400 bg-gray-100 dark:bg-gray-700 rounded px-1.5 py-0.5">{{ t.window }}</span>
          </div>
          <div class="flex items-center gap-2">
            <span v-if="t.slo_breached"
              class="text-xs font-semibold text-red-600 bg-red-50 dark:bg-red-900/20 ring-1 ring-red-400 rounded-full px-2 py-0.5">
              ⚠ SLO Breach
            </span>
            <span v-else class="text-xs font-semibold text-green-600">✓ Within budget</span>
            <template v-if="auth.token && cfgById[t.target_id]">
              <button @click="openEdit(cfgById[t.target_id])" class="text-xs text-blue-600 hover:underline ml-2">Edit</button>
              <button @click="onDelete(t.target_id)" class="text-xs text-red-600 hover:underline">Delete</button>
            </template>
          </div>
        </div>

        <!-- Metrics row -->
        <div class="grid grid-cols-2 sm:grid-cols-4 gap-4 px-4 py-3">
          <div>
            <p class="text-xs text-gray-500 mb-1">Target Uptime</p>
            <p class="text-lg font-bold text-gray-800 dark:text-gray-200">{{ fmtPercent(t.target_uptime, 3) }}</p>
          </div>
          <div>
            <p class="text-xs text-gray-500 mb-1">Actual Uptime</p>
            <p :class="['text-lg font-bold', t.actual_uptime >= t.target_uptime ? 'text-green-600' : 'text-red-600']">
              {{ fmtPercent(t.actual_uptime, 3) }}
            </p>
          </div>
          <div>
            <p class="text-xs text-gray-500 mb-1">Error Budget</p>
            <p :class="['text-lg font-bold', t.remaining_budget_sec >= 0 ? 'text-green-600' : 'text-red-600']">
              {{ t.remaining_budget_sec >= 0
                ? fmtDurationSec(t.remaining_budget_sec)
                : '−' + fmtDurationSec(-t.remaining_budget_sec) }}
            </p>
          </div>
          <div>
            <p class="text-xs text-gray-500 mb-1">Incidents</p>
            <p class="text-lg font-bold text-gray-800 dark:text-gray-200">{{ t.incident_count }}</p>
            <p v-if="t.downtime_minutes > 0" class="text-xs text-gray-400 mt-0.5">
              {{ fmtDurationSec(t.downtime_sec) }} downtime
            </p>
          </div>
        </div>

        <!-- Incidents -->
        <div v-if="t.incidents?.length" class="px-4 pb-3">
          <p class="text-xs font-semibold text-gray-500 uppercase mb-2">Incidents</p>
          <ul class="space-y-1">
            <li
              v-for="(inc, i) in t.incidents.slice(0, 5)"
              :key="i"
              class="flex items-center gap-2 text-xs text-gray-600 dark:text-gray-400"
            >
              <span class="w-1.5 h-1.5 rounded-full flex-shrink-0"
                :class="inc.ended_at ? 'bg-gray-400' : 'bg-red-500 animate-pulse'" />
              <span>{{ fmtRelative(inc.started_at) }}</span>
              <span v-if="inc.duration_sec" class="text-gray-400">— {{ fmtDurationSec(inc.duration_sec) }}</span>
              <span v-else-if="!inc.ended_at" class="text-red-500 font-medium">ongoing</span>
            </li>
          </ul>
        </div>
      </div>

      <!-- Config-only targets (just added, no result yet) -->
      <div v-for="c in cfgOnlyTargets" :key="`cfg-${c.id}`"
        class="bg-white dark:bg-gray-800 rounded-xl border border-gray-200 dark:border-gray-700 shadow-sm px-4 py-3 flex items-center gap-3">
        <span class="text-xs text-gray-400 bg-gray-100 dark:bg-gray-700 rounded px-2 py-0.5">{{ c.window }}</span>
        <div class="flex-1 min-w-0">
          <p class="font-semibold text-gray-900 dark:text-white text-sm truncate">{{ c.id }}</p>
          <p class="text-xs text-gray-400">target uptime {{ fmtPercent(c.target_uptime, 3) }} · awaiting first compute…</p>
        </div>
        <div v-if="auth.token" class="flex gap-2">
          <button @click="openEdit(c)" class="text-xs text-blue-600 hover:underline">Edit</button>
          <button @click="onDelete(c.id)" class="text-xs text-red-600 hover:underline">Delete</button>
        </div>
      </div>
    </div>

    <EmptyState
      v-else-if="!loading && !error"
      title="No SLO targets yet"
      description="Click + New SLO Target to add one. Stored in DB, replicated across the cluster."
      icon="📊"
    />

    <div v-if="loading && !targetList.length" class="text-center py-12 text-gray-400 animate-pulse text-sm">
      Loading SLO data…
    </div>

    <CrudJsonModal
      :open="modal.open"
      :title="modal.title"
      :initialJson="modal.initialJson"
      :submitLabel="modal.mode === 'create' ? 'Create' : 'Save'"
      hint="id: matches a target id/name. target_uptime: 0..1 (e.g. 0.999 = 99.9%). window: 30d|7d|24h|1h"
      @submit="onSubmit"
      @cancel="modal.open = false"
    />
  </div>
</template>
