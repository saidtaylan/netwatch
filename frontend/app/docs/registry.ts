// Documentation registry: maps slugs → markdown content + the sidebar nav tree.
// Add a new page by dropping a .md file here, importing it ?raw, and listing it
// in docsNav under the right category.
import introduction from './content/introduction.md?raw'
import architecture from './content/architecture.md?raw'
import gossipPropagation from './content/gossip-propagation.md?raw'
import probeOwnership from './content/distributed-probe-ownership.md?raw'
import configReference from './content/config-reference.md?raw'

export interface DocPage {
  slug: string
  title: string
  /** one-line summary shown on the docs home */
  summary: string
  body: string
}

export interface DocCategory {
  name: string
  pages: DocPage[]
}

export const docsNav: DocCategory[] = [
  {
    name: 'Concepts',
    pages: [
      {
        slug: 'introduction',
        title: 'Introduction',
        summary: 'What netwatch is, the problems it solves, and its core philosophy.',
        body: introduction,
      },
      {
        slug: 'architecture',
        title: 'Architecture',
        summary: 'Components, processes, and the data flow from probe to alert.',
        body: architecture,
      },
      {
        slug: 'distributed-probe-ownership',
        title: 'Distributed Probe Ownership',
        summary: 'How the cluster picks which nodes probe each target — hash ring, zones, replication factor/percent.',
        body: probeOwnership,
      },
    ],
  },
  {
    name: 'How it works (deep dives)',
    pages: [
      {
        slug: 'gossip-propagation',
        title: 'How a target propagates',
        summary: 'A step-by-step trace of a write replicating across the cluster via gossip + LWW.',
        body: gossipPropagation,
      },
    ],
  },
  {
    name: 'Reference',
    pages: [
      {
        slug: 'config-reference',
        title: 'Configuration reference',
        summary: 'Every config.yaml field: type, default, and meaning.',
        body: configReference,
      },
    ],
  },
]

// Flat lookup: slug → page (+ its category name).
export const docsBySlug: Record<string, DocPage & { category: string }> = {}
for (const cat of docsNav) {
  for (const p of cat.pages) {
    docsBySlug[p.slug] = { ...p, category: cat.name }
  }
}

export const DOCS_HOME_SLUG = 'introduction'
