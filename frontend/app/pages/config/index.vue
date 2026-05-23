<script setup lang="ts">
import type { PeerConfigInfo } from '~/types/api'
import { fmtRelative } from '~/utils/format'

const { configSync } = useCluster()
const api = useApi()
const ui  = useUIStore()
const syncing = ref(false)

const snap = computed(() => configSync.data.value)

// drift_count = peers with a KNOWN different hash. Peers without a hash
// yet (just restarted, haven't broadcast) are excluded by the backend
// from this count — they count as "Unknown", not "Different".
const inSync   = computed(() => (snap.value?.drift_count ?? 0) === 0)
const selfHash = computed(() => snap.value?.self?.config_hash ?? '')
const selfSize = computed(() => snap.value?.self?.config_size ?? 0)
const selfLoaded = computed(() => snap.value?.self?.loaded_at ?? '')
const hashValid  = computed(() => selfHash.value && selfHash.value.length > 4)  // non-empty, non-zero

const unknownCount = computed(() =>
  (snap.value?.peers ?? []).filter(p => !p.config_hash).length
)

// Per-peer status. Backend exposes `in_sync` (true when same OR not yet
// known). Combine with the hash presence to render a proper tri-state.
function peerStatus(p: PeerConfigInfo): 'same' | 'unknown' | 'different' {
  if (!p.config_hash) return 'unknown'
  return p.in_sync ? 'same' : 'different'
}

async function syncNow() {
  syncing.value = true
  try {
    await api.post('/cluster/config/sync')
    ui.addToast('success', 'Config sync triggered')
    await configSync.refresh()
  } catch (e: any) {
    ui.addToast('error', `Sync failed: ${e?.message}`)
  } finally {
    syncing.value = false
  }
}
</script>

<template>
  <div class="max-w-2xl space-y-5">
    <div class="flex items-center justify-between">
      <h2 class="text-xl font-bold text-gray-900 dark:text-white">Config Sync</h2>
      <div class="flex gap-2">
        <button @click="syncNow" :disabled="syncing"
          class="text-sm px-3 py-1.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg transition disabled:opacity-60">
          {{ syncing ? 'Syncing…' : '↻ Sync to peers' }}
        </button>
        <NuxtLink :to="{ name: 'config-push' }" class="text-sm px-3 py-1.5 bg-gray-100 dark:bg-gray-700 hover:bg-gray-200 rounded-lg transition">
          Push config →
        </NuxtLink>
      </div>
    </div>

    <div v-if="snap" class="space-y-4">
      <!-- B24 banner — explain that most drift is now expected -->
      <div class="bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-700 rounded-xl p-3 text-xs text-blue-700 dark:text-blue-300">
        <strong>About this page:</strong> Shows the SHA-256 hash of each
        node's <code>config.yaml</code> file. After B24, the dynamic data
        (apps, channels, targets, silences, SLO targets, maintenance) is
        stored in SQLite and replicated via gossip — drift in those is
        impossible. Hash differences here are normal in a typical multi-node
        cluster, because each node has its own <code>node_name</code>,
        <code>bind_port</code>, <code>state_file</code>, and ports. Use
        <strong>Sync to peers</strong> only when you want to push the
        <em>shared</em> config fields (timeouts, retries, etc.) from this
        node out — per-node bootstrap fields stay untouched.
      </div>

      <!-- This node -->
      <div class="bg-white dark:bg-gray-800 rounded-xl border shadow-sm p-4"
        :class="inSync ? 'border-gray-100 dark:border-gray-700' : 'border-yellow-300 dark:border-yellow-700'">
        <div class="flex items-center justify-between mb-3">
          <h3 class="text-sm font-semibold text-gray-800 dark:text-gray-200">
            This Node <span class="text-xs font-normal text-gray-400">{{ snap.self?.node_name }}</span>
          </h3>
          <span :class="['text-xs font-semibold', inSync ? 'text-green-600' : 'text-yellow-600']">
            <template v-if="inSync && unknownCount > 0">
              ✓ No drift detected · {{ unknownCount }} peer(s) haven't reported yet
            </template>
            <template v-else-if="inSync">
              ✓ In sync with peers
            </template>
            <template v-else>
              ⚠ {{ snap.drift_count }} peer(s) differ
            </template>
          </span>
        </div>
        <div class="text-xs space-y-1 text-gray-600 dark:text-gray-400">
          <div class="flex gap-2">
            <span class="w-20 text-gray-400">Hash</span>
            <span class="font-mono">{{ hashValid ? selfHash : '— loading…' }}</span>
          </div>
          <div class="flex gap-2">
            <span class="w-20 text-gray-400">Size</span>
            <span>{{ selfSize > 0 ? `${selfSize} bytes` : '—' }}</span>
          </div>
          <div class="flex gap-2">
            <span class="w-20 text-gray-400">Loaded</span>
            <span>{{ selfLoaded && !selfLoaded.startsWith('0001') ? fmtRelative(selfLoaded) : '—' }}</span>
          </div>
        </div>
      </div>

      <!-- Peers -->
      <div v-if="snap.peers?.length" class="space-y-2">
        <h3 class="text-xs font-semibold text-gray-500 uppercase tracking-wide">Peers</h3>
        <div v-for="peer in snap.peers" :key="peer.node_name"
          class="bg-white dark:bg-gray-800 rounded-xl border shadow-sm px-4 py-3"
          :class="{
            'border-gray-100 dark:border-gray-700': peerStatus(peer) === 'same',
            'border-gray-200 dark:border-gray-700 opacity-75': peerStatus(peer) === 'unknown',
            'border-yellow-200 dark:border-yellow-800': peerStatus(peer) === 'different',
          }"
        >
          <div class="flex items-center justify-between">
            <span class="text-sm font-medium text-gray-800 dark:text-gray-200">{{ peer.node_name }}</span>
            <span class="text-xs">
              <span v-if="peerStatus(peer) === 'same'" class="text-green-600">✓ Same</span>
              <span v-else-if="peerStatus(peer) === 'unknown'" class="text-gray-400">⏳ Not reported yet</span>
              <span v-else class="text-yellow-600">⚠ Different</span>
            </span>
          </div>
          <p class="text-xs font-mono text-gray-400 mt-1">
            <template v-if="peer.config_hash">{{ peer.config_hash }}</template>
            <template v-else><em>peer just started; waiting for its first hash broadcast</em></template>
          </p>
          <p v-if="peer.loaded_at && !peer.loaded_at.startsWith('0001')" class="text-xs text-gray-400 mt-0.5">
            {{ fmtRelative(peer.loaded_at) }}
          </p>
        </div>
      </div>

      <!-- No peers yet -->
      <div v-else class="text-xs text-gray-400 bg-gray-50 dark:bg-gray-800 rounded-xl p-3">
        No peer config info yet — peers broadcast their hash on startup. If nodes are running, wait ~30s and refresh.
      </div>
    </div>

    <div v-else-if="configSync.error.value?.message?.includes('503')"
      class="bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-xl p-4 text-sm text-gray-500">
      Cluster not enabled — config sync requires <code>cluster.enabled: true</code>.
    </div>

    <div v-else-if="configSync.loading.value" class="text-center py-12 text-gray-400 animate-pulse">Loading…</div>
  </div>
</template>
