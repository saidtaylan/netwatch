package cluster

// configpush.go — Gossip-based shared config distribution.
//
// Two roles:
//   - Sender  (BroadcastConfigPush): iterates alive members, sends the
//             ConfigPushPayload to each via SendReliable (TCP, AES-encrypted).
//             Returns a per-node map of send errors.
//   - Receiver (NotifyMsg): when msg_type == "config_push", calls the
//             registered ConfigPushHandler so the engine can merge and persist
//             the incoming shared fields.
//
// The gossip message is fire-and-deliver: the sender knows whether TCP
// delivery succeeded but not whether the peer applied it successfully.
// Apply errors are logged on the receiving node.

import (
	"encoding/json"
	"log/slog"
	"time"
)

const msgTypeConfigPush = "config_push"

// ConfigPushPayload is the gossip message for distributing shared config.
// It is identified by MsgType == "config_push" in NotifyMsg, distinct from
// the existing "config" (hash-only drift detection) and state payloads.
type ConfigPushPayload struct {
	// MsgType is always "config_push".
	MsgType string `json:"msg_type"`

	// FromNode is the node_name of the originator.
	FromNode string `json:"from_node"`

	// SharedConfig is the JSON-encoded engine.SharedConfig payload.
	// Kept as json.RawMessage so the cluster package does not import engine.
	SharedConfig json.RawMessage `json:"shared_config"`

	PushedAt time.Time `json:"pushed_at"`
}

// ConfigPushHandler is implemented by the engine to receive and apply an
// incoming shared config payload. The raw argument is the JSON-encoded
// engine.SharedConfig from a peer node.
type ConfigPushHandler interface {
	ApplySharedConfigJSON(raw json.RawMessage) error
}

// ── Manager wiring ────────────────────────────────────────────────────────────

// SetConfigPushHandler registers the engine callback invoked when this node
// receives a config_push gossip message from a peer. Must be called before
// the cluster joins; safe to call before New() returns.
func (m *Manager) SetConfigPushHandler(h ConfigPushHandler) {
	m.mu.Lock()
	m.configPushHandler = h
	m.mu.Unlock()
}

// ── Sender ────────────────────────────────────────────────────────────────────

// BroadcastConfigPush sends sharedConfigJSON to every alive cluster member
// (excluding this node) via TCP SendReliable. Returns a map of node names to
// send errors; absent entries indicate success.
func (m *Manager) BroadcastConfigPush(sharedConfigJSON json.RawMessage) map[string]error {
	payload := ConfigPushPayload{
		MsgType:      msgTypeConfigPush,
		FromNode:     m.cfg.NodeName,
		SharedConfig: sharedConfigJSON,
		PushedAt:     time.Now(),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return map[string]error{"marshal": err}
	}

	local := m.list.LocalNode()
	results := make(map[string]error)
	for _, member := range m.list.Members() {
		if member.Name == local.Name {
			continue
		}
		if err := m.list.SendReliable(member, data); err != nil {
			slog.Warn("[CONFIG-PUSH] delivery failed", "to", member.Name, "err", err)
			results[member.Name] = err
		} else {
			results[member.Name] = nil
		}
	}
	return results
}

// handleConfigPush is called from NotifyMsg when msg_type == "config_push".
func (m *Manager) handleConfigPush(data []byte) {
	var p ConfigPushPayload
	if err := json.Unmarshal(data, &p); err != nil {
		slog.Warn("[CONFIG-PUSH] unmarshal failed", "err", err)
		return
	}
	if p.FromNode == m.cfg.NodeName {
		return // ignore own broadcasts (shouldn't happen via SendReliable, but be safe)
	}

	m.mu.RLock()
	h := m.configPushHandler
	m.mu.RUnlock()

	if h == nil {
		slog.Debug("[CONFIG-PUSH] no handler registered, ignoring")
		return
	}

	slog.Info("[CONFIG-PUSH] received shared config from peer", "from", p.FromNode)
	if err := h.ApplySharedConfigJSON(p.SharedConfig); err != nil {
		slog.Error("[CONFIG-PUSH] apply failed", "from", p.FromNode, "err", err)
	}
}
