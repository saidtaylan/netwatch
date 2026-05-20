import type { Config } from 'tailwindcss'

export default {
  darkMode: 'class',
  content: [
    './app/**/*.{vue,ts,js}',
    './types/**/*.ts',
  ],
  theme: {
    extend: {
      colors: {
        // Status colors
        status: {
          up:        '#22c55e', // green-500
          down:      '#ef4444', // red-500
          soft:      '#f97316', // orange-500
          softup:    '#84cc16', // lime-500
          unknown:   '#6b7280', // gray-500
        },
        // Scope/classification
        scope: {
          global:    '#ef4444',
          partial:   '#f97316',
          local:     '#eab308',
          standalone:'#6b7280',
        },
      },
    },
  },
  plugins: [],
} satisfies Config
