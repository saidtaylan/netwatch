package engine

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// AlertChannelConfig defines a named notification channel in config.
type AlertChannelConfig struct {
	Type       string            `json:"type"`
	Parameters map[string]string `json:"parameters,omitempty"`
}

// Alerter is implemented by every notification channel.
// env always contains: NAME, TARGET, HOST, PORT, APP_NAME, STATUS, TYPE.
// When the target is referenced by one or more App entries, env additionally
// carries AFFECTED_APPS (comma-separated app names) and OWNER_TEAMS
// (comma-separated unique team names).
type Alerter interface {
	Send(env map[string]string) error
}

// AlertRunner executes a platform-specific notification script.
// scriptBase is the absolute path WITHOUT extension; the runner appends .sh or .ps1.
type AlertRunner func(scriptBase string, env map[string]string) error

// scriptAlerter triggers a shell or PowerShell script named after the channel.
type scriptAlerter struct {
	name   string
	params map[string]string
	runner AlertRunner
}

func (s *scriptAlerter) Send(env map[string]string) error {
	var base string
	if p, ok := s.params["script"]; ok && p != "" {
		// strip extension if present — runner appends .sh/.ps1
		base = strings.TrimSuffix(strings.TrimSuffix(p, ".sh"), ".ps1")
	} else {
		base = filepath.Join(alertScriptsDir(), s.name)
	}
	merged := mergeVars(s.params, env)
	return s.runner(base, merged)
}

// newAlertChannel builds an Alerter for the given AlertChannelConfig.
func newAlertChannel(name string, cfg AlertChannelConfig, runner AlertRunner) (Alerter, error) {
	switch cfg.Type {
	case "script":
		if runner == nil {
			return nil, fmt.Errorf("type=script requires an alert runner")
		}
		return &scriptAlerter{name: name, params: cfg.Parameters, runner: runner}, nil
	case "mail":
		return newMailAlerter(cfg.Parameters)
	case "webhook":
		return newWebhookAlerter(cfg.Parameters)
	default:
		return nil, fmt.Errorf("unknown notification type %q", cfg.Type)
	}
}

// buildAlertChannels constructs an Alerter for each entry in cfg.Notifications
// and validates all names listed in cfg.DefaultNotify.
func buildAlertChannels(cfg Config, runner AlertRunner) (map[string]Alerter, error) {
	channels := make(map[string]Alerter, len(cfg.Notifications))
	for name, nc := range cfg.Notifications {
		ch, err := newAlertChannel(name, nc, runner)
		if err != nil {
			return nil, fmt.Errorf("notification %q: %w", name, err)
		}
		channels[name] = ch
	}
	for _, name := range cfg.DefaultNotify {
		if _, ok := channels[name]; !ok {
			return nil, fmt.Errorf("default_notify: %q is not defined in notifications", name)
		}
	}
	return channels, nil
}

// sendAlert resolves which channels apply to target t, builds the env map,
// and dispatches each channel concurrently.
//
// Channel resolution:
//  1. union(t.Notify, app.Notifications for every app referencing t) — deduped
//  2. if empty → cfg.DefaultNotify
//  3. if still empty → no-op
//
// When apps reference t, AFFECTED_APPS and OWNER_TEAMS are added to env so
// notification scripts can route to the right humans without parsing config.
func (e *Engine) sendAlert(t Target, status string) {
	e.mu.RLock()
	cfg := e.cfg
	channels := e.channels
	apps := e.appIndex[t.key()]
	e.mu.RUnlock()

	if len(channels) == 0 {
		return
	}

	names := mergeNotifyChannels(t.Notify, apps)
	if len(names) == 0 {
		names = cfg.DefaultNotify
	}
	if len(names) == 0 {
		return
	}

	checker, ok := e.checkers[t.Type]
	if !ok {
		slog.Error("no checker for type", "type", t.Type, "target", t.Name)
		return
	}
	host, port, err := checker.ParseAddr(t.Target)
	if err != nil {
		slog.Error("cannot parse target", "target", t.Target, "err", err)
		return
	}

	// Read persisted state for seq and error_code — available to all channel types
	// (scripts can use $SEQ / $ERROR_CODE, webhooks embed them in JSON).
	e.stateMu.RLock()
	ps := e.lastKnown[t.key()]
	// Build a merged state snapshot for root-cause detection: local states
	// plus any peer states visible to the cluster layer.
	allStates := make(map[string]PersistedState, len(e.lastKnown))
	for k, v := range e.lastKnown {
		allStates[k] = v
	}
	e.stateMu.RUnlock()

	// Overlay peer-observed states so root-cause detection uses cluster-wide knowledge.
	if e.clusterMgr != nil {
		for _, payload := range e.clusterMgr.AllPeerStates() {
			if _, exists := allStates[payload.TargetID]; !exists || payload.Seq > allStates[payload.TargetID].Seq {
				allStates[payload.TargetID] = PersistedState{
					State:     payload.State,
					Seq:       payload.Seq,
					ErrorCode: payload.ErrorCode,
				}
			}
		}
	}

	localDown := status == "unreachable"
	env := map[string]string{
		"NAME":       t.Name,
		"TARGET":     t.Target,
		"HOST":       host,
		"PORT":       port,
		"APP_NAME":   e.AppName(),
		"NODE_NAME":  e.hostname,
		"STATUS":     status,
		"TYPE":       t.Type,
		"SEQ":        strconv.FormatUint(ps.Seq, 10),
		"ERROR_CODE": ps.ErrorCode,
		"SCOPE":      e.computeScope(t.key(), localDown),
	}
	if affected, teams := buildAppContext(apps); affected != "" {
		env["AFFECTED_APPS"] = affected
		env["OWNER_TEAMS"] = teams
	}
	// Topology: root cause + cascading impact (no-op when no depends_on configured).
	for k, v := range e.rootCauseEnv(t, status, allStates) {
		env[k] = v
	}

	slog.Info("sending alert", "target", t.Name, "status", status, "channels", names, "apps", len(apps))

	for _, name := range names {
		ch, ok := channels[name]
		if !ok {
			slog.Warn("alert channel not found", "channel", name, "target", t.Name)
			continue
		}
		go func(n string, c Alerter) {
			if err := c.Send(env); err != nil {
				slog.Error("alert send failed", "channel", n, "target", t.Name, "err", err)
			}
		}(name, ch)
	}
}

// mergeNotifyChannels returns the deduped union of target-level notify entries
// and the notification channels of every app referencing this target. Order is
// preserved: target entries first, then app entries in app declaration order.
func mergeNotifyChannels(targetNotify []string, apps []*App) []string {
	out := make([]string, 0, len(targetNotify))
	seen := make(map[string]bool, len(targetNotify))
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, n := range targetNotify {
		add(n)
	}
	for _, a := range apps {
		for _, n := range a.Notifications {
			add(n)
		}
	}
	return out
}

// buildAppContext returns ("payment-gateway,inventory-api", "fintech,logistics")
// from the app list. Empty when apps is empty.
func buildAppContext(apps []*App) (affected, teams string) {
	if len(apps) == 0 {
		return "", ""
	}
	names := make([]string, 0, len(apps))
	teamSet := make(map[string]bool, len(apps))
	teamList := make([]string, 0, len(apps))
	for _, a := range apps {
		names = append(names, a.Name)
		if a.OwnerTeam != "" && !teamSet[a.OwnerTeam] {
			teamSet[a.OwnerTeam] = true
			teamList = append(teamList, a.OwnerTeam)
		}
	}
	return strings.Join(names, ","), strings.Join(teamList, ",")
}

// mergeVars combines base and override maps; override wins on key collision.
func mergeVars(base, override map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// alertScriptsDir returns the absolute path to the notifications/ script directory.
func alertScriptsDir() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Join(filepath.Dir(exe), "notifications")
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Join(cwd, "notifications")
	}
	return "notifications"
}

// toEnvSlice converts a map to "KEY=VALUE" pairs suitable for cmd.Env.
func toEnvSlice(m map[string]string) []string {
	pairs := make([]string, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, k+"="+v)
	}
	return pairs
}

// ShellRunner runs a .sh script via /bin/sh (Linux/macOS).
func ShellRunner(scriptBase string, env map[string]string) error {
	path := scriptBase + ".sh"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("alert script not found: %s", path)
	}
	cmd := exec.Command("/bin/sh", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), toEnvSlice(env)...)
	return cmd.Run()
}

// PowerShellRunner runs a .ps1 script via PowerShell (Windows).
func PowerShellRunner(scriptBase string, env map[string]string) error {
	path := scriptBase + ".ps1"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("alert script not found: %s", path)
	}
	cmd := exec.Command(
		"powershell.exe",
		"-NonInteractive", "-NoProfile",
		"-ExecutionPolicy", "Bypass",
		"-File", path,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), toEnvSlice(env)...)
	return cmd.Run()
}
