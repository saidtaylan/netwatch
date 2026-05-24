<script setup lang="ts">
/**
 * Cluster Sync — at-a-glance view of cluster consistency.
 *
 * After B24, the dynamic data (apps, channels, targets, silences, SLO,
 * maintenance) lives in SQLite and replicates automatically via gossip,
 * so the operator's only sync concern is:
 *   1. How many of each domain does THIS node hold? (audit visibility)
 *   2. Does config.yaml's static-bootstrap section match across nodes?
 *      (warns about peers running with different timeouts/notifications)
 *
 * The page used to be a maze of three different states per peer; now it's
 * one row per domain (counted live) and one collapsed card for config-yaml
 * hash. Sync button does the right thing for both: pushes shared config
 * fields out and re-broadcasts the hash so peers update their drift view.
 */
import { fmtRelative } from '~/utils/format'

interface DomainSummary {
  label:   string
  count:   number | null
  fetch:   () => Promise<unknown[] | Record<string, unknown>>
}

const { configSync } = useCluster()
const api = useApi()
const ui  = useUIStore()
const syncing = ref(false)

const snap = computed(() => configSync.data.value)

// ── Live DB counts for cluster-replicated domains ─────────────────────────
const counts = reactive<Record<string, number | null>>({
  apps: null, channels: null, targets: null, silences: null, slo: null, maintenance: null,
})

async function refreshCounts() {
  // Fire all 6 reads in parallel; tolerate any single failure.
  const wrap = async <T,>(key: string, fn: () => Promise<T>) => {
    try {
      const v = await fn()
      counts[key] = Array.isArray(v) ? v.length : Object.keys(v as object).length
    } catch {
      counts[key] = null
    }
  }
  await Promise.all([
    wrap('apps',         () => api.get<unknown[]>('/apps')),
    wrap('channels',     () => api.get<Record<string, unknown>>('/channels')),
    wrap('targets',      () => api.get<unknown[]>('/targets')),
    wrap('silences',     () => api.get<unknown[]>('/cluster/silences')),
    wrap('slo',          () => api.get<unknown[]>('/slo/targets')),
    wrap('maintenance',  () => api.get<unknown[]>('/cluster/maintenance')),
  ])
}

onMounted(refreshCounts)

// ── config.yaml hash sync section ────────────────────────────────────────
const selfHash   = computed(() => snap.value?.self?.config_hash ?? '')
const selfSize   = computed(() => snap.value?.self?.config_size ?? 0)
const selfLoaded = computed(() => snap.value?.self?.loaded_at ?? '')
const peers      = computed(() => snap.value?.peers ?? [])
const driftCount = computed(() => snap.value?.drift_count ?? 0)
const knownPeers = computed(() => peers.value.filter(p => !!p.config_hash))
const allKnownInSync = computed(() => driftCount.value === 0 && knownPeers.value.length === peers.value.length)

function peerLabel(hash: string): { label: string; cls: string } {
  if (!hash) return { label: 'Not reported yet', cls: 'text-gray-400' }
  if (hash === selfHash.value) return { label: 'Same', cls: 'text-green-600' }
  return { label: 'Different', cls: 'text-yellow-600' }
}

async function syncNow() {
  syncing.value = true
  try {
    // 1. Push this node's shared config to peers (in-memory apply on each).
    // 2. configSync refresh will re-pull /cluster/config so the hash table
    //    reflects the post-sync state.
    await api.post('/cluster/config/sync')
    ui.addToast('success', 'Shared config pushed to peers')
    await configSync.refresh()
    await refreshCounts()
  } catch (e: unknown) {
    const err = e as { message?: string }
    ui.addToast('error', `Sync failed: ${err?.message ?? e}`)
  } finally {
    syncing.value = false
  }
}
</script>

<template>
  <div class="max-w-3xl space-y-6">
    <div class="flex items-center justify-between">
      <h2 class="text-xl font-bold text-gray-900 dark:text-white">Cluster Sync</h2>
      <button
        @click="syncNow"
        :disabled="syncing"
        class="text-sm px-3 py-1.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg transition disabled:opacity-60"
      >
        {{ syncing ? 'Syncing…' : '↻ Sync shared config to peers' }}
      </button>
    </div>

    <!-- Domain counts: what this node currently holds in DB -->
    <section class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm">
      <header class="px-4 py-3 border-b border-gray-100 dark:border-gray-700 flex items-center justify-between">
        <div>
          <h3 class="text-sm font-semibold text-gray-800 dark:text-gray-200">Replicated Data (this node)</h3>
          <p class="text-xs text-gray-500 mt-0.5">Auto-synced across the cluster via gossip + LWW.</p>
        </div>
        <button @click="refreshCounts" class="text-xs text-blue-500 hover:underline">Refresh</button>
      </header>
      <ul class="divide-y divide-gray-100 dark:divide-gray-700">
        <li v-for="(label, key) in {
              apps:        'Apps',
              channels:    'Notification channels',
              targets:     'Targets',
              silences:    'Silences',
              slo:         'SLO targets',
              maintenance: 'Maintenance windows',
            }"
            :key="key"
            class="flex items-center justify-between px-4 py-2.5 text-sm"
        >
          <span class="text-gray-700 dark:text-gray-300">{{ label }}</span>
          <span class="font-mono text-gray-900 dark:text-white">
            <span v-if="counts[key] === null" class="text-gray-400">—</span>
            <span v-else>{{ counts[key] }}</span>
          </span>
        </li>
      </ul>
    </section>

    <!-- config.yaml hash drift (compact) -->
    <section class="bg-white dark:bg-gray-800 rounded-xl border shadow-sm"
      :class="allKnownInSync ? 'border-gray-100 dark:border-gray-700' : 'border-yellow-300 dark:border-yellow-700'"
    >
      <header class="px-4 py-3 border-b border-gray-100 dark:border-gray-700 flex items-start justify-between gap-3">
        <div>
          <h3 class="text-sm font-semibold text-gray-800 dark:text-gray-200">
            config.yaml hash
            <span class="text-xs font-normal text-gray-400">on disk, per node</span>
          </h3>
          <p class="text-xs text-gray-500 mt-0.5">
            Multi-node clusters normally differ here (each node has its own
            <code>node_name</code>, <code>bind_port</code>, <code>state_file</code>).
            "Sync shared config" pushes the common fields (timeouts, retries,
            notifications) in-memory only — disk files stay as-is.
          </p>
        </div>
        <span class="text-xs whitespace-nowrap"
          :class="allKnownInSync ? 'text-green-600' : 'text-yellow-600'"
        >
          {{ allKnownInSync ? '✓ All same' : `⚠ ${driftCount} differ` }}
        </span>
      </header>

      <div v-if="snap" class="px-4 py-3 space-y-3">
        <!-- This node -->
        <div class="flex items-center justify-between text-sm">
          <div>
            <span class="font-medium text-gray-900 dark:text-white">{{ snap.self?.node_name }}</span>
            <span class="text-xs text-gray-400 ml-2">(this node)</span>
          </div>
          <span class="font-mono text-xs text-gray-500 dark:text-gray-400">
            {{ selfHash || '—' }}
          </span>
        </div>
        <p class="text-xs text-gray-400">
          {{ selfSize > 0 ? `${selfSize} bytes` : '' }}
          <span v-if="selfLoaded && !selfLoaded.startsWith('0001')">
            · loaded {{ fmtRelative(selfLoaded) }}
          </span>
        </p>

        <!-- Peers — compact rows -->
        <div v-if="peers.length" class="border-t border-gray-100 dark:border-gray-700 pt-3 space-y-1.5">
          <div v-for="peer in peers" :key="peer.node_name"
            class="flex items-center justify-between text-sm"
          >
            <span class="text-gray-700 dark:text-gray-300">{{ peer.node_name }}</span>
            <div class="flex items-center gap-3">
              <span class="font-mono text-xs text-gray-400">{{ peer.config_hash || '—' }}</span>
              <span :class="['text-xs', peerLabel(peer.config_hash).cls]">
                {{ peerLabel(peer.config_hash).label }}
              </span>
            </div>
          </div>
        </div>
      </div>

      <div v-else-if="configSync.error.value?.message?.includes('503')"
        class="px-4 py-3 text-xs text-gray-500">
        Cluster not enabled — config sync requires <code>cluster.enabled: true</code>.
      </div>
      <div v-else-if="configSync.loading.value" class="px-4 py-6 text-center text-xs text-gray-400 animate-pulse">
        Loading…
      </div>
    </section>

    <p class="text-xs text-gray-400">
      Need to overwrite a peer's config field manually?
      <NuxtLink :to="{ name: 'config-push' }" class="text-blue-500 hover:underline">Push config →</NuxtLink>
    </p>
  </div>
</template>
