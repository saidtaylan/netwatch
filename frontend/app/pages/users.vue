<script setup lang="ts">
/**
 * /users — Admin-only user management (B32).
 * Lists all users, allows admin to create/edit/delete.
 */
import type { UserPublic } from '~/types/api'

const api = useApi()
const auth = useAuthStore()

const users = ref<UserPublic[]>([])
const loading = ref(false)
const error = ref('')

// Edit/Create modal state
const showForm = ref(false)
const editing = ref<UserPublic | null>(null)
const form = reactive({
  id: '',
  username: '',
  password: '',
  role: 'viewer' as 'admin' | 'operator' | 'viewer',
  display_name: '',
  disabled: false,
})
const formError = ref('')
const formBusy = ref(false)

// Confirm delete modal
const deleteTarget = ref<UserPublic | null>(null)

// Toast
const toast = ref('')

function uuid(): string {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID()
  return 'u-' + Math.random().toString(36).slice(2) + '-' + Date.now().toString(36)
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    users.value = await api.get<UserPublic[]>('/users')
  } catch (e: any) {
    error.value = e?.data?.error ?? e?.message ?? 'Failed to load users.'
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  form.id = uuid()
  form.username = ''
  form.password = ''
  form.role = 'viewer'
  form.display_name = ''
  form.disabled = false
  formError.value = ''
  showForm.value = true
}

function openEdit(u: UserPublic) {
  editing.value = u
  form.id = u.id
  form.username = u.username
  form.password = ''
  form.role = u.role
  form.display_name = u.display_name ?? ''
  form.disabled = !!u.disabled
  formError.value = ''
  showForm.value = true
}

async function submit() {
  formError.value = ''
  formBusy.value = true
  try {
    const body: any = {
      username: form.username.trim(),
      role: form.role,
      display_name: form.display_name.trim() || undefined,
      disabled: form.disabled,
    }
    if (form.password) body.password = form.password
    await api.put<UserPublic>(`/users/${form.id}`, body)
    showToast(editing.value ? 'User updated.' : 'User created.')
    showForm.value = false
    await load()
  } catch (e: any) {
    formError.value = e?.data?.error ?? e?.message ?? 'Save failed.'
  } finally {
    formBusy.value = false
  }
}

async function confirmDelete() {
  if (!deleteTarget.value) return
  const id = deleteTarget.value.id
  deleteTarget.value = null
  try {
    await api.del(`/users/${id}`)
    showToast('User deleted.')
    await load()
  } catch (e: any) {
    error.value = e?.data?.error ?? e?.message ?? 'Delete failed.'
  }
}

function showToast(msg: string) {
  toast.value = msg
  setTimeout(() => { toast.value = '' }, 2500)
}

function fmtDate(iso?: string) {
  if (!iso) return '—'
  try { return new Date(iso).toLocaleString() } catch { return iso }
}

onMounted(() => {
  if (!auth.isAdmin) {
    navigateTo({ name: 'index' })
    return
  }
  load()
})
</script>

<template>
  <div class="p-6 space-y-4">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-xl font-semibold text-gray-900 dark:text-white">Users</h1>
        <p class="text-sm text-gray-500 dark:text-gray-400">Manage admin, operator and viewer accounts.</p>
      </div>
      <button
        @click="openCreate"
        class="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white text-sm rounded-lg transition"
      >+ Add User</button>
    </div>

    <p v-if="error" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>

    <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-700">
      <table class="w-full text-sm">
        <thead class="bg-gray-50 dark:bg-gray-800 text-left text-xs uppercase text-gray-500 dark:text-gray-400">
          <tr>
            <th class="px-4 py-2">Username</th>
            <th class="px-4 py-2">Role</th>
            <th class="px-4 py-2">Display Name</th>
            <th class="px-4 py-2">Created</th>
            <th class="px-4 py-2">Last Login</th>
            <th class="px-4 py-2">Status</th>
            <th class="px-4 py-2 text-right">Actions</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 dark:divide-gray-700">
          <tr v-if="loading">
            <td colspan="7" class="px-4 py-6 text-center text-gray-400">Loading…</td>
          </tr>
          <tr v-else-if="users.length === 0">
            <td colspan="7" class="px-4 py-6 text-center text-gray-400">No users.</td>
          </tr>
          <tr v-for="u in users" :key="u.id" class="hover:bg-gray-50 dark:hover:bg-gray-800/40">
            <td class="px-4 py-2 font-mono text-gray-900 dark:text-white">{{ u.username }}</td>
            <td class="px-4 py-2">
              <span :class="[
                'inline-block text-xs font-semibold px-2 py-0.5 rounded',
                u.role === 'admin' ? 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300' :
                u.role === 'operator' ? 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300' :
                'bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-300',
              ]">{{ u.role }}</span>
            </td>
            <td class="px-4 py-2 text-gray-700 dark:text-gray-300">{{ u.display_name || '—' }}</td>
            <td class="px-4 py-2 text-gray-500">{{ fmtDate(u.created_at) }}</td>
            <td class="px-4 py-2 text-gray-500">{{ fmtDate(u.last_login_at) }}</td>
            <td class="px-4 py-2">
              <span v-if="u.disabled" class="text-xs text-red-500">disabled</span>
              <span v-else class="text-xs text-green-600">active</span>
            </td>
            <td class="px-4 py-2 text-right space-x-2">
              <button
                @click="openEdit(u)"
                class="text-xs text-blue-600 hover:underline"
              >Edit</button>
              <button
                v-if="u.id !== auth.user?.id"
                @click="deleteTarget = u"
                class="text-xs text-red-500 hover:underline"
              >Delete</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Create/Edit modal -->
    <div
      v-if="showForm"
      class="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4"
      @click.self="showForm = false"
    >
      <div class="bg-white dark:bg-gray-900 rounded-lg shadow-xl p-6 w-full max-w-md space-y-4">
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
          {{ editing ? 'Edit User' : 'Add User' }}
        </h2>

        <form @submit.prevent="submit" class="space-y-3">
          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Username</label>
            <input
              v-model="form.username"
              type="text"
              required
              minlength="2"
              class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white px-3 py-2 text-sm"
            />
          </div>

          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
              Password
              <span v-if="editing" class="text-xs text-gray-400">(leave empty to keep)</span>
            </label>
            <input
              v-model="form.password"
              type="password"
              :required="!editing"
              :minlength="form.password ? 8 : 0"
              placeholder="Minimum 8 characters"
              class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white px-3 py-2 text-sm"
            />
          </div>

          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Role</label>
            <select
              v-model="form.role"
              class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white px-3 py-2 text-sm"
            >
              <option value="viewer">viewer</option>
              <option value="operator">operator</option>
              <option value="admin">admin</option>
            </select>
          </div>

          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
              Display Name <span class="text-gray-400">(optional)</span>
            </label>
            <input
              v-model="form.display_name"
              type="text"
              class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white px-3 py-2 text-sm"
            />
          </div>

          <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
            <input v-model="form.disabled" type="checkbox" />
            Disabled
          </label>

          <p v-if="formError" class="text-sm text-red-600 dark:text-red-400">{{ formError }}</p>

          <div class="flex justify-end gap-2 pt-2">
            <button
              type="button"
              @click="showForm = false"
              class="px-3 py-2 text-sm text-gray-600 dark:text-gray-300 hover:underline"
            >Cancel</button>
            <button
              type="submit"
              :disabled="formBusy"
              class="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white text-sm rounded-lg transition disabled:opacity-60"
            >{{ formBusy ? 'Saving…' : (editing ? 'Save' : 'Create') }}</button>
          </div>
        </form>
      </div>
    </div>

    <!-- Delete confirm -->
    <div
      v-if="deleteTarget"
      class="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4"
      @click.self="deleteTarget = null"
    >
      <div class="bg-white dark:bg-gray-900 rounded-lg shadow-xl p-6 w-full max-w-sm space-y-4">
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">Delete User</h2>
        <p class="text-sm text-gray-600 dark:text-gray-400">
          Are you sure you want to delete <span class="font-mono">{{ deleteTarget.username }}</span>?
          This cannot be undone.
        </p>
        <div class="flex justify-end gap-2">
          <button
            @click="deleteTarget = null"
            class="px-3 py-2 text-sm text-gray-600 dark:text-gray-300 hover:underline"
          >Cancel</button>
          <button
            @click="confirmDelete"
            class="px-4 py-2 bg-red-600 hover:bg-red-700 text-white text-sm rounded-lg transition"
          >Delete</button>
        </div>
      </div>
    </div>

    <!-- Toast -->
    <div
      v-if="toast"
      class="fixed bottom-6 right-6 bg-gray-900 text-white text-sm px-4 py-2 rounded-lg shadow-lg z-50"
    >{{ toast }}</div>
  </div>
</template>
