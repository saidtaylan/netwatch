<script setup lang="ts">
import { fmtRelative } from '~/utils/format'

const { configSync } = useCluster()
const api = useApi()
const ui  = useUIStore()
const syncing = ref(false)

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

    <div v-if="configSync.data.value" class="space-y-4">
      <!-- Local node -->
      <div class="bg-white dark:bg-gray-800 rounded-xl border shadow-sm p-4"
        :class="configSync.data.value.in_sync ? 'border-gray-100 dark:border-gray-700' : 'border-yellow-300 dark:border-yellow-700'">
        <div class="flex items-center justify-between mb-3">
          <h3 class="text-sm font-semibold text-gray-800 dark:text-gray-200">This Node</h3>
          <span :class="['text-xs font-semibold', configSync.data.value.in_sync ? 'text-green-600' : 'text-yellow-600']">
            {{ configSync.data.value.in_sync ? '✓ In sync with peers' : `⚠ ${configSync.data.value.drift_count} peer(s) differ` }}
          </span>
        </div>
        <div class="text-xs space-y-1 text-gray-600 dark:text-gray-400">
          <div class="flex gap-2">
            <span class="w-20 text-gray-400">Hash</span>
            <span class="font-mono">{{ configSync.data.value.local_hash }}</span>
          </div>
          <div class="flex gap-2">
            <span class="w-20 text-gray-400">Size</span>
            <span>{{ configSync.data.value.local_size }} bytes</span>
          </div>
          <div class="flex gap-2">
            <span class="w-20 text-gray-400">Loaded</span>
            <span>{{ fmtRelative(configSync.data.value.loaded_at) }}</span>
          </div>
        </div>
      </div>

      <!-- Peers -->
      <div v-if="Object.keys(configSync.data.value.peers).length" class="space-y-2">
        <h3 class="text-xs font-semibold text-gray-500 uppercase tracking-wide">Peers</h3>
        <div v-for="(info, peer) in configSync.data.value.peers" :key="peer"
          class="bg-white dark:bg-gray-800 rounded-xl border shadow-sm px-4 py-3"
          :class="info.hash !== configSync.data.value.local_hash ? 'border-yellow-200 dark:border-yellow-800' : 'border-gray-100 dark:border-gray-700'"
        >
          <div class="flex items-center justify-between">
            <span class="text-sm font-medium text-gray-800 dark:text-gray-200">{{ peer }}</span>
            <span :class="['text-xs', info.hash === configSync.data.value.local_hash ? 'text-green-600' : 'text-yellow-600']">
              {{ info.hash === configSync.data.value.local_hash ? '✓ Same' : '⚠ Different' }}
            </span>
          </div>
          <p class="text-xs font-mono text-gray-400 mt-1">{{ info.hash }}</p>
        </div>
      </div>
    </div>

    <div v-else-if="configSync.error.value?.message?.includes('503')"
      class="bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-xl p-4 text-sm text-gray-500">
      Cluster not enabled — config sync requires <code>cluster.enabled: true</code>.
    </div>

    <div v-else-if="configSync.loading.value" class="text-center py-12 text-gray-400 animate-pulse">Loading…</div>
  </div>
</template>
