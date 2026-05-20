<script setup lang="ts">
import type { TargetState } from '../../../types/api'
import { stateStyle } from '../../utils/classifyState'

const props = defineProps<{ state: TargetState; size?: 'sm' | 'md' }>()
const style = computed(() => stateStyle(props.state))
const size  = computed(() => props.size === 'sm' ? 'text-xs px-1.5 py-0.5' : 'text-sm px-2 py-0.5')
</script>

<template>
  <span :class="[style.color, style.bg, 'inline-flex items-center font-semibold rounded-full ring-1 ring-inset', style.ring.replace('ring-', 'ring-'), size]">
    {{ style.label }}
  </span>
</template>
