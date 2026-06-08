<script setup lang="ts">
/**
 * /logs — Node Log Viewer (B34)
 * Terminal-style flat rendering. Node list auto-discovered from cluster/state.
 */
definePageMeta({ name: 'logs' })

interface LogLine {
  time:   string
  level:  string
  msg:    string
  fields: Record<string, unknown>
}

interface LogResponse {
  node:  string
  path:  string
  lines: LogLine[]
  total: number
  note?: string
}

interface ClusterMember { name: string; addr?: string; port?: number; http_port?: string }

const api   = useApi()
const auth  = useAuthStore()
const nodes = useNodesStore()

// ── Node discovery: pull full member list from cluster/state ───────────────
interface NodeOption { label: string; url: string }
const discoveredNodes = ref<NodeOption[]>([])

// Canonical key for a backend URL (host:port). Used to dedupe nodes that
// appear under different forms — e.g. `http://localhost:10241` from manual
// configuration vs `http://127.0.0.1:10241` from cluster/state both refer to
// the same physical backend.
function urlKey(u: string): string {
  try {
    const x = new URL(u)
    let host = x.hostname.toLowerCase()
    if (host === 'localhost') host = '127.0.0.1'
    return `${host}:${x.port || (x.protocol === 'https:' ? '443' : '80')}`
  } catch {
    return u
  }
}

async function discoverNodes() {
  const seen = new Map<string, NodeOption>()

  // 1) Cluster members are the source of truth — list them first.
  try {
    const state = await api.get<{ members?: ClusterMember[] }>('/cluster/state')
    for (const m of state?.members ?? []) {
      if (!m.addr || !m.http_port) continue
      const url = `http://${m.addr}:${m.http_port}`
      const k = urlKey(url)
      if (!seen.has(k)) seen.set(k, { label: m.name, url })
    }
  } catch { /* non-fatal */ }

  // 2) Add any manually configured backends that are not already covered
  //    by cluster discovery (e.g. a standalone backend not in the cluster).
  nodes.configured.forEach((n, i) => {
    const k = urlKey(n.url)
    if (!seen.has(k)) {
      seen.set(k, { label: n.label ?? `node-${i + 1}`, url: n.url })
    }
  })

  discoveredNodes.value = Array.from(seen.values())

  // If the currently selected URL keys to a discovered node, prefer that
  // discovered URL so subsequent fetches all use the canonical form.
  const curKey = urlKey(selectedNodeUrl.value)
  const match = seen.get(curKey)
  if (match) selectedNodeUrl.value = match.url
}

const nodeOptions = computed<NodeOption[]>(() =>
  discoveredNodes.value.length ? discoveredNodes.value
  : nodes.configured.map((n, i) => ({ label: n.label ?? `node-${i + 1}`, url: n.url }))
)
const selectedNodeUrl = ref(nodes.active ?? nodes.configured[0]?.url ?? '')

// ── Filters ────────────────────────────────────────────
const levelFilter = ref('')
const searchQuery = ref('')
const limit       = ref(500)

// ── State ──────────────────────────────────────────────
const lines     = ref<LogLine[]>([])
const nodeName  = ref('')
const loading   = ref(false)
const error     = ref('')
const liveMode  = ref(false)
let   pollTimer: ReturnType<typeof setInterval> | null = null

// ── Fetch ──────────────────────────────────────────────
async function fetchLogs() {
  if (!selectedNodeUrl.value) return
  loading.value = true
  error.value   = ''
  try {
    const p = new URLSearchParams({ limit: String(limit.value) })
    if (levelFilter.value) p.set('level',  levelFilter.value)
    if (searchQuery.value) p.set('search', searchQuery.value)
    const data = await $fetch<LogResponse>(`${selectedNodeUrl.value}/logs?${p}`, {
      headers: auth.token ? { Authorization: `Bearer ${auth.token}` } : {},
    })
    lines.value    = data.lines ?? []
    nodeName.value = data.node  ?? ''
    if (data.note) error.value = data.note
  } catch (e: any) {
    error.value = e?.data?.error ?? e?.message ?? 'Failed to fetch logs'
    lines.value = []
  } finally {
    loading.value = false
    nextTick(scrollToBottom)
  }
}

function stopLive() {
  if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
}
watch(liveMode, (val) => {
  if (val) { fetchLogs(); pollTimer = setInterval(fetchLogs, 3000) }
  else     { stopLive() }
})
watch([selectedNodeUrl, levelFilter], () => { if (!liveMode.value) fetchLogs() })

// ── Terminal rendering ─────────────────────────────────
// Pre-render all lines into a single string to avoid per-row component cost.
const terminalHtml = computed(() => {
  if (!lines.value.length) return ''
  return lines.value.map(l => {
    const ts  = fmtTime(l.time)
    const lvl = (l.level ?? '?').toUpperCase().padEnd(5)
    const col = levelColor(l.level)
    const fields = l.fields && Object.keys(l.fields).length
      ? ' ' + Object.entries(l.fields).map(([k, v]) =>
          `<span class="text-purple-300">${k}</span>=<span class="text-cyan-300">${escHtml(typeof v === 'object' ? JSON.stringify(v) : String(v))}</span>`
        ).join(' ')
      : ''
    // Inline styles to guarantee colors regardless of theme/purge.
    return `<div class="log-row leading-5 px-3"><span style="color:#9ca3af">${ts} </span><span class="${col}" style="font-weight:700">${lvl}</span> <span style="color:#fff">${escHtml(l.msg)}</span>${fields}</div>`
  }).join('')
})

function levelColor(level: string): string {
  const l = level?.toUpperCase()
  if (l === 'ERROR')              return 'text-red-400'
  if (l === 'WARN' || l === 'WARNING') return 'text-yellow-400'
  if (l === 'DEBUG')              return 'text-gray-400'
  return 'text-blue-400'
}
function escHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}
function fmtTime(ts: string): string {
  if (!ts) return '        '
  try {
    const d = new Date(ts)
    return d.toLocaleTimeString('en-GB', { hour12: false }) +
           '.' + String(d.getMilliseconds()).padStart(3, '0')
  } catch { return ts.slice(11, 23) }
}

// ── Scroll ─────────────────────────────────────────────
const terminalEl = ref<HTMLElement | null>(null)
function scrollToBottom() {
  if (terminalEl.value) terminalEl.value.scrollTop = terminalEl.value.scrollHeight
}

// ── Export ─────────────────────────────────────────────
function exportJSON() {
  const blob = new Blob([JSON.stringify(lines.value, null, 2)], { type: 'application/json' })
  const a = document.createElement('a'); a.href = URL.createObjectURL(blob)
  a.download = `${nodeName.value || 'logs'}.json`; a.click()
}
function exportText() {
  const text = lines.value.map(l =>
    `${l.time} ${(l.level ?? '').padEnd(5)} ${l.msg}` +
    (l.fields ? ' ' + Object.entries(l.fields).map(([k,v]) => `${k}=${JSON.stringify(v)}`).join(' ') : '')
  ).join('\n')
  const blob = new Blob([text], { type: 'text/plain' })
  const a = document.createElement('a'); a.href = URL.createObjectURL(blob)
  a.download = `${nodeName.value || 'logs'}.txt`; a.click()
}

// ── Lifecycle ──────────────────────────────────────────
onMounted(async () => { await discoverNodes(); fetchLogs() })
onUnmounted(stopLive)
</script>

<template>
  <div class="flex flex-col h-full min-h-0 gap-3 p-4">

    <!-- Toolbar -->
    <div class="flex flex-wrap items-center gap-2">
      <h1 class="text-base font-bold text-gray-900 dark:text-white mr-1">Node Logs</h1>

      <select v-model="selectedNodeUrl"
        class="text-sm border border-gray-300 dark:border-gray-600 rounded-lg px-2 py-1.5 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-blue-500">
        <option v-for="n in nodeOptions" :key="n.url" :value="n.url">{{ n.label }}</option>
      </select>

      <select v-model="levelFilter"
        class="text-sm border border-gray-300 dark:border-gray-600 rounded-lg px-2 py-1.5 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-blue-500">
        <option value="">All levels</option>
        <option value="DEBUG">DEBUG</option>
        <option value="INFO">INFO</option>
        <option value="WARN">WARN</option>
        <option value="ERROR">ERROR</option>
      </select>

      <input v-model="searchQuery" type="text" placeholder="Search…" @keydown.enter="fetchLogs"
        class="text-sm border border-gray-300 dark:border-gray-600 rounded-lg px-3 py-1.5 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-blue-500 w-44" />

      <select v-model.number="limit"
        class="text-sm border border-gray-300 dark:border-gray-600 rounded-lg px-2 py-1.5 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-blue-500">
        <option :value="100">100</option>
        <option :value="500">500</option>
        <option :value="2000">2000</option>
      </select>

      <button @click="fetchLogs" :disabled="loading"
        class="text-sm px-3 py-1.5 rounded-lg bg-blue-600 hover:bg-blue-700 text-white transition disabled:opacity-50">
        {{ loading ? '…' : 'Fetch' }}
      </button>

      <button @click="liveMode = !liveMode"
        :class="['text-sm px-3 py-1.5 rounded-lg font-medium transition', liveMode ? 'bg-green-600 text-white' : 'bg-gray-200 dark:bg-gray-700 text-gray-700 dark:text-gray-300 hover:bg-gray-300 dark:hover:bg-gray-600']">
        {{ liveMode ? '⬤ Live' : 'Live' }}
      </button>

      <div class="ml-auto flex items-center gap-2">
        <button v-if="lines.length" @click="exportJSON"
          class="text-xs px-2 py-1.5 rounded-lg bg-gray-100 dark:bg-gray-700 hover:bg-gray-200 dark:hover:bg-gray-600 text-gray-600 dark:text-gray-300 transition">↓ JSON</button>
        <button v-if="lines.length" @click="exportText"
          class="text-xs px-2 py-1.5 rounded-lg bg-gray-100 dark:bg-gray-700 hover:bg-gray-200 dark:hover:bg-gray-600 text-gray-600 dark:text-gray-300 transition">↓ TXT</button>
        <span class="text-xs text-gray-400 font-mono tabular-nums">{{ lines.length }} lines</span>
      </div>
    </div>

    <!-- Error -->
    <div v-if="error" class="text-xs text-yellow-600 dark:text-yellow-400 px-1">⚠ {{ error }}</div>

    <!-- Terminal -->
    <div ref="terminalEl"
      class="flex-1 min-h-0 overflow-y-auto rounded-xl font-mono text-xs py-2 select-text"
      style="background-color: #000; color: #e5e7eb;"
    >
      <div v-if="!lines.length && !loading" class="px-3 py-4" style="color:#9ca3af">
        No log lines. Adjust filters or click Fetch.
      </div>
      <!-- Single innerHTML render — no per-line component overhead -->
      <div v-else v-html="terminalHtml" />
    </div>

  </div>
</template>

<style scoped>
/* v-html bypasses scoped attribute selectors, so use deep selector */
:deep(.log-row) {
  background-color: transparent;
  transition: background-color 80ms ease-out;
}
:deep(.log-row:hover) {
  background-color: #1f2937; /* gray-800 — clearly lighter than pure black */
}
</style>
