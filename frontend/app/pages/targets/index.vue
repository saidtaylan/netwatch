<script setup lang="ts">
import type { FleetTarget, TargetState, TargetType } from '~/types/api'
import { isDown } from '~/utils/classifyState'

const { fleet, targetList } = useFleet()

// ── Filters ────────────────────────────────────────────────────────────────
const search    = ref('')
const statusFilter = ref<'all' | 'up' | 'down'>('all')
const typeFilter   = ref<TargetType | 'all'>('all')

const availableTypes = computed<string[]>(() => {
  const types = new Set<string>()
  for (const t of targetList.value) types.add(t.type)
  return ['all', ...Array.from(types).sort()]
})

const filtered = computed<Array<[string, FleetTarget]>>(() => {
  return Object.entries(fleet.data.value?.targets ?? {}).filter(([id, t]) => {
    if (search.value && !t.name.toLowerCase().includes(search.value.toLowerCase())
        && !t.target.toLowerCase().includes(search.value.toLowerCase())
        && !id.toLowerCase().includes(search.value.toLowerCase())) return false
    if (statusFilter.value === 'up'   && isDown(t.consensus_state)) return false
    if (statusFilter.value === 'down' && !isDown(t.consensus_state)) return false
    if (typeFilter.value !== 'all' && t.type !== typeFilter.value) return false
    return true
  })
})

const counts = computed(() => {
  const all = targetList.value
  return {
    total: all.length,
    up:    all.filter(t => t.consensus_state === 'up').length,
    down:  all.filter(t => isDown(t.consensus_state)).length,
  }
})
</script>

<template>
  <div class="space-y-4">
    <!-- Header -->
    <div class="flex items-center justify-between flex-wrap gap-3">
      <div>
        <h2 class="text-xl font-bold text-gray-900 dark:text-white">Targets</h2>
        <p class="text-sm text-gray-500 dark:text-gray-400 mt-0.5">
          {{ counts.total }} total · {{ counts.up }} up · {{ counts.down }} down
        </p>
      </div>
    </div>

    <!-- Filter bar -->
    <div class="flex flex-wrap items-center gap-2">
      <input
        v-model="search"
        type="search"
        placeholder="Search targets…"
        class="rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 text-gray-900 dark:text-white text-sm px-3 py-1.5 w-52 focus:outline-none focus:ring-2 focus:ring-blue-500"
      />

      <div class="flex gap-1">
        <button
          v-for="opt in (['all', 'up', 'down'] as const)"
          :key="opt"
          :class="['text-xs px-2.5 py-1 rounded-full transition', statusFilter === opt
            ? 'bg-blue-600 text-white'
            : 'bg-gray-100 dark:bg-gray-700 text-gray-600 dark:text-gray-300 hover:bg-gray-200 dark:hover:bg-gray-600']"
          @click="statusFilter = opt"
        >
          {{ opt.charAt(0).toUpperCase() + opt.slice(1) }}
        </button>
      </div>

      <select
        v-model="typeFilter"
        class="text-xs rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 text-gray-700 dark:text-gray-300 px-2 py-1.5 focus:outline-none"
      >
        <option v-for="t in availableTypes" :key="t" :value="t">{{ t === 'all' ? 'All types' : t.toUpperCase() }}</option>
      </select>

      <span v-if="fleet.loading.value" class="text-xs text-gray-400 animate-pulse">Refreshing…</span>
    </div>

    <!-- Error -->
    <ErrorBanner :error="fleet.error.value" title="Failed to load targets" @retry="fleet.refresh()" />

    <!-- Table -->
    <div class="bg-white dark:bg-gray-800 rounded-xl shadow-sm border border-gray-100 dark:border-gray-700 overflow-hidden">
      <!-- Table header -->
      <div class="hidden sm:grid grid-cols-[1fr_auto_auto_auto_auto] gap-4 px-4 py-2 border-b border-gray-100 dark:border-gray-700 text-xs font-semibold text-gray-500 uppercase tracking-wide">
        <span>Target</span>
        <span>Type</span>
        <span>Status</span>
        <span>Scope</span>
        <span></span>
      </div>

      <!-- Skeleton while loading -->
      <SkeletonRow v-if="fleet.loading.value && targetList.length === 0" :rows="6" :cols="4" />

      <!-- Rows -->
      <ul v-else class="divide-y divide-gray-100 dark:divide-gray-700">
        <li v-for="entry in filtered" :key="entry[0]">
          <TargetRow :target="entry[1]" :id="entry[0]" />
        </li>
      </ul>

      <!-- Empty states -->
      <EmptyState
        v-if="!fleet.loading.value && filtered.length === 0 && targetList.length > 0"
        title="No matching targets"
        description="Try adjusting your search or filters."
        icon="🔍"
      />
      <EmptyState
        v-if="!fleet.loading.value && targetList.length === 0"
        title="No targets found"
        description="No targets in the backend fleet yet."
        icon="📡"
      />
      <div v-if="fleet.loading.value && targetList.length === 0"
        class="py-12 text-center text-sm text-gray-400 animate-pulse">
        Loading targets…
      </div>
    </div>

    <!-- Result count -->
    <p v-if="filtered.length && targetList.length !== filtered.length" class="text-xs text-gray-400">
      Showing {{ filtered.length }} of {{ targetList.length }} targets
    </p>
  </div>
</template>
