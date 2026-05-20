<script setup lang="ts">
import type { ConfigSyncSnapshot } from '~/types/api'
import { fmtRelative } from '~/utils/format'

defineProps<{ snapshot: ConfigSyncSnapshot | null }>()
</script>

<template>
  <div v-if="snapshot" class="bg-white dark:bg-gray-800 rounded-xl p-4 border shadow-sm"
    :class="snapshot.in_sync ? 'border-gray-100 dark:border-gray-700' : 'border-yellow-300 dark:border-yellow-700'"
  >
    <div class="flex items-center justify-between mb-2">
      <p class="text-xs text-gray-500 uppercase tracking-wide">Config Sync</p>
      <span :class="['text-xs font-semibold', snapshot.in_sync ? 'text-green-600' : 'text-yellow-600']">
        {{ snapshot.in_sync ? '✓ In sync' : `⚠ ${snapshot.drift_count} drift` }}
      </span>
    </div>
    <p class="text-xs font-mono text-gray-500 truncate">{{ snapshot.local_hash }}</p>
    <p class="text-xs text-gray-400 mt-1">Updated {{ fmtRelative(snapshot.loaded_at) }}</p>
    <div v-if="!snapshot.in_sync && Object.keys(snapshot.peers).length" class="mt-2 space-y-1">
      <p v-for="(info, peer) in snapshot.peers" :key="peer"
        :class="['text-xs', info.hash !== snapshot.local_hash ? 'text-yellow-600' : 'text-gray-400']"
      >
        {{ peer }}: {{ info.hash === snapshot.local_hash ? 'in sync' : `different (${info.hash})` }}
      </p>
    </div>
  </div>
</template>
