// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  compatibilityDate: '2025-07-15',
  ssr: false,
  devtools: { enabled: true },

  // Logs — requires direct HTTP to every node (firewall blocked in prod)
  // Audit Log — not yet implemented
  // Pages kept on disk for future use; excluded from routing here.
  hooks: {
    'pages:extend'(pages) {
      const exclude = ['/logs', '/audit']
      for (let i = pages.length - 1; i >= 0; i--) {
        if (exclude.includes(pages[i].path ?? '')) pages.splice(i, 1)
      }
    },
  },

  modules: [
    '@nuxtjs/tailwindcss',
    '@nuxtjs/color-mode',
    '@pinia/nuxt',
  ],

  // Auto-import all components without subfolder prefix
  // (e.g. app/components/targets/TargetRow.vue → <TargetRow>, not <TargetsTargetRow>)
  components: [
    { path: '~/components', pathPrefix: false },
  ],

  colorMode: {
    classSuffix: '',
    preference: 'system',
    fallback: 'light',
  },

  // Prerender the SPA shell so `pnpm build` emits .output/public/index.html.
  // Without this, ssr:false + `nuxt build` only produces a nitro server (no
  // static index.html), which breaks serving the app from nginx / static hosts.
  nitro: {
    prerender: {
      routes: ['/'],
    },
  },

  runtimeConfig: {
    public: {
      // Override with NUXT_PUBLIC_DEFAULT_BACKEND_URL env var
      defaultBackendUrl: '',
    },
  },

  tailwindcss: {
    cssPath: '~/assets/css/main.css',
    configPath: 'tailwind.config.ts',
  },

  app: {
    head: {
      title: 'netwatch',
      meta: [
        { charset: 'utf-8' },
        { name: 'viewport', content: 'width=device-width, initial-scale=1' },
        { name: 'description', content: 'netwatch — Distributed Network Monitoring' },
      ],
    },
  },
})
