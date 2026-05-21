<script setup lang="ts">
import type { KeyringStatus } from '~/types/api'

const api = useApi()
const ui  = useUIStore()

const { data: keyring, refresh } = usePolling<KeyringStatus>(
  () => api.get<KeyringStatus>('/cluster/keyring/rotate'),
  { intervalMs: 60000 }
)

const newKey     = ref('')
const submitting = ref(false)

async function rotate(action: 'add' | 'use' | 'remove', key?: string) {
  submitting.value = true
  try {
    const resolvedKey = (key ?? newKey.value.trim()) || undefined
    await api.post('/cluster/keyring/rotate', { action, key: resolvedKey })
    ui.addToast('success', `Keyring action "${action}" applied`)
    newKey.value = ''
    await refresh()
  } catch (e: any) {
    ui.addToast('error', `Keyring action failed: ${e?.message}`)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="max-w-lg space-y-5">
    <div class="flex items-center gap-3">
      <NuxtLink :to="{ name: 'config' }" class="text-gray-400 hover:text-gray-600 text-sm">← Config</NuxtLink>
      <h2 class="text-xl font-bold text-gray-900 dark:text-white">Keyring</h2>
    </div>
    <p class="text-sm text-gray-500">Manage AES encryption keys for gossip traffic. Zero-downtime rotation: add → use → remove.</p>

    <!-- Status -->
    <div v-if="keyring" class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm p-4">
      <div class="flex gap-4 text-sm">
        <div>
          <p class="text-xs text-gray-500">Keys in ring</p>
          <p class="text-2xl font-bold text-gray-900 dark:text-white">{{ keyring.key_count }}</p>
        </div>
        <div>
          <p class="text-xs text-gray-500">Primary prefix</p>
          <p class="text-lg font-mono text-gray-700 dark:text-gray-300">{{ keyring.primary_prefix }}…</p>
        </div>
      </div>
    </div>

    <!-- Rotation steps -->
    <div class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm p-5 space-y-4">
      <h3 class="text-sm font-semibold text-gray-800 dark:text-gray-200">Key Actions</h3>

      <div class="flex gap-2">
        <input v-model="newKey" type="text" placeholder="base64-encoded AES key"
          class="flex-1 rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm px-3 py-2 focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono text-xs" />
      </div>

      <div class="flex flex-wrap gap-2">
        <button @click="rotate('add')" :disabled="!newKey.trim() || submitting"
          class="text-sm px-3 py-1.5 bg-green-600 hover:bg-green-700 text-white rounded-lg transition disabled:opacity-60">
          Add key
        </button>
        <button @click="rotate('use')" :disabled="!newKey.trim() || submitting"
          class="text-sm px-3 py-1.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg transition disabled:opacity-60">
          Make primary
        </button>
        <button @click="rotate('remove')" :disabled="!newKey.trim() || submitting"
          class="text-sm px-3 py-1.5 bg-red-600 hover:bg-red-700 text-white rounded-lg transition disabled:opacity-60">
          Remove key
        </button>
      </div>

      <div class="text-xs text-gray-400 space-y-1">
        <p>1. <strong>Add</strong> the new key — all nodes can now decrypt with either key</p>
        <p>2. <strong>Make primary</strong> — all nodes start encrypting with the new key</p>
        <p>3. <strong>Remove</strong> the old key — rotation complete, zero downtime</p>
      </div>
    </div>
  </div>
</template>
