<script setup lang="ts">
const nodes = useNodesStore()
const { selectActiveNode } = useNodeConnection()

const newUrl   = ref('')
const newLabel = ref('')
const testing  = ref<string | null>(null)

function add() {
  if (!newUrl.value.trim()) return
  nodes.addNode(newUrl.value.trim(), newLabel.value.trim() || undefined)
  newUrl.value = ''
  newLabel.value = ''
}

async function testNode(url: string) {
  testing.value = url
  try {
    await $fetch(`${url}/health`, { timeout: 3000 })
    nodes.markHealthy(url)
  } catch {
    nodes.markUnhealthy(url)
  } finally {
    testing.value = null
  }
}

async function switchTo(url: string) {
  nodes.setActive(url)
  nodes.markHealthy(url)
}

const healthIcon: Record<string, string> = {
  healthy:   '✓',
  unhealthy: '✗',
  unknown:   '?',
}
const healthColor: Record<string, string> = {
  healthy:   'text-green-600',
  unhealthy: 'text-red-600',
  unknown:   'text-gray-400',
}
</script>

<template>
  <div class="max-w-xl space-y-5">
    <h2 class="text-xl font-bold text-gray-900 dark:text-white">Backend Nodes</h2>
    <p class="text-sm text-gray-500">Manage which netwatch backend nodes the UI connects to.</p>

    <!-- Add node -->
    <div class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm p-4">
      <h3 class="text-sm font-semibold text-gray-800 dark:text-gray-200 mb-3">Add Node</h3>
      <div class="flex gap-2">
        <input v-model="newUrl" type="url" placeholder="http://192.168.1.10:10240"
          class="flex-1 rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm px-3 py-2 focus:outline-none focus:ring-2 focus:ring-blue-500"
          @keydown.enter="add" />
        <input v-model="newLabel" type="text" placeholder="Label (opt)"
          class="w-28 rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm px-3 py-2 focus:outline-none"
          @keydown.enter="add" />
        <button @click="add" :disabled="!newUrl.trim()"
          class="px-3 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm transition disabled:opacity-60">
          Add
        </button>
      </div>
    </div>

    <!-- Configured nodes -->
    <div v-if="nodes.configured.length" class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm overflow-hidden">
      <ul class="divide-y divide-gray-100 dark:divide-gray-700">
        <li v-for="node in nodes.configured" :key="node.url" class="flex items-center gap-3 px-4 py-3">
          <!-- Active indicator -->
          <span :class="['w-2 h-2 rounded-full flex-shrink-0', nodes.active === node.url ? 'bg-blue-500' : 'bg-gray-300']" />

          <!-- URL + label -->
          <div class="flex-1 min-w-0">
            <p class="text-sm font-mono text-gray-800 dark:text-gray-200 truncate">{{ node.url }}</p>
            <p v-if="node.label" class="text-xs text-gray-400">{{ node.label }}</p>
          </div>

          <!-- Health -->
          <span :class="['text-sm font-bold', healthColor[nodes.health[node.url] ?? 'unknown']]">
            {{ testing === node.url ? '…' : healthIcon[nodes.health[node.url] ?? 'unknown'] }}
          </span>

          <!-- Actions -->
          <div class="flex gap-1.5">
            <button @click="testNode(node.url)" :disabled="testing === node.url"
              class="text-xs px-2 py-1 bg-gray-100 dark:bg-gray-700 hover:bg-gray-200 rounded transition">
              Test
            </button>
            <button v-if="nodes.active !== node.url" @click="switchTo(node.url)"
              class="text-xs px-2 py-1 bg-blue-50 dark:bg-blue-900/20 text-blue-600 hover:bg-blue-100 rounded transition">
              Use
            </button>
            <span v-else class="text-xs px-2 py-1 text-blue-600 font-semibold">Active</span>
            <button @click="nodes.removeNode(node.url)"
              class="text-xs px-2 py-1 text-red-500 hover:text-red-700 hover:bg-red-50 rounded transition">
              ✕
            </button>
          </div>
        </li>
      </ul>
    </div>

    <EmptyState v-else title="No nodes configured" description="Add a backend URL above." icon="🖥️" />
  </div>
</template>
