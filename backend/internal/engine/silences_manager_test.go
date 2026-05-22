package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/saidtaylan/netwatch/internal/storage"
	"github.com/saidtaylan/netwatch/internal/storage/gossip"
)

func makeSilencesManager(t *testing.T) *silencesManager {
	t.Helper()
	mem := storage.NewMemoryStorage()
	t.Cleanup(func() { _ = mem.Close() })
	gs := gossip.NewStorage(mem, nil, nil, "test-node")
	m, err := newSilencesManager(context.Background(), gs, "test-node")
	if err != nil {
		t.Fatalf("newSilencesManager: %v", err)
	}
	t.Cleanup(m.Close)
	return m
}

func TestSilences_ExactNameMatch(t *testing.T) {
	m := makeSilencesManager(t)
	if err := m.Set(Silence{
		ID:        "sil-1",
		Matchers:  []SilenceMatcher{{Field: "name", Value: "db-primary"}},
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	if !m.IsSilenced(Target{Name: "db-primary"}) {
		t.Error("expected db-primary to be silenced")
	}
	if m.IsSilenced(Target{Name: "api"}) {
		t.Error("api should NOT be silenced")
	}
}

func TestSilences_RegexNameMatch(t *testing.T) {
	m := makeSilencesManager(t)
	if err := m.Set(Silence{
		ID:        "sil-regex",
		Matchers:  []SilenceMatcher{{Field: "name", Value: "^payments-.*$", IsRegex: true}},
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	if !m.IsSilenced(Target{Name: "payments-api"}) {
		t.Error("payments-api should be silenced by regex")
	}
	if !m.IsSilenced(Target{Name: "payments-db"}) {
		t.Error("payments-db should be silenced by regex")
	}
	if m.IsSilenced(Target{Name: "checkout"}) {
		t.Error("checkout should NOT be silenced")
	}
}

func TestSilences_TypeMatch(t *testing.T) {
	m := makeSilencesManager(t)
	if err := m.Set(Silence{
		ID:        "sil-tcp",
		Matchers:  []SilenceMatcher{{Field: "type", Value: "tcp"}},
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	if !m.IsSilenced(Target{Name: "x", Type: "tcp"}) {
		t.Error("tcp target should be silenced")
	}
	if m.IsSilenced(Target{Name: "y", Type: "http"}) {
		t.Error("http target should NOT be silenced")
	}
}

func TestSilences_ANDSemantics(t *testing.T) {
	m := makeSilencesManager(t)
	// Silence: type=tcp AND name matches "db-*"
	if err := m.Set(Silence{
		ID: "sil-and",
		Matchers: []SilenceMatcher{
			{Field: "type", Value: "tcp"},
			{Field: "name", Value: "^db-.*$", IsRegex: true},
		},
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	if !m.IsSilenced(Target{Name: "db-primary", Type: "tcp"}) {
		t.Error("tcp db-primary must match BOTH matchers")
	}
	if m.IsSilenced(Target{Name: "db-primary", Type: "http"}) {
		t.Error("http db-primary should NOT match (type mismatch)")
	}
	if m.IsSilenced(Target{Name: "api", Type: "tcp"}) {
		t.Error("tcp api should NOT match (name mismatch)")
	}
}

func TestSilences_ExpiredIgnored(t *testing.T) {
	m := makeSilencesManager(t)
	if err := m.Set(Silence{
		ID:        "sil-expired",
		Matchers:  []SilenceMatcher{{Field: "name", Value: "x"}},
		ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if m.IsSilenced(Target{Name: "x"}) {
		t.Error("expired silence should be ignored")
	}
}

func TestSilences_Cancel(t *testing.T) {
	m := makeSilencesManager(t)
	_ = m.Set(Silence{
		ID:        "sil-c",
		Matchers:  []SilenceMatcher{{Field: "name", Value: "y"}},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if !m.IsSilenced(Target{Name: "y"}) {
		t.Fatal("setup precondition failed")
	}
	if err := m.Cancel("sil-c"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if m.IsSilenced(Target{Name: "y"}) {
		t.Error("y should not be silenced after cancel")
	}
}

func TestSilences_ValidationRejectsBadInput(t *testing.T) {
	m := makeSilencesManager(t)

	cases := []struct {
		name string
		s    Silence
	}{
		{"empty matchers", Silence{ID: "x", ExpiresAt: time.Now().Add(time.Hour)}},
		{"unknown field", Silence{ID: "x", Matchers: []SilenceMatcher{{Field: "bogus", Value: "y"}}, ExpiresAt: time.Now().Add(time.Hour)}},
		{"empty value", Silence{ID: "x", Matchers: []SilenceMatcher{{Field: "name", Value: ""}}, ExpiresAt: time.Now().Add(time.Hour)}},
		{"bad regex", Silence{ID: "x", Matchers: []SilenceMatcher{{Field: "name", Value: "(((", IsRegex: true}}, ExpiresAt: time.Now().Add(time.Hour)}},
		{"empty ID", Silence{Matchers: []SilenceMatcher{{Field: "name", Value: "y"}}, ExpiresAt: time.Now().Add(time.Hour)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := m.Set(c.s); err == nil {
				t.Errorf("expected error for %s, got nil", c.name)
			}
		})
	}
}

func TestSilences_List(t *testing.T) {
	m := makeSilencesManager(t)
	_ = m.Set(Silence{
		ID:        "a",
		Matchers:  []SilenceMatcher{{Field: "name", Value: "x"}},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	_ = m.Set(Silence{
		ID:        "b",
		Matchers:  []SilenceMatcher{{Field: "name", Value: "y"}},
		ExpiresAt: time.Now().Add(-time.Minute), // expired — should not show
	})
	got := m.List()
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("expected only active silence 'a', got %+v", got)
	}
}

func TestSilences_PruneExpired(t *testing.T) {
	m := makeSilencesManager(t)
	_ = m.Set(Silence{
		ID:        "live",
		Matchers:  []SilenceMatcher{{Field: "name", Value: "x"}},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	// Inject an expired one directly via the storage path (bypassing Set's
	// validation, which doesn't care about ExpiresAt — but we'd still want
	// a more straightforward way; just inserting another with -time works).
	_ = m.Set(Silence{
		ID:        "expired",
		Matchers:  []SilenceMatcher{{Field: "name", Value: "y"}},
		ExpiresAt: time.Now().Add(-time.Hour),
	})

	m.PruneExpired()

	m.mu.RLock()
	_, hasLive := m.cache["live"]
	_, hasExpired := m.cache["expired"]
	m.mu.RUnlock()
	if !hasLive {
		t.Error("live silence should be retained")
	}
	if hasExpired {
		t.Error("expired silence should be tombstoned")
	}
}

func TestSilences_PersistAcrossInstances(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	gs := gossip.NewStorage(mem, nil, nil, "node-A")

	m1, err := newSilencesManager(context.Background(), gs, "node-A")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	_ = m1.Set(Silence{
		ID:        "persist",
		Matchers:  []SilenceMatcher{{Field: "name", Value: "z"}},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	m1.Close()

	m2, err := newSilencesManager(context.Background(), gs, "node-A")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	defer m2.Close()
	if !m2.IsSilenced(Target{Name: "z"}) {
		t.Error("second manager should see persisted silence")
	}
}

func TestSilences_SplitBrainBlocksWrites(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	isolated := &flagChecker{value: true}
	gs := gossip.NewStorage(mem, isolated, nil, "node-1")

	m, err := newSilencesManager(context.Background(), gs, "node-1")
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	defer m.Close()

	err = m.Set(Silence{
		ID:        "blocked",
		Matchers:  []SilenceMatcher{{Field: "name", Value: "x"}},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if !errors.Is(err, storage.ErrSplitBrain) {
		t.Errorf("expected ErrSplitBrain, got %v", err)
	}
}

func TestSilences_GenerateSilenceID_Unique(t *testing.T) {
	a := GenerateSilenceID()
	b := GenerateSilenceID()
	if a == b {
		t.Errorf("expected unique IDs, got %q twice", a)
	}
	if len(a) < 10 {
		t.Errorf("ID too short: %q", a)
	}
}
