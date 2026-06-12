package engine

// users_manager.go — Storage-backed user account manager (B28).
//
// Users are stored in the `users` table and replicated across the cluster
// via gossip LWW. The in-memory cache provides fast O(1) lookups by
// username for the auth hot path (login, JWT verification).
//
// Architecture:
//   - The manager holds an in-memory cache (byUsername map) for O(1) lookups
//   - CreateUser/UpdateUser/DeleteUser write to storage (gossip broadcast)
//   - Storage Watch subscriptions keep the cache in sync with peer changes
//   - Passwords are bcrypt-hashed before storage; plaintext never persisted

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/saidtaylan/netwatch/internal/storage"
	"github.com/saidtaylan/netwatch/internal/storage/gossip"
)

// usersManager manages user accounts backed by the storage layer.
// All methods are safe for concurrent use.
type usersManager struct {
	mu         sync.RWMutex
	users      map[string]User // id → User
	byUsername map[string]string // username → id (for fast login lookup)

	storage     *gossip.Storage
	watchCancel context.CancelFunc
}

// newUsersManager constructs a manager backed by the storage layer.
// Loads the current user set from storage into the in-memory cache,
// then starts a Watch goroutine to keep the cache in sync with peer
// broadcasts.
func newUsersManager(parent context.Context, gs *gossip.Storage) (*usersManager, error) {
	if gs == nil {
		return nil, fmt.Errorf("users: nil storage")
	}
	ctx, cancel := context.WithCancel(parent)
	m := &usersManager{
		users:       make(map[string]User),
		byUsername:   make(map[string]string),
		storage:     gs,
		watchCancel: cancel,
	}

	if err := m.loadFromStorage(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("users: initial load: %w", err)
	}

	go m.watchStorageLoop(ctx)

	return m, nil
}

// Close stops the Watch goroutine.
func (m *usersManager) Close() {
	if m.watchCancel != nil {
		m.watchCancel()
	}
}

// ── Read operations ────────────────────────────────────────────────────────

// UserCount returns the total number of non-disabled users.
func (m *usersManager) UserCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.users)
}

// SetupCompleted returns true when at least one user exists in the DB.
func (m *usersManager) SetupCompleted() bool {
	return m.UserCount() > 0
}

// GetByUsername returns the user with the given username.
// Returns false if not found.
func (m *usersManager) GetByUsername(username string) (User, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.byUsername[username]
	if !ok {
		return User{}, false
	}
	u, ok := m.users[id]
	return u, ok
}

// GetByID returns the user with the given ID.
func (m *usersManager) GetByID(id string) (User, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[id]
	return u, ok
}

// List returns all non-tombstoned users (without password hashes).
func (m *usersManager) List() []UserPublic {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]UserPublic, 0, len(m.users))
	for _, u := range m.users {
		out = append(out, u.Public())
	}
	return out
}

// ── Write operations ───────────────────────────────────────────────────────

// CreateUser creates a new user. Password must already be hashed.
// Returns the created user.
func (m *usersManager) CreateUser(user User) error {
	// Check username uniqueness
	m.mu.RLock()
	if _, exists := m.byUsername[user.Username]; exists {
		m.mu.RUnlock()
		return fmt.Errorf("username %q already exists", user.Username)
	}
	m.mu.RUnlock()

	payload, err := json.Marshal(user)
	if err != nil {
		return fmt.Errorf("marshal user: %w", err)
	}

	ver := m.storage.NextVersion()
	if err := m.storage.Upsert(context.Background(),
		storage.TableUsers, user.ID, payload, ver); err != nil {
		return fmt.Errorf("storage upsert: %w", err)
	}

	// Update local cache immediately
	m.mu.Lock()
	m.applyUser(user)
	m.mu.Unlock()

	slog.Info("[USERS] created", "id", user.ID, "username", user.Username, "role", user.Role)
	return nil
}

// UpdateUser updates an existing user. The ID must match an existing user.
func (m *usersManager) UpdateUser(user User) error {
	m.mu.RLock()
	existing, exists := m.users[user.ID]
	m.mu.RUnlock()
	if !exists {
		return fmt.Errorf("user %q not found", user.ID)
	}

	// If username changed, check uniqueness
	if user.Username != existing.Username {
		m.mu.RLock()
		if otherID, taken := m.byUsername[user.Username]; taken && otherID != user.ID {
			m.mu.RUnlock()
			return fmt.Errorf("username %q already taken", user.Username)
		}
		m.mu.RUnlock()
	}

	payload, err := json.Marshal(user)
	if err != nil {
		return fmt.Errorf("marshal user: %w", err)
	}

	ver := m.storage.NextVersion()
	if err := m.storage.Upsert(context.Background(),
		storage.TableUsers, user.ID, payload, ver); err != nil {
		return fmt.Errorf("storage upsert: %w", err)
	}

	m.mu.Lock()
	m.applyUser(user)
	m.mu.Unlock()

	slog.Info("[USERS] updated", "id", user.ID, "username", user.Username)
	return nil
}

// UpdateLastLogin sets the last_login_at field for a user.
func (m *usersManager) UpdateLastLogin(userID string) {
	m.mu.RLock()
	u, ok := m.users[userID]
	m.mu.RUnlock()
	if !ok {
		return
	}
	u.LastLoginAt = time.Now().UTC().Format(time.RFC3339)

	payload, err := json.Marshal(u)
	if err != nil {
		return
	}
	ver := m.storage.NextVersion()
	_ = m.storage.Upsert(context.Background(),
		storage.TableUsers, u.ID, payload, ver)

	m.mu.Lock()
	m.applyUser(u)
	m.mu.Unlock()
}

// DeleteUser soft-deletes a user by ID.
func (m *usersManager) DeleteUser(id string) (bool, error) {
	m.mu.RLock()
	_, exists := m.users[id]
	m.mu.RUnlock()
	if !exists {
		return false, nil
	}

	ver := m.storage.NextVersion()
	if err := m.storage.Delete(context.Background(),
		storage.TableUsers, id, ver); err != nil {
		return false, fmt.Errorf("storage delete: %w", err)
	}

	m.mu.Lock()
	m.removeUser(id)
	m.mu.Unlock()

	slog.Info("[USERS] deleted", "id", id)
	return true, nil
}

// ── Storage interaction ────────────────────────────────────────────────────

// loadFromStorage populates the in-memory user cache (and the username→id
// index) from the users table at startup, skipping tombstoned and malformed
// rows. Returns a storage error.
func (m *usersManager) loadFromStorage(ctx context.Context) error {
	recs, err := m.storage.List(ctx, storage.TableUsers, storage.Filter{})
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, rec := range recs {
		if rec.Tombstone {
			continue
		}
		var u User
		if err := json.Unmarshal(rec.Payload, &u); err != nil {
			slog.Warn("[USERS] malformed user in storage", "id", rec.ID, "err", err)
			continue
		}
		m.applyUser(u)
	}
	if n := len(m.users); n > 0 {
		slog.Info("[USERS] loaded from storage", "count", n)
	}
	return nil
}

// watchStorageLoop forwards storage change events (local and gossip-replicated)
// to applyStorageEvent for the manager's lifetime, keeping the user cache in
// sync cluster-wide. Exits when ctx is cancelled or the channel closes.
func (m *usersManager) watchStorageLoop(ctx context.Context) {
	ch, err := m.storage.Watch(ctx, storage.TableUsers)
	if err != nil {
		slog.Warn("[USERS] watch failed", "err", err)
		return
	}
	for evt := range ch {
		m.applyStorageEvent(evt)
	}
}

// applyStorageEvent applies one storage event to the user cache under the lock:
// an upsert decodes and stores the user, a delete removes it by id.
func (m *usersManager) applyStorageEvent(evt storage.Event) {
	switch evt.Type {
	case storage.EventUpsert:
		var u User
		if err := json.Unmarshal(evt.Record.Payload, &u); err != nil {
			slog.Warn("[USERS] watch unmarshal failed", "id", evt.Record.ID, "err", err)
			return
		}
		m.mu.Lock()
		m.applyUser(u)
		m.mu.Unlock()
	case storage.EventDelete:
		m.mu.Lock()
		m.removeUser(evt.Record.ID)
		m.mu.Unlock()
	}
}

// ── Cache mutators (caller must hold m.mu) ─────────────────────────────────

// applyUser inserts or updates a user in the cache and keeps the username→id
// index consistent, removing a stale username mapping when a user is renamed.
// The caller must hold m.mu.
func (m *usersManager) applyUser(u User) {
	// Remove old username mapping if username changed
	if old, ok := m.users[u.ID]; ok && old.Username != u.Username {
		delete(m.byUsername, old.Username)
	}
	m.users[u.ID] = u
	m.byUsername[u.Username] = u.ID
}

// removeUser deletes a user from the cache and its username index by id.
// The caller must hold m.mu.
func (m *usersManager) removeUser(id string) {
	if u, ok := m.users[id]; ok {
		delete(m.byUsername, u.Username)
		delete(m.users, id)
	}
}
