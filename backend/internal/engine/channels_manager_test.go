package engine

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/saidtaylan/netwatch/internal/storage"
	"github.com/saidtaylan/netwatch/internal/storage/gossip"
)

// capturedChannels captures the latest map[string]Alerter handed to the
// engine's setChannels callback.
type capturedChannels struct {
	mu  sync.Mutex
	out map[string]Alerter
}

func (c *capturedChannels) publish(m map[string]Alerter) {
	c.mu.Lock()
	c.out = m
	c.mu.Unlock()
}

func (c *capturedChannels) snapshot() map[string]Alerter {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]Alerter, len(c.out))
	for k, v := range c.out {
		out[k] = v
	}
	return out
}

// noopRunner is an AlertRunner that does nothing (used so newAlertChannel
// accepts script-type channels in tests).
func noopRunner(string, map[string]string) error { return nil }

func makeChannelsManager(t *testing.T, seed map[string]AlertChannelConfig) (*channelsManager, *capturedChannels) {
	t.Helper()
	mem := storage.NewMemoryStorage()
	t.Cleanup(func() { _ = mem.Close() })
	gs := gossip.NewStorage(mem, nil, nil, "test-node")
	cap := &capturedChannels{}
	cm, err := newChannelsManager(context.Background(), gs, "test-node",
		AlertRunner(noopRunner), seed, cap.publish)
	if err != nil {
		t.Fatalf("newChannelsManager: %v", err)
	}
	t.Cleanup(cm.Close)
	return cm, cap
}

func TestChannels_SeedFromConfig(t *testing.T) {
	seed := map[string]AlertChannelConfig{
		"ops": {Type: "script", Parameters: map[string]string{"script": "ops"}},
		"dba": {Type: "script", Parameters: map[string]string{"script": "dba"}},
	}
	_, cap := makeChannelsManager(t, seed)
	snap := cap.snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(snap))
	}
	if _, ok := snap["ops"]; !ok {
		t.Error("expected 'ops' channel")
	}
}

func TestChannels_Upsert(t *testing.T) {
	cm, cap := makeChannelsManager(t, nil)

	err := cm.Upsert("ops", AlertChannelConfig{
		Type:       "script",
		Parameters: map[string]string{"script": "ops"},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, ok := cap.snapshot()["ops"]; !ok {
		t.Error("expected 'ops' in rebuilt channels")
	}
}

func TestChannels_Upsert_RejectsInvalid(t *testing.T) {
	cm, _ := makeChannelsManager(t, nil)
	err := cm.Upsert("bad", AlertChannelConfig{Type: "nonsense-type"})
	if err == nil {
		t.Fatal("expected invalid channel type to be rejected")
	}
	if _, ok := cm.Get("bad"); ok {
		t.Error("invalid channel should not have been stored")
	}
}

func TestChannels_Delete(t *testing.T) {
	cm, cap := makeChannelsManager(t, map[string]AlertChannelConfig{
		"ops": {Type: "script", Parameters: map[string]string{"script": "ops"}},
	})
	if _, ok := cap.snapshot()["ops"]; !ok {
		t.Fatal("setup precondition failed")
	}

	ok, err := cm.Delete("ops")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !ok {
		t.Error("expected delete to return true for existing channel")
	}
	if _, ok := cap.snapshot()["ops"]; ok {
		t.Error("expected 'ops' to be removed from channels map")
	}

	ok, _ = cm.Delete("ops")
	if ok {
		t.Error("second delete should return false")
	}
}

func TestChannels_PersistAcrossInstances(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	gs := gossip.NewStorage(mem, nil, nil, "node-A")

	cm1, err := newChannelsManager(context.Background(), gs, "node-A",
		AlertRunner(noopRunner), nil, func(map[string]Alerter) {})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := cm1.Upsert("ops", AlertChannelConfig{
		Type:       "script",
		Parameters: map[string]string{"script": "ops"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	cm1.Close()

	cap := &capturedChannels{}
	cm2, err := newChannelsManager(context.Background(), gs, "node-A",
		AlertRunner(noopRunner), nil, cap.publish)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	defer cm2.Close()
	if _, ok := cap.snapshot()["ops"]; !ok {
		t.Error("second manager should load persisted 'ops' channel")
	}
}

func TestChannels_SplitBrainBlocksWrites(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	isolated := &flagChecker{value: true}
	gs := gossip.NewStorage(mem, isolated, nil, "node-1")

	cm, err := newChannelsManager(context.Background(), gs, "node-1",
		AlertRunner(noopRunner), nil, func(map[string]Alerter) {})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	defer cm.Close()

	err = cm.Upsert("blocked", AlertChannelConfig{
		Type:       "script",
		Parameters: map[string]string{"script": "x"},
	})
	if !errors.Is(err, storage.ErrSplitBrain) {
		t.Errorf("expected ErrSplitBrain, got %v", err)
	}
}
