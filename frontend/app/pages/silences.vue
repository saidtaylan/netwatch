<script setup lang="ts">
/**
 * Silences (B24.5) — matcher-based alert mutes. Sibling of Maintenance
 * Windows; differs in that matchers can target by id/name/type with
 * optional regex (vs maintenance's fixed target_ids list).
 */
interface SilenceMatcher {
  field: 'id' | 'name' | 'type'
  value: string
  is_regex?: boolean
}
interface Silence {
  id: string
  matchers: SilenceMatcher[]
  started_at: string
  expires_at: string
  comment?: string
  created_by?: string
}

const api  = useApi()
const ui   = useUIStore()
const auth = useAuthStore()

const list = usePolling<Silence[]>(() => api.get<Silence[]>('/cluster/silences'))
const silences = computed<Silence[]>(() =>
  [...(list.data.value ?? [])].sort((a, b) => a.expires_at.localeCompare(b.expires_at))
)

const modal = reactive({
  open: false,
  title: 'New Silence',
  initialJson: '',
})

function openCreate() {
  modal.title = 'New Silence'
  modal.initialJson = JSON.stringify({
    matchers: [{ field: 'name', value: '^prod-.*', is_regex: true }],
    duration: '30m',
    comment: 'Investigating INC-1234',
    created_by: 'sre',
  }, null, 2)
  modal.open = true
}

async function onSubmit(json: string) {
  try {
    const body = JSON.parse(json)
    await api.put('/cluster/silences', body)
    ui.addToast('success', 'Silence created')
    modal.open = false
    await list.refresh()
  } catch (err: any) {
    ui.addToast('error', `Create failed: ${err?.data?.error ?? err?.message ?? err}`)
    modal.open = false
  }
}

async function onCancel(s: Silence) {
  if (!confirm(`Cancel silence "${s.id}"?`)) return
  try {
    await api.del(`/cluster/silences/${encodeURIComponent(s.id)}`)
    ui.addToast('success', 'Silence cancelled')
    await list.refresh()
  } catch (err: any) {
    ui.addToast('error', `Cancel failed: ${err?.data?.error ?? err?.message ?? err}`)
  }
}

function matcherSummary(s: Silence): string {
  return s.matchers.map(m =>
    `${m.field}${m.is_regex ? '~' : '='}${m.value}`
  ).join(' AND ')
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between flex-wrap gap-3">
      <div>
        <h2 class="text-xl font-bold text-gray-900 dark:text-white">Silences</h2>
        <p class="text-sm text-gray-500">
          Matcher-based alert mutes. {{ silences.length }} active.
        </p>
      </div>
      <button
        v-if="auth.token"
        @click="openCreate"
        class="text-sm px-3 py-1.5 rounded-lg bg-blue-600 hover:bg-blue-700 text-white font-medium transition"
      >+ New Silence</button>
    </div>

    <ErrorBanner :error="list.error.value" title="Failed to load silences" @retry="list.refresh()" />

    <div v-if="silences.length" class="space-y-2">
      <div
        v-for="s in silences"
        :key="s.id"
        class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm p-4"
      >
        <div class="flex items-start gap-3">
          <div class="flex-1 min-w-0">
            <p class="font-mono text-xs text-gray-500 dark:text-gray-400">{{ s.id }}</p>
            <p class="mt-1 text-sm text-gray-900 dark:text-white">
              <span class="font-mono text-xs bg-gray-100 dark:bg-gray-700 px-2 py-0.5 rounded">{{ matcherSummary(s) }}</span>
            </p>
            <p v-if="s.comment" class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ s.comment }}</p>
            <p class="mt-1 text-xs text-gray-400">
              expires {{ new Date(s.expires_at).toLocaleString() }}
              <span v-if="s.created_by">· by {{ s.created_by }}</span>
            </p>
          </div>
          <button
            v-if="auth.token"
            @click="onCancel(s)"
            class="text-xs text-red-600 hover:underline px-2 py-1"
          >Cancel</button>
        </div>
      </div>
    </div>

    <EmptyState
      v-else-if="!list.loading.value"
      title="No active silences"
      description="Silences mute alerts for matching targets. Use them during active incidents."
      icon="🔕"
    />

    <CrudJsonModal
      :open="modal.open"
      :title="modal.title"
      :initialJson="modal.initialJson"
      submitLabel="Create"
      hint="matchers: [{field: id|name|type, value, is_regex?}]. AND within a silence. duration: Go duration (e.g. 30m, 2h)."
      @submit="onSubmit"
      @cancel="modal.open = false"
    />
  </div>
</template>
