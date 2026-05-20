<script setup lang="ts">
const nodes = useNodesStore()

const status = computed(() => {
  if (!nodes.activeUrl) return 'disconnected'
  const h = nodes.health[nodes.activeUrl]
  if (h === 'unhealthy') return 'failover'
  if (h === 'healthy')   return 'connected'
  return 'connecting'
})

const styles = {
  connected:    'bg-green-500',
  connecting:   'bg-yellow-400 animate-pulse',
  failover:     'bg-orange-500 animate-pulse',
  disconnected: 'bg-red-500',
}

const labels = {
  connected:    'Connected',
  connecting:   'Connecting…',
  failover:     'Failover',
  disconnected: 'No backend',
}
</script>

<template>
  <div class="flex items-center gap-1.5 text-xs text-gray-600 dark:text-gray-400">
    <span :class="['w-2 h-2 rounded-full', styles[status]]" />
    <span>{{ labels[status] }}</span>
    <span v-if="nodes.activeUrl && status === 'connected'" class="text-gray-400 truncate max-w-32">
      {{ nodes.activeUrl.replace(/^https?:\/\//, '') }}
    </span>
  </div>
</template>
