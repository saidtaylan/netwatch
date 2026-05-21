<script setup lang="ts">
const sections = [
  'concepts', 'navigation', 'cluster-overview', 'targets', 'topology',
  'scope-classification', 'slo', 'maintenance', 'alerts', 'config', 'geo',
]

const active = ref('concepts')

function scrollTo(id: string) {
  active.value = id
  document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
</script>

<template>
  <div class="flex gap-6 max-w-5xl">
    <!-- Sidebar TOC -->
    <aside class="w-44 flex-shrink-0 hidden lg:block">
      <nav class="sticky top-4 space-y-1 text-sm">
        <p class="text-xs font-semibold uppercase tracking-wide text-gray-400 mb-2">Contents</p>
        <button
          v-for="s in sections" :key="s"
          :class="['block w-full text-left px-2 py-1 rounded transition',
            active === s ? 'bg-blue-50 dark:bg-blue-900/30 text-blue-600 dark:text-blue-400 font-medium' : 'text-gray-500 hover:text-gray-800 dark:hover:text-gray-200']"
          @click="scrollTo(s)"
        >{{ s.split('-').map(w => w[0].toUpperCase() + w.slice(1)).join(' ') }}</button>
      </nav>
    </aside>

    <!-- Content -->
    <article class="flex-1 space-y-10 text-sm text-gray-700 dark:text-gray-300 min-w-0">
      <div class="flex items-center gap-3">
        <h1 class="text-2xl font-bold text-gray-900 dark:text-white">📖 netwatch UI — User Guide</h1>
      </div>

      <!-- CONCEPTS -->
      <section :id="sections[0]">
        <h2 class="text-lg font-bold text-gray-900 dark:text-white mb-3">Core Concepts</h2>
        <div class="space-y-3">
          <div class="bg-blue-50 dark:bg-blue-900/20 rounded-xl p-4">
            <h3 class="font-semibold mb-1">What is netwatch?</h3>
            <p>A distributed network monitoring agent. Multiple nodes form a gossip cluster, each probing the same targets. They share state via gossip protocol and collectively decide whether an outage is real, local, or a network partition.</p>
          </div>
          <div class="grid sm:grid-cols-2 gap-3">
            <div class="bg-gray-50 dark:bg-gray-800 rounded-xl p-3">
              <p class="font-semibold text-xs uppercase tracking-wide text-gray-500 mb-1">Target</p>
              <p>A service endpoint being monitored. Can be TCP, HTTP, ICMP ping, DNS, or SQL. Each target has a unique ID, probe type, and optional app associations.</p>
            </div>
            <div class="bg-gray-50 dark:bg-gray-800 rounded-xl p-3">
              <p class="font-semibold text-xs uppercase tracking-wide text-gray-500 mb-1">Probe</p>
              <p>A health check sent to a target. Each node probes its assigned targets on a schedule (probe_interval_sec). Multiple nodes → multiple probe perspectives.</p>
            </div>
            <div class="bg-gray-50 dark:bg-gray-800 rounded-xl p-3">
              <p class="font-semibold text-xs uppercase tracking-wide text-gray-500 mb-1">Consensus State</p>
              <p>The cluster's agreed-upon state for a target after merging all node perspectives: <strong>up</strong>, <strong>soft_down</strong> (pending retries), <strong>hard_down</strong> (confirmed down), <strong>soft_up</strong> (recovering).</p>
            </div>
            <div class="bg-gray-50 dark:bg-gray-800 rounded-xl p-3">
              <p class="font-semibold text-xs uppercase tracking-wide text-gray-500 mb-1">Quorum</p>
              <p>The cluster needs a majority (≥50% by default) of nodes alive before it sends alerts. This prevents alert storms when the monitoring network itself is partitioned.</p>
            </div>
          </div>
        </div>
      </section>

      <!-- NAVIGATION -->
      <section :id="sections[1]">
        <h2 class="text-lg font-bold text-gray-900 dark:text-white mb-3">Navigation</h2>
        <div class="overflow-x-auto">
          <table class="w-full text-xs">
            <thead>
              <tr class="border-b border-gray-200 dark:border-gray-700">
                <th class="text-left py-2 pr-4 font-semibold">Page</th>
                <th class="text-left py-2 pr-4 font-semibold">What you see</th>
                <th class="text-left py-2 font-semibold">When to use</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-gray-800">
              <tr><td class="py-2 pr-4 font-mono">Cluster Overview</td><td class="py-2 pr-4">Health summary, active outages, member list</td><td class="py-2">First page — is everything OK?</td></tr>
              <tr><td class="py-2 pr-4 font-mono">Targets</td><td class="py-2 pr-4">All targets, filterable by status/type</td><td class="py-2">See every service at a glance</td></tr>
              <tr><td class="py-2 pr-4 font-mono">Topology</td><td class="py-2 pr-4">Dependency graph, cascading impact</td><td class="py-2">Root cause analysis during outage</td></tr>
              <tr><td class="py-2 pr-4 font-mono">SLO</td><td class="py-2 pr-4">Uptime ratios, error budgets, incidents</td><td class="py-2">Monthly reliability reporting</td></tr>
              <tr><td class="py-2 pr-4 font-mono">Maintenance</td><td class="py-2 pr-4">Scheduled silence windows</td><td class="py-2">Before planned downtime</td></tr>
              <tr><td class="py-2 pr-4 font-mono">Alerts</td><td class="py-2 pr-4">State-change feed (session only)</td><td class="py-2">Recent events during active monitoring</td></tr>
              <tr><td class="py-2 pr-4 font-mono">Config Sync</td><td class="py-2 pr-4">Per-node config hash comparison</td><td class="py-2">Detect config drift across nodes</td></tr>
              <tr><td class="py-2 pr-4 font-mono">Geo Latency</td><td class="py-2 pr-4">Per-region probe latency, anomalies</td><td class="py-2">Regional connectivity investigation</td></tr>
            </tbody>
          </table>
        </div>
      </section>

      <!-- CLUSTER OVERVIEW -->
      <section :id="sections[2]">
        <h2 class="text-lg font-bold text-gray-900 dark:text-white mb-3">Cluster Overview</h2>
        <div class="space-y-2">
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Cluster Nodes</span>
            <p>Number of alive gossip members. If below <code>expected_node_count</code> and below <code>min_quorum_ratio</code>, the cluster enters isolated mode and stops sending alerts.</p>
          </div>
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Targets Up / Down</span>
            <p>Live aggregate count. Down = hard_down (confirmed after max_retries). Does <em>not</em> count soft_down (still retrying) or soft_up (recovering).</p>
          </div>
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Config Drift</span>
            <p>Compares the SHA-256 hash of each node's config file. "In sync" = all nodes have identical configs. A drift means one node was updated without syncing the rest — use "Push Config" to fix.</p>
          </div>
          <div class="flex gap-3 p-3 bg-orange-50 dark:bg-orange-900/20 rounded-xl">
            <span class="w-28 font-semibold text-xs text-orange-600 flex-shrink-0 mt-0.5">Isolated Mode</span>
            <p>Quorum lost. The node can still probe but suppresses all alert sends to prevent false-positive storms. The cluster will exit isolated mode automatically when enough nodes rejoin.</p>
          </div>
        </div>
      </section>

      <!-- TARGETS -->
      <section :id="sections[3]">
        <h2 class="text-lg font-bold text-gray-900 dark:text-white mb-3">Targets &amp; State Machine</h2>
        <div class="space-y-3">
          <p>Every target follows this state machine:</p>
          <div class="bg-gray-900 dark:bg-gray-950 rounded-xl px-4 py-3 font-mono text-xs text-green-400 overflow-auto">
            UP → SOFT_DOWN (probe failed, retrying max_retries times)<br>
            SOFT_DOWN → HARD_DOWN (all retries exhausted → alert fired)<br>
            HARD_DOWN / SOFT_DOWN → SOFT_UP (first success, waiting recovery_probes)<br>
            SOFT_UP → UP (recovery_probes consecutive successes → resolved alert)
          </div>
          <div class="space-y-2">
            <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
              <span class="w-36 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Target List columns</span>
              <p><strong>Name / Address</strong> — display name + probe endpoint.<br>
              <strong>Type</strong> — tcp | http | ping | dns | sql.<br>
              <strong>Status</strong> — consensus state badge (green=UP, red=DOWN, orange=SOFT_DOWN).<br>
              <strong>Scope</strong> — agreement across nodes (see below).<br>
              <strong>Classification</strong> — algorithm verdict (see below).<br>
              <strong>App</strong> — affected application name (if configured).</p>
            </div>
            <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
              <span class="w-36 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Target Detail</span>
              <p><strong>By-Node Breakdown</strong> — each cluster node's individual view of this target (state, error code, sequence number). This is how you see "2 nodes say DOWN, 1 says UP".<br>
              <strong>Dependency Chips</strong> — orange = depends on, yellow = cascades to, red = root cause.<br>
              <strong>Prober Assignment</strong> — which nodes are actively probing this target (from distributed probe ownership).<br>
              <strong>Geo Latency</strong> — per-node probe latency with anomaly flag.</p>
            </div>
          </div>
        </div>
      </section>

      <!-- TOPOLOGY -->
      <section :id="sections[4]">
        <h2 class="text-lg font-bold text-gray-900 dark:text-white mb-3">Topology — Dependency Graph</h2>
        <div class="space-y-2">
          <p>Topology shows the <strong>depends_on</strong> relationships you configured. When a "root" service (no dependencies) fails, every service that depends on it is shown as a "cascading impact".</p>
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Root Targets</span>
            <p>Targets with no <code>depends_on</code>. e.g., db-primary. If this target fails, the cascading chain starts here.</p>
          </div>
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Cascades to</span>
            <p>If <strong>this</strong> target goes down, which other targets will also likely fail? e.g., db-primary cascades to api-gateway and checkout because they depend on the database.</p>
          </div>
          <div class="flex gap-3 p-3 bg-orange-50 dark:bg-orange-900/20 rounded-xl">
            <span class="w-28 font-semibold text-xs text-orange-600 flex-shrink-0 mt-0.5">Root Cause</span>
            <p>When api-gateway is down AND db-primary is also down, the system determines that <strong>db-primary is the root cause</strong> (it's deeper in the dependency chain). This prevents alert spam — you get one alert at the root, not one per dependent.</p>
          </div>
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">UNKNOWN state</span>
            <p>First probe hasn't completed yet (probe_interval_sec not elapsed). Wait one cycle and refresh. UNKNOWN is not an alert — it's "we don't know yet".</p>
          </div>
        </div>
      </section>

      <!-- SCOPE + CLASSIFICATION -->
      <section :id="sections[5]">
        <h2 class="text-lg font-bold text-gray-900 dark:text-white mb-3">Scope &amp; Classification</h2>
        <p class="mb-3">These two fields tell you <strong>how bad</strong> and <strong>why</strong> a target is down.</p>
        <div class="space-y-2 mb-4">
          <p class="font-semibold text-xs uppercase tracking-wide text-gray-400">Scope — how many nodes agree?</p>
          <div class="flex gap-3 p-3 bg-red-50 dark:bg-red-900/20 rounded-xl">
            <span class="w-28 font-bold text-red-600 text-xs flex-shrink-0 mt-0.5">GLOBAL</span>
            <p>All cluster nodes see this target as DOWN. Definitive outage.</p>
          </div>
          <div class="flex gap-3 p-3 bg-orange-50 dark:bg-orange-900/20 rounded-xl">
            <span class="w-28 font-bold text-orange-600 text-xs flex-shrink-0 mt-0.5">PARTIAL</span>
            <p>Some nodes see DOWN, others see UP. Ambiguous — could be the service, could be network. Look at the by-node breakdown to see which nodes disagree.</p>
          </div>
          <div class="flex gap-3 p-3 bg-yellow-50 dark:bg-yellow-900/20 rounded-xl">
            <span class="w-28 font-bold text-yellow-600 text-xs flex-shrink-0 mt-0.5">NODE_LOCAL</span>
            <p>Only <strong>this</strong> node sees DOWN, all others see UP. Very likely a local network issue on that monitoring node, not a real service outage.</p>
          </div>
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-bold text-gray-500 text-xs flex-shrink-0 mt-0.5">STANDALONE</span>
            <p>Cluster mode disabled, or only one node available. The node's local view is the only view.</p>
          </div>
        </div>
        <div class="space-y-2">
          <p class="font-semibold text-xs uppercase tracking-wide text-gray-400">Classification — why is it down?</p>
          <div class="flex gap-3 p-3 bg-red-50 dark:bg-red-900/20 rounded-xl">
            <span class="w-36 font-bold text-red-600 text-xs flex-shrink-0 mt-0.5">REAL_OUTAGE</span>
            <p>All nodes agree it's down and none of the monitoring nodes are offline. High confidence this is a real service failure. Page someone.</p>
          </div>
          <div class="flex gap-3 p-3 bg-orange-50 dark:bg-orange-900/20 rounded-xl">
            <span class="w-36 font-bold text-orange-600 text-xs flex-shrink-0 mt-0.5">NETWORK_PARTITION</span>
            <p>Some nodes see it down, some don't. The disagreement suggests a network path issue — either between monitoring nodes and the target, or between monitoring nodes themselves. Check node-level connectivity. <strong>This was what you saw in the screenshot</strong> — node-3 could reach db-primary but node-1 and node-2 couldn't.</p>
          </div>
          <div class="flex gap-3 p-3 bg-yellow-50 dark:bg-yellow-900/20 rounded-xl">
            <span class="w-36 font-bold text-yellow-600 text-xs flex-shrink-0 mt-0.5">LOCAL_FAILURE</span>
            <p>Only one monitoring node sees the failure. Likely a misconfiguration or network issue on <em>that specific monitoring node</em>, not the target service.</p>
          </div>
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-36 font-bold text-gray-500 text-xs flex-shrink-0 mt-0.5">AMBIGUOUS</span>
            <p>Not enough data to classify. Happens during early startup, single-node clusters, or when some monitoring nodes are also unreachable.</p>
          </div>
        </div>
        <div class="mt-3 flex gap-3 p-3 bg-blue-50 dark:bg-blue-900/20 rounded-xl">
          <span class="w-28 font-semibold text-xs text-blue-600 flex-shrink-0 mt-0.5">Confidence</span>
          <p>A 0–100% score indicating how sure the algorithm is about its classification. Low confidence → check manually. High confidence → trust the verdict.</p>
        </div>
      </section>

      <!-- SLO -->
      <section :id="sections[6]">
        <h2 class="text-lg font-bold text-gray-900 dark:text-white mb-3">SLO Dashboard</h2>
        <div class="space-y-2">
          <p>SLO = <strong>Service Level Objective</strong>. A formal reliability promise: "this service will be available X% of the time over a rolling N-day window."</p>
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Target Uptime</span>
            <p>The promised uptime percentage. e.g., 99.9% = max 43.8 minutes downtime per 30 days.</p>
          </div>
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Actual Uptime</span>
            <p>What was actually observed in the measurement window. Calculated from hard_down incidents recorded during that window.</p>
          </div>
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Error Budget</span>
            <p>How much downtime is left before breaching the SLO. Positive = still within budget. Negative = SLO already breached. e.g., 43m budget − 20m actual downtime = 23m remaining.</p>
          </div>
          <div class="flex gap-3 p-3 bg-red-50 dark:bg-red-900/20 rounded-xl">
            <span class="w-28 font-semibold text-xs text-red-600 flex-shrink-0 mt-0.5">SLO Breach</span>
            <p>Error budget exhausted. An alert is sent (once per window, edge-triggered). The breach badge stays until the window rolls and performance recovers.</p>
          </div>
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Incidents</span>
            <p>Continuous hard_down periods counted within the SLO window. Each incident shows start time, end time (or "ongoing"), and duration.</p>
          </div>
          <div class="flex gap-3 p-3 bg-blue-50 dark:bg-blue-900/20 rounded-xl">
            <span class="w-28 font-semibold text-xs text-blue-600 flex-shrink-0 mt-0.5">Configuration</span>
            <p>SLO targets are defined in <code>config.yaml</code> under <code>slo.targets</code>. Each entry needs <code>id</code>, <code>target_uptime</code> (0.0–1.0), and <code>window</code> (24h / 7d / 30d). See docs for planned edit UI.</p>
          </div>
        </div>
      </section>

      <!-- MAINTENANCE -->
      <section :id="sections[7]">
        <h2 class="text-lg font-bold text-gray-900 dark:text-white mb-3">Maintenance Windows</h2>
        <p class="mb-3">Suppress alerts for a target during planned downtime. Windows are replicated to all cluster nodes via gossip — you only need to create it on one node.</p>
        <div class="space-y-2">
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Creating</span>
            <p>Click "+ New Window", enter the target ID (e.g., <code>db-primary</code>), choose duration, add a reason. The window starts immediately and is replicated within seconds.</p>
          </div>
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">During window</span>
            <p>Probes continue (you still see the current state). Only alert dispatch is suppressed. SLO downtime is still counted — maintenance doesn't pause SLO tracking.</p>
          </div>
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Cancelling</span>
            <p>Click the Cancel button on an active window. Alerts resume immediately after cancellation. A confirm dialog prevents accidental cancellation.</p>
          </div>
        </div>
      </section>

      <!-- ALERTS -->
      <section :id="sections[8]">
        <h2 class="text-lg font-bold text-gray-900 dark:text-white mb-3">Alert Feed</h2>
        <div class="bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-yellow-800 rounded-xl p-4 mb-3">
          <p class="font-semibold mb-1">⚠ Client-side only (current version)</p>
          <p>The alert feed detects state changes while <strong>the UI tab is open</strong>. It polls <code>/fleet/status</code> every 5 seconds and shows transitions (UP→DOWN, DOWN→UP). If you close and reopen the tab, history is lost. Persistent alert history is planned (B7).</p>
        </div>
        <div class="space-y-2">
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Each entry</span>
            <p>Shows target name, DOWN/UP transition, scope, classification, error code, affected apps, and timestamp. Sequence number (seq) tracks how many state transitions this target has had.</p>
          </div>
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Max entries</span>
            <p>Ring buffer capped at 100 entries per session. Oldest entries are dropped automatically.</p>
          </div>
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Actual alerts</span>
            <p>Real alert delivery (email, webhook, scripts) happens in the backend — independent of whether the UI is open. The UI feed is for visual monitoring during incidents, not for paging.</p>
          </div>
        </div>
      </section>

      <!-- CONFIG -->
      <section :id="sections[9]">
        <h2 class="text-lg font-bold text-gray-900 dark:text-white mb-3">Config Management</h2>
        <div class="space-y-2">
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Config Sync</span>
            <p>Shows each node's config SHA-256 hash. "In sync" means all nodes have identical configs. If drift is detected, use "Sync to peers" to push this node's shared config to all others.</p>
          </div>
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Push Config</span>
            <p>Update shared fields (timeouts, intervals, notification channels) across all nodes in one API call. Node-specific fields (port, targets, cluster.node_name) are never overwritten.</p>
          </div>
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Keyring Rotate</span>
            <p>Zero-downtime AES key rotation: (1) Add new key, (2) Make it primary (all nodes now encrypt with new key, can decrypt both), (3) Remove old key. The cluster stays encrypted throughout.</p>
          </div>
        </div>
      </section>

      <!-- GEO -->
      <section :id="sections[10]">
        <h2 class="text-lg font-bold text-gray-900 dark:text-white mb-3">Geo Latency</h2>
        <p class="mb-3">Each cluster node probes from a different zone or region. Geo Latency shows the probe round-trip time from each zone to each target.</p>
        <div class="space-y-2">
          <div class="flex gap-3 p-3 bg-gray-50 dark:bg-gray-800 rounded-xl">
            <span class="w-28 font-semibold text-xs text-gray-500 flex-shrink-0 mt-0.5">Use case</span>
            <p>If eu-west-1a takes 5ms but eu-west-1c takes 280ms to reach api-gateway, there's a regional network issue — not a service outage. Geo Latency is how you tell the difference.</p>
          </div>
          <div class="flex gap-3 p-3 bg-orange-50 dark:bg-orange-900/20 rounded-xl">
            <span class="w-28 font-semibold text-xs text-orange-600 flex-shrink-0 mt-0.5">Anomaly</span>
            <p>Flagged when any node's latency is more than 3× the minimum observed latency across all nodes. This indicates a significant regional discrepancy worth investigating.</p>
          </div>
        </div>
      </section>

      <div class="border-t border-gray-200 dark:border-gray-700 pt-4 text-xs text-gray-400">
        netwatch docs — v{{ $config.public.version ?? 'dev' }}. This page is auto-served from the UI, no external hosting needed.
      </div>
    </article>
  </div>
</template>
