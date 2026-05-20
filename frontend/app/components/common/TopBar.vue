<script setup lang="ts">
const colorMode = useColorMode()
const auth      = useAuthStore()
const { logout } = useAuth()

const isDark  = computed(() => colorMode.value === 'dark')
function toggleDark() {
  colorMode.preference = isDark.value ? 'light' : 'dark'
}
</script>

<template>
  <header class="flex items-center gap-3 px-4 py-2.5 bg-white dark:bg-gray-800 border-b border-gray-200 dark:border-gray-700 h-12">
    <!-- Page title slot -->
    <h1 class="text-sm font-semibold text-gray-800 dark:text-gray-200 flex-1">
      <slot />
    </h1>

    <ConnectionStatus />

    <!-- Dark mode toggle -->
    <button
      class="p-1.5 rounded-md text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-white hover:bg-gray-100 dark:hover:bg-gray-700 transition"
      @click="toggleDark"
      :title="isDark ? 'Switch to light' : 'Switch to dark'"
    >
      {{ isDark ? '☀️' : '🌙' }}
    </button>

    <!-- Logout -->
    <button
      v-if="auth.isAuthenticated"
      class="text-xs text-gray-500 hover:text-red-500 transition"
      @click="logout"
    >
      Logout
    </button>
  </header>
</template>
