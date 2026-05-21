<script setup lang="ts">
import type { SLOSnapshot } from '~/types/api'
import { fmtPercent, fmtDurationSec, fmtRelative } from '~/utils/format'

const api = useApi()
const { data: slo, error, loading, refresh } = usePolling<SLOSnapshot>(
  () => api.get<SLOSnapshot>('/slo'),
  { intervalMs: 60000 }
)

const enabled = computed(() => error.value?.message?.includes('503') === false && !error.value)
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h2 class="text-xl font-bold text-gray-900 dark:text-white">SLO Dashboard</h2>
      <button @click="refresh" class="text-xs text-blue-500 hover:underline">Refresh</button>
    </div>

    <ErrorBanner :error="error" title="Failed to load SLO data" @retry="refresh()" />

    <div v-if="error?.message?.includes('503')" class="bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-300 rounded-xl p-4">
      <p class="text-sm text-yellow-700 dark:text-yellow-300">SLO tracking is disabled. Set <code>slo.enabled: true</code> in config.yaml.</p>
    </div>

    <div v-else-if="slo?.targets?.length" class="space-y-3">
      <div
        v-for="t in slo.targets"
        :key="t.id"
        class="bg-white dark:bg-gray-800 rounded-xl border shadow-sm overflow-hidden"
        :class="t.breached ? 'border-red-300 dark:border-red-700' : 'border-gray-100 dark:border-gray-700'"
      >
        <!-- Header -->
        <div class="flex items-center justify-between px-4 py-3 border-b border-gray-100 dark:border-gray-700">
          <div class="flex items-center gap-2">
            <NuxtLink :to="{ name: 'targets-id', params: { id: t.id } }" class="font-semibold text-gray-900 dark:text-white hover:underline text-sm">
              {{ t.name ?? t.id }}
            </NuxtLink>
            <span class="text-xs text-gray-400">{{ t.window }}</span>
          </div>
          <span v-if="t.breached" class="text-xs font-semibold text-red-600 bg-red-50 dark:bg-red-900/20 ring-1 ring-red-400 rounded-full px-2 py-0.5">
            SLO Breach
          </span>
          <span v-else class="text-xs font-semibold text-green-600">✓ Within budget</span>
        </div>

        <!-- Metrics row -->
        <div class="grid grid-cols-2 sm:grid-cols-4 gap-4 px-4 py-3">
          <div>
            <p class="text-xs text-gray-500">Target</p>
            <p class="text-lg font-bold text-gray-800 dark:text-gray-200">{{ fmtPercent(t.target_uptime, 3) }}</p>
          </div>
          <div>
            <p class="text-xs text-gray-500">Actual</p>
            <p :class="['text-lg font-bold', t.actual_uptime >= t.target_uptime ? 'text-green-600' : 'text-red-600']">
              {{ fmtPercent(t.actual_uptime, 3) }}
            </p>
          </div>
          <div>
            <p class="text-xs text-gray-500">Error Budget</p>
            <p :class="['text-lg font-bold', t.error_budget_seconds >= 0 ? 'text-green-600' : 'text-red-600']">
              {{ t.error_budget_seconds >= 0 ? fmtDurationSec(t.error_budget_seconds) : '−' + fmtDurationSec(-t.error_budget_seconds) }}
            </p>
          </div>
          <div>
            <p class="text-xs text-gray-500">Incidents</p>
            <p class="text-lg font-bold text-gray-800 dark:text-gray-200">{{ t.incidents?.length ?? 0 }}</p>
          </div>
        </div>

        <!-- Recent incidents -->
        <div v-if="t.incidents?.length" class="px-4 pb-3">
          <p class="text-xs font-semibold text-gray-500 uppercase mb-1">Recent Incidents</p>
          <ul class="space-y-1">
            <li
              v-for="(inc, i) in t.incidents.slice(0, 5)"
              :key="i"
              class="flex items-center gap-2 text-xs text-gray-600 dark:text-gray-400"
            >
              <span class="w-1.5 h-1.5 rounded-full" :class="inc.ended_at ? 'bg-gray-400' : 'bg-red-500 animate-pulse'" />
              <span>{{ fmtRelative(inc.started_at) }}</span>
              <span v-if="inc.duration_sec" class="text-gray-400">— {{ fmtDurationSec(inc.duration_sec) }}</span>
              <span v-else class="text-red-500">ongoing</span>
            </li>
          </ul>
        </div>
      </div>
    </div>

    <EmptyState v-else-if="!loading && !error" title="No SLO targets configured" description="Add slo.targets to config.yaml." icon="📊" />
    <div v-if="loading && !slo" class="text-center py-12 text-gray-400 animate-pulse text-sm">Loading SLO data…</div>
  </div>
</template>
