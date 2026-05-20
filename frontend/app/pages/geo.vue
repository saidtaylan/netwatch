<script setup lang="ts">
import type { GeoLatencySnapshot } from '~/types/api'
import { fmtLatency } from '~/utils/format'

const api = useApi()
const { targetList } = useFleet()

// Load geo latency for all targets
const geoData = ref<Record<string, GeoLatencySnapshot>>({})
const loading = ref(false)

async function loadGeo() {
  loading.value = true
  const results = await Promise.allSettled(
    targetList.value.map(async t => {
      const id = t.name // or target id
      const data = await api.get<GeoLatencySnapshot>(`/geo/latency/${encodeURIComponent(id)}`)
      return { id, data }
    })
  )
  for (const r of results) {
    if (r.status === 'fulfilled') geoData.value[r.value.id] = r.value.data
  }
  loading.value = false
}

onMounted(loadGeo)
watch(() => targetList.value.length, loadGeo)
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h2 class="text-xl font-bold text-gray-900 dark:text-white">Geo Latency</h2>
      <button @click="loadGeo" :disabled="loading" class="text-xs text-blue-500 hover:underline">Refresh</button>
    </div>
    <p class="text-sm text-gray-500">Per-node probe latency across regions. Anomaly = any node >3× the minimum.</p>

    <div v-if="Object.keys(geoData).length" class="space-y-3">
      <div v-for="(geo, id) in geoData" :key="id"
        class="bg-white dark:bg-gray-800 rounded-xl border shadow-sm overflow-hidden"
        :class="geo.anomaly ? 'border-orange-300 dark:border-orange-700' : 'border-gray-100 dark:border-gray-700'"
      >
        <div class="flex items-center justify-between px-4 py-3 border-b border-gray-100 dark:border-gray-700">
          <NuxtLink :to="`/targets/${encodeURIComponent(id)}`" class="text-sm font-semibold text-gray-900 dark:text-white hover:underline">
            {{ id }}
          </NuxtLink>
          <span v-if="geo.anomaly" class="text-xs text-orange-600 bg-orange-50 dark:bg-orange-900/20 rounded-full px-2 py-0.5">⚠ Latency anomaly</span>
        </div>
        <div class="divide-y divide-gray-100 dark:divide-gray-700">
          <div v-for="n in geo.by_node" :key="n.node"
            class="flex items-center gap-3 px-4 py-2.5 text-sm"
            :class="n.anomaly ? 'bg-orange-50/50 dark:bg-orange-900/10' : ''"
          >
            <span class="font-medium text-gray-700 dark:text-gray-300 w-32 truncate">{{ n.node }}</span>
            <span v-if="n.region" class="text-xs text-gray-400 w-20 truncate">{{ n.region }}</span>
            <span v-if="n.zone"   class="text-xs text-gray-400 w-20 truncate">{{ n.zone }}</span>
            <span :class="['ml-auto font-mono text-sm', n.anomaly ? 'text-orange-600 font-bold' : 'text-gray-700 dark:text-gray-300']">
              {{ fmtLatency(n.latency) }}
            </span>
            <span v-if="n.anomaly" class="text-xs text-orange-500">⚠</span>
          </div>
        </div>
      </div>
    </div>

    <div v-else-if="loading" class="text-center py-12 text-gray-400 animate-pulse text-sm">Loading geo data…</div>
    <EmptyState v-else title="No geo data" description="Requires cluster mode with multiple nodes in different regions." icon="🌍" />
  </div>
</template>
