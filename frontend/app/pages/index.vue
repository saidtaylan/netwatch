<script setup lang="ts">
import type { TargetCounts } from '~/types/api'
import { fmtRelative } from '../utils/format'

const api = useApi()
const { clusterState, configSync } = useCluster()

// Version
const version = ref<{ version: string } | null>(null)
onMounted(async () => {
  try { version.value = await api.get('/version') } catch {}
})

const { fleet, quorumHealthy, isolated, counts, downTargetIds } = useFleet()
// cluster/state.members includes all peers + self → total node count
const memberCount = computed(() => clusterState.data.value?.members?.length ?? fleet.data.value?.cluster?.alive_count ?? 0)
// Sort members deterministically (by name) so the list does not flicker
// every poll. Backend started sorting in B24+, but older nodes or
// alternate sources may return shuffled order; this is the defensive copy.
const members = computed(() =>
  [...(clusterState.data.value?.members ?? [])].sort((a, b) => a.name.localeCompare(b.name))
)
const quorum      = quorumHealthy
const drift       = computed(() => configSync.data.value?.drift_count ?? 0)
const downTargets = downTargetIds
const totalTargets = computed(() =>
  (counts.value.up ?? 0) +
  (counts.value.hard_down ?? 0) +
  (counts.value.soft_down ?? 0) +
  ((counts.value as { soft_up?: number }).soft_up ?? 0) +
  (counts.value.unknown ?? 0)
)
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <h2 class="text-xl font-bold text-gray-900 dark:text-white">Cluster Overview</h2>
      <span v-if="version" class="text-xs text-gray-400">v{{ version.version }}</span>
    </div>

    <!-- Errors -->
    <ErrorBanner :error="clusterState.error.value" title="Failed to load cluster state" @retry="clusterState.refresh()" />
    <ErrorBanner :error="fleet.error.value" title="Failed to load fleet status" @retry="fleet.refresh()" />

    <!-- Status cards -->
    <div class="grid grid-cols-2 md:grid-cols-4 gap-4">
      <!-- Skeleton while first load -->
      <template v-if="fleet.loading.value && !fleet.data.value">
        <SkeletonCard :count="4" />
      </template>

      <!-- Actual cards -->
      <template v-else>
        <!-- Cluster nodes -->
        <div class="bg-white dark:bg-gray-800 rounded-xl p-4 shadow-sm border border-gray-100 dark:border-gray-700">
          <p class="text-xs text-gray-500 uppercase tracking-wide mb-1">Cluster Nodes</p>
          <p class="text-2xl font-bold text-gray-900 dark:text-white">{{ memberCount }}</p>
          <p v-if="isolated" class="text-xs text-orange-500 mt-1">⚠ Isolated mode</p>
          <p v-else-if="quorum === true" class="text-xs text-green-500 mt-1">✓ Quorum healthy</p>
          <p v-else-if="quorum === false" class="text-xs text-red-500 mt-1">✗ Quorum lost</p>
        </div>

        <!-- Targets UP -->
        <div class="bg-white dark:bg-gray-800 rounded-xl p-4 shadow-sm border border-gray-100 dark:border-gray-700">
          <p class="text-xs text-gray-500 uppercase tracking-wide mb-1">Targets Up</p>
          <p class="text-2xl font-bold text-green-600">{{ counts.up }}</p>
          <p v-if="counts.soft_up" class="text-xs text-lime-500 mt-1">+{{ counts.soft_up }} recovering</p>
          <p class="text-xs text-gray-400 mt-1">of {{ totalTargets }} total</p>
        </div>

        <!-- Targets Down -->
        <div class="bg-white dark:bg-gray-800 rounded-xl p-4 shadow-sm border border-gray-100 dark:border-gray-700">
          <p class="text-xs text-gray-500 uppercase tracking-wide mb-1">Targets Down</p>
          <p :class="['text-2xl font-bold', counts.hard_down ? 'text-red-600' : 'text-gray-400']">{{ counts.hard_down }}</p>
          <p v-if="counts.soft_down" class="text-xs text-orange-500 mt-1">+{{ counts.soft_down }} soft down</p>
          <p v-if="counts.unknown" class="text-xs text-gray-400 mt-1">{{ counts.unknown }} unknown (no probe yet)</p>
        </div>

        <!-- Config drift -->
        <div class="bg-white dark:bg-gray-800 rounded-xl p-4 shadow-sm border border-gray-100 dark:border-gray-700">
          <p class="text-xs text-gray-500 uppercase tracking-wide mb-1">Config Drift</p>
          <p :class="['text-2xl font-bold', drift ? 'text-yellow-600' : 'text-green-600']">
            {{ drift ? `${drift} peer${drift > 1 ? 's' : ''}` : 'In sync' }}
          </p>
          <NuxtLink :to="{ name: 'config' }" class="text-xs text-blue-500 hover:underline mt-1 block">View →</NuxtLink>
        </div>
      </template>
    </div>

    <!-- Down targets list -->
    <div v-if="downTargets.length" class="bg-white dark:bg-gray-800 rounded-xl shadow-sm border border-gray-100 dark:border-gray-700 overflow-hidden">
      <div class="px-4 py-3 border-b border-gray-100 dark:border-gray-700">
        <h3 class="text-sm font-semibold text-gray-700 dark:text-gray-300">🔴 Down Targets</h3>
      </div>
      <ul class="divide-y divide-gray-100 dark:divide-gray-700">
        <li v-for="id in downTargets.slice(0, 10)" :key="id">
          <NuxtLink
            :to="{ name: 'targets-id', params: { id } }"
            class="flex items-center px-4 py-2.5 hover:bg-gray-50 dark:hover:bg-gray-700/50 transition text-sm"
          >
            <span class="flex-1 text-gray-800 dark:text-gray-200 font-mono text-xs">{{ id }}</span>
            <span class="text-gray-400 text-xs">→</span>
          </NuxtLink>
        </li>
      </ul>
      <div v-if="downTargets.length > 10" class="px-4 py-2 text-xs text-gray-400">
        +{{ downTargets.length - 10 }} more
        <NuxtLink :to="{ name: 'targets' }" class="text-blue-500 hover:underline ml-1">View all →</NuxtLink>
      </div>
    </div>

    <!-- Cluster member list -->
    <div v-if="members.length" class="bg-white dark:bg-gray-800 rounded-xl shadow-sm border border-gray-100 dark:border-gray-700">
      <div class="px-4 py-3 border-b border-gray-100 dark:border-gray-700">
        <h3 class="text-sm font-semibold text-gray-700 dark:text-gray-300">Cluster Members</h3>
      </div>
      <ul class="divide-y divide-gray-100 dark:divide-gray-700">
        <li
          v-for="m in members"
          :key="m.name"
          class="flex items-center gap-3 px-4 py-2.5 text-sm"
        >
          <span :class="['w-2 h-2 rounded-full flex-shrink-0', m.status === 'alive' ? 'bg-green-400' : 'bg-red-400']" />
          <span class="font-medium text-gray-900 dark:text-white">{{ m.name }}</span>
          <span v-if="m.self" class="text-xs bg-blue-100 dark:bg-blue-900/40 text-blue-600 dark:text-blue-300 rounded px-1.5 py-0.5">you</span>
          <span v-if="m.zone" class="text-xs text-gray-400">{{ m.zone }}</span>
          <span v-if="m.region" class="text-xs text-gray-400">{{ m.region }}</span>
          <span class="ml-auto text-xs text-gray-400 font-mono">{{ m.addr }}:{{ m.port }}</span>
        </li>
      </ul>
    </div>

    <!-- Loading / empty -->
    <EmptyState
      v-if="!clusterState.loading.value && !fleet.loading.value && memberCount === 0 && counts.up === 0 && counts.hard_down === 0"
      title="No data yet"
      description="Waiting for backend to respond. Check your connection."
      icon="📡"
    />
  </div>
</template>
