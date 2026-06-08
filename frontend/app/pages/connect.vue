<script setup lang="ts">
definePageMeta({ layout: 'auth' })

const nodes = useNodesStore()
const { selectActiveNode, seedFromEnv } = useNodeConnection()
const { checkStatus } = useAuth()

const backendUrls = ref([''])
const loading = ref(false)
const error = ref('')

onMounted(() => {
  seedFromEnv()
  if (nodes.configured.length > 0) {
    backendUrls.value = nodes.configured.map(n => n.url)
  }
  // If already authenticated, go to dashboard
  const auth = useAuthStore()
  if (auth.isAuthenticated) {
    navigateTo({ name: 'index' })
  }
})

function addUrl() { backendUrls.value.push('') }
function removeUrl(i: number) { backendUrls.value.splice(i, 1) }

async function connect() {
  error.value = ''
  loading.value = true
  try {
    nodes.reset()
    for (const url of backendUrls.value.filter(u => u.trim())) {
      nodes.addNode(url.trim())
    }
    if (nodes.configured.length === 0) {
      error.value = 'Enter at least one backend URL.'
      return
    }

    const winner = await selectActiveNode()
    if (!winner) {
      error.value = 'No backend node reachable. Check the URL(s) and try again.'
      return
    }

    // Check auth status to determine next page
    const status = await checkStatus(winner)
    if (!status.setup_completed) {
      await navigateTo({ name: 'setup' })
    } else {
      await navigateTo({ name: 'login' })
    }
  } catch (e: any) {
    error.value = e?.data?.error ?? e?.message ?? 'Connection failed.'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <form @submit.prevent="connect" class="space-y-5">
    <div>
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white mb-1">Connect to Netwatch</h2>
      <p class="text-sm text-gray-500 dark:text-gray-400">
        Enter your netwatch backend URL(s). The fastest responding node will be used.
      </p>
    </div>

    <div class="space-y-2">
      <label class="block text-sm font-medium text-gray-700 dark:text-gray-300">Backend URL(s)</label>
      <div v-for="(_, i) in backendUrls" :key="i" class="flex gap-2">
        <input
          v-model="backendUrls[i]"
          type="url"
          placeholder="http://192.168.1.10:10240"
          class="flex-1 rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white px-3 py-2 text-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
        />
        <button
          v-if="backendUrls.length > 1"
          type="button"
          class="px-2 text-gray-400 hover:text-red-500 transition"
          @click="removeUrl(i)"
        >✕</button>
      </div>
      <button
        type="button"
        class="text-sm text-blue-600 hover:underline"
        @click="addUrl"
      >+ Add another node</button>
    </div>

    <p v-if="error" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>

    <button
      type="submit"
      :disabled="loading || backendUrls.every(u => !u.trim())"
      class="w-full py-2.5 px-4 bg-blue-600 hover:bg-blue-700 text-white font-medium rounded-lg text-sm transition disabled:opacity-60"
    >
      {{ loading ? 'Connecting…' : 'Connect' }}
    </button>
  </form>
</template>
