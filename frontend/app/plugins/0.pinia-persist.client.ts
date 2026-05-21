/**
 * 0.pinia-persist.client.ts
 *
 * The "0." prefix ensures this plugin runs BEFORE all other client plugins,
 * which means pinia-plugin-persistedstate is registered before any store is
 * first accessed (e.g., by auth.global.ts middleware or composables).
 *
 * pinia-plugin-persistedstate reads from localStorage synchronously on first
 * store access after this plugin is registered.
 */
import { createPersistedState } from 'pinia-plugin-persistedstate'

export default defineNuxtPlugin((nuxtApp) => {
  // @ts-ignore — $pinia is injected by @pinia/nuxt
  nuxtApp.$pinia.use(createPersistedState({ storage: localStorage }))
})
