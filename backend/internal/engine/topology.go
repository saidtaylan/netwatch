package engine

import (
	"fmt"
	"sort"
	"strings"
)

// ── DependencyGraph ───────────────────────────────────────────────────────────

// DependencyGraph holds the directed dependency relationships between targets.
//
// edges[A] = [B, C] means target A depends on B and C (A needs B and C to
// function). When B goes down A is considered a cascading victim.
//
// reverse[B] = [A] is the inverse: B is depended on by A. Used to compute
// cascading impact quickly.
type DependencyGraph struct {
	edges   map[string][]string // targetID → targets it depends on
	reverse map[string][]string // targetID → targets that depend on it
}

// buildDependencyGraph constructs a DependencyGraph from config targets.
// Returns an error if:
//   - a depends_on entry references an unknown target ID
//   - a dependency cycle is detected
//
// Disabled (Enabled=false) targets are excluded from the graph.
func buildDependencyGraph(targets []Target) (*DependencyGraph, error) {
	known := make(map[string]bool, len(targets))
	for _, t := range targets {
		if t.active() {
			known[t.key()] = true
		}
	}

	g := &DependencyGraph{
		edges:   make(map[string][]string),
		reverse: make(map[string][]string),
	}

	for _, t := range targets {
		if !t.active() {
			continue
		}
		for _, dep := range t.DependsOn {
			if !known[dep] {
				return nil, fmt.Errorf("target %q: depends_on references unknown target %q", t.key(), dep)
			}
			g.edges[t.key()] = append(g.edges[t.key()], dep)
			g.reverse[dep] = append(g.reverse[dep], t.key())
		}
	}

	if err := g.detectCycles(targets); err != nil {
		return nil, err
	}
	return g, nil
}

// detectCycles runs a DFS coloring pass to find cycles.
// 0 = unvisited, 1 = in-stack (grey), 2 = finished (black).
func (g *DependencyGraph) detectCycles(targets []Target) error {
	color := make(map[string]int)

	var dfs func(id string) error
	dfs = func(id string) error {
		color[id] = 1
		for _, dep := range g.edges[id] {
			switch color[dep] {
			case 1:
				return fmt.Errorf("dependency cycle detected: %q → %q", id, dep)
			case 0:
				if err := dfs(dep); err != nil {
					return err
				}
			}
		}
		color[id] = 2
		return nil
	}

	for _, t := range targets {
		if t.active() && color[t.key()] == 0 {
			if err := dfs(t.key()); err != nil {
				return err
			}
		}
	}
	return nil
}

// FindRootCause returns the deepest hard_down dependency of failedID.
// If none of failedID's dependencies are hard_down in allStates, failedID is
// itself the root cause. allStates should contain the union of local and peer
// states for the best cross-cluster accuracy.
//
// When g is nil (no depends_on configured) failedID is always returned.
func (g *DependencyGraph) FindRootCause(failedID string, allStates map[string]PersistedState) string {
	if g == nil || len(g.edges) == 0 {
		return failedID
	}

	// Walk dependencies recursively; the deepest down node is root cause.
	visited := make(map[string]bool)

	var walk func(id string) string
	walk = func(id string) string {
		if visited[id] {
			return id // cycle guard (shouldn't happen after validation, but be safe)
		}
		visited[id] = true
		for _, dep := range g.edges[id] {
			ps, ok := allStates[dep]
			if ok && ps.State == "hard_down" {
				return walk(dep)
			}
		}
		return id
	}

	return walk(failedID)
}

// CascadingImpact returns all target IDs that (transitively) depend on
// failedID — i.e. what would break if failedID stays down. Sorted for
// deterministic output. Does NOT filter by current state (returns structural
// impact, not current observation).
func (g *DependencyGraph) CascadingImpact(failedID string) []string {
	if g == nil || len(g.reverse) == 0 {
		return nil
	}

	visited := make(map[string]bool)
	var result []string

	var walk func(id string)
	walk = func(id string) {
		for _, dep := range g.reverse[id] {
			if !visited[dep] {
				visited[dep] = true
				result = append(result, dep)
				walk(dep)
			}
		}
	}

	walk(failedID)
	sort.Strings(result)
	return result
}

// DependencyDepth returns the hop distance from rootCause to failedID through
// the reverse (depended-on-by) graph. Returns 0 when they are the same node
// or when no path exists.
func (g *DependencyGraph) DependencyDepth(failedID, rootCause string) int {
	if g == nil || failedID == rootCause {
		return 0
	}

	type item struct {
		id    string
		depth int
	}
	queue := []item{{rootCause, 0}}
	visited := map[string]bool{rootCause: true}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, dep := range g.reverse[cur.id] {
			if dep == failedID {
				return cur.depth + 1
			}
			if !visited[dep] {
				visited[dep] = true
				queue = append(queue, item{dep, cur.depth + 1})
			}
		}
	}
	return 0
}

// HasDependencies returns true if any target in the config declares a
// depends_on list, i.e. the graph is non-trivial.
func (g *DependencyGraph) HasDependencies() bool {
	return g != nil && len(g.edges) > 0
}

// ── TopologySnapshot ──────────────────────────────────────────────────────────

// TargetTopology is one node in the GET /topology response.
type TargetTopology struct {
	Name            string   `json:"name"`
	DependsOn       []string `json:"depends_on,omitempty"`       // direct deps
	DependedOnBy    []string `json:"depended_on_by,omitempty"`   // direct reverse
	CascadingImpact []string `json:"cascading_impact,omitempty"` // transitive reverse
}

// TopologySnapshot is the full GET /topology response payload.
type TopologySnapshot struct {
	Targets map[string]TargetTopology `json:"targets"`
}

// TopologySnapshot returns the dependency graph as a serialisable snapshot.
// Safe to call from multiple goroutines.
func (e *Engine) TopologySnapshot() TopologySnapshot {
	e.mu.RLock()
	targets := e.cfg.Targets
	g := e.topoGraph
	e.mu.RUnlock()

	snap := TopologySnapshot{
		Targets: make(map[string]TargetTopology, len(targets)),
	}

	for _, t := range targets {
		if !t.active() {
			continue
		}
		key := t.key()
		tt := TargetTopology{Name: t.Name}
		if g != nil {
			if deps := g.edges[key]; len(deps) > 0 {
				cp := make([]string, len(deps))
				copy(cp, deps)
				tt.DependsOn = cp
			}
			if revs := g.reverse[key]; len(revs) > 0 {
				cp := make([]string, len(revs))
				copy(cp, revs)
				tt.DependedOnBy = cp
			}
			tt.CascadingImpact = g.CascadingImpact(key)
		}
		snap.Targets[key] = tt
	}
	return snap
}

// ── rootCauseEnv ──────────────────────────────────────────────────────────────

// rootCauseEnv computes ROOT_CAUSE, CASCADING_IMPACT and DEPENDENCY_DEPTH for
// a target that is transitioning to unreachable. Returned map is empty when
// the topology graph has no dependency edges (no-op for simple configs).
//
// allStates is the combined local + cluster-wide state map provided by the
// caller so root-cause detection can see peer-observed states.
func (e *Engine) rootCauseEnv(t Target, status string, allStates map[string]PersistedState) map[string]string {
	env := map[string]string{}
	if status != "unreachable" {
		return env // root cause only meaningful on down events
	}

	e.mu.RLock()
	g := e.topoGraph
	e.mu.RUnlock()

	if g == nil || !g.HasDependencies() {
		return env
	}

	root := g.FindRootCause(t.key(), allStates)
	env["ROOT_CAUSE"] = root

	impact := g.CascadingImpact(t.key())
	if len(impact) > 0 {
		env["CASCADING_IMPACT"] = strings.Join(impact, ",")
	}

	depth := g.DependencyDepth(t.key(), root)
	env["DEPENDENCY_DEPTH"] = fmt.Sprintf("%d", depth)

	return env
}
