package main

import (
	"bufio"
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
	"strings"
	"syscall"
	"text/template"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/saidtaylan/netwatch/internal/engine"
)

func main() {
	// Subcommand routing — if the first argument is a known verb (not a flag),
	// handle it and exit. Everything else starts the monitoring agent.
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		switch os.Args[1] {
		case "init":
			cmdInit(os.Args[2:])
		case "leave":
			cmdLeave(os.Args[2:])
		case "uninstall":
			cmdUninstall(os.Args[2:])
		default:
			fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\nUsage:\n", os.Args[1])
			fmt.Fprintf(os.Stderr, "  %s [--config FILE]          start the monitoring agent\n", engine.BinaryName)
			fmt.Fprintf(os.Stderr, "  %s init [--config-dir DIR]  generate config skeleton + systemd unit\n", engine.BinaryName)
			fmt.Fprintf(os.Stderr, "  %s leave [--port PORT]      tell a running agent to leave the cluster\n", engine.BinaryName)
			fmt.Fprintf(os.Stderr, "  %s uninstall                stop service, remove unit, optionally delete config\n", engine.BinaryName)
			os.Exit(1)
		}
		return
	}

	// ── Agent startup ─────────────────────────────────────────────────────────
	configPath := flag.String("config", "", "path to config.yaml (default: config.yaml next to the binary)")
	flag.Parse()

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
		slog.Warn("could not determine hostname", "err", err)
	}

	e := engine.New(hostname, engine.ShellRunner, *configPath)

	if err := e.Init(); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}

	// leaveCh carries the reason for an HTTP-triggered graceful shutdown.
	leaveCh := make(chan string, 1)

	// Unified shutdown goroutine — handles OS signals and HTTP /cluster/leave.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		var reason string
		select {
		case sig := <-sigCh:
			reason = fmt.Sprintf("signal %v", sig)
		case r := <-leaveCh:
			reason = "HTTP leave"
			if r != "" {
				reason += ": " + r
			}
		}
		slog.Info("shutting down", "reason", reason)
		e.Shutdown()
		os.Exit(0)
	}()

	reg := prometheus.NewRegistry()
	engine.RegisterMetrics(reg)
	if e.ClusterManager() != nil {
		engine.RegisterClusterMetrics(reg)
	}

	mux := http.NewServeMux()

	// /metrics serves the current probe state. Probes run autonomously on their
	// own schedule (interval_sec) and are NOT triggered by Prometheus scrapes.
	// NotifyScrape() is called on each request so the watchdog can detect
	// whether Prometheus has stopped scraping.
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

	// /cluster/state returns membership and per-node target states.
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

	// /cluster/probers returns per-target prober assignments — useful for
	// debugging "why is my node not probing X?" or verifying zone-aware
	// spread. Includes candidate sets, picked probers, and the alerting
	// primary for every target the cluster knows about.
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

	// /fleet/status returns a cluster-wide summary: member list with zones,
	// quorum / isolated flags, and aggregated target counts. Intentionally
	// summary-only — per-target detail lives in /cluster/state and
	// /cluster/probers. DownTargets is capped at FleetDownTargetsCap to
	// keep the payload bounded.
	mux.HandleFunc("/fleet/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mgr := e.ClusterManager()
		if mgr == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "cluster not enabled (set cluster.enabled: true in config)",
			})
			return
		}
		if err := json.NewEncoder(w).Encode(mgr.FleetSummarySnapshot()); err != nil {
			slog.Error("fleet status encode error", "err", err)
		}
	})

	// /cluster/leave triggers a graceful cluster leave + process exit.
	// Accepts an optional "reason" query parameter for the shutdown log.
	mux.HandleFunc("/cluster/leave", func(w http.ResponseWriter, r *http.Request) {
		reason := r.URL.Query().Get("reason")
		slog.Info("cluster leave requested via HTTP", "reason", reason)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "leaving"})
		// Non-blocking send — ignore if a shutdown is already in progress.
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

// cmdInit generates a config skeleton, a credentials file, and a systemd unit.
func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configDir := fs.String("config-dir", filepath.Join("/etc", engine.BinaryName), "directory to write config files")
	_ = fs.Parse(args)

	if err := os.MkdirAll(*configDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating config dir: %v\n", err)
		os.Exit(1)
	}

	configFile := filepath.Join(*configDir, "config.yaml")
	credsFile := filepath.Join(*configDir, "credentials.env")
	unitDir := "/etc/systemd/system"
	unitFile := filepath.Join(unitDir, engine.BinaryName+".service")

	writeIfAbsent(configFile, configSkeleton(*configDir))
	writeIfAbsent(credsFile, credsSkeleton)

	// Write the systemd unit only when the systemd directory exists.
	// On non-systemd systems (macOS, Alpine with OpenRC, etc.) print a hint instead.
	systemdAvailable := false
	if info, err := os.Stat(unitDir); err == nil && info.IsDir() {
		writeIfAbsent(unitFile, systemdUnit(*configDir))
		systemdAvailable = true
	} else {
		fmt.Printf("  (skipped) systemd unit — %s not found on this system\n", unitDir)
		fmt.Printf("  Unit content written to: %s\n", filepath.Join(*configDir, engine.BinaryName+".service"))
		writeIfAbsent(filepath.Join(*configDir, engine.BinaryName+".service"), systemdUnit(*configDir))
	}

	fmt.Println()
	fmt.Printf("Config dir   : %s\n", *configDir)
	fmt.Printf("Config file  : %s\n", configFile)
	fmt.Printf("Credentials  : %s\n", credsFile)
	if systemdAvailable {
		fmt.Printf("Systemd unit : %s\n", unitFile)
	}
	fmt.Println()
	fmt.Printf("Next steps:\n")
	fmt.Printf("  1. Edit %s\n", configFile)
	if systemdAvailable {
		fmt.Printf("  2. sudo systemctl daemon-reload\n")
		fmt.Printf("  3. sudo systemctl enable --now %s\n", engine.BinaryName)
	} else {
		fmt.Printf("  2. Copy the .service file to your init system's unit directory\n")
		fmt.Printf("  3. Enable and start the service\n")
	}
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

// cmdUninstall stops the service, removes the systemd unit, and optionally
// deletes the config directory.
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

	// 1. Graceful leave — best-effort; don't fail if agent isn't running.
	leaveURL := fmt.Sprintf("http://localhost:%s/cluster/leave?reason=uninstall", *port)
	if resp, err := http.Post(leaveURL, "application/json", nil); err == nil { //nolint:noctx
		resp.Body.Close()
		fmt.Println("✓ leave signal sent to running agent")
	} else {
		fmt.Printf("  agent not reachable (%v) — continuing\n", err)
	}

	// 2. systemd stop + disable + unit removal.
	unitFile := filepath.Join("/etc/systemd/system", engine.BinaryName+".service")
	systemctlRun("stop", engine.BinaryName)
	systemctlRun("disable", engine.BinaryName)
	if err := os.Remove(unitFile); err == nil {
		fmt.Printf("✓ removed %s\n", unitFile)
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "  warning: remove unit file: %v\n", err)
	}
	systemctlRun("daemon-reload")

	// 3. Optionally delete config directory.
	cfgDir := filepath.Join("/etc", engine.BinaryName)
	fmt.Printf("\nDelete config directory %s? [y/N]: ", cfgDir)
	answer2, _ := reader.ReadString('\n')
	if strings.ToLower(strings.TrimSpace(answer2)) == "y" {
		if err := os.RemoveAll(cfgDir); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: %v\n", err)
		} else {
			fmt.Printf("✓ removed %s\n", cfgDir)
		}
	} else {
		fmt.Printf("  config kept at %s\n", cfgDir)
	}

	fmt.Printf("\n%s uninstalled.\n", engine.BinaryName)
}

// systemctlRun executes a systemctl command and prints result.
func systemctlRun(subcmdArgs ...string) {
	cmd := exec.Command("systemctl", subcmdArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "  systemctl %s: %v\n", strings.Join(subcmdArgs, " "), err)
		if len(out) > 0 {
			fmt.Fprintf(os.Stderr, "  %s\n", strings.TrimSpace(string(out)))
		}
	} else {
		fmt.Printf("✓ systemctl %s\n", strings.Join(subcmdArgs, " "))
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

// ── Config / unit templates ───────────────────────────────────────────────────

func configSkeleton(cfgDir string) string {
	type tdata struct{ BinaryName, CfgDir string }
	tmpl := template.Must(template.New("cfg").Parse(
		`# {{.BinaryName}} configuration
# Full reference: https://github.com/saidtaylan/netwatch

app_name: "{{.BinaryName}}-agent"
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

# apps: []                       # optional app→target indirection
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
# Recommended file permissions: chmod 600 credentials.env

# SLACK_WEBHOOK_URL=https://hooks.slack.com/services/...
# DB_PASSWORD=secret
`

func systemdUnit(cfgDir string) string {
	type tdata struct{ BinaryName, CfgDir string }
	tmpl := template.Must(template.New("unit").Parse(
		`[Unit]
Description={{.BinaryName}} network monitoring agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/{{.BinaryName}} --config {{.CfgDir}}/config.yaml
Restart=on-failure
RestartSec=5s
# CAP_NET_RAW is required for ICMP ping probes.
# Remove these two lines if you do not use type: ping targets.
AmbientCapabilities=CAP_NET_RAW
CapabilityBoundingSet=CAP_NET_RAW

[Install]
WantedBy=multi-user.target
`))
	var sb strings.Builder
	_ = tmpl.Execute(&sb, tdata{BinaryName: engine.BinaryName, CfgDir: cfgDir})
	return sb.String()
}
