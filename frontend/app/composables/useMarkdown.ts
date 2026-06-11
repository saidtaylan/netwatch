import MarkdownIt from 'markdown-it'
import anchor from 'markdown-it-anchor'
import hljs from 'highlight.js/lib/core'
import bash from 'highlight.js/lib/languages/bash'
import yaml from 'highlight.js/lib/languages/yaml'
import go from 'highlight.js/lib/languages/go'
import json from 'highlight.js/lib/languages/json'
import typescript from 'highlight.js/lib/languages/typescript'

hljs.registerLanguage('bash', bash)
hljs.registerLanguage('sh', bash)
hljs.registerLanguage('yaml', yaml)
hljs.registerLanguage('go', go)
hljs.registerLanguage('json', json)
hljs.registerLanguage('typescript', typescript)
hljs.registerLanguage('ts', typescript)

export interface TocEntry {
  id: string
  text: string
  level: number // 2 or 3
}

// Shared slugify so the in-page TOC anchors match markdown-it-anchor's ids.
function slugify(s: string): string {
  return s
    .trim()
    .toLowerCase()
    .replace(/[^\w\s-]/g, '')
    .replace(/\s+/g, '-')
}

const md: MarkdownIt = new MarkdownIt({
  html: false,
  linkify: true,
  breaks: false,
  highlight(str, lang): string {
    if (lang && hljs.getLanguage(lang)) {
      try {
        return `<pre class="hljs"><code>${hljs.highlight(str, { language: lang }).value}</code></pre>`
      } catch { /* fall through */ }
    }
    return `<pre class="hljs"><code>${md.utils.escapeHtml(str)}</code></pre>`
  },
})

// Adds id="" to every heading (so the TOC can link to #id). No permalink symbol
// or link wrapper — headings render as plain, fully-styleable text.
md.use(anchor, { slugify })

/** Render markdown to sanitized HTML and extract an h2/h3 table of contents. */
export function renderMarkdown(src: string): { html: string; toc: TocEntry[] } {
  const toc: TocEntry[] = []
  const tokens = md.parse(src, {})
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i]
    if (t.type === 'heading_open' && (t.tag === 'h2' || t.tag === 'h3')) {
      const text = tokens[i + 1]?.content ?? ''
      toc.push({ id: slugify(text), text, level: t.tag === 'h2' ? 2 : 3 })
    }
  }
  return { html: md.render(src), toc }
}
