<script setup lang="ts">
const ui     = useUIStore()
const alerts = useAlertsStore()

import type { RouteLocationNamedRaw } from 'vue-router'

interface NavItem {
  label:    string
  icon:     string
  to:       RouteLocationNamedRaw
  badge?:   string | number
  disabled?: boolean
  soon?:    boolean
}

const sections: { title?: string; items: NavItem[] }[] = [
  {
    items: [
      { label: 'Cluster Overview', icon: '🌐', to: { name: 'index' } },
      { label: 'Targets',          icon: '📡', to: { name: 'targets' } },
      { label: 'Apps',             icon: '📦', to: { name: 'apps' } },
      { label: 'Topology',         icon: '🗺️',  to: { name: 'topology' } },
    ],
  },
  {
    title: 'Observability',
    items: [
      { label: 'Alerts',    icon: '🔔', to: { name: 'alerts' } },
      { label: 'SLO',       icon: '📊', to: { name: 'slo' } },
      { label: 'Geo Latency', icon: '🌍', to: { name: 'geo' } },
      { label: 'Audit Log', icon: '📋', to: { name: 'audit' },    soon: true },
    ],
  },
  {
    title: 'Operations',
    items: [
      { label: 'Maintenance', icon: '🔧', to: { name: 'maintenance' } },
      { label: 'Silences',    icon: '🔕', to: { name: 'silences' },   soon: true },
    ],
  },
  {
    title: 'Config',
    items: [
      { label: 'Config Sync',  icon: '⚙️',  to: { name: 'config' } },
      { label: 'Push Config',  icon: '📤', to: { name: 'config-push' } },
      { label: 'Keyring',      icon: '🔑', to: { name: 'config-keyring' } },
    ],
  },
  {
    title: 'Settings',
    items: [
      { label: 'Backend Nodes', icon: '🖥️', to: { name: 'settings-nodes' } },
      { label: 'Preferences',   icon: '⚙️',  to: { name: 'settings' } },
    ],
  },
]

const alertCount = computed(() => alerts.unresolvedCount)
</script>

<template>
  <aside
    :class="['flex flex-col bg-gray-900 text-gray-200 h-screen transition-all duration-200 overflow-y-auto', ui.sidebarCollapsed ? 'w-14' : 'w-56']"
  >
    <!-- Logo -->
    <div class="flex items-center gap-2 px-3 py-4 border-b border-gray-800">
      <span class="text-xl">📡</span>
      <span v-if="!ui.sidebarCollapsed" class="font-bold text-white text-sm">netwatch</span>
      <button
        class="ml-auto text-gray-500 hover:text-white"
        @click="ui.toggleSidebar()"
        :title="ui.sidebarCollapsed ? 'Expand' : 'Collapse'"
      >
        {{ ui.sidebarCollapsed ? '›' : '‹' }}
      </button>
    </div>

    <!-- Nav -->
    <nav class="flex-1 py-2">
      <template v-for="section in sections" :key="section.title">
        <p
          v-if="section.title && !ui.sidebarCollapsed"
          class="px-3 pt-3 pb-1 text-xs font-semibold uppercase tracking-wider text-gray-500"
        >
          {{ section.title }}
        </p>
        <NuxtLink
          v-for="item in section.items"
          :key="item.to.name as string"
          :to="item.disabled || item.soon ? undefined : item.to"
          :aria-label="item.label + (item.soon ? ' (coming soon)' : '')"
          :aria-disabled="item.disabled || item.soon ? 'true' : undefined"
          :tabindex="item.disabled || item.soon ? -1 : undefined"
          :class="[
            'flex items-center gap-2 px-3 py-2 text-sm rounded-md mx-1 transition',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-400',
            item.disabled || item.soon ? 'opacity-40 cursor-not-allowed' : 'hover:bg-gray-800 cursor-pointer',
          ]"
          active-class="bg-gray-800 text-white"
        >
          <span class="text-base">{{ item.icon }}</span>
          <span v-if="!ui.sidebarCollapsed" class="flex-1 truncate">{{ item.label }}</span>
          <span
            v-if="!ui.sidebarCollapsed && item.label === 'Alerts' && alertCount"
            class="bg-red-500 text-white text-xs rounded-full px-1.5 py-0.5 min-w-[1.2rem] text-center"
          >{{ alertCount }}</span>
          <span v-if="!ui.sidebarCollapsed && item.soon" class="text-xs bg-gray-700 text-gray-400 rounded px-1">Soon</span>
        </NuxtLink>
      </template>
    </nav>

    <!-- Bottom -->
    <div class="border-t border-gray-800 p-2">
      <ConnectionStatus />
    </div>
  </aside>
</template>
