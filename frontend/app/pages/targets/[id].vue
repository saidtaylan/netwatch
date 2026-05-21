<script setup lang="ts">
import type { ProberAssignmentSnapshot } from '~/types/api'
import { stateStyle, SCOPE_STYLE, CLASS_STYLE } from '~/utils/classifyState'
import { fmtLatency, fmtRelative } from '~/utils/format'

const route   = useRoute()
const id      = computed(() => decodeURIComponent(route.params.id as string))

const { fleet }    = useFleet()
const { topology } = useTopology()
const { geo }      = useGeoLatency(id.value)
const api          = useApi()

const target = computed(() => fleet.data.value?.targets?.[id.value] ?? null)
const topo   = computed(() => topology.data.value?.targets?.[id.value] ?? null)
const style  = computed(() => target.value ? stateStyle(target.value.consensus_state) : null)

// Prober assignment
const probers = ref<ProberAssignmentSnapshot | null>(null)
onMounted(async () => {
  try { probers.value = await api.get<ProberAssignmentSnapshot>('/cluster/probers') } catch {}
})

const targetProbers = computed(() => probers.value?.targets?.[id.value] ?? null)

// Down/up node split from by_node
const downNodes = computed(() =>
  Object.entries(target.value?.by_node ?? {})
    .filter(([, s]) => s.state === 'hard_down' || s.state === 'soft_down')
    .map(([n]) => n)
)
const upNodes = computed(() =>
  Object.entries(target.value?.by_node ?? {})
    .filter(([, s]) => s.state === 'up' || s.state === 'soft_up')
    .map(([n]) => n)
)
</script>

<template>
  <div class="space-y-5 max-w-4xl">
    <!-- Back -->
    <div class="flex items-center gap-2">
      <NuxtLink :to="{ name: 'targets' }" class="text-sm text-gray-400 hover:text-gray-600">← Targets</NuxtLink>
    </div>

    <!-- Error -->
    <ErrorBanner :error="fleet.error.value" title="Failed to load target" @retry="fleet.refresh()" />

    <!-- Skeleton -->
    <div v-if="fleet.loading.value && !target" class="space-y-4" aria-busy="true">
      <div class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 p-5">
        <div class="h-6 w-48 bg-gray-200 dark:bg-gray-700 rounded animate-pulse mb-3" />
        <div class="h-4 w-64 bg-gray-100 dark:bg-gray-800 rounded animate-pulse" />
      </div>
      <SkeletonRow :rows="4" :cols="4" />
    </div>

    <!-- Not found -->
    <EmptyState
      v-else-if="!fleet.loading.value && !target"
      :title="`Target '${id}' not found`"
      description="It may have been removed or the ID is incorrect."
      icon="🔍"
    />

    <template v-if="target && style">
      <!-- Header -->
      <div class="bg-white dark:bg-gray-800 rounded-xl border shadow-sm p-5"
        :class="style.ring.replace('ring-', 'border-')">
        <div class="flex items-start justify-between gap-4 flex-wrap">
          <div>
            <div class="flex items-center gap-2 mb-1">
              <h2 class="text-xl font-bold text-gray-900 dark:text-white">{{ target.name }}</h2>
              <StatusBadge :state="target.consensus_state" />
              <SeverityBadge v-if="target.severity" :severity="target.severity" />
            </div>
            <p class="text-sm font-mono text-gray-500">{{ target.target }}</p>
            <p class="text-xs text-gray-400 mt-0.5">type: {{ target.type }}</p>
          </div>
          <div class="text-right text-xs text-gray-400 space-y-0.5">
            <p>Scope: <span :class="SCOPE_STYLE[target.scope]?.color ?? ''">{{ SCOPE_STYLE[target.scope]?.label }}</span></p>
            <p>Classification: <span :class="CLASS_STYLE[target.classification]?.color ?? ''">{{ CLASS_STYLE[target.classification]?.label }}</span></p>
          </div>
        </div>

        <!-- Affected apps -->
        <div v-if="target.affected_apps?.length" class="flex flex-wrap gap-2 mt-3">
          <span
            v-for="app in target.affected_apps" :key="app"
            class="text-xs bg-blue-50 dark:bg-blue-900/20 text-blue-700 dark:text-blue-300 ring-1 ring-blue-200 rounded-full px-2 py-0.5"
          >
            📦 {{ app }}
          </span>
        </div>
      </div>

      <!-- Scope / Classification detail -->
      <div class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm p-5">
        <h3 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">Scope Analysis</h3>
        <ScopeClassificationCard
          :scope="target.scope"
          :classification="target.classification"
          :confidence="target.confidence"
          :down-nodes="downNodes"
          :up-nodes="upNodes"
        />
      </div>

      <!-- By-node breakdown -->
      <div v-if="Object.keys(target.by_node ?? {}).length" class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm p-5">
        <h3 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">Node Breakdown</h3>
        <ByNodeBreakdown :by-node="target.by_node" />
      </div>

      <!-- Dependencies -->
      <div v-if="topo && (topo.depends_on?.length || topo.reverse_deps?.length || topo.cascading_impact?.length)"
        class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm p-5">
        <h3 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">Dependencies</h3>
        <div class="space-y-3">
          <div v-if="target.root_cause" class="flex flex-wrap gap-2">
            <DependencyChip :id="target.root_cause" :label="target.root_cause" role="root_cause" />
          </div>
          <div v-if="topo.depends_on?.length" class="flex flex-wrap gap-2">
            <p class="text-xs text-gray-500 w-full">Depends on</p>
            <DependencyChip v-for="dep in topo.depends_on" :key="dep" :id="dep" :label="dep" role="dependency" />
          </div>
          <div v-if="topo.cascading_impact?.length" class="flex flex-wrap gap-2">
            <p class="text-xs text-gray-500 w-full">Cascading impact (if down)</p>
            <DependencyChip v-for="dep in topo.cascading_impact" :key="dep" :id="dep" :label="dep" role="impact" />
          </div>
          <div v-if="topo.reverse_deps?.length" class="flex flex-wrap gap-2">
            <p class="text-xs text-gray-500 w-full">Referenced by</p>
            <NuxtLink
              v-for="dep in topo.reverse_deps" :key="dep"
              :to="{ name: 'targets-id', params: { id: dep } }"
              class="text-xs text-gray-500 hover:text-blue-500 hover:underline"
            >{{ dep }}</NuxtLink>
          </div>
        </div>
      </div>

      <!-- Probe assignments -->
      <div v-if="targetProbers" class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm p-5">
        <h3 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">Probe Assignments</h3>
        <div class="space-y-1">
          <p class="text-xs text-gray-500">Probing nodes ({{ targetProbers.probers.length }}):</p>
          <div class="flex flex-wrap gap-1.5">
            <span
              v-for="p in targetProbers.probers" :key="p"
              :class="['text-xs rounded-full px-2.5 py-0.5 font-mono ring-1', p === targetProbers.primary
                ? 'bg-blue-50 dark:bg-blue-900/20 text-blue-700 ring-blue-300'
                : 'bg-gray-50 dark:bg-gray-700 text-gray-600 dark:text-gray-300 ring-gray-200']"
            >{{ p }}{{ p === targetProbers.primary ? ' (primary)' : '' }}</span>
          </div>
          <p v-if="targetProbers.probe_from?.length" class="text-xs text-gray-400 mt-1">
            Pinned via probe_from: {{ targetProbers.probe_from.join(', ') }}
          </p>
        </div>
      </div>

      <!-- Geo latency -->
      <div v-if="geo.data.value?.by_node?.length" class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm p-5"
        :class="geo.data.value.anomaly ? 'border-orange-300 dark:border-orange-700' : ''"
      >
        <div class="flex items-center justify-between mb-3">
          <h3 class="text-sm font-semibold text-gray-700 dark:text-gray-300">Geo Latency</h3>
          <span v-if="geo.data.value.anomaly" class="text-xs text-orange-500">⚠ Latency anomaly detected</span>
        </div>
        <div class="space-y-1">
          <div v-for="n in geo.data.value.by_node" :key="n.node"
            class="flex items-center gap-3 text-sm"
            :class="n.anomaly ? 'text-orange-600' : 'text-gray-700 dark:text-gray-300'"
          >
            <span class="w-32 truncate font-medium">{{ n.node }}</span>
            <span v-if="n.region" class="text-xs text-gray-400 w-20 truncate">{{ n.region }}</span>
            <span class="ml-auto font-mono text-xs">{{ fmtLatency(n.latency) }}</span>
            <span v-if="n.anomaly" class="text-xs">⚠</span>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>
