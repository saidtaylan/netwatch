package engine

import "fmt"

// App represents a logical application that depends on one or more targets.
//
// Apps are optional. When defined, they enrich notifications with two pieces
// of context that pure target-level monitoring cannot provide:
//   - AFFECTED_APPS: which logical applications are impacted by this target
//   - OWNER_TEAMS: which teams should be paged
//
// Notification channel selection becomes the union of Target.Notify and the
// Notifications of every app that references this target. If both are empty,
// Config.DefaultNotify is used.
type App struct {
	Name          string   `json:"name"`
	OwnerTeam     string   `json:"owner_team,omitempty"`
	Uses          []string `json:"uses"` // target IDs or Names
	Notifications []string `json:"notifications,omitempty"`
}

// AppTargetIndex maps a target Key() to the apps that reference it.
type AppTargetIndex map[string][]*App

// buildAppTargetIndex walks cfg.Apps and produces a Key() → []*App lookup.
// Returns an empty (non-nil) map when no apps are defined so callers can
// treat the result uniformly.
func buildAppTargetIndex(cfg Config) AppTargetIndex {
	idx := make(AppTargetIndex, len(cfg.Targets))
	if len(cfg.Apps) == 0 {
		return idx
	}
	for i := range cfg.Apps {
		a := &cfg.Apps[i]
		for _, ref := range a.Uses {
			idx[ref] = append(idx[ref], a)
		}
	}
	return idx
}

// validateApps verifies that every app references known target keys, has a
// unique name, and points at notification channels that exist.
func validateApps(cfg Config) error {
	if len(cfg.Apps) == 0 {
		return nil
	}
	targetKeys := make(map[string]bool, len(cfg.Targets))
	for _, t := range cfg.Targets {
		if t.active() {
			targetKeys[t.key()] = true
		}
	}
	seen := make(map[string]bool, len(cfg.Apps))
	for _, a := range cfg.Apps {
		if a.Name == "" {
			return fmt.Errorf("app: name is required")
		}
		if seen[a.Name] {
			return fmt.Errorf("duplicate app name %q", a.Name)
		}
		seen[a.Name] = true
		if len(a.Uses) == 0 {
			return fmt.Errorf("app %q: uses must reference at least one target", a.Name)
		}
		for _, ref := range a.Uses {
			if !targetKeys[ref] {
				return fmt.Errorf("app %q: uses %q is not a known target id/name", a.Name, ref)
			}
		}
		for _, n := range a.Notifications {
			if _, ok := cfg.Notifications[n]; !ok {
				return fmt.Errorf("app %q: notification %q is not defined", a.Name, n)
			}
		}
	}
	return nil
}
