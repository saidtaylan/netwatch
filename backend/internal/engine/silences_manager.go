package engine

// silences_manager.go — matcher-based alert silences (B24.5).
//
// A Silence acknowledges an active incident: "we know X is broken, stop
// paging us about it." Differs from MaintenanceWindow in two ways:
//
//   1. Maintenance is planned downtime (scheduled in advance, target-ID
//      based); silences are ad-hoc mutes (created during an active
//      incident, matcher-based).
//   2. Matchers can be ID exact, Name exact, Type exact, or "name~regex"
//      for prefix/suffix patterns. This lets operators silence "all
//      payments-* alerts" with one rule instead of N maintenance windows.
//
// Storage model: storage.TableSilences, cluster-replicated via gossip.
// Both maintenance + silences contribute to shouldAlert suppression —
// they OR together (either match → suppress).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/saidtaylan/netwatch/internal/storage"
	"github.com/saidtaylan/netwatch/internal/storage/gossip"
)

// SilenceMatcher describes one match condition. A target matches the
// matcher when its key/name/type satisfies the rule.
//
// Field encodes which dimension is checked ("id", "name", "type"). When
// IsRegex is true, Value is compiled as a regexp; otherwise an exact
// string match is performed.
type SilenceMatcher struct {
	Field   string `json:"field"`             // "id" | "name" | "type"
	Value   string `json:"value"`             // exact string OR regex when IsRegex
	IsRegex bool   `json:"is_regex,omitempty"`
}

// Silence is one cluster-wide alert mute rule.
//
// All matchers must hold (AND semantics within a single Silence). To
// express OR across rules, operators define multiple Silences.
//
// Lifecycle: created with ExpiresAt in the future. shouldAlert consults
// IsSilenced() on every alert dispatch. Expired silences are pruned to
// tombstones by runSilencesPruner.
type Silence struct {
	// ID uniquely identifies this silence for cancellation.
	// Format: "sil-<RFC3339>-<random6>"
	ID string `json:"id"`

	// Matchers is the AND-conjoined set of conditions. Must be non-empty.
	Matchers []SilenceMatcher `json:"matchers"`

	StartedAt time.Time `json:"started_at"`
	ExpiresAt time.Time `json:"expires_at"`

	// Comment is the operator's justification (e.g. "INC-1234: rolling
	// restart in progress"). Optional but recommended.
	Comment string `json:"comment,omitempty"`

	// CreatedBy is an optional human/service identifier (e.g. SRE
	// rotation handle).
	CreatedBy string `json:"created_by,omitempty"`
}

// silenceCacheEntry holds the parsed/compiled form of a Silence for fast
// matching. Compiled regexes are cached so we don't recompile on every
// IsSilenced call (which is on the alert hot path).
type silenceCacheEntry struct {
	silence  Silence
	patterns []*regexp.Regexp // index-aligned with Silence.Matchers; nil when matcher is not a regex
}

// silencesManager owns the storage-backed Silence registry.
//
// Mirrors maintenance + apps patterns: loadFromStorage at startup, Watch
// goroutine for peer updates, Upsert/Delete via storage, PruneExpired
// writes tombstones.
type silencesManager struct {
	mu       sync.RWMutex
	storage  *gossip.Storage
	nodeName string

	// cache holds the parsed silences; key = silence ID.
	cache map[string]*silenceCacheEntry

	watchCancel context.CancelFunc
}

// newSilencesManager constructs a storage-backed silences manager.
func newSilencesManager(parent context.Context, gs *gossip.Storage, nodeName string) (*silencesManager, error) {
	if gs == nil {
		return nil, fmt.Errorf("silences: nil storage")
	}
	ctx, cancel := context.WithCancel(parent)
	m := &silencesManager{
		storage:     gs,
		nodeName:    nodeName,
		cache:       make(map[string]*silenceCacheEntry),
		watchCancel: cancel,
	}
	if err := m.loadFromStorage(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("silences: initial load: %w", err)
	}
	go m.watchLoop(ctx)
	return m, nil
}

// Close stops the Watch goroutine. Safe to call multiple times.
func (m *silencesManager) Close() {
	if m.watchCancel != nil {
		m.watchCancel()
	}
}

// IsSilenced reports whether target t matches any active silence.
// Hot-path: must be lock-light and O(N*M) over (silences * matchers)
// which is small in practice (silences are short-lived and rare).
func (m *silencesManager) IsSilenced(t Target) bool {
	now := time.Now()
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, entry := range m.cache {
		if !now.Before(entry.silence.ExpiresAt) {
			continue
		}
		if matchesSilence(t, entry) {
			return true
		}
	}
	return false
}

// matchesSilence returns true when every matcher in the silence holds
// for target t (AND semantics).
func matchesSilence(t Target, e *silenceCacheEntry) bool {
	for i, mch := range e.silence.Matchers {
		var subject string
		switch mch.Field {
		case "id":
			subject = t.key()
		case "name":
			subject = t.Name
		case "type":
			subject = t.Type
		default:
			return false // unknown field → never matches
		}
		if mch.IsRegex {
			pat := e.patterns[i]
			if pat == nil || !pat.MatchString(subject) {
				return false
			}
		} else if subject != mch.Value {
			return false
		}
	}
	return true
}

// List returns all active (non-expired) silences.
func (m *silencesManager) List() []Silence {
	now := time.Now()
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Silence, 0, len(m.cache))
	for _, e := range m.cache {
		if now.Before(e.silence.ExpiresAt) {
			out = append(out, e.silence)
		}
	}
	return out
}

// Set adds or replaces a silence. Validates matchers (non-empty + regex
// compiles + known field). Cluster-replicated via storage.
func (m *silencesManager) Set(s Silence) error {
	if s.ID == "" {
		return fmt.Errorf("silences: empty ID")
	}
	if len(s.Matchers) == 0 {
		return fmt.Errorf("silences: at least one matcher required")
	}
	entry, err := compileSilence(s)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("silences: marshal: %w", err)
	}
	ver := m.storage.NextVersion()
	if err := m.storage.Upsert(context.Background(),
		storage.TableSilences, s.ID, payload, ver); err != nil {
		return fmt.Errorf("silences: storage upsert: %w", err)
	}
	m.mu.Lock()
	m.cache[s.ID] = entry
	m.mu.Unlock()
	return nil
}

// Cancel removes a silence by ID. Idempotent.
func (m *silencesManager) Cancel(id string) error {
	m.mu.RLock()
	_, exists := m.cache[id]
	m.mu.RUnlock()
	if !exists {
		return nil
	}
	ver := m.storage.NextVersion()
	if err := m.storage.Delete(context.Background(),
		storage.TableSilences, id, ver); err != nil {
		return fmt.Errorf("silences: storage delete: %w", err)
	}
	m.mu.Lock()
	delete(m.cache, id)
	m.mu.Unlock()
	return nil
}

// PruneExpired tombstones silences that have already passed their
// ExpiresAt. Called periodically by the engine.
func (m *silencesManager) PruneExpired() {
	now := time.Now()
	m.mu.RLock()
	var expired []string
	for id, e := range m.cache {
		if !now.Before(e.silence.ExpiresAt) {
			expired = append(expired, id)
		}
	}
	m.mu.RUnlock()
	for _, id := range expired {
		ver := m.storage.NextVersion()
		if err := m.storage.Delete(context.Background(),
			storage.TableSilences, id, ver); err != nil {
			slog.Warn("silences: prune storage delete failed", "id", id, "err", err)
			continue
		}
		m.mu.Lock()
		delete(m.cache, id)
		m.mu.Unlock()
	}
}

// GenerateSilenceID returns a unique silence ID.
func GenerateSilenceID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("sil-%s-%s",
		time.Now().UTC().Format("20060102T150405Z"),
		hex.EncodeToString(b)[:6])
}

// ── internal: storage interaction ──────────────────────────────────────

// loadFromStorage populates the in-memory silence cache from the silences table
// at startup. It skips tombstoned and already-expired rows, compiles each
// silence's matchers (regex/field) into a ready-to-evaluate entry, and tolerates
// malformed or uncompilable records by logging and skipping them.
func (m *silencesManager) loadFromStorage(ctx context.Context) error {
	recs, err := m.storage.List(ctx, storage.TableSilences, storage.Filter{})
	if err != nil {
		return err
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rec := range recs {
		if rec.Tombstone {
			continue
		}
		var s Silence
		if err := json.Unmarshal(rec.Payload, &s); err != nil {
			slog.Warn("silences: malformed record in storage", "id", rec.ID, "err", err)
			continue
		}
		if !now.Before(s.ExpiresAt) {
			continue // expired — let PruneExpired tombstone it later
		}
		entry, err := compileSilence(s)
		if err != nil {
			slog.Warn("silences: failed to compile loaded silence", "id", s.ID, "err", err)
			continue
		}
		m.cache[s.ID] = entry
	}
	if n := len(m.cache); n > 0 {
		slog.Info("silences: loaded from storage", "count", n)
	}
	return nil
}

// watchLoop applies storage change events (local and gossip-replicated) to the
// silence cache for the manager's lifetime: upserts compile and store the
// silence, deletes drop it. Exits when ctx is cancelled or the channel closes.
func (m *silencesManager) watchLoop(ctx context.Context) {
	ch, err := m.storage.Watch(ctx, storage.TableSilences)
	if err != nil {
		slog.Warn("silences: watch failed", "err", err)
		return
	}
	for evt := range ch {
		switch evt.Type {
		case storage.EventUpsert:
			var s Silence
			if err := json.Unmarshal(evt.Record.Payload, &s); err != nil {
				slog.Warn("silences: watch unmarshal failed", "id", evt.Record.ID, "err", err)
				continue
			}
			entry, err := compileSilence(s)
			if err != nil {
				slog.Warn("silences: watch compile failed", "id", s.ID, "err", err)
				continue
			}
			m.mu.Lock()
			m.cache[s.ID] = entry
			m.mu.Unlock()
		case storage.EventDelete:
			m.mu.Lock()
			delete(m.cache, evt.Record.ID)
			m.mu.Unlock()
		}
	}
}

// compileSilence validates a Silence and produces its silenceCacheEntry.
//
// Validation:
//   - Each matcher's Field must be "id", "name", or "type".
//   - Regex matchers must compile cleanly.
//   - Empty Value is rejected (matchers must have content).
func compileSilence(s Silence) (*silenceCacheEntry, error) {
	patterns := make([]*regexp.Regexp, len(s.Matchers))
	for i, m := range s.Matchers {
		switch m.Field {
		case "id", "name", "type":
		default:
			return nil, fmt.Errorf("silences: unknown matcher field %q (must be id/name/type)", m.Field)
		}
		if m.Value == "" {
			return nil, fmt.Errorf("silences: matcher %d has empty value", i)
		}
		if m.IsRegex {
			pat, err := regexp.Compile(m.Value)
			if err != nil {
				return nil, fmt.Errorf("silences: matcher %d invalid regex %q: %w", i, m.Value, err)
			}
			patterns[i] = pat
		}
	}
	return &silenceCacheEntry{silence: s, patterns: patterns}, nil
}
