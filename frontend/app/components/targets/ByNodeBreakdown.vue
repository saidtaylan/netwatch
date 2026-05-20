<script setup lang="ts">
import type { PeerTargetState } from '~/types/api'
import { stateStyle } from '~/utils/classifyState'
import { fmtLatency } from '~/utils/format'

defineProps<{ byNode: Record<string, PeerTargetState> }>()
</script>

<template>
  <div class="overflow-x-auto">
    <table class="w-full text-sm">
      <thead>
        <tr class="border-b border-gray-100 dark:border-gray-700">
          <th class="text-left text-xs font-semibold text-gray-500 uppercase pb-2 pr-4">Node</th>
          <th class="text-left text-xs font-semibold text-gray-500 uppercase pb-2 pr-4">State</th>
          <th class="text-left text-xs font-semibold text-gray-500 uppercase pb-2 pr-4">Seq</th>
          <th class="text-left text-xs font-semibold text-gray-500 uppercase pb-2 pr-4">Latency</th>
          <th class="text-left text-xs font-semibold text-gray-500 uppercase pb-2">Error</th>
        </tr>
      </thead>
      <tbody class="divide-y divide-gray-50 dark:divide-gray-800">
        <tr v-for="(state, node) in byNode" :key="node" class="py-2">
          <td class="py-2 pr-4 font-medium text-gray-800 dark:text-gray-200">{{ node }}</td>
          <td class="py-2 pr-4">
            <StatusBadge :state="state.state" size="sm" />
          </td>
          <td class="py-2 pr-4 text-gray-500 font-mono text-xs">{{ state.seq }}</td>
          <td class="py-2 pr-4 text-gray-500 font-mono text-xs">
            {{ state.latency ? fmtLatency(state.latency) : '—' }}
          </td>
          <td class="py-2 text-xs text-red-500 font-mono truncate max-w-64" :title="state.error_code">
            {{ state.error_code || '—' }}
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
