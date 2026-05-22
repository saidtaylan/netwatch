<script setup lang="ts">
import { isDown } from '~/utils/classifyState'

const { topology } = useTopology()
const { targetIndex } = useFleet()  // targetIndex is Record<id, FleetTarget>

const nodes = computed(() => {
  if (!topology.data.value?.targets) return []
  return Object.entries(topology.data.value.targets).map(([id, node]) => {
    const fleetTarget = targetIndex.value[id]   // O(1) lookup by ID
    return {
      id,
      name:            node.name || id,
      depends_on:      node.depends_on ?? [],
      reverse_deps:    node.reverse_deps ?? [],
      cascading:       node.cascading_impact ?? [],
      state:           fleetTarget?.consensus_state ?? 'unknown',
      isDown:          fleetTarget ? isDown(fleetTarget.consensus_state) : false,
    }
  })
})

// Root nodes = no dependencies
const roots = computed(() => nodes.value.filter(n => n.depends_on.length === 0))
// Dependent nodes
const dependents = computed(() => nodes.value.filter(n => n.depends_on.length > 0))
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-center justify-between">
      <div>
        <h2 class="text-xl font-bold text-gray-900 dark:text-white">Topology</h2>
        <p class="text-sm text-gray-500 mt-0.5">Target dependency graph — root cause and cascading impact.</p>
      </div>
      <span class="text-xs text-gray-400 bg-blue-50 dark:bg-blue-900/20 px-2 py-1 rounded-lg">
        Graph visualization coming in a future sprint
      </span>
    </div>

    <!-- Root nodes (no deps) -->
    <div v-if="roots.length">
      <h3 class="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">Root / Independent Targets</h3>
      <div class="space-y-2">
        <div
          v-for="n in roots"
          :key="n.id"
          class="bg-white dark:bg-gray-800 rounded-xl border shadow-sm px-4 py-3 flex items-start gap-4"
          :class="n.isDown ? 'border-red-200 dark:border-red-800' : 'border-gray-100 dark:border-gray-700'"
        >
          <div class="flex-1 min-w-0">
            <div class="flex items-center gap-2">
              <NuxtLink :to="{ name: 'targets-id', params: { id: n.id } }" class="text-sm font-semibold text-gray-900 dark:text-white hover:underline">
                {{ n.name }}
              </NuxtLink>
              <StatusBadge :state="n.state" size="sm" />
            </div>
            <div v-if="n.cascading.length" class="flex flex-wrap gap-1 mt-2">
              <span class="text-xs text-gray-400">Cascades to:</span>
              <DependencyChip v-for="dep in n.cascading" :key="dep" :id="dep" :label="dep" role="impact" />
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Dependent nodes -->
    <div v-if="dependents.length">
      <h3 class="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">Dependent Targets</h3>
      <div class="space-y-2">
        <div
          v-for="n in dependents"
          :key="n.id"
          class="bg-white dark:bg-gray-800 rounded-xl border shadow-sm px-4 py-3"
          :class="n.isDown ? 'border-red-200 dark:border-red-800' : 'border-gray-100 dark:border-gray-700'"
        >
          <div class="flex items-center gap-2 mb-2">
            <NuxtLink :to="{ name: 'targets-id', params: { id: n.id } }" class="text-sm font-semibold text-gray-900 dark:text-white hover:underline">
              {{ n.name }}
            </NuxtLink>
            <StatusBadge :state="n.state" size="sm" />
          </div>
          <div class="flex flex-wrap gap-1.5">
            <DependencyChip v-for="dep in n.depends_on" :key="dep" :id="dep" :label="dep" role="dependency" />
            <DependencyChip v-for="dep in n.cascading" :key="dep" :id="dep" :label="dep" role="impact" />
          </div>
        </div>
      </div>
    </div>

    <!-- Full table -->
    <div v-if="nodes.length" class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm overflow-hidden">
      <div class="px-4 py-3 border-b border-gray-100 dark:border-gray-700">
        <h3 class="text-sm font-semibold text-gray-700 dark:text-gray-300">All Targets</h3>
      </div>
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-gray-100 dark:border-gray-700">
            <th class="text-left text-xs font-semibold text-gray-500 uppercase px-4 py-2">Target</th>
            <th class="text-left text-xs font-semibold text-gray-500 uppercase px-4 py-2">Depends on</th>
            <th class="text-left text-xs font-semibold text-gray-500 uppercase px-4 py-2">Cascades to</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-50 dark:divide-gray-800">
          <tr v-for="n in nodes" :key="n.id">
            <td class="px-4 py-2.5">
              <div class="flex items-center gap-2">
                <NuxtLink :to="{ name: 'targets-id', params: { id: n.id } }" class="font-medium text-gray-800 dark:text-gray-200 hover:underline">{{ n.name }}</NuxtLink>
                <StatusBadge :state="n.state" size="sm" />
              </div>
            </td>
            <td class="px-4 py-2.5">
              <div class="flex flex-wrap gap-1">
                <span v-if="!n.depends_on.length" class="text-xs text-gray-300">—</span>
                <NuxtLink v-for="dep in n.depends_on" :key="dep" :to="{ name: 'targets-id', params: { id: dep } }"
                  class="text-xs text-orange-600 hover:underline">{{ dep }}</NuxtLink>
              </div>
            </td>
            <td class="px-4 py-2.5">
              <div class="flex flex-wrap gap-1">
                <span v-if="!n.cascading.length" class="text-xs text-gray-300">—</span>
                <NuxtLink v-for="dep in n.cascading" :key="dep" :to="{ name: 'targets-id', params: { id: dep } }"
                  class="text-xs text-yellow-600 hover:underline">{{ dep }}</NuxtLink>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <EmptyState
      v-if="!topology.loading.value && !nodes.length"
      title="No topology data"
      description="Add depends_on fields to your targets in config.yaml to build the dependency graph."
      icon="🗺️"
    />
  </div>
</template>
