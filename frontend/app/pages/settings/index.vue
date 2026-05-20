<script setup lang="ts">
const ui        = useUIStore()
const colorMode = useColorMode()
const auth      = useAuthStore()
const { logout } = useAuth()

const pollingOptions = [
  { label: '1s',  ms: 1000 },
  { label: '3s',  ms: 3000 },
  { label: '5s',  ms: 5000 },
  { label: '10s', ms: 10000 },
  { label: '30s', ms: 30000 },
  { label: '60s', ms: 60000 },
]
</script>

<template>
  <div class="max-w-lg space-y-6">
    <h2 class="text-xl font-bold text-gray-900 dark:text-white">Preferences</h2>

    <!-- Polling interval -->
    <div class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm p-5">
      <h3 class="text-sm font-semibold text-gray-800 dark:text-gray-200 mb-3">Polling Interval</h3>
      <p class="text-xs text-gray-500 mb-3">How often the UI refreshes data from the backend.</p>
      <div class="flex flex-wrap gap-2">
        <button
          v-for="opt in pollingOptions"
          :key="opt.ms"
          :class="['text-sm px-3 py-1.5 rounded-lg transition', ui.pollingIntervalMs === opt.ms
            ? 'bg-blue-600 text-white'
            : 'bg-gray-100 dark:bg-gray-700 text-gray-700 dark:text-gray-300 hover:bg-gray-200']"
          @click="ui.setPollingInterval(opt.ms)"
        >{{ opt.label }}</button>
      </div>
    </div>

    <!-- Theme -->
    <div class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm p-5">
      <h3 class="text-sm font-semibold text-gray-800 dark:text-gray-200 mb-3">Appearance</h3>
      <div class="flex gap-2">
        <button
          v-for="mode in ['light', 'dark', 'system']"
          :key="mode"
          :class="['text-sm px-3 py-1.5 rounded-lg transition capitalize', colorMode.preference === mode
            ? 'bg-blue-600 text-white'
            : 'bg-gray-100 dark:bg-gray-700 text-gray-700 dark:text-gray-300 hover:bg-gray-200']"
          @click="colorMode.preference = mode"
        >{{ mode }}</button>
      </div>
    </div>

    <!-- Session -->
    <div class="bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 shadow-sm p-5">
      <h3 class="text-sm font-semibold text-gray-800 dark:text-gray-200 mb-3">Session</h3>
      <div class="flex items-center justify-between">
        <div>
          <p class="text-sm text-gray-700 dark:text-gray-300">Role: <strong>{{ auth.role }}</strong></p>
          <p class="text-xs text-gray-400 mt-0.5">Token stored in localStorage</p>
        </div>
        <button
          @click="logout"
          class="text-sm px-3 py-1.5 bg-red-50 hover:bg-red-100 text-red-600 rounded-lg transition"
        >Disconnect</button>
      </div>
    </div>
  </div>
</template>
