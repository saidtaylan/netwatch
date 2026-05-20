<script setup lang="ts">
import type { Scope, Classification } from '~/types/api'
import { SCOPE_STYLE, CLASS_STYLE } from '~/utils/classifyState'
import { fmtPercent } from '~/utils/format'

defineProps<{
  scope:          Scope
  classification: Classification
  confidence:     number
  downNodes?:     string[]
  upNodes?:       string[]
}>()
</script>

<template>
  <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
    <div class="bg-gray-50 dark:bg-gray-900/40 rounded-xl p-3">
      <p class="text-xs text-gray-500 mb-1">Scope</p>
      <p :class="['text-sm font-semibold', SCOPE_STYLE[scope]?.color ?? 'text-gray-600']">
        {{ SCOPE_STYLE[scope]?.label ?? scope }}
      </p>
    </div>
    <div class="bg-gray-50 dark:bg-gray-900/40 rounded-xl p-3">
      <p class="text-xs text-gray-500 mb-1">Classification</p>
      <p :class="['text-sm font-semibold', CLASS_STYLE[classification]?.color ?? 'text-gray-600']">
        {{ CLASS_STYLE[classification]?.label ?? classification }}
      </p>
    </div>
    <div class="bg-gray-50 dark:bg-gray-900/40 rounded-xl p-3">
      <p class="text-xs text-gray-500 mb-1">Confidence</p>
      <p class="text-sm font-semibold text-gray-800 dark:text-gray-200">{{ fmtPercent(confidence, 0) }}</p>
    </div>
    <div v-if="downNodes?.length" class="sm:col-span-3 bg-red-50 dark:bg-red-900/10 rounded-xl p-3">
      <p class="text-xs text-red-500 font-medium mb-1">DOWN nodes</p>
      <p class="text-xs font-mono text-red-700 dark:text-red-400">{{ downNodes.join(', ') }}</p>
    </div>
    <div v-if="upNodes?.length" class="sm:col-span-3 bg-green-50 dark:bg-green-900/10 rounded-xl p-3">
      <p class="text-xs text-green-600 font-medium mb-1">UP nodes</p>
      <p class="text-xs font-mono text-green-700 dark:text-green-400">{{ upNodes.join(', ') }}</p>
    </div>
  </div>
</template>
