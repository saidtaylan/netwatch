package engine

// channels_manager.go — storage-backed notification channel registry (B24.4).
//
// Notification channels (script/mail/webhook Alerters) now live in
// storage.TableNotifChannels, cluster-replicated via gossip.
//
// Storage holds the *config* for each channel (AlertChannelConfig: type +
// parameters). The runtime Alerter instance is rebuilt from that config
// every time the registry changes, via newAlertChannel(name, cfg, runner).
//
// The runner (ShellRunner / PowerShellRunner) is platform-dependent and
// supplied by the engine — it is not stored in the DB.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/saidtaylan/netwatch/internal/storage"
	"github.com/saidtaylan/netwatch/internal/storage/gossip"
)

// channelsManager owns the storage-backed channel registry and pushes a
// rebuilt map[string]Alerter into the engine whenever it changes.
//
// On every change, the manager calls newAlertChannel to instantiate the
// Alerter. If instantiation fails (e.g. webhook URL invalid), the bad
// channel is skipped with a warning — the rest of the registry stays
// functional. The engine's existing config validation pipeline still
// rejects bad channels at config-load time; this is the runtime safety
// net for peer broadcasts that arrive with malformed configs.
type channelsManager struct {
	mu sync.RWMutex

	storage  *gossip.Storage
	nodeName string
	runner   AlertRunner

	// channels holds the live registry; key = channel name. Storage
	// payload is AlertChannelConfig; the Alerter is rebuilt on demand.
	cfgs map[string]AlertChannelConfig

	publisher func(channels map[string]Alerter)

	watchCancel context.CancelFunc
}

// newChannelsManager constructs a storage-backed channel manager.
//
// seedChannels: first-boot migration from config.yaml. Written to DB
// once when the table is empty.
//
// publisher: invoked with the freshly-built map[string]Alerter whenever
// the registry changes. The engine uses this to keep e.channels in sync.
func newChannelsManager(
	parent context.Context,
	gs *gossip.Storage,
	nodeName string,
	runner AlertRunner,
	seedChannels map[string]AlertChannelConfig,
	publisher func(channels map[string]Alerter),
) (*channelsManager, error) {
	if gs == nil {
		return nil, fmt.Errorf("channels: nil storage")
	}
	ctx, cancel := context.WithCancel(parent)
	m := &channelsManager{
		storage:     gs,
		nodeName:    nodeName,
		runner:      runner,
		cfgs:        make(map[string]AlertChannelConfig),
		publisher:   publisher,
		watchCancel: cancel,
	}

	if err := m.loadFromStorage(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("channels: initial load: %w", err)
	}

	if len(m.cfgs) == 0 && len(seedChannels) > 0 {
		slog.Info("channels: seeding from config.yaml", "count", len(seedChannels))
		for name, c := range seedChannels {
			if err := m.upsertNamed(name, c); err != nil {
				slog.Warn("channels: seed upsert failed", "name", name, "err", err)
			}
		}
	}

	m.publishRebuild()

	go m.watchLoop(ctx)
	return m, nil
}

// Close stops the Watch goroutine. Safe to call multiple times.
func (m *channelsManager) Close() {
	if m.watchCancel != nil {
		m.watchCancel()
	}
}

// Channels returns a snapshot of the current channel configurations
// (NOT the live Alerter instances — those are owned by the engine).
func (m *channelsManager) Channels() map[string]AlertChannelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]AlertChannelConfig, len(m.cfgs))
	for k, v := range m.cfgs {
		out[k] = v
	}
	return out
}

// Get returns the channel config by name, or (zero, false).
func (m *channelsManager) Get(name string) (AlertChannelConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.cfgs[name]
	return c, ok
}

// Upsert adds or replaces a channel. Storage-backed; cluster-replicated.
//
// Validates the config by attempting to instantiate the Alerter once
// before writing — invalid channels never enter the DB, so peers don't
// receive malformed configs.
func (m *channelsManager) Upsert(name string, c AlertChannelConfig) error {
	if name == "" {
		return fmt.Errorf("channels: empty name")
	}
	// Validate locally before persisting.
	if _, err := newAlertChannel(name, c, m.runner); err != nil {
		return fmt.Errorf("channels: invalid config for %q: %w", name, err)
	}
	if err := m.upsertNamed(name, c); err != nil {
		return err
	}
	m.publishRebuild()
	return nil
}

// upsertNamed performs the storage upsert + cache update. Separate so the
// seed path can reuse it without re-validating each entry (config already
// passed through buildAlertChannels at load time).
func (m *channelsManager) upsertNamed(name string, c AlertChannelConfig) error {
	payload, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("channels: marshal: %w", err)
	}
	ver := m.storage.NextVersion()
	if err := m.storage.Upsert(context.Background(),
		storage.TableNotifChannels, name, payload, ver); err != nil {
		return fmt.Errorf("channels: storage upsert: %w", err)
	}
	m.mu.Lock()
	m.cfgs[name] = c
	m.mu.Unlock()
	return nil
}

// Delete removes a channel. Tombstone is gossip-replicated.
func (m *channelsManager) Delete(name string) (bool, error) {
	m.mu.RLock()
	_, exists := m.cfgs[name]
	m.mu.RUnlock()
	if !exists {
		return false, nil
	}
	ver := m.storage.NextVersion()
	if err := m.storage.Delete(context.Background(),
		storage.TableNotifChannels, name, ver); err != nil {
		return false, fmt.Errorf("channels: storage delete: %w", err)
	}
	m.mu.Lock()
	delete(m.cfgs, name)
	m.mu.Unlock()
	m.publishRebuild()
	return true, nil
}

// ── internal: storage + index ─────────────────────────────────────────

func (m *channelsManager) loadFromStorage(ctx context.Context) error {
	recs, err := m.storage.List(ctx, storage.TableNotifChannels, storage.Filter{})
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rec := range recs {
		if rec.Tombstone {
			continue
		}
		var c AlertChannelConfig
		if err := json.Unmarshal(rec.Payload, &c); err != nil {
			slog.Warn("channels: malformed record in storage", "id", rec.ID, "err", err)
			continue
		}
		m.cfgs[rec.ID] = c
	}
	if n := len(m.cfgs); n > 0 {
		slog.Info("channels: loaded from storage", "count", n)
	}
	return nil
}

func (m *channelsManager) watchLoop(ctx context.Context) {
	ch, err := m.storage.Watch(ctx, storage.TableNotifChannels)
	if err != nil {
		slog.Warn("channels: watch failed", "err", err)
		return
	}
	for evt := range ch {
		switch evt.Type {
		case storage.EventUpsert:
			var c AlertChannelConfig
			if err := json.Unmarshal(evt.Record.Payload, &c); err != nil {
				slog.Warn("channels: watch unmarshal failed", "id", evt.Record.ID, "err", err)
				continue
			}
			m.mu.Lock()
			m.cfgs[evt.Record.ID] = c
			m.mu.Unlock()
		case storage.EventDelete:
			m.mu.Lock()
			delete(m.cfgs, evt.Record.ID)
			m.mu.Unlock()
		}
		m.publishRebuild()
	}
}

// publishRebuild instantiates the Alerter for every cfg and pushes the
// fresh map[string]Alerter into the engine via the publisher callback.
//
// Channels that fail to instantiate are skipped with a warning — the
// rest of the registry stays functional. This is the runtime safety net
// for malformed configs received via peer broadcast.
func (m *channelsManager) publishRebuild() {
	if m.publisher == nil {
		return
	}
	m.mu.RLock()
	snap := make(map[string]AlertChannelConfig, len(m.cfgs))
	for k, v := range m.cfgs {
		snap[k] = v
	}
	m.mu.RUnlock()

	out := make(map[string]Alerter, len(snap))
	for name, c := range snap {
		ch, err := newAlertChannel(name, c, m.runner)
		if err != nil {
			slog.Warn("channels: skipping invalid runtime config", "name", name, "err", err)
			continue
		}
		out[name] = ch
	}
	m.publisher(out)
}
