<script setup lang="ts">
import type { NuxtError } from '#app'

/**
 * Nuxt 4 error page — catches all unhandled errors
 * (404 not found, 500 server errors, route errors, etc).
 *
 * Auto-mounted by Nuxt when an error is thrown anywhere in the app
 * or when no route matches.
 */
const props = defineProps<{ error: NuxtError }>()

const auth = useAuthStore()

const isNotFound = computed(() => props.error?.statusCode === 404)
const isAuthError = computed(() =>
  props.error?.statusCode === 401 || props.error?.statusCode === 403
)

const title = computed(() => {
  if (isNotFound.value)  return 'Page not found'
  if (isAuthError.value) return 'Access denied'
  return 'Something went wrong'
})

const description = computed(() => {
  if (isNotFound.value)  return 'The page you are looking for does not exist.'
  if (isAuthError.value) return 'Your session may have expired. Please re-authenticate.'
  return props.error?.message || 'An unexpected error occurred.'
})

const code  = computed(() => props.error?.statusCode ?? 500)
const isDev = import.meta.dev

async function goHome() {
  // clearError navigates and clears the error state
  await clearError({ redirect: '/' })
}

async function goSetup() {
  await clearError({ redirect: '/setup' })
}
</script>

<template>
  <div class="min-h-screen flex items-center justify-center bg-gray-50 dark:bg-gray-900 px-4">
    <div class="max-w-md w-full text-center">
      <!-- Status code -->
      <p class="text-7xl font-bold text-gray-200 dark:text-gray-800 mb-2 select-none">
        {{ code }}
      </p>

      <!-- Title -->
      <h1 class="text-2xl font-bold text-gray-900 dark:text-white mb-2">
        {{ title }}
      </h1>

      <!-- Description -->
      <p class="text-sm text-gray-500 dark:text-gray-400 mb-6">
        {{ description }}
      </p>

      <!-- Actions -->
      <div class="flex flex-col gap-2 items-center">
        <button
          v-if="!isAuthError && auth.isAuthenticated"
          class="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white text-sm font-medium rounded-lg transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-400"
          @click="goHome"
        >
          ← Go to Cluster Overview
        </button>
        <button
          v-if="isAuthError || !auth.isAuthenticated"
          class="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white text-sm font-medium rounded-lg transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-400"
          @click="goSetup"
        >
          Go to Setup
        </button>
        <button
          class="px-4 py-2 text-sm text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200 transition focus-visible:outline-none focus-visible:underline"
          @click="$router.go(-1)"
        >
          Go back
        </button>
      </div>

      <!-- Stack trace in dev -->
      <details
        v-if="error?.stack && isDev"
        class="mt-8 text-left text-xs"
      >
        <summary class="cursor-pointer text-gray-400">Stack trace (dev)</summary>
        <pre class="mt-2 p-3 bg-gray-100 dark:bg-gray-800 rounded overflow-auto">{{ error.stack }}</pre>
      </details>
    </div>
  </div>
</template>
