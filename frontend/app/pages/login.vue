<script setup lang="ts">
definePageMeta({ layout: 'auth' })

const { login: doLogin, checkStatus } = useAuth()
const nodes = useNodesStore()
const auth = useAuthStore()

const username = ref('')
const password = ref('')
const loading = ref(false)
const error = ref('')
const checking = ref(true)

onMounted(async () => {
  if (nodes.configured.length === 0) {
    await navigateTo({ name: 'connect' })
    return
  }
  // Already authenticated → dashboard
  if (auth.isAuthenticated) {
    await navigateTo({ name: 'index' })
    return
  }
  // No admin user yet → setup wizard
  try {
    const status = await checkStatus(nodes.activeUrl!)
    if (!status.setup_completed) {
      await navigateTo({ name: 'setup' })
      return
    }
  } catch {
    // Backend unreachable — let user see form + error
  } finally {
    checking.value = false
  }
})

async function handleLogin() {
  error.value = ''
  loading.value = true
  try {
    await doLogin(username.value.trim(), password.value)
    await navigateTo({ name: 'index' })
  } catch (e: any) {
    error.value = e?.data?.error ?? e?.message ?? 'Login failed.'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div v-if="checking" class="py-8 text-center text-sm text-gray-400">
    Checking authentication…
  </div>
  <form v-else @submit.prevent="handleLogin" class="space-y-5">
    <div>
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white mb-1">Sign In</h2>
      <p class="text-sm text-gray-500 dark:text-gray-400">
        Log in with your netwatch credentials.
      </p>
    </div>

    <div>
      <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Username</label>
      <input
        v-model="username"
        type="text"
        placeholder="admin"
        required
        autofocus
        class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white px-3 py-2 text-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
      />
    </div>

    <div>
      <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Password</label>
      <input
        v-model="password"
        type="password"
        placeholder="••••••••"
        required
        class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white px-3 py-2 text-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
      />
    </div>

    <p v-if="error" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>

    <button
      type="submit"
      :disabled="loading"
      class="w-full py-2.5 px-4 bg-blue-600 hover:bg-blue-700 text-white font-medium rounded-lg text-sm transition disabled:opacity-60"
    >
      {{ loading ? 'Signing in…' : 'Sign In' }}
    </button>

    <div class="flex items-center justify-between text-xs text-gray-400">
      <NuxtLink to="/connect" class="text-blue-500 hover:underline">← Change backend</NuxtLink>
      <NuxtLink :to="{ name: 'reset-password' }" class="text-gray-400 hover:text-blue-500 hover:underline transition">
        Forgot password?
      </NuxtLink>
    </div>
  </form>
</template>
