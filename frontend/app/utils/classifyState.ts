import type { TargetState, Scope, Classification } from '~/types/api'

export interface StateStyle {
  color:   string   // Tailwind text color class
  bg:      string   // Tailwind bg class
  ring:    string   // Tailwind ring class
  label:   string
  icon:    string   // heroicons name (solid)
}

export const STATE_STYLE: Record<TargetState, StateStyle> = {
  up:        { color: 'text-green-600',   bg: 'bg-green-50 dark:bg-green-900/20',   ring: 'ring-green-500',  label: 'UP',        icon: 'check-circle' },
  soft_up:   { color: 'text-lime-600',    bg: 'bg-lime-50  dark:bg-lime-900/20',    ring: 'ring-lime-500',   label: 'SOFT UP',   icon: 'arrow-trending-up' },
  soft_down: { color: 'text-orange-600',  bg: 'bg-orange-50 dark:bg-orange-900/20', ring: 'ring-orange-500', label: 'SOFT DOWN', icon: 'exclamation-triangle' },
  hard_down: { color: 'text-red-600',     bg: 'bg-red-50   dark:bg-red-900/20',     ring: 'ring-red-500',    label: 'DOWN',      icon: 'x-circle' },
  unknown:   { color: 'text-gray-500',    bg: 'bg-gray-50  dark:bg-gray-900/20',    ring: 'ring-gray-400',   label: 'UNKNOWN',   icon: 'question-mark-circle' },
}

export const SCOPE_STYLE: Record<Scope, { color: string; label: string }> = {
  GLOBAL:     { color: 'text-red-600',    label: 'Global Outage' },
  PARTIAL:    { color: 'text-orange-500', label: 'Partial' },
  NODE_LOCAL: { color: 'text-yellow-600', label: 'Local' },
  STANDALONE: { color: 'text-gray-500',   label: 'Standalone' },
}

export const CLASS_STYLE: Record<Classification, { color: string; label: string }> = {
  REAL_OUTAGE:        { color: 'text-red-600',    label: 'Real Outage' },
  NETWORK_PARTITION:  { color: 'text-orange-500', label: 'Network Partition' },
  LOCAL_FAILURE:      { color: 'text-yellow-600', label: 'Local Failure' },
  AMBIGUOUS:          { color: 'text-gray-500',   label: 'Ambiguous' },
}

export function stateStyle(state: TargetState): StateStyle {
  return STATE_STYLE[state] ?? STATE_STYLE.unknown
}

export function isDown(state: TargetState): boolean {
  return state === 'hard_down' || state === 'soft_down'
}
