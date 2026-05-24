<script setup lang="ts">
/**
 * Notification channels (B24.4) — storage-backed Alerter registry.
 * Three types supported: script | mail | webhook. Parameters vary by type.
 */
interface ChannelConfig {
  type: 'script' | 'mail' | 'webhook'
  parameters?: Record<string, string>
}

const api  = useApi()
const ui   = useUIStore()
const auth = useAuthStore()

// /channels returns a map (name → config). Convert to a sorted array for the UI.
const raw = usePolling<Record<string, ChannelConfig>>(() => api.get('/channels'))
const channels = computed<Array<{ name: string; cfg: ChannelConfig }>>(() =>
  Object.entries(raw.data.value ?? {})
    .map(([name, cfg]) => ({ name, cfg }))
    .sort((a, b) => a.name.localeCompare(b.name))
)

const modal = reactive({
  open: false,
  mode: 'create' as 'create' | 'edit',
  title: '',
  initialJson: '',
  editingName: '' as string,
})

function templateFor(type: 'script' | 'mail' | 'webhook'): ChannelConfig {
  if (type === 'webhook') return {
    type: 'webhook',
    parameters: {
      url: 'https://alertmanager.local/api/v2/alerts',
      format: 'alertmanager',
      timeout_sec: '10',
    },
  }
  if (type === 'mail') return {
    type: 'mail',
    parameters: {
      smtp_host: 'smtp.example.com',
      smtp_port: '587',
      from: 'alerts@example.com',
      to: 'sre@example.com',
      tls_mode: 'starttls',
    },
  }
  return {
    type: 'script',
    parameters: { script: 'channel-name' },
  }
}

function openCreate() {
  modal.mode = 'create'
  modal.title = 'New Channel'
  modal.editingName = ''
  modal.initialJson = JSON.stringify({
    name: 'my-channel',
    ...templateFor('script'),
  }, null, 2)
  modal.open = true
}

function openEdit(name: string, cfg: ChannelConfig) {
  modal.mode = 'edit'
  modal.title = `Edit ${name}`
  modal.editingName = name
  modal.initialJson = JSON.stringify(cfg, null, 2)
  modal.open = true
}

async function onSubmit(json: string) {
  try {
    const body = JSON.parse(json)
    // For create we accept {name, type, parameters} in one body; the
    // backend takes the name from the path, so we strip it before send.
    const name = modal.mode === 'edit' ? modal.editingName : body.name
    if (!name) { ui.addToast('error', 'Channel must have a name'); modal.open = false; return }
    const cfg: ChannelConfig = { type: body.type, parameters: body.parameters }
    await api.put(`/channels/${encodeURIComponent(name)}`, cfg)
    ui.addToast('success', modal.mode === 'create' ? `Channel ${name} created` : `Channel ${name} updated`)
    modal.open = false
    await raw.refresh()
  } catch (err: any) {
    ui.addToast('error', `Save failed: ${err?.data?.error ?? err?.message ?? err}`)
    modal.open = false
  }
}

async function onDelete(name: string) {
  if (!confirm(`Delete channel "${name}"? Targets referencing it will lose this alert path.`)) return
  try {
    await api.del(`/channels/${encodeURIComponent(name)}`)
    ui.addToast('success', `Channel ${name} deleted`)
    await raw.refresh()
  } catch (err: any) {
    ui.addToast('error', `Delete failed: ${err?.data?.error ?? err?.message ?? err}`)
  }
}

function paramSummary(cfg: ChannelConfig): string {
  if (!cfg.parameters) return ''
  const keys = Object.keys(cfg.parameters)
  if (keys.length <= 2) return keys.map(k => `${k}=${cfg.parameters![k]}`).join(' · ')
  return `${keys.length} parameters`
}

function badgeColor(type: string): string {
  if (type === 'webhook') return 'bg-purple-100 dark:bg-purple-900/30 text-purple-700 dark:text-purple-300'
  if (type === 'mail')    return 'bg-amber-100 dark:bg-amber-900/30 text-amber-700 dark:text-amber-300'
  return 'bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-300'
}

// ── View script source ────────────────────────────────────────────────
// Script channels point at a .sh/.ps1 file on disk (DB stores only the
// channel definition, not the source). Resolves via the server-side
// GET /channels/{name}/script endpoint so the user can audit what code
// will actually run when this alert fires.
interface ScriptSource {
  name:    string
  path:    string
  content: string
  size:    number
}
const scriptModal = reactive({
  open: false,
  loading: false,
  data: null as ScriptSource | null,
  error: '' as string,
  channel: '' as string,
})

async function openScript(name: string) {
  scriptModal.open = true
  scriptModal.loading = true
  scriptModal.error = ''
  scriptModal.data = null
  scriptModal.channel = name
  try {
    scriptModal.data = await api.get<ScriptSource>(`/channels/${encodeURIComponent(name)}/script`)
  } catch (err: any) {
    scriptModal.error = err?.data?.hint ?? err?.data?.error ?? err?.message ?? 'Failed to load script'
  } finally {
    scriptModal.loading = false
  }
}

function closeScript() {
  scriptModal.open = false
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between flex-wrap gap-3">
      <div>
        <h2 class="text-xl font-bold text-gray-900 dark:text-white">Notification Channels</h2>
        <p class="text-sm text-gray-500">{{ channels.length }} channel(s). Cluster-replicated; validated on save.</p>
      </div>
      <button
        v-if="auth.token"
        @click="openCreate"
        class="text-sm px-3 py-1.5 rounded-lg bg-blue-600 hover:bg-blue-700 text-white font-medium transition"
      >+ New Channel</button>
    </div>

    <ErrorBanner :error="raw.error.value" title="Failed to load channels" @retry="raw.refresh()" />

    <div v-if="channels.length" class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm overflow-hidden">
      <ul class="divide-y divide-gray-100 dark:divide-gray-700">
        <li v-for="c in channels" :key="c.name" class="flex items-center gap-3 px-4 py-3">
          <span :class="['text-xs uppercase font-bold rounded-full px-2 py-0.5', badgeColor(c.cfg.type)]">
            {{ c.cfg.type }}
          </span>
          <div class="flex-1 min-w-0">
            <p class="font-medium text-gray-900 dark:text-white">{{ c.name }}</p>
            <p class="text-xs text-gray-500 dark:text-gray-400 truncate">{{ paramSummary(c.cfg) }}</p>
          </div>
          <div class="flex gap-2">
            <button v-if="c.cfg.type === 'script'" @click="openScript(c.name)"
              class="text-xs text-gray-600 dark:text-gray-300 hover:underline">View script</button>
            <template v-if="auth.token">
              <button @click="openEdit(c.name, c.cfg)" class="text-xs text-blue-600 hover:underline">Edit</button>
              <button @click="onDelete(c.name)" class="text-xs text-red-600 hover:underline">Delete</button>
            </template>
          </div>
        </li>
      </ul>
    </div>

    <EmptyState
      v-else-if="!raw.loading.value"
      title="No notification channels"
      description="Click + New Channel to set up scripts, email, or webhook destinations."
      icon="📣"
    />

    <CrudJsonModal
      :open="modal.open"
      :title="modal.title"
      :initialJson="modal.initialJson"
      :submitLabel="modal.mode === 'create' ? 'Create' : 'Save'"
      hint="type: script|mail|webhook. parameters keys are type-specific (script: script; webhook: url/format/timeout_sec; mail: smtp_host/port/from/to/tls_mode)."
      @submit="onSubmit"
      @cancel="modal.open = false"
    />

    <!-- View script source modal — read-only audit view -->
    <Teleport to="body">
      <Transition name="fade">
        <div v-if="scriptModal.open" class="fixed inset-0 z-50 flex items-center justify-center">
          <div class="absolute inset-0 bg-black/40" @click="closeScript" />
          <div class="relative z-10 bg-white dark:bg-gray-800 rounded-xl shadow-2xl p-6 w-full max-w-3xl mx-4">
            <div class="flex items-start justify-between mb-3">
              <div>
                <h3 class="text-lg font-semibold text-gray-900 dark:text-white">
                  Script for <code>{{ scriptModal.channel }}</code>
                </h3>
                <p v-if="scriptModal.data" class="text-xs text-gray-500 mt-1 font-mono">
                  {{ scriptModal.data.path }} · {{ scriptModal.data.size }} bytes
                </p>
              </div>
              <button @click="closeScript" class="text-gray-400 hover:text-gray-600 text-xl leading-none">×</button>
            </div>

            <div v-if="scriptModal.loading" class="py-12 text-center text-sm text-gray-400 animate-pulse">
              Loading script…
            </div>

            <div v-else-if="scriptModal.error" class="bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-300 dark:border-yellow-700 rounded-lg p-3 text-sm text-yellow-700 dark:text-yellow-300">
              {{ scriptModal.error }}
            </div>

            <div v-else-if="scriptModal.data">
              <pre class="font-mono text-xs bg-gray-900 text-gray-100 rounded-lg p-4 overflow-x-auto max-h-96 whitespace-pre">{{ scriptModal.data.content }}</pre>
              <p class="mt-3 text-xs text-gray-500">
                <strong>How scripts work:</strong> netwatch reads the channel
                definition from the DB to learn the script <em>name</em>,
                then looks for <code>name.sh</code> (or <code>name.ps1</code>
                on Windows) on disk in this node's
                <code>notifications/</code> directory. The DB does not store
                the file content — edit the file on disk to change behavior.
              </p>
            </div>

            <div class="mt-5 flex justify-end">
              <button @click="closeScript"
                class="px-4 py-2 text-sm rounded-lg bg-gray-100 hover:bg-gray-200 dark:bg-gray-700 dark:hover:bg-gray-600 transition">
                Close
              </button>
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>
