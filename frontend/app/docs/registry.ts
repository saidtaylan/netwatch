// Documentation registry: maps slugs → markdown content + the sidebar nav tree.
// Add a new page by dropping a .md file in ./content, importing it ?raw, and
// listing it in docsNav under the right category.
import introduction from './content/introduction.md?raw'
import architecture from './content/architecture.md?raw'
import stateMachine from './content/state-machine.md?raw'
import storageLww from './content/storage-lww.md?raw'
import scopeClassification from './content/scope-classification.md?raw'
import quorumIsolation from './content/quorum-isolation.md?raw'
import probeOwnership from './content/distributed-probe-ownership.md?raw'
import rootCauseTopology from './content/root-cause-topology.md?raw'
import notifications from './content/notifications.md?raw'
import slo from './content/slo.md?raw'
import maintenanceSilences from './content/maintenance-silences.md?raw'
import geoLatency from './content/geo-latency.md?raw'
import gossipPropagation from './content/gossip-propagation.md?raw'
import exactlyOnceAlerting from './content/exactly-once-alerting.md?raw'
import antiEntropy from './content/anti-entropy.md?raw'
import configSync from './content/config-sync.md?raw'
import security from './content/security.md?raw'
import configReference from './content/config-reference.md?raw'
import probeTypes from './content/probe-types.md?raw'
import httpApi from './content/http-api.md?raw'
import metrics from './content/metrics.md?raw'
import alertEnv from './content/alert-env.md?raw'
import cli from './content/cli.md?raw'
import gettingStarted from './content/getting-started.md?raw'
import deployment from './content/deployment.md?raw'

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
      { slug: 'introduction', title: 'Introduction', summary: 'What netwatch is, the problems it solves, and its core philosophy.', body: introduction },
      { slug: 'architecture', title: 'Architecture', summary: 'Components, processes, and the data flow from probe to alert.', body: architecture },
      { slug: 'state-machine', title: 'State machine', summary: 'How a target moves through up / soft_down / hard_down / recovering.', body: stateMachine },
      { slug: 'storage-lww', title: 'Storage & LWW', summary: 'Per-node SQLite, the table schema, and last-writer-wins conflict resolution.', body: storageLww },
      { slug: 'scope-classification', title: 'Scope classification', summary: 'Real outage vs network partition vs local failure — and how it is decided.', body: scopeClassification },
      { slug: 'quorum-isolation', title: 'Quorum & isolated mode', summary: 'How split-brain is detected and why a minority goes silent.', body: quorumIsolation },
    ],
  },
  {
    name: 'Probing & alerting',
    pages: [
      { slug: 'distributed-probe-ownership', title: 'Distributed Probe Ownership', summary: 'How the cluster picks which nodes probe each target.', body: probeOwnership },
      { slug: 'root-cause-topology', title: 'Root cause & topology', summary: 'depends_on, root-cause analysis, and cascading impact.', body: rootCauseTopology },
      { slug: 'notifications', title: 'Notifications & channels', summary: 'Script, mail and webhook channels, and how channels are chosen.', body: notifications },
      { slug: 'slo', title: 'SLO tracking', summary: 'Uptime objectives, error budgets, rolling windows and breach alerts.', body: slo },
      { slug: 'maintenance-silences', title: 'Maintenance & silences', summary: 'Planned-downtime windows and matcher-based alert mutes.', body: maintenanceSilences },
      { slug: 'geo-latency', title: 'Geo latency & regions', summary: 'Per-region latency, anomaly detection, and region-pinned probers.', body: geoLatency },
    ],
  },
  {
    name: 'How it works (deep dives)',
    pages: [
      { slug: 'gossip-propagation', title: 'How a target propagates', summary: 'A step-by-step trace of a write replicating across the cluster.', body: gossipPropagation },
      { slug: 'exactly-once-alerting', title: 'Exactly-once alerting', summary: 'The full decision chain: responsible node, quorum gate, confirmations, failover.', body: exactlyOnceAlerting },
      { slug: 'anti-entropy', title: 'Anti-entropy & rejoin', summary: 'Push-pull reconciliation, and why rolling restarts never storm.', body: antiEntropy },
      { slug: 'config-sync', title: 'Config sync & drift', summary: 'Detecting and reconciling divergent shared config across nodes.', body: configSync },
    ],
  },
  {
    name: 'Security',
    pages: [
      { slug: 'security', title: 'Authentication & encryption', summary: 'JWT auth, users & roles, and the gossip keyring (zero-downtime rotation).', body: security },
    ],
  },
  {
    name: 'Reference',
    pages: [
      { slug: 'config-reference', title: 'Configuration', summary: 'Every config.yaml field: type, default, and meaning.', body: configReference },
      { slug: 'probe-types', title: 'Probe types', summary: 'Per-type options for tcp / http / ping / dns / sql.', body: probeTypes },
      { slug: 'http-api', title: 'HTTP API', summary: 'Every REST endpoint, grouped by area, with auth requirements.', body: httpApi },
      { slug: 'metrics', title: 'Metrics', summary: 'Every Prometheus metric: type, labels, and meaning.', body: metrics },
      { slug: 'alert-env', title: 'Alert env variables', summary: 'Every variable passed to alert channels.', body: alertEnv },
      { slug: 'cli', title: 'CLI', summary: 'The netwatch binary subcommands: init, join, validate, leave, keyring.', body: cli },
    ],
  },
  {
    name: 'Operations',
    pages: [
      { slug: 'getting-started', title: 'Getting started', summary: 'Run a node, log in, add your first target, read the UI.', body: gettingStarted },
      { slug: 'deployment', title: 'Deployment', summary: 'systemd, Ansible, Docker and Helm — all install from prebuilt releases.', body: deployment },
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
