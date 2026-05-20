<template>
  <div class="min-h-screen flex items-center justify-center bg-gray-100 dark:bg-gray-900 px-4">
    <div class="w-full max-w-md">
      <!-- Logo -->
      <div class="text-center mb-8">
        <div class="text-4xl mb-2">📡</div>
        <h1 class="text-2xl font-bold text-gray-900 dark:text-white">netwatch</h1>
        <p class="text-sm text-gray-500 dark:text-gray-400 mt-1">Distributed Network Monitoring</p>
      </div>
      <div class="bg-white dark:bg-gray-800 rounded-2xl shadow-lg p-8">
        <slot />
      </div>
    </div>
    <Toast />
  </div>
</template>
