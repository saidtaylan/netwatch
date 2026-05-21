<script setup lang="ts">
import { isDown } from '~/utils/classifyState'

const { fleet, targetList } = useFleet()

const apps = computed(() => {
  const map: Record<string, { targets: Array<{ id: string; name: string; state: string }> }> = {}
  if (!fleet.data.value?.targets) return map
  for (const [id, t] of Object.entries(fleet.data.value.targets)) {
    for (const app of (t.affected_apps ?? [])) {
      if (!map[app]) map[app] = { targets: [] }
      map[app].targets.push({ id, name: t.name, state: t.consensus_state })
    }
  }
  return map
})

const appList = computed(() =>
  Object.entries(apps.value).map(([name, info]) => ({
    name,
    targets: info.targets,
    downCount: info.targets.filter(t => isDown(t.state as any)).length,
    status: info.targets.some(t => isDown(t.state as any)) ? 'degraded' : 'healthy',
  }))
)
</script>

<template>
  <div class="space-y-4">
    <h2 class="text-xl font-bold text-gray-900 dark:text-white">Apps</h2>
    <p class="text-sm text-gray-500">Applications and their monitored targets.</p>

    <div v-if="appList.length" class="space-y-3">
      <div
        v-for="app in appList"
        :key="app.name"
        class="bg-white dark:bg-gray-800 rounded-xl border shadow-sm p-4"
        :class="app.downCount ? 'border-red-200 dark:border-red-800' : 'border-gray-100 dark:border-gray-700'"
      >
        <div class="flex items-center gap-2 mb-2">
          <span :class="['w-2 h-2 rounded-full', app.downCount ? 'bg-red-500' : 'bg-green-500']" />
          <h3 class="font-semibold text-gray-900 dark:text-white">{{ app.name }}</h3>
          <span v-if="app.downCount" class="text-xs text-red-600 bg-red-50 dark:bg-red-900/20 px-2 py-0.5 rounded-full">
            {{ app.downCount }} down
          </span>
        </div>
        <div class="flex flex-wrap gap-2">
          <NuxtLink
            v-for="t in app.targets"
            :key="t.id"
            :to="{ name: 'targets-id', params: { id: t.id } }"
            class="flex items-center gap-1.5 text-xs bg-gray-50 dark:bg-gray-700 rounded-lg px-2.5 py-1 hover:bg-gray-100 dark:hover:bg-gray-600 transition"
          >
            <span :class="['w-1.5 h-1.5 rounded-full', isDown(t.state as any) ? 'bg-red-500' : 'bg-green-500']" />
            {{ t.name }}
          </NuxtLink>
        </div>
      </div>
    </div>

    <EmptyState
      v-else
      title="No apps configured"
      description="Add apps: sections to your config.yaml to group targets by application."
      icon="📦"
    />
  </div>
</template>
