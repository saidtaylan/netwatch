<script setup lang="ts">
import type { FleetTarget } from '~/types/api'
import { stateStyle, SCOPE_STYLE, CLASS_STYLE } from '~/utils/classifyState'

const props = defineProps<{ target: FleetTarget; id: string }>()
const style = computed(() => stateStyle(props.target.consensus_state))
</script>

<template>
  <NuxtLink
    :to="{ name: 'targets-id', params: { id } }"
    class="flex items-center gap-3 px-4 py-3 hover:bg-gray-50 dark:hover:bg-gray-700/40 transition cursor-pointer"
  >
    <!-- Status dot -->
    <span :class="['w-2.5 h-2.5 rounded-full flex-shrink-0', style.bg.split(' ')[0].replace('bg-', 'bg-').replace('50', '400').replace('dark:bg-', '')]" />

    <!-- Name + type -->
    <div class="flex-1 min-w-0">
      <p class="text-sm font-medium text-gray-900 dark:text-white truncate">{{ target.name }}</p>
      <p class="text-xs text-gray-500 dark:text-gray-400 font-mono truncate">{{ target.target }}</p>
    </div>

    <!-- Type badge -->
    <span class="hidden sm:inline text-xs bg-gray-100 dark:bg-gray-700 text-gray-600 dark:text-gray-300 rounded px-1.5 py-0.5 uppercase font-mono">
      {{ target.type }}
    </span>

    <!-- Status badge -->
    <StatusBadge :state="target.consensus_state" size="sm" />

    <!-- Scope -->
    <span
      :class="['hidden md:inline text-xs font-medium', SCOPE_STYLE[target.scope]?.color ?? 'text-gray-500']"
    >
      {{ SCOPE_STYLE[target.scope]?.label ?? target.scope }}
    </span>

    <!-- Classification -->
    <span
      v-if="target.consensus_state === 'hard_down' || target.consensus_state === 'soft_down'"
      :class="['hidden lg:inline text-xs', CLASS_STYLE[target.classification]?.color ?? 'text-gray-400']"
    >
      {{ CLASS_STYLE[target.classification]?.label ?? target.classification }}
    </span>

    <!-- Apps -->
    <span v-if="target.affected_apps?.length" class="hidden xl:inline text-xs text-gray-400 truncate max-w-32">
      {{ target.affected_apps.slice(0, 2).join(', ') }}{{ target.affected_apps.length > 2 ? '…' : '' }}
    </span>

    <span class="text-gray-300 dark:text-gray-600 text-xs">›</span>
  </NuxtLink>
</template>
