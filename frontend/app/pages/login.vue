<script setup lang="ts">
definePageMeta({ layout: 'auth' })

const token   = ref('')
const loading = ref(false)
const error   = ref('')
const { login } = useAuth()

async function submit() {
  error.value   = ''
  loading.value = true
  try {
    await login(token.value.trim())
    await navigateTo('/')
  } catch (e: any) {
    error.value = 'Invalid token. Please try again.'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <form @submit.prevent="submit" class="space-y-5">
    <div>
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">Enter Admin Token</h2>
      <p class="text-sm text-gray-500 dark:text-gray-400 mt-1">Your session has expired or you logged out.</p>
    </div>

    <div>
      <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Admin Token</label>
      <input
        v-model="token"
        type="password"
        placeholder="Bearer token"
        autofocus
        class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 text-gray-900 dark:text-white px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
      />
    </div>

    <p v-if="error" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>

    <button
      type="submit"
      :disabled="loading"
      class="w-full py-2.5 bg-blue-600 hover:bg-blue-700 text-white font-medium rounded-lg text-sm transition disabled:opacity-60"
    >{{ loading ? 'Verifying…' : 'Login' }}</button>

    <NuxtLink to="/setup" class="block text-center text-sm text-blue-600 hover:underline">
      Change backend connection →
    </NuxtLink>
  </form>
</template>
