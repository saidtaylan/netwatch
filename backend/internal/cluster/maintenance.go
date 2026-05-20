package cluster

// maintenance.go — Gossip propagation for maintenance window set/cancel actions.
//
// When an operator calls PUT /cluster/maintenance on any node, the receiving
// node:
//   1. Applies the window locally (RAM + disk)
//   2. Broadcasts a MaintenanceBroadcast to all peers via SendReliable (TCP)
//
// Each peer receives the broadcast via NotifyMsg and calls the registered
// MaintenanceHandler to apply it locally.

import (
	"encoding/json"
	"log/slog"
	"time"
)

const msgTypeMaintenance = "maintenance"

// MaintenanceBroadcast is the gossip payload for maintenance set/cancel.
type MaintenanceBroadcast struct {
	MsgType string `json:"msg_type"` // always "maintenance"

	// Action is either "set" or "cancel".
	Action string `json:"action"`

	// Window is set when Action == "set".
	Window *MaintenanceWindowPayload `json:"window,omitempty"`

	// CancelID is set when Action == "cancel".
	CancelID string `json:"cancel_id,omitempty"`

	OriginNode string    `json:"origin_node"`
	Timestamp  time.Time `json:"timestamp"`
}

// MaintenanceWindowPayload is the wire format for a maintenance window.
// It mirrors engine.MaintenanceWindow but lives here to avoid import cycles.
type MaintenanceWindowPayload struct {
	ID        string    `json:"id"`
	TargetIDs []string  `json:"target_ids"`
	StartedAt time.Time `json:"started_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Reason    string    `json:"reason,omitempty"`
	StartedBy string    `json:"started_by,omitempty"`
}

// MaintenanceHandler is implemented by the engine to apply incoming maintenance
// broadcasts. The cluster package does not import engine — it calls back via this interface.
type MaintenanceHandler interface {
	ApplyMaintenanceSet(w MaintenanceWindowPayload) error
	ApplyMaintenanceCancel(id string) error
}

// SetMaintenanceHandler registers the handler called when a maintenance gossip
// message arrives.
func (m *Manager) SetMaintenanceHandler(h MaintenanceHandler) {
	m.mu.Lock()
	m.maintenanceHandler = h
	m.mu.Unlock()
}

// BroadcastMaintenanceSet sends a "set" action to all cluster peers via TCP.
func (m *Manager) BroadcastMaintenanceSet(w MaintenanceWindowPayload) {
	msg := MaintenanceBroadcast{
		MsgType:    msgTypeMaintenance,
		Action:     "set",
		Window:     &w,
		OriginNode: m.cfg.NodeName,
		Timestamp:  time.Now(),
	}
	m.broadcastMaintenance(msg)
}

// BroadcastMaintenanceCancel sends a "cancel" action to all cluster peers.
func (m *Manager) BroadcastMaintenanceCancel(id string) {
	msg := MaintenanceBroadcast{
		MsgType:    msgTypeMaintenance,
		Action:     "cancel",
		CancelID:   id,
		OriginNode: m.cfg.NodeName,
		Timestamp:  time.Now(),
	}
	m.broadcastMaintenance(msg)
}

func (m *Manager) broadcastMaintenance(msg MaintenanceBroadcast) {
	data, err := json.Marshal(msg)
	if err != nil {
		slog.Error("[MAINTENANCE] marshal failed", "err", err)
		return
	}
	local := m.list.LocalNode()
	for _, member := range m.list.Members() {
		if member.Name == local.Name {
			continue
		}
		if err := m.list.SendReliable(member, data); err != nil {
			slog.Warn("[MAINTENANCE] delivery failed", "to", member.Name, "err", err)
		}
	}
}

// handleMaintenance is called from NotifyMsg when msg_type == "maintenance".
func (m *Manager) handleMaintenance(data []byte) {
	var msg MaintenanceBroadcast
	if err := json.Unmarshal(data, &msg); err != nil {
		slog.Warn("[MAINTENANCE] unmarshal failed", "err", err)
		return
	}
	if msg.OriginNode == m.cfg.NodeName {
		return // ignore own broadcasts
	}

	m.mu.RLock()
	h := m.maintenanceHandler
	m.mu.RUnlock()

	if h == nil {
		return
	}

	switch msg.Action {
	case "set":
		if msg.Window == nil {
			slog.Warn("[MAINTENANCE] set action missing window")
			return
		}
		slog.Info("[MAINTENANCE] received set from peer", "from", msg.OriginNode, "id", msg.Window.ID)
		if err := h.ApplyMaintenanceSet(*msg.Window); err != nil {
			slog.Error("[MAINTENANCE] apply set failed", "err", err)
		}
	case "cancel":
		if msg.CancelID == "" {
			slog.Warn("[MAINTENANCE] cancel action missing id")
			return
		}
		slog.Info("[MAINTENANCE] received cancel from peer", "from", msg.OriginNode, "id", msg.CancelID)
		if err := h.ApplyMaintenanceCancel(msg.CancelID); err != nil {
			slog.Error("[MAINTENANCE] apply cancel failed", "err", err)
		}
	default:
		slog.Warn("[MAINTENANCE] unknown action", "action", msg.Action)
	}
}
