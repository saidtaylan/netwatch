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
