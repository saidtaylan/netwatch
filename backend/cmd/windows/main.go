package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	"github.com/saidtaylan/netwatch/internal/cluster"
	"github.com/saidtaylan/netwatch/internal/engine"
	sigs_yaml "sigs.k8s.io/yaml"
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
		case "init":
			cmdInit(os.Args[2:])
		case "join":
			cmdJoin(os.Args[2:])
		case "keyring":
			cmdKeyring(os.Args[2:])
		case "leave":
			cmdLeave(os.Args[2:])
		case "validate":
			cmdValidate(os.Args[2:])
		case "uninstall":
			cmdUninstall(os.Args[2:])
		default:
			fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
			printUsage(os.Stderr)
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

	// ── Maintenance window endpoints (Windows) ──────────────────────────────────
	mux.HandleFunc("/cluster/maintenance", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet, "":
			_ = json.NewEncoder(w).Encode(e.MaintenanceWindows())
		case http.MethodPut:
			if !checkAdminAuth(e, w, r) {
				return
			}
			var req struct {
				TargetIDs []string `json:"target_ids"`
				Duration  string   `json:"duration"`
				Reason    string   `json:"reason,omitempty"`
				StartedBy string   `json:"started_by,omitempty"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			dur, err := time.ParseDuration(req.Duration)
			if err != nil || dur <= 0 || len(req.TargetIDs) == 0 {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid duration or empty target_ids"})
				return
			}
			now := time.Now().UTC()
			win := engine.MaintenanceWindow{
				ID: engine.GenerateWindowID(), TargetIDs: req.TargetIDs,
				StartedAt: now, ExpiresAt: now.Add(dur),
				Reason: req.Reason, StartedBy: req.StartedBy,
			}
			if err := e.SetMaintenance(win); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			if mgr := e.ClusterManager(); mgr != nil {
				mgr.BroadcastMaintenanceSet(cluster.MaintenanceWindowPayload{
					ID: win.ID, TargetIDs: win.TargetIDs,
					StartedAt: win.StartedAt, ExpiresAt: win.ExpiresAt,
					Reason: win.Reason, StartedBy: win.StartedBy,
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": win.ID, "expires_at": win.ExpiresAt, "targets": win.TargetIDs,
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/cluster/maintenance/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !checkAdminAuth(e, w, r) {
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/cluster/maintenance/")
		if err := e.CancelMaintenance(id); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if mgr := e.ClusterManager(); mgr != nil {
			mgr.BroadcastMaintenanceCancel(id)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"cancelled": true})
	})

	// GET /cluster/config — config-sync drift snapshot (P1.5)
	// PUT /cluster/config — push shared fields to all nodes
	mux.HandleFunc("/cluster/config", func(w http.ResponseWriter, r *http.Request) {
		mgr := e.ClusterManager()
		if r.Method == http.MethodGet || r.Method == "" {
			if mgr == nil {
				http.Error(w, `{"error":"cluster disabled"}`, http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(mgr.ConfigSyncSnapshot()); err != nil {
				slog.Error("cluster/config encode error", "err", err)
			}
			return
		}
		if r.Method == http.MethodPut {
			if !checkAdminAuth(e, w, r) {
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if mgr == nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "cluster mode disabled — config push requires cluster.enabled=true",
				})
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "read body: " + err.Error()})
				return
			}
			scJSON, err := parseSharedConfigBody(body, r.Header.Get("Content-Type"))
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			applyErr := e.ApplySharedConfigJSON(scJSON)
			appliedLocally := applyErr == nil
			peerResults := mgr.BroadcastConfigPush(scJSON)
			broadcastTo := make([]string, 0, len(peerResults))
			failedNodes := make(map[string]string)
			for node, sendErr := range peerResults {
				broadcastTo = append(broadcastTo, node)
				if sendErr != nil {
					failedNodes[node] = sendErr.Error()
				}
			}
			var sc engine.SharedConfig
			_ = json.Unmarshal(scJSON, &sc)
			_ = json.NewEncoder(w).Encode(engine.ConfigPushResult{
				AppliedLocally: appliedLocally,
				BroadcastTo:    broadcastTo,
				FailedNodes:    failedNodes,
				FieldsApplied:  engine.AppliedFields(sc),
				PushedAt:       time.Now(),
			})
			return
		}
		http.Error(w, `{"error":"use GET or PUT"}`, http.StatusMethodNotAllowed)
	})

	// POST /cluster/config/sync — take this node's shared fields, push to all peers.
	mux.HandleFunc("/cluster/config/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"use POST"}`, http.StatusMethodNotAllowed)
			return
		}
		if !checkAdminAuth(e, w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		mgr := e.ClusterManager()
		if mgr == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "cluster mode disabled — config sync requires cluster.enabled=true",
			})
			return
		}
		sc, err := e.ExtractSharedConfig()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		scJSON, err := json.Marshal(sc)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		peerResults := mgr.BroadcastConfigPush(scJSON)
		broadcastTo := make([]string, 0, len(peerResults))
		failedNodes := make(map[string]string)
		for node, sendErr := range peerResults {
			broadcastTo = append(broadcastTo, node)
			if sendErr != nil {
				failedNodes[node] = sendErr.Error()
			}
		}
		_ = json.NewEncoder(w).Encode(engine.ConfigPushResult{
			AppliedLocally: false,
			BroadcastTo:    broadcastTo,
			FailedNodes:    failedNodes,
			FieldsApplied:  engine.AppliedFields(sc),
			PushedAt:       time.Now(),
		})
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
		if !checkAdminAuth(e, w, r) {
			return
		}
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
	slog.Info(engine.BinaryName+" listening", "port", port, "alias", e.NodeAlias(), "host", hostname)

	if mgr := e.ClusterManager(); mgr != nil {
		printJoinBanner(e)
	}

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
	fmt.Printf("  node_alias : %s\n", cfg.NodeAlias)
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

node_alias: "{{.BinaryName}}-agent"
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

// checkAdminAuth validates the Authorization: Bearer <token> header.
// Authentication flow (B28): JWT verification first, then raw setup_token fallback.
func checkAdminAuth(e *engine.Engine, w http.ResponseWriter, r *http.Request) bool {
	setupToken := e.SetupToken()
	if setupToken == "" {
		return true
	}
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix {
		w.Header().Set("WWW-Authenticate", `Bearer realm="netwatch-admin"`)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Authorization: Bearer <token> header required",
		})
		return false
	}
	bearerToken := auth[len(prefix):]

	// Try JWT verification first
	claims, err := engine.VerifyJWT(bearerToken, setupToken)
	if err == nil {
		if claims.Role != "admin" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "admin role required"})
			return false
		}
		return true
	}

	// Fallback: raw setup_token match
	if bearerToken == setupToken {
		return true
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid or expired token"})
	return false
}

// parseSharedConfigBody converts a JSON or YAML body to a json.RawMessage.
func parseSharedConfigBody(body []byte, contentType string) (json.RawMessage, error) {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}
	switch ct {
	case "application/x-yaml", "text/yaml", "application/yaml":
		var m interface{}
		if err := sigs_yaml.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("YAML parse: %w", err)
		}
		out, err := json.Marshal(m)
		if err != nil {
			return nil, fmt.Errorf("YAML→JSON: %w", err)
		}
		return json.RawMessage(out), nil
	default:
		if !json.Valid(body) {
			return nil, fmt.Errorf("invalid JSON body")
		}
		return json.RawMessage(body), nil
	}
}

// ── init / join / keyring subcommands (Windows) ──────────────────────────────

// cmdInit generates a config skeleton at the chosen directory. With --cluster
// it enables cluster mode, generates a fresh keyring, and prints a copy-paste
// `netwatch join` command for other nodes.
func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	defaultDir := filepath.Join(os.Getenv("PROGRAMDATA"), engine.BinaryName)
	if defaultDir == engine.BinaryName {
		defaultDir = filepath.Join("C:\\ProgramData", engine.BinaryName)
	}
	configDir := fs.String("config-dir", defaultDir, "directory to write config files")
	cluster := fs.Bool("cluster", false, "generate a cluster-enabled config with a random keyring + join command")
	bindPort := fs.Int("bind-port", 7946, "gossip bind port when --cluster is set")
	force := fs.Bool("force", false, "overwrite an existing config without prompting")
	_ = fs.Parse(args)

	if err := os.MkdirAll(*configDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating config dir: %v\n", err)
		os.Exit(1)
	}

	configFile := filepath.Join(*configDir, "config.yaml")
	credsFile := filepath.Join(*configDir, "credentials.env")

	if _, err := os.Stat(configFile); err == nil && !*force {
		fmt.Printf("Config already exists at %s\n", configFile)
		if !promptYesNo("Overwrite?", false) {
			fmt.Println("Aborted. Re-run with --force to overwrite without prompting.")
			os.Exit(1)
		}
	}

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = engine.BinaryName + "-node"
	}

	var keyringKey string
	if *cluster {
		k, err := engine.GenerateKeyringKey()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error generating keyring: %v\n", err)
			os.Exit(1)
		}
		keyringKey = k
	}

	cfg := configSkeleton(*configDir)
	if *cluster {
		cfg = clusterConfigSkeleton(*configDir, hostname, *bindPort, keyringKey)
	}
	if err := os.WriteFile(configFile, []byte(cfg), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", configFile, err)
		os.Exit(1)
	}
	fmt.Printf("  (written) %s\n", configFile)

	writeIfAbsent(credsFile, credsSkeleton)

	fmt.Println()
	fmt.Printf("Config dir   : %s\n", *configDir)
	fmt.Printf("Config file  : %s\n", configFile)
	fmt.Printf("Credentials  : %s\n", credsFile)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  1. Edit %s (add targets, notifications, etc.)\n", configFile)
	fmt.Printf("  2. Register as a Windows Service:\n")
	fmt.Printf("     %s service install --config %s\n", engine.BinaryName, configFile)

	if *cluster {
		advertiseAddr := defaultAdvertiseAddr()
		joinAddr := fmt.Sprintf("%s:%d", advertiseAddr, *bindPort)
		fmt.Println()
		fmt.Println("─────────────────────────────────────────────────────────")
		fmt.Println("  Cluster enabled — keep this keyring SECRET")
		fmt.Println()
		fmt.Printf("  Keyring: %s\n", keyringKey)
		fmt.Printf("  Node   : %s\n", hostname)
		fmt.Printf("  Addr   : %s   (auto-detected; override in config if needed)\n", joinAddr)
		fmt.Println()
		fmt.Println("  To add another node, run on it:")
		fmt.Println()
		fmt.Printf("    %s join ^\n", engine.BinaryName)
		fmt.Printf("      --keyring %s ^\n", keyringKey)
		fmt.Printf("      --addr %s\n", joinAddr)
		fmt.Println("─────────────────────────────────────────────────────────")
	}
}

// cmdJoin writes a cluster-enabled config so this node joins an existing cluster.
func cmdJoin(args []string) {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	keyring := fs.String("keyring", "", "base64 AES key (required)")
	addr := fs.String("addr", "", "any peer's bind address as host:port (required)")
	cfgPath := fs.String("config", "", "destination config.yaml (default: %PROGRAMDATA%\\netwatch\\config.yaml)")
	bindPort := fs.Int("bind-port", 7946, "this node's gossip bind port")
	nodeName := fs.String("node-name", "", "cluster identity for this node (default: hostname)")
	_ = fs.Parse(args)

	if *keyring == "" {
		fmt.Fprintln(os.Stderr, "error: --keyring is required")
		os.Exit(1)
	}
	if *addr == "" {
		fmt.Fprintln(os.Stderr, "error: --addr is required (e.g. --addr 192.168.1.10:7946)")
		os.Exit(1)
	}
	if _, _, err := net.SplitHostPort(*addr); err != nil {
		fmt.Fprintf(os.Stderr, "error: --addr is not host:port — %v\n", err)
		os.Exit(1)
	}
	if !validKeyringKey(*keyring) {
		fmt.Fprintln(os.Stderr, "error: --keyring is not a valid base64 AES key (16, 24, or 32 raw bytes)")
		os.Exit(1)
	}

	path := *cfgPath
	if path == "" {
		path = filepath.Join(os.Getenv("PROGRAMDATA"), engine.BinaryName, "config.yaml")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating config dir: %v\n", err)
		os.Exit(1)
	}

	name := *nodeName
	if name == "" {
		h, _ := os.Hostname()
		if h == "" {
			h = engine.BinaryName + "-node"
		}
		name = h
	}

	var m map[string]interface{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = sigs_yaml.Unmarshal(raw, &m)
	}
	if m == nil {
		// credentials_file omitted on purpose — see Linux cmdJoin for rationale.
		m = map[string]interface{}{
			"port":                "10240",
			"state_file":          filepath.Join(filepath.Dir(path), "state.json"),
			"log_path":            filepath.Join(filepath.Dir(path), "agent.log"),
			"timeout":             5,
			"max_retries":         2,
			"retry_interval_sec":  30,
			"probe_interval_sec":  60,
			"ticker_interval_sec": 5,
			"reload_interval_sec": 30,
			"notifications":       map[string]interface{}{},
			"default_notify":      []string{},
			"targets":             []interface{}{},
		}
	}

	m["cluster"] = map[string]interface{}{
		"enabled":   true,
		"node_name": name,
		"bind_port": *bindPort,
		"peers":     []string{*addr},
		"keyring":   []string{*keyring},
	}

	out, err := sigs_yaml.Marshal(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error marshalling config: %v\n", err)
		os.Exit(1)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing temp config: %v\n", err)
		os.Exit(1)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "error renaming config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Config written to %s\n", path)
	fmt.Printf("  cluster.enabled  : true\n")
	fmt.Printf("  cluster.node_name: %s\n", name)
	fmt.Printf("  cluster.bind_port: %d\n", *bindPort)
	fmt.Printf("  cluster.peers    : [%s]\n", *addr)
	fmt.Printf("  cluster.keyring  : %s (%d bytes)\n", maskKeyring(*keyring), keyringRawLen(*keyring))
	fmt.Println()
	fmt.Println("If netwatch is already running:")
	fmt.Println("  → hot-reload picks this up within reload_interval_sec (~30s)")
	fmt.Println()
	fmt.Println("Otherwise start the Windows Service:")
	fmt.Printf("  sc start %s\n", engine.BinaryName)
	fmt.Println("  (or register first via: netwatch service install)")
}

// cmdKeyring dispatches `keyring` subcommands.
func cmdKeyring(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: netwatch keyring <generate>")
		os.Exit(1)
	}
	switch args[0] {
	case "generate":
		k, err := engine.GenerateKeyringKey()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(k)
	default:
		fmt.Fprintf(os.Stderr, "unknown keyring subcommand %q (try: generate)\n", args[0])
		os.Exit(1)
	}
}

// printJoinBanner writes the operator-facing cluster banner to stdout.
func printJoinBanner(e *engine.Engine) {
	addr := e.LocalClusterAddr()
	key := e.ClusterPrimaryKey()
	if addr == "" || key == "" {
		return
	}
	members := e.ClusterMemberCount()
	fmt.Println()
	fmt.Println("=========================================================")
	fmt.Println("  netwatch cluster ready")
	fmt.Println()
	fmt.Printf("  Node     : %s\n", e.ClusterManager().NodeName())
	fmt.Printf("  Address  : %s\n", addr)
	fmt.Printf("  Members  : %d\n", members)
	fmt.Println()
	fmt.Println("  To add another node, run on it:")
	fmt.Println()
	fmt.Printf("    %s join ^\n", engine.BinaryName)
	fmt.Printf("      --keyring %s ^\n", key)
	fmt.Printf("      --addr %s\n", addr)
	fmt.Println()
	fmt.Println("=========================================================")
}

// ── helpers shared by subcommands (Windows) ──────────────────────────────────

func promptYesNo(prompt string, defaultYes bool) bool {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	fmt.Printf("%s %s: ", prompt, suffix)
	rd := bufio.NewReader(os.Stdin)
	line, err := rd.ReadString('\n')
	if err != nil {
		return defaultYes
	}
	s := strings.ToLower(strings.TrimSpace(line))
	if s == "" {
		return defaultYes
	}
	return s == "y" || s == "yes"
}

func validKeyringKey(s string) bool {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(s)
		if err != nil {
			return false
		}
	}
	return len(raw) == 16 || len(raw) == 24 || len(raw) == 32
}

func keyringRawLen(s string) int {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		raw, _ = base64.RawStdEncoding.DecodeString(s)
	}
	return len(raw)
}

func maskKeyring(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return "****" + s[len(s)-6:]
}

func defaultAdvertiseAddr() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "<your-ip>"
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		if ip4 := ipnet.IP.To4(); ip4 != nil {
			if s := ip4.String(); !strings.HasPrefix(s, "169.254.") {
				return s
			}
		}
	}
	return "<your-ip>"
}

func printUsage(w io.Writer) {
	bn := engine.BinaryName
	fmt.Fprintf(w, "Usage:\n")
	fmt.Fprintf(w, "  %s [--config FILE]                  start the monitoring agent\n", bn)
	fmt.Fprintf(w, "  %s init [--cluster] [--config-dir DIR] [--bind-port N] [--force]\n", bn)
	fmt.Fprintf(w, "                                      generate config skeleton (and join command if --cluster)\n")
	fmt.Fprintf(w, "  %s join --keyring K --addr H:P [--config PATH] [--bind-port N] [--node-name N]\n", bn)
	fmt.Fprintf(w, "                                      join an existing cluster\n")
	fmt.Fprintf(w, "  %s keyring generate                 print a fresh AES-256 keyring key (base64)\n", bn)
	fmt.Fprintf(w, "  %s validate [--config FILE]         validate config without starting\n", bn)
	fmt.Fprintf(w, "  %s service install [--config FILE]  register as a Windows Service\n", bn)
	fmt.Fprintf(w, "  %s service remove                   unregister the Windows Service\n", bn)
	fmt.Fprintf(w, "  %s leave [--port PORT]              tell a running agent to leave the cluster\n", bn)
	fmt.Fprintf(w, "  %s uninstall [--port PORT]          stop service, remove it, optionally delete config\n", bn)
}

// ── Cluster config template (Windows) ────────────────────────────────────────

func clusterConfigSkeleton(cfgDir, nodeName string, bindPort int, keyringKey string) string {
	type tdata struct {
		BinaryName, CfgDir, NodeName, KeyringKey string
		BindPort                                 int
	}
	tmpl := template.Must(template.New("clusterCfg").Parse(
		`# {{.BinaryName}} configuration — cluster mode
# Full reference: https://github.com/saidtaylan/netwatch

# Optional human-readable label for this agent.
# node_alias: "{{.NodeName}}"

port: "10240"
state_file: "{{.CfgDir}}/state.json"
log_path:   "{{.CfgDir}}/agent.log"
credentials_file: "{{.CfgDir}}/credentials.env"

timeout:            5
max_retries:        2
retry_interval_sec: 30
probe_interval_sec: 60
ticker_interval_sec: 5
reload_interval_sec: 30

notifications: {}

default_notify: []

targets:
  - name: example-tcp
    type: tcp
    target: "127.0.0.1:22"

cluster:
  enabled: true
  node_name: "{{.NodeName}}"
  bind_port: {{.BindPort}}
  # advertise_addr: ""        # leave empty for memberlist auto-detect
  peers: []                   # other nodes will be added via 'netwatch join'
  keyring:
    - "{{.KeyringKey}}"
  expected_node_count: 1      # bump as more nodes join
  min_quorum_ratio: 0.5
  probe_replication_factor: 3

# admin:
#   token: "${ADMIN_TOKEN}"   # required for write-capable HTTP endpoints
`))
	var sb strings.Builder
	_ = tmpl.Execute(&sb, tdata{
		BinaryName: engine.BinaryName,
		CfgDir:     cfgDir,
		NodeName:   nodeName,
		BindPort:   bindPort,
		KeyringKey: keyringKey,
	})
	return sb.String()
}
