<script setup lang="ts">
import type { ClusterMember } from '~/types/api'

const props = defineProps<{
  member: ClusterMember
  isLocal?: boolean
}>()
</script>

<template>
  <div class="bg-white dark:bg-gray-800 rounded-xl p-4 border border-gray-100 dark:border-gray-700 shadow-sm">
    <div class="flex items-center gap-2 mb-2">
      <span class="w-2 h-2 rounded-full bg-green-400" />
      <span class="font-semibold text-sm text-gray-900 dark:text-white">{{ member.name }}</span>
      <span v-if="isLocal" class="text-xs bg-blue-100 dark:bg-blue-900/40 text-blue-700 dark:text-blue-300 rounded px-1.5 py-0.5">you</span>
    </div>
    <div class="space-y-1 text-xs text-gray-500 dark:text-gray-400">
      <div class="flex gap-1">
        <span class="w-14 flex-shrink-0">Addr</span>
        <span class="font-mono">{{ member.addr }}</span>
      </div>
      <div v-if="member.zone" class="flex gap-1">
        <span class="w-14 flex-shrink-0">Zone</span>
        <span>{{ member.zone }}</span>
      </div>
      <div v-if="member.region" class="flex gap-1">
        <span class="w-14 flex-shrink-0">Region</span>
        <span>{{ member.region }}</span>
      </div>
    </div>
  </div>
</template>
