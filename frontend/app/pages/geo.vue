<script setup lang="ts">
/**
 * Geo Latency page — per-node probe latency for every target. Fans out
 * one /geo/latency/{targetID} call per target. We use the canonical
 * target key (id, fall back to name) so the backend can look up the
 * latency map; using display name silently returned an empty by_node
 * list when ID and name differ (e.g. "cf-dns" vs "cloudflare-dns").
 */
import type { GeoLatencySnapshot } from '~/types/api'
import { fmtLatency } from '~/utils/format'

const api = useApi()
const { targetList, targetById, isStandalone } = useFleet()

// A latency of 0 is ambiguous: it means either "this node isn't currently a
// designated prober for this target" (idle — target may be perfectly healthy)
// or "the probe ran and failed" (target is down, so there's no round-trip to
// report). Distinguish using the target's own consensus state so a down target
// doesn't misleadingly read as "not probing" everywhere.
function zeroLatencyLabel(targetId: string): string {
  const t = targetById(targetId)
  if (t && (t.consensus_state === 'hard_down' || t.consensus_state === 'soft_down')) return '— down'
  return '— not probing'
}

// One Promise.allSettled per loadGeo call; results indexed by target.id.
const geoData = ref<Record<string, GeoLatencySnapshot>>({})
const loading = ref(false)
const loaded  = ref(false)   // distinguishes "never loaded" from "loaded but empty"

// Compute min non-zero latency per target so we can highlight outliers
// in the row. Backend already exposes `anomaly` at the snapshot level,
// but we want per-row visual cues too.
function minLatency(g: GeoLatencySnapshot): number {
  let min = Infinity
  for (const n of g.by_node) {
    if (n.latency_seconds > 0 && n.latency_seconds < min) min = n.latency_seconds
  }
  return Number.isFinite(min) ? min : 0
}
function isOutlier(n: { latency_seconds: number }, min: number): boolean {
  return min > 0 && n.latency_seconds > 0 && n.latency_seconds > 3 * min
}

async function loadGeo() {
  if (targetList.value.length === 0) {
    geoData.value = {}
    loaded.value = true
    return
  }
  loading.value = true
  const next: Record<string, GeoLatencySnapshot> = {}
  const results = await Promise.allSettled(
    targetList.value.map(async t => {
      const key = t.id || t.name
      try {
        const data = await api.get<GeoLatencySnapshot>(`/geo/latency/${encodeURIComponent(key)}`)
        return { key, data }
      } catch {
        return null
      }
    })
  )
  for (const r of results) {
    if (r.status === 'fulfilled' && r.value) next[r.value.key] = r.value.data
  }
  geoData.value = next
  loading.value = false
  loaded.value = true
}

// Wait for the first non-empty target list before considering ourselves loaded.
onMounted(() => {
  if (targetList.value.length) loadGeo()
})
watch(() => targetList.value.length, (n) => {
  if (n > 0) loadGeo()
})

const entries = computed(() =>
  Object.entries(geoData.value)
    .sort(([a], [b]) => a.localeCompare(b))
)
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h2 class="text-xl font-bold text-gray-900 dark:text-white">Geo Latency</h2>
      <button @click="loadGeo" :disabled="loading"
        class="text-xs text-blue-500 hover:underline disabled:opacity-50">
        {{ loading ? 'Refreshing…' : 'Refresh' }}
      </button>
    </div>
    <p class="text-sm text-gray-500">
      Per-node probe latency for every target. Anomaly = any node > 3× the minimum.
      Latency 0 means either the node isn't a designated prober for that target ("not probing")
      or the target is currently down, so there's no round-trip to measure ("down").
    </p>

    <!-- Standalone: geo latency compares the same target across cluster nodes
         in different regions. A single standalone node has nothing to compare
         against, so this view stays empty by design — not an error. -->
    <div v-if="isStandalone" class="rounded-xl border border-blue-200 dark:border-blue-900/50 bg-blue-50 dark:bg-blue-900/20 px-4 py-6 text-sm text-blue-800 dark:text-blue-300 text-center">
      <p class="text-2xl mb-2">🌍</p>
      <p class="font-semibold mb-1">Geo latency needs a cluster</p>
      <p class="text-blue-700/80 dark:text-blue-300/80 max-w-md mx-auto">
        This view compares each target's probe latency across nodes in different
        regions. This node runs standalone (<code class="text-xs">cluster.enabled: false</code>),
        so there's only one vantage point. Enable clustering and set
        <code class="text-xs">cluster.region</code> on each node to populate this map.
      </p>
    </div>

    <div v-else-if="entries.length" class="space-y-3">
      <div v-for="[id, geo] in entries" :key="id"
        class="bg-white dark:bg-gray-800 rounded-xl border shadow-sm overflow-hidden"
        :class="geo.anomaly ? 'border-orange-300 dark:border-orange-700' : 'border-gray-100 dark:border-gray-700'"
      >
        <div class="flex items-center justify-between px-4 py-3 border-b border-gray-100 dark:border-gray-700">
          <NuxtLink :to="{ name: 'targets-id', params: { id } }"
            class="text-sm font-semibold text-gray-900 dark:text-white hover:underline">
            {{ id }}
          </NuxtLink>
          <span v-if="geo.anomaly"
            class="text-xs text-orange-600 bg-orange-50 dark:bg-orange-900/20 rounded-full px-2 py-0.5">
            ⚠ Latency anomaly
          </span>
        </div>
        <div class="divide-y divide-gray-100 dark:divide-gray-700">
          <div v-for="n in geo.by_node" :key="n.node_name"
            class="flex items-center gap-3 px-4 py-2.5 text-sm"
            :class="isOutlier(n, minLatency(geo)) ? 'bg-orange-50/50 dark:bg-orange-900/10' : ''"
          >
            <span class="font-medium text-gray-700 dark:text-gray-300 w-32 truncate">{{ n.node_name }}</span>
            <span v-if="n.region" class="text-xs text-gray-400 w-24 truncate">{{ n.region }}</span>
            <span :class="['ml-auto font-mono text-sm',
              n.latency_seconds === 0
                ? 'text-gray-400 italic'
                : (isOutlier(n, minLatency(geo)) ? 'text-orange-600 font-bold' : 'text-gray-700 dark:text-gray-300')]">
              {{ n.latency_seconds === 0 ? zeroLatencyLabel(id) : fmtLatency(n.latency_seconds) }}
            </span>
            <span v-if="isOutlier(n, minLatency(geo))" class="text-xs text-orange-500">⚠</span>
          </div>
        </div>
      </div>
    </div>

    <div v-else-if="loading && !loaded" class="text-center py-12 text-gray-400 animate-pulse text-sm">
      Loading geo data…
    </div>
    <EmptyState
      v-else
      title="No geo data yet"
      :description="targetList.length === 0
        ? 'Waiting for targets to load…'
        : 'All targets returned empty latency maps. This usually means cluster.zone is unset on most nodes, or no probes have completed yet — wait a probe interval and refresh.'"
      icon="🌍"
    />
  </div>
</template>
