<script setup lang="ts">
import { fmtRelative } from '~/utils/format'
import { SCOPE_STYLE, CLASS_STYLE } from '~/utils/classifyState'

// Fleet polling keeps alerts store populated via state-change detection.
// SLO polling pushes breach/recovery alerts via useSLO composable (B17).
const { fleet } = useFleet()
const { slo }   = useSLO()
const alerts    = useAlertsStore()

function statusColor(status: string) {
  return status === 'unreachable' ? 'text-red-600' : 'text-green-600'
}

// SLO alerts have id prefixed with "slo-"
function isSLOAlert(id: string): boolean {
  return id.startsWith('slo-')
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <div>
        <h2 class="text-xl font-bold text-gray-900 dark:text-white">Alert Feed</h2>
        <p class="text-sm text-gray-500 mt-0.5">In-memory feed — last 100 state changes detected during this session.</p>
      </div>
      <button v-if="alerts.items.length" @click="alerts.clear()"
        class="text-xs text-red-500 hover:text-red-700 hover:underline">Clear</button>
    </div>

    <!-- Note about B25 (persistent alert history) -->
    <div class="bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-700 rounded-xl px-4 py-3 text-sm text-blue-700 dark:text-blue-300">
      💡 Client-side feed: state changes (UP/DOWN) and SLO breaches detected while the UI is open. Persistent alert history with ack/mute (B25) is coming.
    </div>

    <div v-if="alerts.items.length" class="bg-white dark:bg-gray-800 rounded-xl shadow-sm border border-gray-100 dark:border-gray-700 overflow-hidden">
      <ul class="divide-y divide-gray-100 dark:divide-gray-700">
        <li
          v-for="alert in alerts.items"
          :key="alert.id"
          :class="['px-4 py-3 flex items-start gap-3', alert.acked ? 'opacity-50' : '']"
        >
          <!-- Status indicator -->
          <span :class="['mt-0.5 w-2 h-2 rounded-full flex-shrink-0', alert.status === 'unreachable' ? 'bg-red-500' : 'bg-green-500']" />

          <div class="flex-1 min-w-0">
            <div class="flex items-center gap-2 flex-wrap">
              <NuxtLink :to="{ name: 'targets-id', params: { id: alert.target_id } }"
                class="text-sm font-medium text-gray-900 dark:text-white hover:underline">
                {{ alert.target_name }}
              </NuxtLink>
              <!-- SLO breach indicator (B17) -->
              <span v-if="isSLOAlert(alert.id)"
                class="text-xs font-semibold text-purple-600 bg-purple-50 dark:bg-purple-900/20 ring-1 ring-purple-300 rounded-full px-1.5 py-0.5">
                📊 SLO
              </span>
              <span :class="['text-xs font-semibold', statusColor(alert.status)]">
                {{ isSLOAlert(alert.id)
                   ? (alert.status === 'unreachable' ? 'BREACHED' : 'RECOVERED')
                   : (alert.status === 'unreachable' ? 'DOWN' : 'UP') }}
              </span>
              <!-- State-change alerts: show scope/classification. SLO alerts: skip them -->
              <template v-if="!isSLOAlert(alert.id)">
                <span :class="['text-xs', SCOPE_STYLE[alert.scope]?.color ?? 'text-gray-400']">
                  {{ SCOPE_STYLE[alert.scope]?.label }}
                </span>
                <span :class="['text-xs', CLASS_STYLE[alert.classification]?.color ?? 'text-gray-400']">
                  {{ CLASS_STYLE[alert.classification]?.label }}
                </span>
              </template>
            </div>
            <p v-if="alert.error_code" class="text-xs text-gray-500 font-mono mt-0.5 truncate">{{ alert.error_code }}</p>
            <div class="flex items-center gap-3 mt-1">
              <span class="text-xs text-gray-400">{{ fmtRelative(alert.timestamp) }}</span>
              <span class="text-xs text-gray-400">seq={{ alert.seq }}</span>
              <span v-if="alert.affected_apps?.length" class="text-xs text-gray-400">
                apps: {{ alert.affected_apps.join(', ') }}
              </span>
            </div>
          </div>

          <!-- Actions (B5 placeholder — disabled) -->
          <div class="flex gap-2 flex-shrink-0">
            <button class="text-xs text-gray-300 cursor-not-allowed" title="Ack (B5 — coming soon)" disabled>Ack</button>
          </div>
        </li>
      </ul>
    </div>

    <EmptyState
      v-else
      title="No alerts yet"
      description="State changes will appear here as they are detected. Keep the UI open to capture events."
      icon="🔔"
    />
  </div>
</template>
