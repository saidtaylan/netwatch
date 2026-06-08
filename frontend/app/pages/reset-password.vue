<script setup lang="ts">
/**
 * /reset-password — Password recovery using setup_token.
 * No auth required. Uses the auth layout (no sidebar/navbar).
 */
definePageMeta({ layout: 'auth', name: 'reset-password' })

const nodes  = useNodesStore()
const router = useRouter()

const form = reactive({
  username:    '',
  setupToken:  '',
  newPassword: '',
  confirm:     '',
})
const loading  = ref(false)
const error    = ref('')
const success  = ref(false)

const backendUrl = computed(() =>
  nodes.active ?? nodes.healthyNodes[0]?.url ?? nodes.configured[0]?.url ?? ''
)

const passwordMismatch = computed(() =>
  form.confirm.length > 0 && form.newPassword !== form.confirm
)

async function submit() {
  error.value = ''
  if (!form.username.trim() || !form.setupToken.trim() || !form.newPassword.trim()) {
    error.value = 'All fields are required.'
    return
  }
  if (form.newPassword !== form.confirm) {
    error.value = 'Passwords do not match.'
    return
  }
  if (!backendUrl.value) {
    error.value = 'No backend node available. Go back to /connect first.'
    return
  }
  loading.value = true
  try {
    await $fetch(`${backendUrl.value}/auth/reset-password`, {
      method: 'POST',
      body: {
        username:     form.username.trim(),
        setup_token:  form.setupToken.trim(),
        new_password: form.newPassword,
      },
    })
    success.value = true
  } catch (err: any) {
    error.value = err?.data?.error ?? err?.message ?? 'Reset failed.'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <!-- Success state -->
  <div v-if="success" class="text-center space-y-4">
    <div class="text-4xl">✅</div>
    <p class="text-gray-900 dark:text-white font-semibold">Password reset successful</p>
    <p class="text-sm text-gray-500">You can now log in with your new password.</p>
    <button @click="router.push({ name: 'login' })"
      class="w-full py-2.5 px-4 bg-blue-600 hover:bg-blue-700 text-white font-medium rounded-lg text-sm transition">
      Go to Login
    </button>
  </div>

  <!-- Form -->
  <form v-else @submit.prevent="submit" class="space-y-5">

    <div>
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white mb-1">Reset Password</h2>
      <p class="text-sm text-gray-500 dark:text-gray-400">
        Provide your <code class="text-xs bg-gray-100 dark:bg-gray-700 px-1 rounded">setup_token</code> from <code class="text-xs bg-gray-100 dark:bg-gray-700 px-1 rounded">config.yaml</code> to reset any user's password.
      </p>
    </div>

    <div>
      <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Username</label>
      <input
        v-model="form.username"
        type="text"
        autocomplete="username"
        placeholder="admin"
        class="w-full px-3 py-2 rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
      />
    </div>

    <div>
      <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Setup Token</label>
      <input
        v-model="form.setupToken"
        type="password"
        autocomplete="off"
        placeholder="From config.yaml › admin.setup_token"
        class="w-full px-3 py-2 rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm font-mono focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
      />
      <p class="mt-1 text-xs text-gray-400">The <code>admin.setup_token</code> value from your node's <code>config.yaml</code>.</p>
    </div>

    <div>
      <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">New Password</label>
      <input
        v-model="form.newPassword"
        type="password"
        autocomplete="new-password"
        placeholder="Min 8 characters"
        class="w-full px-3 py-2 rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
      />
    </div>

    <div>
      <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Confirm Password</label>
      <input
        v-model="form.confirm"
        type="password"
        autocomplete="new-password"
        placeholder="Repeat new password"
        :class="[
          'w-full px-3 py-2 rounded-lg border text-sm focus:outline-none focus-visible:ring-2 bg-white dark:bg-gray-700 text-gray-900 dark:text-white',
          passwordMismatch
            ? 'border-red-400 focus-visible:ring-red-400'
            : 'border-gray-300 dark:border-gray-600 focus-visible:ring-blue-500',
        ]"
      />
      <p v-if="passwordMismatch" class="mt-1 text-xs text-red-500">Passwords do not match.</p>
    </div>

    <p v-if="error" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>

    <button
      type="submit"
      :disabled="loading || passwordMismatch"
      class="w-full py-2.5 px-4 bg-blue-600 hover:bg-blue-700 text-white font-medium rounded-lg text-sm transition disabled:opacity-60"
    >
      {{ loading ? 'Resetting…' : 'Reset Password' }}
    </button>

    <p class="text-center text-xs text-gray-400">
      <NuxtLink :to="{ name: 'login' }" class="text-blue-500 hover:underline">← Back to Login</NuxtLink>
    </p>
  </form>
</template>
