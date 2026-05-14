package cluster

// Test helpers — exported utilities for use in other packages' test files.
// These are NOT intended for use in production code.

// NewTestManager creates a Manager with a pre-seeded ring and empty peerStates,
// suitable for unit tests that need to exercise hash ring and scope logic without
// starting a real memberlist instance.
func NewTestManager(nodeName string, ring []string) *Manager {
	ringCopy := make([]string, len(ring))
	copy(ringCopy, ring)
	return &Manager{
		cfg:        Config{NodeName: nodeName},
		ring:       ringCopy,
		peerStates: make(map[string]map[string]GossipPayload),
	}
}

// SetIsolated directly flips the isolated flag on a Manager.
// Used in unit tests to simulate quorum-lost / quorum-recovered transitions.
func (m *Manager) SetIsolated(v bool) {
	m.isolated.Store(v)
}

// SetPeerState injects a GossipPayload into peerStates for the given
// (nodeName, targetID) pair. Used in unit tests to simulate gossip arrivals
// without running real network code.
func (m *Manager) SetPeerState(nodeName, targetID string, p GossipPayload) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.peerStates[nodeName] == nil {
		m.peerStates[nodeName] = make(map[string]GossipPayload)
	}
	m.peerStates[nodeName][targetID] = p
}

// SetTestAliveSet marks the given node names as alive for aliveSet().
// Use this in unit tests that need multi-node membership without booting a
// real memberlist instance. Passing no names clears the override and falls
// back to the default (m.list-based) behaviour.
func (m *Manager) SetTestAliveSet(names ...string) {
	if len(names) == 0 {
		m.testAliveOverride = nil
		return
	}
	m.testAliveOverride = make(map[string]bool, len(names))
	for _, n := range names {
		m.testAliveOverride[n] = true
	}
}

// SetTestZones installs a per-node zone override consulted by zoneOf().
// Use this in unit tests to drive zone-aware prober selection without
// memberlist NodeMeta. Passing a nil map clears the override.
func (m *Manager) SetTestZones(zones map[string]string) {
	if zones == nil {
		m.testZoneOverride = nil
		return
	}
	clone := make(map[string]string, len(zones))
	for k, v := range zones {
		clone[k] = v
	}
	m.testZoneOverride = clone
}

// SetTestRegions installs a per-node region override consulted by regionOf()
// (P1.6). Use this in unit tests to drive geo-latency logic without memberlist
// NodeMeta. Passing a nil map clears the override.
func (m *Manager) SetTestRegions(regions map[string]string) {
	if regions == nil {
		m.testRegionOverride = nil
		return
	}
	clone := make(map[string]string, len(regions))
	for k, v := range regions {
		clone[k] = v
	}
	m.testRegionOverride = clone
}
