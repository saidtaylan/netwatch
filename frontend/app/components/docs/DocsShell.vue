<script setup lang="ts">
import { docsNav, docsBySlug } from '~/docs/registry'
import { renderMarkdown } from '~/composables/useMarkdown'

const props = defineProps<{ slug: string }>()
const router = useRouter()

// Links inside the rendered markdown are plain <a> (v-html), so a relative link
// like [Architecture](architecture) would do a native navigation — which 404s
// from the /docs home (resolves to /architecture). Intercept clicks on internal
// links and route them to /docs/<slug> via the SPA router instead.
function onContentClick(e: MouseEvent) {
  const a = (e.target as HTMLElement).closest('a')
  if (!a) return
  const href = a.getAttribute('href') || ''
  if (!href || /^(https?:|mailto:|#)/.test(href) || a.getAttribute('target') === '_blank') return
  e.preventDefault()
  const slug = href.replace(/^\/?(docs\/)?/, '').replace(/\.md$/, '').replace(/[?#].*$/, '')
  if (slug) router.push({ name: 'docs-slug', params: { slug } })
}

const page = computed(() => docsBySlug[props.slug])
const rendered = computed(() =>
  page.value ? renderMarkdown(page.value.body) : { html: '', toc: [] },
)

// Active heading highlight in the right-hand TOC (scroll spy).
const activeId = ref('')
let observer: IntersectionObserver | null = null

function setupSpy() {
  observer?.disconnect()
  if (typeof window === 'undefined') return
  nextTick(() => {
    const headings = Array.from(
      document.querySelectorAll('.docs-content h2[id], .docs-content h3[id]'),
    )
    observer = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (e.isIntersecting) activeId.value = e.target.id
        }
      },
      { rootMargin: '0px 0px -75% 0px', threshold: 0 },
    )
    headings.forEach((h) => observer!.observe(h))
  })
}

onMounted(setupSpy)
watch(() => props.slug, () => { activeId.value = ''; setupSpy() })
onBeforeUnmount(() => observer?.disconnect())
</script>

<template>
  <div class="flex gap-8 items-start">
    <!-- ── Left: category nav ───────────────────────────────────── -->
    <aside class="w-56 flex-shrink-0 hidden md:block">
      <nav class="sticky top-4 space-y-5 text-sm pb-10 max-h-[calc(100vh-2rem)] overflow-y-auto">
        <div v-for="cat in docsNav" :key="cat.name">
          <p class="text-xs font-semibold uppercase tracking-wide text-gray-400 mb-1.5">{{ cat.name }}</p>
          <ul class="space-y-0.5">
            <li v-for="p in cat.pages" :key="p.slug">
              <NuxtLink
                :to="{ name: 'docs-slug', params: { slug: p.slug } }"
                :class="['block px-2 py-1 rounded transition',
                  p.slug === slug
                    ? 'bg-blue-50 dark:bg-blue-900/30 text-blue-600 dark:text-blue-400 font-medium'
                    : 'text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-gray-100 hover:bg-gray-50 dark:hover:bg-gray-800']"
              >{{ p.title }}</NuxtLink>
            </li>
          </ul>
        </div>
      </nav>
    </aside>

    <!-- ── Center: rendered markdown ────────────────────────────── -->
    <article class="flex-1 min-w-0">
      <template v-if="page">
        <p class="text-xs uppercase tracking-wide text-gray-400 mb-1">{{ page.category }}</p>
        <!-- eslint-disable-next-line vue/no-v-html -->
        <div class="docs-content" v-html="rendered.html" @click="onContentClick" />
      </template>
      <div v-else class="text-gray-500 py-12">
        <p class="text-lg font-semibold">Page not found</p>
        <p class="text-sm mt-1">No documentation page with slug “{{ slug }}”.</p>
        <NuxtLink :to="{ name: 'docs' }" class="text-blue-600 dark:text-blue-400 text-sm mt-3 inline-block">← Back to docs home</NuxtLink>
      </div>
    </article>

    <!-- ── Right: in-page TOC ───────────────────────────────────── -->
    <aside v-if="rendered.toc.length" class="w-52 flex-shrink-0 hidden xl:block">
      <nav class="sticky top-4 text-sm max-h-[calc(100vh-2rem)] overflow-y-auto pb-10">
        <p class="text-xs font-semibold uppercase tracking-wide text-gray-400 mb-2">On this page</p>
        <ul class="space-y-1 border-l border-gray-200 dark:border-gray-700">
          <li v-for="h in rendered.toc" :key="h.id" :class="h.level === 3 ? 'pl-6' : 'pl-3'">
            <a
              :href="`#${h.id}`"
              :class="['block py-0.5 -ml-px border-l-2 transition',
                activeId === h.id
                  ? 'border-blue-500 text-blue-600 dark:text-blue-400 font-medium'
                  : 'border-transparent text-gray-500 hover:text-gray-800 dark:hover:text-gray-200']"
            >{{ h.text }}</a>
          </li>
        </ul>
      </nav>
    </aside>
  </div>
</template>

<style>
/* ── Prose styling for rendered markdown ─────────────────────────── */
.docs-content { color: rgb(55 65 81); line-height: 1.7; font-size: 0.925rem; }
.dark .docs-content { color: rgb(209 213 219); }
.docs-content h1 { font-size: 1.875rem; font-weight: 700; color: rgb(17 24 39); margin: 0 0 1rem; }
.docs-content h2 { font-size: 1.4rem; font-weight: 700; color: rgb(17 24 39); margin: 2.25rem 0 0.85rem; padding-top: 0.5rem; border-top: 1px solid rgb(243 244 246); scroll-margin-top: 1rem; }
.docs-content h3 { font-size: 1.1rem; font-weight: 600; color: rgb(31 41 55); margin: 1.6rem 0 0.6rem; scroll-margin-top: 1rem; }
.dark .docs-content h1, .dark .docs-content h2 { color: rgb(255 255 255); }
.dark .docs-content h3 { color: rgb(229 231 235); }
.dark .docs-content h2 { border-top-color: rgb(31 41 55); }
.docs-content p { margin: 0.85rem 0; }
.docs-content a { color: rgb(37 99 235); text-decoration: none; }
.docs-content a:hover { text-decoration: underline; }
.dark .docs-content a { color: rgb(96 165 250); }
.docs-content ul, .docs-content ol { margin: 0.85rem 0; padding-left: 1.4rem; }
.docs-content ul { list-style: disc; }
.docs-content ol { list-style: decimal; }
.docs-content li { margin: 0.3rem 0; }
.docs-content strong { font-weight: 600; color: rgb(17 24 39); }
.dark .docs-content strong { color: rgb(243 244 246); }
.docs-content blockquote { border-left: 3px solid rgb(96 165 250); background: rgb(239 246 255); padding: 0.6rem 1rem; margin: 1rem 0; border-radius: 0 0.5rem 0.5rem 0; }
.dark .docs-content blockquote { background: rgba(30 58 138 / 0.2); }
/* inline code */
.docs-content code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.85em; background: rgb(243 244 246); color: rgb(190 24 93); padding: 0.1rem 0.35rem; border-radius: 0.3rem; }
.dark .docs-content code { background: rgb(31 41 55); color: rgb(244 114 182); }
/* code blocks */
.docs-content pre { background: rgb(249 250 251); border: 1px solid rgb(229 231 235); border-radius: 0.6rem; padding: 0.9rem 1.1rem; overflow-x: auto; margin: 1rem 0; font-size: 0.83rem; line-height: 1.55; }
.dark .docs-content pre { background: rgb(17 24 39); border-color: rgb(31 41 55); }
.docs-content pre code { background: none; color: rgb(31 41 55); padding: 0; font-size: inherit; }
.dark .docs-content pre code { color: rgb(229 231 235); }
/* tables */
.docs-content table { width: 100%; border-collapse: collapse; margin: 1rem 0; font-size: 0.85rem; }
.docs-content th, .docs-content td { border: 1px solid rgb(229 231 235); padding: 0.5rem 0.7rem; text-align: left; vertical-align: top; }
.dark .docs-content th, .dark .docs-content td { border-color: rgb(55 65 81); }
.docs-content th { background: rgb(249 250 251); font-weight: 600; }
.dark .docs-content th { background: rgb(31 41 55); }
.docs-content hr { border: 0; border-top: 1px solid rgb(229 231 235); margin: 2rem 0; }
.dark .docs-content hr { border-top-color: rgb(55 65 81); }

/* ── Compact highlight.js theme (adapts to dark mode) ────────────── */
.hljs-comment, .hljs-quote { color: rgb(107 114 128); font-style: italic; }
.hljs-keyword, .hljs-selector-tag, .hljs-built_in, .hljs-section { color: rgb(147 51 234); }
.hljs-string, .hljs-attr, .hljs-template-tag { color: rgb(22 163 74); }
.hljs-number, .hljs-literal { color: rgb(217 119 6); }
.hljs-title, .hljs-title.function_, .hljs-name { color: rgb(37 99 235); }
.hljs-attribute, .hljs-variable, .hljs-type { color: rgb(220 38 38); }
.dark .hljs-keyword, .dark .hljs-selector-tag, .dark .hljs-built_in, .dark .hljs-section { color: rgb(196 181 253); }
.dark .hljs-string, .dark .hljs-attr, .dark .hljs-template-tag { color: rgb(134 239 172); }
.dark .hljs-number, .dark .hljs-literal { color: rgb(251 191 36); }
.dark .hljs-title, .dark .hljs-title.function_, .dark .hljs-name { color: rgb(147 197 253); }
.dark .hljs-attribute, .dark .hljs-variable, .dark .hljs-type { color: rgb(252 165 165); }
</style>
