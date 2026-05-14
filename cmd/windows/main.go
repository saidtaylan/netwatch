package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/saidtaylan/netwatch/internal/engine"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

func main() {
	// Subcommand routing — first non-flag argument selects the action.
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		switch os.Args[1] {
		case "service":
			cmdService(os.Args[2:])
		case "leave":
			cmdLeave(os.Args[2:])
		case "validate":
			cmdValidate(os.Args[2:])
		case "uninstall":
			cmdUninstall(os.Args[2:])
		default:
			fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\nUsage:\n", os.Args[1])
			fmt.Fprintf(os.Stderr, "  %s [--config FILE]              start the monitoring agent\n", engine.BinaryName)
			fmt.Fprintf(os.Stderr, "  %s validate [--config FILE]     validate config without starting\n", engine.BinaryName)
			fmt.Fprintf(os.Stderr, "  %s service install [--config F] register as a Windows Service\n", engine.BinaryName)
			fmt.Fprintf(os.Stderr, "  %s service remove               unregister the Windows Service\n", engine.BinaryName)
			fmt.Fprintf(os.Stderr, "  %s leave [--port PORT]          tell a running agent to leave the cluster\n", engine.BinaryName)
			fmt.Fprintf(os.Stderr, "  %s uninstall [--port PORT]      stop service, remove it, optionally delete config\n", engine.BinaryName)
			os.Exit(1)
		}
		return
	}

	configPath := flag.String("config", "", "path to config.yaml (default: config.yaml next to the binary)")
	flag.Parse()

	isService, err := svc.IsWindowsService()
	if err != nil {
		slog.Error("cannot determine service mode", "err", err)
		os.Exit(1)
	}

	if isService {
		if err := svc.Run(engine.BinaryName, &agentService{configPath: *configPath}); err != nil {
			slog.Error("service failed", "err", err)
			os.Exit(1)
		}
		return
	}

	// Direct invocation: set up OS signal handling and start the agent.
	leaveCh := make(chan string, 1)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		select {
		case leaveCh <- fmt.Sprintf("signal %v", sig):
		default:
		}
	}()
	runAgent(*configPath, leaveCh)
}

// agentService implements the Windows Service control interface.
type agentService struct{ configPath string }

func (s *agentService) Execute(_ []string, changeReq <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	leaveCh := make(chan string, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		runAgent(s.configPath, leaveCh)
	}()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case req := <-changeReq:
			switch req.Cmd {
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				select {
				case leaveCh <- "windows service stop":
				default:
				}
				// Give the agent up to 10 s to shut down gracefully before
				// the service manager kills the process anyway.
				select {
				case <-done:
				case <-time.After(10 * time.Second):
				}
				return false, 0
			}
		case <-done:
			return false, 0
		}
	}
}

// runAgent initialises the monitoring engine and starts the HTTP server.
// It blocks until leaveCh receives a reason string, then shuts down and exits.
func runAgent(configPath string, leaveCh chan string) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
		slog.Warn("could not determine hostname", "err", err)
	}

	e := engine.New(hostname, engine.PowerShellRunner, configPath)
	if err := e.Init(); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}

	// Unified shutdown goroutine — handles both OS signals (forwarded via leaveCh)
	// and HTTP /cluster/leave requests.
	go func() {
		reason := <-leaveCh
		slog.Info("shutting down", "reason", reason)
		e.Shutdown()
		os.Exit(0)
	}()

	reg := prometheus.NewRegistry()
	engine.RegisterMetrics(reg)
	if e.ClusterManager() != nil {
		engine.RegisterClusterMetrics(reg)
	}
	if e.SLOEnabled() {
		engine.RegisterSLOMetrics(reg)
	}

	mux := http.NewServeMux()

	baseMetrics := promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg})
	mux.Handle("/metrics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.NotifyScrape()
		baseMetrics.ServeHTTP(w, r)
	}))

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(e.Status()); err != nil {
			slog.Error("status encode error", "err", err)
		}
	})

	mux.HandleFunc("/topology", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(e.TopologySnapshot()); err != nil {
			slog.Error("topology encode error", "err", err)
		}
	})

	mux.HandleFunc("/cluster/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mgr := e.ClusterManager()
		if mgr == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "cluster not enabled (set cluster.enabled: true in config)",
			})
			return
		}
		if err := json.NewEncoder(w).Encode(mgr.Snapshot()); err != nil {
			slog.Error("cluster state encode error", "err", err)
		}
	})

	mux.HandleFunc("/cluster/probers", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mgr := e.ClusterManager()
		if mgr == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "cluster not enabled (set cluster.enabled: true in config)",
			})
			return
		}
		if err := json.NewEncoder(w).Encode(mgr.ProberAssignmentsSnapshot()); err != nil {
			slog.Error("cluster probers encode error", "err", err)
		}
	})

	mux.HandleFunc("/fleet/status", func(w http.ResponseWriter, r *http.Request) {
		snap := e.FleetSnapshot()
		if r.URL.Query().Get("format") == "text" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprint(w, fleetStatusText(snap))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(snap); err != nil {
			slog.Error("fleet status encode error", "err", err)
		}
	})

	mux.HandleFunc("/slo", func(w http.ResponseWriter, r *http.Request) {
		snap := e.SLOSnapshot()
		if snap == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "SLO tracking not enabled (set slo.enabled: true in config)",
			})
			return
		}
		if r.URL.Query().Get("format") == "text" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprint(w, sloText(snap))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(snap); err != nil {
			slog.Error("slo encode error", "err", err)
		}
	})

	mux.HandleFunc("/cluster/config", func(w http.ResponseWriter, _ *http.Request) {
		mgr := e.ClusterManager()
		if mgr == nil {
			http.Error(w, `{"error":"cluster disabled"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(mgr.ConfigSyncSnapshot()); err != nil {
			slog.Error("cluster/config encode error", "err", err)
		}
	})

	mux.HandleFunc("/geo/latency/", func(w http.ResponseWriter, r *http.Request) {
		targetID := strings.TrimPrefix(r.URL.Path, "/geo/latency/")
		if targetID == "" {
			http.Error(w, `{"error":"targetID required"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		snap := e.GeoLatencySnapshot(targetID)
		if err := json.NewEncoder(w).Encode(snap); err != nil {
			slog.Error("geo/latency encode error", "err", err)
		}
	})

	mux.HandleFunc("/cluster/keyring/rotate", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mgr := e.ClusterManager()
		if mgr == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "cluster not enabled"})
			return
		}
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(mgr.KeyringInfo())
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "use GET or POST"})
			return
		}
		var body struct {
			Action string `json:"action"`
			Key    string `json:"key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		raw, err := base64.StdEncoding.DecodeString(body.Key)
		if err != nil {
			raw, err = base64.RawStdEncoding.DecodeString(body.Key)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "key: base64 decode failed: " + err.Error()})
				return
			}
		}
		switch body.Action {
		case "add":
			err = mgr.KeyringAddKey(raw)
		case "use":
			err = mgr.KeyringUseKey(raw)
		case "remove":
			err = mgr.KeyringRemoveKey(raw)
		default:
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "action must be add, use, or remove"})
			return
		}
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		slog.Info("keyring rotation step completed", "action", body.Action)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"action":  body.Action,
			"keyring": mgr.KeyringInfo(),
		})
	})

	mux.HandleFunc("/cluster/leave", func(w http.ResponseWriter, r *http.Request) {
		reason := r.URL.Query().Get("reason")
		slog.Info("cluster leave requested via HTTP", "reason", reason)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "leaving"})
		select {
		case leaveCh <- reason:
		default:
		}
	})

	port := e.Port()
	slog.Info(engine.BinaryName+" listening", "port", port, "app", e.AppName(), "host", hostname)

	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}

// ── Subcommands ───────────────────────────────────────────────────────────────

// cmdService handles "service install" and "service remove".
func cmdService(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: %s service install|remove\n", engine.BinaryName)
		os.Exit(1)
	}
	switch args[0] {
	case "install":
		cmdServiceInstall(args[1:])
	case "remove":
		cmdServiceRemove()
	default:
		fmt.Fprintf(os.Stderr, "unknown service subcommand %q (want install|remove)\n", args[0])
		os.Exit(1)
	}
}

func cmdServiceInstall(args []string) {
	fs := flag.NewFlagSet("service install", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config.yaml")
	_ = fs.Parse(args)

	exePath, err := exec.LookPath(os.Args[0])
	if err != nil {
		if exePath, err = os.Executable(); err != nil {
			fmt.Fprintf(os.Stderr, "cannot determine executable path: %v\n", err)
			os.Exit(1)
		}
	}

	svcArgs := []string{"--config", *configPath}
	if err := installService(exePath, svcArgs); err != nil {
		fmt.Fprintf(os.Stderr, "error installing service: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Service %q installed successfully.\n", engine.BinaryName)
	fmt.Printf("Start it with: sc start %s\n", engine.BinaryName)
}

func cmdServiceRemove() {
	if err := removeService(); err != nil {
		fmt.Fprintf(os.Stderr, "error removing service: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Service %q removed successfully.\n", engine.BinaryName)
}

// cmdLeave sends a graceful-leave request to a running agent over HTTP.
func cmdLeave(args []string) {
	fs := flag.NewFlagSet("leave", flag.ExitOnError)
	port := fs.String("port", "10240", "port the running agent is listening on")
	reason := fs.String("reason", "", "optional reason text")
	_ = fs.Parse(args)

	leaveURL := fmt.Sprintf("http://localhost:%s/cluster/leave", *port)
	if *reason != "" {
		leaveURL += "?reason=" + url.QueryEscape(*reason)
	}

	resp, err := http.Post(leaveURL, "application/json", nil) //nolint:noctx
	if err != nil {
		fmt.Fprintf(os.Stderr, "error contacting agent: %v\n  Is it running on port %s?\n", err, *port)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Response %d: %s\n", resp.StatusCode, strings.TrimSpace(string(body)))
}

// cmdValidate loads and validates the config file without starting any
// goroutines or network connections. Exits 0 on success, 1 on failure.
func cmdValidate(args []string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config.yaml (default: auto-detect)")
	_ = fs.Parse(args)

	cfg, err := engine.ValidateConfigFile(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "INVALID  %v\n", err)
		os.Exit(1)
	}

	activeCount := 0
	for _, t := range cfg.Targets {
		if t.Enabled == nil || *t.Enabled {
			activeCount++
		}
	}

	fmt.Printf("OK  config is valid\n\n")
	fmt.Printf("  app_name   : %s\n", cfg.AppName)
	fmt.Printf("  targets    : %d total, %d active\n", len(cfg.Targets), activeCount)
	fmt.Printf("  apps       : %d\n", len(cfg.Apps))
	fmt.Printf("  channels   : %d notification channels\n", len(cfg.Notifications))

	clusterStatus := "disabled"
	if cfg.Cluster.Enabled {
		clusterStatus = fmt.Sprintf("enabled (node=%s, peers=%d)", cfg.Cluster.NodeName, len(cfg.Cluster.Peers))
	}
	fmt.Printf("  cluster    : %s\n", clusterStatus)

	sloStatus := "disabled"
	if cfg.SLO != nil && cfg.SLO.Enabled {
		sloStatus = fmt.Sprintf("enabled (%d targets)", len(cfg.SLO.Targets))
	}
	fmt.Printf("  slo        : %s\n", sloStatus)
}

// cmdUninstall stops and removes the Windows Service, then optionally deletes
// the config directory.
func cmdUninstall(args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	port := fs.String("port", "10240", "port the running agent is listening on")
	_ = fs.Parse(args)

	fmt.Printf("This will stop and remove the %s service.\n", engine.BinaryName)
	fmt.Print("Continue? [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	if strings.ToLower(strings.TrimSpace(answer)) != "y" {
		fmt.Println("Aborted.")
		return
	}

	// 1. Graceful leave — best-effort.
	leaveURL := fmt.Sprintf("http://localhost:%s/cluster/leave?reason=uninstall", *port)
	if resp, err := http.Post(leaveURL, "application/json", nil); err == nil { //nolint:noctx
		resp.Body.Close()
		fmt.Println("  leave signal sent to running agent")
	} else {
		fmt.Printf("  agent not reachable (%v) — continuing\n", err)
	}

	// 2. Stop and delete the Windows Service.
	scRun("stop", engine.BinaryName)
	scRun("delete", engine.BinaryName)

	// 3. Optionally delete config directory.
	cfgDir := filepath.Join(os.Getenv("ProgramData"), engine.BinaryName)
	fmt.Printf("\nDelete config directory %s? [y/N]: ", cfgDir)
	answer2, _ := reader.ReadString('\n')
	if strings.ToLower(strings.TrimSpace(answer2)) == "y" {
		if err := os.RemoveAll(cfgDir); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: %v\n", err)
		} else {
			fmt.Printf("  removed %s\n", cfgDir)
		}
	} else {
		fmt.Printf("  config kept at %s\n", cfgDir)
	}

	fmt.Printf("\n%s uninstalled.\n", engine.BinaryName)
}

// scRun executes an sc.exe command and prints the result.
func scRun(subcmdArgs ...string) {
	cmd := exec.Command("sc", subcmdArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "  sc %s: %v\n", strings.Join(subcmdArgs, " "), err)
		if len(out) > 0 {
			fmt.Fprintf(os.Stderr, "  %s\n", strings.TrimSpace(string(out)))
		}
	} else {
		fmt.Printf("  sc %s OK\n", strings.Join(subcmdArgs, " "))
	}
}

// writeIfAbsent writes content to path only if the file does not already exist.
func writeIfAbsent(path, content string) {
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("  (exists, skipped) %s\n", path)
		return
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Printf("  (created) %s\n", path)
}

// ── Windows Service helpers ───────────────────────────────────────────────────

func installService(exePath string, svcArgs []string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.CreateService(engine.BinaryName, exePath, mgr.Config{
		DisplayName: engine.BinaryName + " Monitoring Agent",
		StartType:   mgr.StartAutomatic,
	}, svcArgs...)
	if err != nil {
		return err
	}
	defer s.Close()

	if err := eventlog.InstallAsEventCreate(engine.BinaryName, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil {
		_ = s.Delete()
		return err
	}
	return nil
}

func removeService() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(engine.BinaryName)
	if err != nil {
		return fmt.Errorf("service %q not found: %w", engine.BinaryName, err)
	}
	defer s.Close()

	if err := s.Delete(); err != nil {
		return err
	}
	return eventlog.Remove(engine.BinaryName)
}

// ── Config template ───────────────────────────────────────────────────────────

func configSkeleton(cfgDir string) string {
	type tdata struct{ BinaryName, CfgDir string }
	tmpl := template.Must(template.New("cfg").Parse(
		`# {{.BinaryName}} configuration
# Full reference: https://github.com/saidtaylan/netwatch

app_name: "{{.BinaryName}}-agent"
port: "10240"
state_file: "{{.CfgDir}}\state.json"
log_path:   "{{.CfgDir}}\agent.log"
credentials_file: "{{.CfgDir}}\credentials.env"

timeout:            5
max_retries:        2
retry_interval_sec: 30
probe_interval_sec: 60
ticker_interval_sec: 5
reload_interval_sec: 30

# watchdog_threshold_sec: 120   # 0 = disabled (default)

notifications: {}
#  slack:
#    type: webhook
#    parameters:
#      url: "${SLACK_WEBHOOK_URL}"
#      format: generic           # generic | alertmanager

default_notify: []

targets:
  - name: example-tcp
    type: tcp
    target: "127.0.0.1:22"

# apps: []                       # optional app->target indirection
# cluster:
#   enabled: false
#   node_name: "node-1"
#   bind_port: 7946
#   peers: []
#   expected_node_count: 3
`))
	var sb strings.Builder
	_ = tmpl.Execute(&sb, tdata{BinaryName: engine.BinaryName, CfgDir: cfgDir})
	return sb.String()
}

const credsSkeleton = `# Credentials for the monitoring agent.
# Values are referenced in config.yaml as ${VAR_NAME}.
# Recommended: icacls credentials.env /inheritance:r /grant:r "%USERNAME%:R"

# SLACK_WEBHOOK_URL=https://hooks.slack.com/services/...
# DB_PASSWORD=secret
`

// ── Text format helpers ───────────────────────────────────────────────────────

func fleetStatusText(snap engine.FleetSnapshot) string {
	var b strings.Builder

	if snap.Cluster != nil {
		c := snap.Cluster
		quorum := "OK"
		if !c.QuorumHealthy {
			quorum = "LOST"
		}
		isolated := "no"
		if c.Isolated {
			isolated = "YES"
		}
		fmt.Fprintf(&b, "CLUSTER  %d nodes  quorum=%s  isolated=%s  replication=%d\n",
			c.AliveCount, quorum, isolated, c.ReplicationFactor)
		fmt.Fprintf(&b, "MEMBERS  %s\n", strings.Join(c.Members, ", "))
	} else {
		fmt.Fprintln(&b, "CLUSTER  standalone (no cluster configured)")
	}

	s := snap.Summary
	fmt.Fprintf(&b, "TARGETS  %d total  |  %d UP  |  %d SOFT  |  %d DOWN  |  %d UNKNOWN\n\n",
		s.Total, s.Up, s.SoftDown, s.HardDown, s.Unknown)

	if len(snap.Incidents) > 0 {
		fmt.Fprintln(&b, "INCIDENTS")
		for _, inc := range snap.Incidents {
			rc := ""
			if inc.RootCause != "" && inc.RootCause != inc.TargetID {
				rc = "  root:" + inc.RootCause
			}
			fmt.Fprintf(&b, "  %-24s  %-10s  seq=%-4d%s\n",
				inc.TargetName, inc.Scope, inc.Seq, rc)
		}
		fmt.Fprintln(&b)
	}

	if len(snap.Targets) == 0 {
		fmt.Fprintln(&b, "(no targets)")
		return b.String()
	}

	const colFmt = "%-28s  %-9s  %-10s  %-20s  %-5s\n"
	fmt.Fprintf(&b, colFmt, "NAME", "STATE", "SCOPE", "CLASSIFICATION", "CONF")
	fmt.Fprintln(&b, strings.Repeat("-", 80))

	targets := make([]engine.FleetTarget, len(snap.Targets))
	copy(targets, snap.Targets)
	sort.Slice(targets, func(i, j int) bool {
		order := map[string]int{"hard_down": 0, "soft_down": 1, "unknown": 2, "up": 3}
		oi := order[targets[i].ConsensusState]
		oj := order[targets[j].ConsensusState]
		if oi != oj {
			return oi < oj
		}
		return targets[i].Name < targets[j].Name
	})

	for _, t := range targets {
		state := strings.ToUpper(t.ConsensusState)
		scope := t.Scope
		if scope == "" {
			scope = "-"
		}
		cls := t.Classification
		if cls == "" {
			cls = "-"
		}
		conf := "-"
		if t.Confidence > 0 {
			conf = fmt.Sprintf("%.2f", t.Confidence)
		}
		extra := ""
		if t.RootCause != "" && t.RootCause != t.Name {
			extra = "  root:" + t.RootCause
		}
		if len(t.AffectedApps) > 0 {
			extra += "  apps:" + strings.Join(t.AffectedApps, ",")
		}
		fmt.Fprintf(&b, colFmt, t.Name, state, scope, cls, conf)
		if extra != "" {
			fmt.Fprintf(&b, "  └─%s\n", strings.TrimSpace(extra))
		}
	}
	return b.String()
}

func sloText(snap *engine.SLOSnapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Computed at: %s\n\n", snap.ComputedAt.Format(time.RFC3339))

	if len(snap.Targets) == 0 {
		fmt.Fprintln(&b, "(no SLO targets configured)")
		return b.String()
	}

	const colFmt = "%-24s  %-6s  %-8s  %-8s  %-10s  %s\n"
	fmt.Fprintf(&b, colFmt, "TARGET", "WINDOW", "TARGET%", "ACTUAL%", "STATUS", "BUDGET REMAINING")
	fmt.Fprintln(&b, strings.Repeat("-", 80))

	ids := make([]string, 0, len(snap.Targets))
	for id := range snap.Targets {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		r := snap.Targets[id]
		status := "OK"
		if r.SLOBreached {
			status = "BREACHED"
		}
		budget := formatBudget(r.RemainingBudgetSec)
		fmt.Fprintf(&b, colFmt,
			r.TargetID,
			r.Window,
			fmt.Sprintf("%.3f%%", r.TargetUptime*100),
			fmt.Sprintf("%.3f%%", r.ActualUptime*100),
			status,
			budget,
		)
	}
	return b.String()
}

func formatBudget(sec int64) string {
	sign := "+"
	if sec < 0 {
		sign = "-"
		sec = -sec
	}
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	if h > 0 {
		return fmt.Sprintf("%s%dh%02dm%02ds", sign, h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%s%dm%02ds", sign, m, s)
	}
	return fmt.Sprintf("%s%ds", sign, s)
}
