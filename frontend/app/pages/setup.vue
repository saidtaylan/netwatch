<script setup lang="ts">
definePageMeta({ layout: 'auth' })

const { setup: doSetup, checkStatus } = useAuth()
const nodes = useNodesStore()
const auth = useAuthStore()

const setupToken = ref('')
const username = ref('')
const password = ref('')
const displayName = ref('')
const loading = ref(false)
const error = ref('')

// Result after setup
const setupDone = ref(false)
const createdUser = ref<any>(null)

// Guard: setup is one-time. If already done, redirect away.
const checking = ref(true)

onMounted(async () => {
  if (nodes.configured.length === 0) {
    await navigateTo({ name: 'connect' })
    return
  }
  try {
    const status = await checkStatus(nodes.activeUrl!)
    if (status.setup_completed) {
      // Setup already done — admin user exists. Send to login (or dashboard if JWT valid).
      await navigateTo({ name: auth.isAuthenticated ? 'index' : 'login' })
      return
    }
  } catch {
    // Backend unreachable — let user retry; ErrorBanner will show
  } finally {
    checking.value = false
  }
})

async function handleSetup() {
  error.value = ''
  loading.value = true
  try {
    const resp = await doSetup(
      setupToken.value.trim(),
      username.value.trim(),
      password.value,
      displayName.value.trim() || undefined,
    )
    createdUser.value = resp.user
    setupDone.value = true
  } catch (e: any) {
    error.value = e?.data?.error ?? e?.message ?? 'Setup failed.'
  } finally {
    loading.value = false
  }
}

function goToDashboard() {
  navigateTo({ name: 'index' })
}
</script>

<template>
  <div class="space-y-5">
    <!-- Loading guard check -->
    <div v-if="checking" class="py-8 text-center text-sm text-gray-400">
      Checking setup status…
    </div>

    <!-- Before setup -->
    <template v-else-if="!setupDone">
      <div>
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white mb-1">Initial Setup</h2>
        <p class="text-sm text-gray-500 dark:text-gray-400">
          Create the first admin user. You'll need the <code class="text-blue-500">setup_token</code> from your <code>config.yaml</code>.
        </p>
      </div>

      <form @submit.prevent="handleSetup" class="space-y-4">
        <div>
          <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Setup Token</label>
          <input
            v-model="setupToken"
            type="password"
            placeholder="From config.yaml → admin.setup_token"
            required
            class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white px-3 py-2 text-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
          />
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Username</label>
          <input
            v-model="username"
            type="text"
            placeholder="admin"
            required
            class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white px-3 py-2 text-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
          />
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Password</label>
          <input
            v-model="password"
            type="password"
            placeholder="Minimum 8 characters"
            minlength="8"
            required
            class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white px-3 py-2 text-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
          />
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Display Name <span class="text-gray-400">(optional)</span></label>
          <input
            v-model="displayName"
            type="text"
            placeholder="John Doe"
            class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white px-3 py-2 text-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
          />
        </div>

        <p v-if="error" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>

        <button
          type="submit"
          :disabled="loading"
          class="w-full py-2.5 px-4 bg-blue-600 hover:bg-blue-700 text-white font-medium rounded-lg text-sm transition disabled:opacity-60"
        >
          {{ loading ? 'Creating…' : 'Create Admin User' }}
        </button>
      </form>
    </template>

    <!-- After setup: show credentials -->
    <template v-else>
      <div class="text-center">
        <div class="text-green-500 text-4xl mb-3">✓</div>
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white mb-2">Setup Complete!</h2>
        <p class="text-sm text-gray-500 dark:text-gray-400 mb-4">
          Admin user created successfully. Save these credentials:
        </p>
      </div>

      <div class="bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-lg p-4 space-y-2">
        <div class="flex justify-between items-center">
          <span class="text-sm text-gray-500 dark:text-gray-400">Username</span>
          <span class="text-sm font-mono font-semibold text-gray-900 dark:text-white">{{ createdUser?.username }}</span>
        </div>
        <div class="flex justify-between items-center">
          <span class="text-sm text-gray-500 dark:text-gray-400">Password</span>
          <span class="text-sm font-mono font-semibold text-gray-900 dark:text-white">{{ password }}</span>
        </div>
        <div class="flex justify-between items-center">
          <span class="text-sm text-gray-500 dark:text-gray-400">Role</span>
          <span class="text-sm font-mono font-semibold text-green-600 dark:text-green-400">{{ createdUser?.role }}</span>
        </div>
      </div>

      <div class="bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800 rounded-lg p-3">
        <p class="text-sm text-amber-700 dark:text-amber-300">
          ⚠️ Save these credentials now. The password cannot be recovered — only reset by an admin.
        </p>
      </div>

      <button
        @click="goToDashboard"
        class="w-full py-2.5 px-4 bg-green-600 hover:bg-green-700 text-white font-medium rounded-lg text-sm transition"
      >
        Go to Dashboard →
      </button>
    </template>
  </div>
</template>
