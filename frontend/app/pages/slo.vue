<script setup lang="ts">
import type { SLOSnapshot, SLOTargetResult } from '~/types/api'
import { fmtPercent, fmtDurationSec, fmtRelative } from '~/utils/format'

const api = useApi()
const { data: slo, error, loading, refresh } = usePolling<SLOSnapshot>(
  () => api.get<SLOSnapshot>('/slo'),
  { intervalMs: 60000 }
)

// Backend returns targets as Record<id, SLOResult> — convert to sorted array
const targetList = computed<SLOTargetResult[]>(() => {
  if (!slo.value?.targets) return []
  return Object.values(slo.value.targets).sort((a, b) => {
    // Breached first, then by remaining budget
    if (a.slo_breached !== b.slo_breached) return a.slo_breached ? -1 : 1
    return a.remaining_budget_sec - b.remaining_budget_sec
  })
})
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <div>
        <h2 class="text-xl font-bold text-gray-900 dark:text-white">SLO Dashboard</h2>
        <p v-if="slo?.computed_at" class="text-xs text-gray-400 mt-0.5">
          Computed {{ fmtRelative(slo.computed_at) }}
        </p>
      </div>
      <button @click="refresh" class="text-xs text-blue-500 hover:underline">Refresh</button>
    </div>

    <ErrorBanner :error="error" title="Failed to load SLO data" @retry="refresh()" />

    <div v-if="error?.message?.includes('503')" class="bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-300 dark:border-yellow-700 rounded-xl p-4">
      <p class="text-sm text-yellow-700 dark:text-yellow-300">SLO tracking is disabled. Set <code>slo.enabled: true</code> in config.yaml.</p>
    </div>

    <div v-else-if="targetList.length" class="space-y-3">
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
    </div>

    <EmptyState
      v-else-if="!loading && !error"
      title="No SLO targets configured"
      description="Add slo.targets to config.yaml to start tracking uptime objectives."
      icon="📊"
    />

    <div v-if="loading && !targetList.length" class="text-center py-12 text-gray-400 animate-pulse text-sm">
      Loading SLO data…
    </div>
  </div>
</template>
