<script setup lang="ts">
/**
 * Apps page (B24.3 CRUD-enabled).
 *
 * Apps are now stored in the storage-backed registry (cluster-replicated).
 * The page fetches /apps directly instead of deriving from fleet.affected_apps —
 * that derivation only surfaced apps whose targets were *currently being
 * probed* and known to the fleet snapshot, which silently hid newly-created
 * apps until their first probe cycle.
 */
import { isDown } from '~/utils/classifyState'

interface App {
  name: string
  owner_team?: string
  uses: string[]
  notifications?: string[]
}

const api  = useApi()
const ui   = useUIStore()
const auth = useAuthStore()
const { fleet, targetIndex } = useFleet()

// Poll /apps independently — it's a small, fast endpoint.
const appsRes = usePolling<App[]>(() => api.get<App[]>('/apps'))

const apps = computed<App[]>(() =>
  [...(appsRes.data.value ?? [])].sort((a, b) => a.name.localeCompare(b.name))
)

function appStatus(app: App): { downCount: number; targets: Array<{ id: string; name: string; state: string }> } {
  const targets = app.uses.map(id => {
    const t = targetIndex.value[id]
    return {
      id,
      name: t?.name ?? id,
      state: t?.consensus_state ?? 'unknown',
    }
  })
  return {
    targets,
    downCount: targets.filter(t => isDown(t.state)).length,
  }
}

// ── CRUD ───────────────────────────────────────────────────────────────
const modal = reactive({
  open: false,
  mode: 'create' as 'create' | 'edit',
  title: '',
  initialJson: '',
  editingName: '' as string,
})

function openCreate() {
  modal.mode = 'create'
  modal.title = 'New App'
  modal.editingName = ''
  modal.initialJson = JSON.stringify({
    name: 'my-app',
    owner_team: 'team-name',
    uses: ['target-id-1'],
    notifications: ['ops'],
  }, null, 2)
  modal.open = true
}

function openEdit(app: App) {
  modal.mode = 'edit'
  modal.title = `Edit ${app.name}`
  modal.editingName = app.name
  modal.initialJson = JSON.stringify(app, null, 2)
  modal.open = true
}

async function onSubmit(json: string) {
  try {
    const body = JSON.parse(json) as App
    const name = modal.mode === 'edit' ? modal.editingName : body.name
    if (!name) { ui.addToast('error', 'App must have a name'); modal.open = false; return }
    await api.put(`/apps/${encodeURIComponent(name)}`, body)
    ui.addToast('success', modal.mode === 'create' ? `App ${name} created` : `App ${name} updated`)
    modal.open = false
    await appsRes.refresh()
  } catch (err: any) {
    ui.addToast('error', `Save failed: ${err?.data?.error ?? err?.message ?? err}`)
    modal.open = false
  }
}

async function onDelete(app: App) {
  if (!confirm(`Delete app "${app.name}"?`)) return
  try {
    await api.del(`/apps/${encodeURIComponent(app.name)}`)
    ui.addToast('success', `App ${app.name} deleted`)
    await appsRes.refresh()
  } catch (err: any) {
    ui.addToast('error', `Delete failed: ${err?.data?.error ?? err?.message ?? err}`)
  }
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between flex-wrap gap-3">
      <div>
        <h2 class="text-xl font-bold text-gray-900 dark:text-white">Apps</h2>
        <p class="text-sm text-gray-500">{{ apps.length }} apps registered.</p>
      </div>
      <button
        v-if="auth.token"
        @click="openCreate"
        class="text-sm px-3 py-1.5 rounded-lg bg-blue-600 hover:bg-blue-700 text-white font-medium transition"
      >+ New App</button>
    </div>

    <ErrorBanner :error="appsRes.error.value" title="Failed to load apps" @retry="appsRes.refresh()" />

    <div v-if="apps.length" class="space-y-3">
      <div
        v-for="app in apps"
        :key="app.name"
        class="bg-white dark:bg-gray-800 rounded-xl border shadow-sm p-4"
        :class="appStatus(app).downCount ? 'border-red-200 dark:border-red-800' : 'border-gray-100 dark:border-gray-700'"
      >
        <div class="flex items-center gap-2 mb-2">
          <span :class="['w-2 h-2 rounded-full', appStatus(app).downCount ? 'bg-red-500' : 'bg-green-500']" />
          <h3 class="font-semibold text-gray-900 dark:text-white">{{ app.name }}</h3>
          <span v-if="app.owner_team" class="text-xs text-gray-400">· {{ app.owner_team }}</span>
          <span v-if="appStatus(app).downCount" class="text-xs text-red-600 bg-red-50 dark:bg-red-900/20 px-2 py-0.5 rounded-full">
            {{ appStatus(app).downCount }} down
          </span>
          <div v-if="auth.token" class="ml-auto flex gap-2">
            <button @click="openEdit(app)" class="text-xs text-blue-600 hover:underline">Edit</button>
            <button @click="onDelete(app)" class="text-xs text-red-600 hover:underline">Delete</button>
          </div>
        </div>
        <div class="flex flex-wrap gap-2">
          <NuxtLink
            v-for="t in appStatus(app).targets"
            :key="t.id"
            :to="{ name: 'targets-id', params: { id: t.id } }"
            class="flex items-center gap-1.5 text-xs bg-gray-50 dark:bg-gray-700 rounded-lg px-2.5 py-1 hover:bg-gray-100 dark:hover:bg-gray-600 transition"
          >
            <span :class="['w-1.5 h-1.5 rounded-full', isDown(t.state) ? 'bg-red-500' : (t.state === 'unknown' ? 'bg-gray-400' : 'bg-green-500')]" />
            {{ t.name }}
          </NuxtLink>
        </div>
        <p v-if="app.notifications?.length" class="mt-2 text-xs text-gray-500">
          notify: {{ app.notifications.join(', ') }}
        </p>
      </div>
    </div>

    <EmptyState
      v-else-if="!appsRes.loading.value"
      title="No apps configured"
      description="Click + New App or add apps: in your config.yaml seed."
      icon="📦"
    />

    <CrudJsonModal
      :open="modal.open"
      :title="modal.title"
      :initialJson="modal.initialJson"
      :submitLabel="modal.mode === 'create' ? 'Create' : 'Save'"
      hint="Fields: name, owner_team, uses (target IDs/names), notifications (channel names)"
      @submit="onSubmit"
      @cancel="modal.open = false"
    />
  </div>
</template>
