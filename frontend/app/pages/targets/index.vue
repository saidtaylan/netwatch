<script setup lang="ts">
import type { FleetTarget, TargetType } from '~/types/api'
import { isDown } from '~/utils/classifyState'

const api  = useApi()
const ui   = useUIStore()
const auth = useAuthStore()
const { fleet, targetList } = useFleet()

// ── CRUD state ──────────────────────────────────────────────────────────
const modal = reactive({
  open: false,
  mode: 'create' as 'create' | 'edit',
  title: '',
  initialJson: '',
  editingKey: '' as string,
})

function openCreate() {
  modal.mode = 'create'
  modal.title = 'New Target'
  modal.editingKey = ''
  modal.initialJson = JSON.stringify({
    id: 'my-new-target',
    type: 'tcp',
    target: 'host:port',
    name: 'display-name',
  }, null, 2)
  modal.open = true
}

function openEdit(t: FleetTarget) {
  modal.mode = 'edit'
  modal.title = `Edit ${t.id}`
  modal.editingKey = t.id
  // Hydrate from current backend snapshot — we don't fetch /targets/{id}
  // (no GET-by-id endpoint), the FleetTarget already carries the fields
  // operators care about. Caller can refine the JSON before saving.
  modal.initialJson = JSON.stringify({
    id: t.id,
    type: t.type,
    target: t.target,
    name: t.name,
  }, null, 2)
  modal.open = true
}

async function onSubmit(json: string) {
  try {
    const body = JSON.parse(json)
    const key = modal.mode === 'edit' ? modal.editingKey : (body.id || body.name)
    if (!key) {
      ui.addToast('error', 'Target must have an id or name')
      modal.open = false
      return
    }
    await api.put(`/targets/${encodeURIComponent(key)}`, body)
    ui.addToast('success', modal.mode === 'create' ? `Target ${key} created` : `Target ${key} updated`)
    modal.open = false
    await fleet.refresh()
  } catch (err: any) {
    const msg = err?.data?.error ?? err?.message ?? 'Save failed'
    ui.addToast('error', `Save failed: ${msg}`)
    modal.open = false
  }
}

async function onDelete(t: FleetTarget) {
  if (!confirm(`Delete target "${t.id}"? Probe loop will stop on all nodes.`)) return
  try {
    await api.del(`/targets/${encodeURIComponent(t.id)}`)
    ui.addToast('success', `Target ${t.id} deleted`)
    await fleet.refresh()
  } catch (err: any) {
    const msg = err?.data?.error ?? err?.message ?? 'Delete failed'
    ui.addToast('error', `Delete failed: ${msg}`)
  }
}

// ── Filters ────────────────────────────────────────────────────────────────
const search    = ref('')
const statusFilter = ref<'all' | 'up' | 'down'>('all')
const typeFilter   = ref<TargetType | 'all'>('all')

const availableTypes = computed<string[]>(() => {
  const types = new Set<string>()
  for (const t of targetList.value) types.add(t.type)
  return ['all', ...Array.from(types).sort()]
})

const filtered = computed<FleetTarget[]>(() => {
  return targetList.value.filter(t => {
    const id = t.id
    if (search.value && !t.name.toLowerCase().includes(search.value.toLowerCase())
        && !t.target.toLowerCase().includes(search.value.toLowerCase())
        && !id.toLowerCase().includes(search.value.toLowerCase())) return false
    if (statusFilter.value === 'up'   && isDown(t.consensus_state)) return false
    if (statusFilter.value === 'down' && !isDown(t.consensus_state)) return false
    if (typeFilter.value !== 'all' && t.type !== typeFilter.value) return false
    return true
  })
})

const localCounts = computed(() => ({
  total: targetList.value.length,
  up:    targetList.value.filter(t => t.consensus_state === 'up').length,
  down:  targetList.value.filter(t => isDown(t.consensus_state)).length,
  unknown: targetList.value.filter(t => t.consensus_state === 'unknown').length,
}))
</script>

<template>
  <div class="space-y-4">
    <!-- Header -->
    <div class="flex items-center justify-between flex-wrap gap-3">
      <div>
        <h2 class="text-xl font-bold text-gray-900 dark:text-white">Targets</h2>
        <p class="text-sm text-gray-500 dark:text-gray-400 mt-0.5">
          {{ localCounts.total }} total · {{ localCounts.up }} up · {{ localCounts.down }} down
          <span v-if="localCounts.unknown">· {{ localCounts.unknown }} unknown</span>
        </p>
      </div>
      <button
        v-if="auth.token"
        @click="openCreate"
        class="text-sm px-3 py-1.5 rounded-lg bg-blue-600 hover:bg-blue-700 text-white font-medium transition"
      >+ New Target</button>
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
      <SkeletonRow v-if="fleet.loading.value && targetList.length === 0" :rows="6" :cols="4" />

      <ul v-else class="divide-y divide-gray-100 dark:divide-gray-700">
        <li v-for="t in filtered" :key="t.id" class="flex items-stretch">
          <div class="flex-1">
            <TargetRow :target="t" :id="t.id" />
          </div>
          <div v-if="auth.token" class="flex items-center gap-1 px-3 border-l border-gray-100 dark:border-gray-700">
            <button
              @click.stop="openEdit(t)"
              class="text-xs text-blue-600 hover:underline px-1.5 py-1"
              title="Edit"
            >Edit</button>
            <button
              @click.stop="onDelete(t)"
              class="text-xs text-red-600 hover:underline px-1.5 py-1"
              title="Delete"
            >Delete</button>
          </div>
        </li>
      </ul>

      <EmptyState
        v-if="!fleet.loading.value && filtered.length === 0 && targetList.length > 0"
        title="No matching targets"
        description="Try adjusting your search or filters."
        icon="🔍"
      />
      <EmptyState
        v-if="!fleet.loading.value && targetList.length === 0"
        title="No targets configured"
        description="Click + New Target to add your first probe."
        icon="📡"
      />
    </div>

    <p v-if="filtered.length && targetList.length !== filtered.length" class="text-xs text-gray-400">
      Showing {{ filtered.length }} of {{ targetList.length }} targets
    </p>

    <CrudJsonModal
      :open="modal.open"
      :title="modal.title"
      :initialJson="modal.initialJson"
      :submitLabel="modal.mode === 'create' ? 'Create' : 'Save'"
      hint="Fields: id, type (tcp|http|ping|dns|sql), target (host:port), name, notify[], depends_on[], options{}"
      @submit="onSubmit"
      @cancel="modal.open = false"
    />
  </div>
</template>
