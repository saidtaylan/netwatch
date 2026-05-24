<script setup lang="ts">
/**
 * Cluster Sync — field-level effective-config diff across the cluster.
 *
 * Replaces the previous SHA-256 hash drift view. Hash compares are useless
 * here because each node legitimately has different node_name, bind_port,
 * state_file, log_path values — those are bootstrap fields, not drift.
 *
 * Approach:
 *   /cluster/sync/aggregate (this node) fans out to each peer's
 *   /cluster/sync/effective in parallel and returns one row per node
 *   with its SHARED-only effective config (timeouts/retries/notifications/
 *   keyring/peers/replication_factor). The page picks the local node as
 *   the baseline, then walks every other node and renders only the
 *   fields whose values differ — never the noise. Nodes that match the
 *   baseline collapse to a one-line "Same" row.
 */
import { fmtRelative } from '~/utils/format'

interface NodeAggregate {
  node_name:         string
  http_addr?:        string
  is_self:           boolean
  reachable:         boolean
  error?:            string
  effective_config?: Record<string, unknown>
}
interface AggregateSnapshot {
  local_node: string
  nodes:      NodeAggregate[]
}

const api = useApi()
const ui  = useUIStore()

const agg = usePolling<AggregateSnapshot>(
  () => api.get<AggregateSnapshot>('/cluster/sync/aggregate'),
  { intervalMs: 10000 }
)
const syncing = ref(false)

// Local DB counts (cluster-replicated domains — always identical across
// the cluster, so we count just this node).
const counts = reactive<Record<string, number | null>>({
  apps: null, channels: null, targets: null, silences: null, slo: null, maintenance: null,
})

async function refreshCounts() {
  const wrap = async <T,>(key: string, fn: () => Promise<T>) => {
    try {
      const v = await fn()
      counts[key] = Array.isArray(v) ? v.length : Object.keys(v as object).length
    } catch { counts[key] = null }
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

// ── Field-level diff against the local baseline ────────────────────────
//
// Baseline = the local node's effective config. For every peer, walk both
// trees recursively and collect the (path, baseline, peer) entries that
// differ. Equality uses JSON serialization — fine for the bounded set of
// fields in SharedConfig.
const baseline = computed<Record<string, unknown>>(() => {
  const self = agg.data.value?.nodes.find(n => n.is_self)
  return self?.effective_config ?? {}
})

interface FieldDiff { path: string; baseline: unknown; peer: unknown }

function diffAgainstBaseline(peer: Record<string, unknown> | undefined): FieldDiff[] {
  if (!peer) return []
  const out: FieldDiff[] = []
  walk('', baseline.value, peer, out)
  return out
}

function walk(prefix: string, a: unknown, b: unknown, out: FieldDiff[]) {
  if (JSON.stringify(a) === JSON.stringify(b)) return
  // If both sides are plain objects, recurse to surface the diff inside
  // rather than dumping the whole subtree.
  if (isPlainObject(a) && isPlainObject(b)) {
    const keys = new Set([...Object.keys(a as object), ...Object.keys(b as object)])
    for (const k of keys) {
      walk(prefix ? `${prefix}.${k}` : k, (a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k], out)
    }
    return
  }
  out.push({ path: prefix || '(root)', baseline: a, peer: b })
}

function isPlainObject(v: unknown): boolean {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function fmtValue(v: unknown): string {
  if (v === undefined) return '— (unset)'
  if (v === null) return 'null'
  if (typeof v === 'string') return v
  return JSON.stringify(v)
}

// ── Per-node UI state: which nodes are expanded ───────────────────────
const expanded = ref<Set<string>>(new Set())
function toggle(name: string) {
  if (expanded.value.has(name)) expanded.value.delete(name)
  else expanded.value.add(name)
  // Force reactivity on Set mutation
  expanded.value = new Set(expanded.value)
}

interface PeerRow {
  node: NodeAggregate
  status: 'self' | 'same' | 'different' | 'unreachable'
  diffCount: number
  diffs: FieldDiff[]
}

const rows = computed<PeerRow[]>(() => {
  const nodes = agg.data.value?.nodes ?? []
  return [...nodes]
    .sort((a, b) => a.node_name.localeCompare(b.node_name))
    .map(n => {
      if (n.is_self) return { node: n, status: 'self', diffCount: 0, diffs: [] }
      if (!n.reachable) return { node: n, status: 'unreachable', diffCount: 0, diffs: [] }
      const diffs = diffAgainstBaseline(n.effective_config)
      return {
        node: n,
        status: diffs.length === 0 ? 'same' : 'different',
        diffCount: diffs.length,
        diffs,
      }
    })
})

const differCount = computed(() => rows.value.filter(r => r.status === 'different').length)
const unreachableCount = computed(() => rows.value.filter(r => r.status === 'unreachable').length)

// ── Pagination ─────────────────────────────────────────────────────────
const pageSize = 10
const page = ref(1)
const totalPages = computed(() => Math.max(1, Math.ceil(rows.value.length / pageSize)))
const pagedRows = computed(() => rows.value.slice((page.value - 1) * pageSize, page.value * pageSize))
watch(() => rows.value.length, () => { if (page.value > totalPages.value) page.value = totalPages.value })

async function syncNow() {
  syncing.value = true
  try {
    await api.post('/cluster/config/sync')
    ui.addToast('success', 'Shared config pushed to peers')
    await Promise.all([agg.refresh(), refreshCounts()])
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

    <!-- Replicated data counts: domains that auto-sync via gossip -->
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

    <!-- Effective shared-config diff per node -->
    <section class="bg-white dark:bg-gray-800 rounded-xl border shadow-sm"
      :class="differCount > 0 ? 'border-yellow-300 dark:border-yellow-700' : 'border-gray-100 dark:border-gray-700'"
    >
      <header class="px-4 py-3 border-b border-gray-100 dark:border-gray-700 flex items-start justify-between gap-3">
        <div>
          <h3 class="text-sm font-semibold text-gray-800 dark:text-gray-200">Effective Shared Config</h3>
          <p class="text-xs text-gray-500 mt-0.5">
            Field-by-field comparison. Per-node fields (<code>node_name</code>,
            <code>bind_port</code>, <code>state_file</code>, ports) are
            excluded — they are bootstrap data, not drift. Baseline is this
            node ({{ agg.data.value?.local_node ?? '—' }}).
          </p>
        </div>
        <span class="text-xs whitespace-nowrap"
          :class="differCount > 0 ? 'text-yellow-600' : 'text-green-600'"
        >
          {{ differCount > 0 ? `⚠ ${differCount} differ` : '✓ All same' }}
          <span v-if="unreachableCount > 0" class="text-gray-400 ml-2">· {{ unreachableCount }} unreachable</span>
        </span>
      </header>

      <ErrorBanner v-if="agg.error.value" :error="agg.error.value" title="Failed to aggregate cluster config" @retry="agg.refresh()" />

      <ul v-if="rows.length" class="divide-y divide-gray-100 dark:divide-gray-700">
        <li v-for="row in pagedRows" :key="row.node.node_name">
          <!-- Row header — always shown -->
          <div
            :class="['flex items-center gap-3 px-4 py-2.5 text-sm',
              row.status === 'different' ? 'cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-700/30' : '']"
            @click="row.status === 'different' && toggle(row.node.node_name)"
          >
            <span class="font-medium text-gray-900 dark:text-white">{{ row.node.node_name }}</span>
            <span v-if="row.status === 'self'" class="text-xs text-gray-400">(this node · baseline)</span>
            <span v-if="row.node.http_addr" class="font-mono text-xs text-gray-400">{{ row.node.http_addr }}</span>
            <span class="ml-auto text-xs">
              <template v-if="row.status === 'self'"><span class="text-blue-600">Baseline</span></template>
              <template v-else-if="row.status === 'same'"><span class="text-green-600">✓ Same</span></template>
              <template v-else-if="row.status === 'unreachable'">
                <span class="text-gray-400">unreachable</span>
              </template>
              <template v-else>
                <span class="text-yellow-600">⚠ {{ row.diffCount }} field(s) differ</span>
                <span class="ml-2 text-gray-400">{{ expanded.has(row.node.node_name) ? '▾' : '▸' }}</span>
              </template>
            </span>
          </div>

          <!-- Diff body — only when expanded -->
          <div v-if="row.status === 'different' && expanded.has(row.node.node_name)"
            class="px-4 pb-3 -mt-1 text-xs"
          >
            <table class="w-full bg-gray-50 dark:bg-gray-900 rounded-lg">
              <thead>
                <tr class="text-left text-gray-500 dark:text-gray-400">
                  <th class="px-3 py-1.5 font-semibold">Field</th>
                  <th class="px-3 py-1.5 font-semibold">Baseline ({{ agg.data.value?.local_node }})</th>
                  <th class="px-3 py-1.5 font-semibold">{{ row.node.node_name }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-200 dark:divide-gray-700">
                <tr v-for="d in row.diffs" :key="d.path">
                  <td class="px-3 py-1.5 font-mono text-gray-700 dark:text-gray-300">{{ d.path }}</td>
                  <td class="px-3 py-1.5 font-mono text-gray-600 dark:text-gray-400">{{ fmtValue(d.baseline) }}</td>
                  <td class="px-3 py-1.5 font-mono text-yellow-600 dark:text-yellow-400">{{ fmtValue(d.peer) }}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <!-- Unreachable detail (compact) -->
          <div v-if="row.status === 'unreachable' && row.node.error"
            class="px-4 pb-2 text-xs text-gray-500"
          >{{ row.node.error }}</div>
        </li>
      </ul>

      <!-- Pagination -->
      <div v-if="totalPages > 1" class="px-4 py-2.5 border-t border-gray-100 dark:border-gray-700 flex items-center justify-between text-xs">
        <span class="text-gray-500">Page {{ page }} / {{ totalPages }}</span>
        <div class="flex gap-1">
          <button :disabled="page === 1" @click="page--"
            class="px-2 py-1 rounded bg-gray-100 dark:bg-gray-700 text-gray-700 dark:text-gray-300 hover:bg-gray-200 disabled:opacity-40">‹</button>
          <button :disabled="page === totalPages" @click="page++"
            class="px-2 py-1 rounded bg-gray-100 dark:bg-gray-700 text-gray-700 dark:text-gray-300 hover:bg-gray-200 disabled:opacity-40">›</button>
        </div>
      </div>

      <div v-else-if="agg.loading.value && !rows.length" class="px-4 py-6 text-center text-xs text-gray-400 animate-pulse">
        Loading cluster state…
      </div>
    </section>

    <p class="text-xs text-gray-400">
      Need to overwrite a peer's config field manually?
      <NuxtLink :to="{ name: 'config-push' }" class="text-blue-500 hover:underline">Push config →</NuxtLink>
    </p>
  </div>
</template>
