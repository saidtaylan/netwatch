package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"github.com/saidtaylan/netwatch/internal/engine"
	"github.com/saidtaylan/netwatch/internal/storage"
	sigs_yaml "sigs.k8s.io/yaml"
)

func main() {
	// Subcommand routing — if the first argument is a known verb (not a flag),
	// handle it and exit. Everything else starts the monitoring agent.
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		switch os.Args[1] {
		case "init":
			cmdInit(os.Args[2:])
		case "join":
			cmdJoin(os.Args[2:])
		case "keyring":
			cmdKeyring(os.Args[2:])
		case "leave":
			cmdLeave(os.Args[2:])
		case "uninstall":
			cmdUninstall(os.Args[2:])
		case "validate":
			cmdValidate(os.Args[2:])
		default:
			fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
			printUsage(os.Stderr)
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
	if e.SLOEnabled() {
		engine.RegisterSLOMetrics(reg)
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

	// /version — build metadata for the UI footer.
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"version":    engine.BinaryName + "-dev",
			"build_time": "",
		})
	})

	// /auth/whoami — token validation for the UI setup/login flow.
	// Returns {"role":"admin"} when the token is valid, {"role":"anonymous"} when
	// no auth is configured, or 401 when the token is wrong.
	mux.HandleFunc("/auth/whoami", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		adminToken := e.AdminToken()
		if adminToken == "" {
			// Auth not configured — everyone is allowed.
			_ = json.NewEncoder(w).Encode(map[string]string{"role": "anonymous"})
			return
		}
		const prefix = "Bearer "
		auth := r.Header.Get("Authorization")
		if len(auth) > len(prefix) && auth[:len(prefix)] == prefix && auth[len(prefix):] == adminToken {
			_ = json.NewEncoder(w).Encode(map[string]string{"role": "admin"})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid token"})
	})

	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(e.Status()); err != nil {
			slog.Error("status encode error", "err", err)
		}
	})

	// /topology returns the target dependency graph (depends_on relationships).
	// Useful for understanding root-cause chains and cascading impact.
	mux.HandleFunc("/topology", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(e.TopologySnapshot()); err != nil {
			slog.Error("topology encode error", "err", err)
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

	// /fleet/status returns the rich engine-level fleet view: per-target
	// consensus state, scope, by-node breakdown, affected apps, root cause,
	// and active incidents. Works in both standalone and cluster mode.
	// ?format=text for a terminal-friendly ASCII table; default is JSON.
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

	// /slo returns SLO metrics for all configured SLO targets: uptime ratio,
	// error budget, incident history, and breach status.
	// Returns 503 when slo.enabled is false.
	// ?format=text for a terminal-friendly ASCII table; default is JSON.
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

	// /slo/targets — B12: CRUD for SLO target definitions.
	//
	//   GET    /slo/targets          → list current SLO targets
	//   PUT    /slo/targets/{id}     → upsert (add or update) an SLO target
	//   DELETE /slo/targets/{id}     → remove an SLO target
	//
	// Changes are held in memory and lost on restart until persistent config
	// write is implemented (tracked in sprint.md B12).
	mux.HandleFunc("/slo/targets", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet || r.Method == "" {
			targets := e.SLOTargets()
			if targets == nil {
				targets = []engine.SLOTarget{}
			}
			_ = json.NewEncoder(w).Encode(targets)
			return
		}
		http.Error(w, `{"error":"use GET /slo/targets or PUT|DELETE /slo/targets/{id}"}`, http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("/slo/targets/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := strings.TrimPrefix(r.URL.Path, "/slo/targets/")
		if id == "" {
			http.Error(w, `{"error":"target id required"}`, http.StatusBadRequest)
			return
		}

		if r.Method == http.MethodDelete {
			if !checkAdminAuth(e, w, r) {
				return
			}
			deleted, err := e.DeleteSLOTarget(id)
			if err != nil {
				if errors.Is(err, storage.ErrSplitBrain) {
					w.Header().Set("Retry-After", "10")
					w.WriteHeader(http.StatusServiceUnavailable)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"error": "cluster lost quorum; writes paused until peers recover",
					})
					return
				}
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			if deleted {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]string{"deleted": id})
			} else {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			}
			return
		}

		if r.Method == http.MethodPut {
			if !checkAdminAuth(e, w, r) {
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
			if err != nil {
				http.Error(w, `{"error":"read body"}`, http.StatusBadRequest)
				return
			}
			var st engine.SLOTarget
			if err := json.Unmarshal(body, &st); err != nil {
				http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
				return
			}
			// Use path id, not body id (body id optional)
			st.ID = id
			if st.TargetUptime <= 0 || st.TargetUptime >= 1 {
				http.Error(w, `{"error":"target_uptime must be between 0 and 1 (exclusive)"}`, http.StatusBadRequest)
				return
			}
			if st.Window == "" {
				st.Window = "30d"
			}
			if err := e.UpsertSLOTarget(st); err != nil {
				if errors.Is(err, storage.ErrSplitBrain) {
					w.Header().Set("Retry-After", "10")
					w.WriteHeader(http.StatusServiceUnavailable)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"error": "cluster lost quorum; writes paused until peers recover",
					})
					return
				}
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(st)
			return
		}

		http.Error(w, `{"error":"use PUT or DELETE"}`, http.StatusMethodNotAllowed)
	})

	// /geo/latency/{targetID} returns the P1.6 per-node latency view for a
	// specific target, including region labels and the anomaly flag.
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

	// /cluster/keyring/rotate supports zero-downtime AES key rotation.
	// Rotation procedure (call on every node):
	//   1. POST {"action":"add","key":"base64key"}  — add new key so all nodes can decrypt
	//   2. POST {"action":"use","key":"base64key"}  — promote key to primary (encrypt with it)
	//   3. POST {"action":"remove","key":"base64key"} — drop the old key
	// GET returns current keyring info (key count + non-sensitive prefixes).
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
			Action string `json:"action"` // add | use | remove
			Key    string `json:"key"`    // base64-encoded AES key
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		raw, err := base64.StdEncoding.DecodeString(body.Key)
		if err != nil {
			// Try URL-safe base64 as well.
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

	// /cluster/leave triggers a graceful cluster leave + process exit.
	// Accepts an optional "reason" query parameter for the shutdown log.
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

	// ── Maintenance window endpoints ──────────────────────────────────────────
	// GET  /cluster/maintenance        — list active windows (no auth)
	// PUT  /cluster/maintenance        — set new window (auth required)
	// DELETE /cluster/maintenance/{id} — cancel window   (auth required)
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
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON: " + err.Error()})
				return
			}
			if len(req.TargetIDs) == 0 {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "target_ids must not be empty"})
				return
			}
			dur, err := time.ParseDuration(req.Duration)
			if err != nil || dur <= 0 {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "duration must be a valid Go duration (e.g. 30m, 2h)"})
				return
			}

			now := time.Now().UTC()
			win := engine.MaintenanceWindow{
				ID:        engine.GenerateWindowID(),
				TargetIDs: req.TargetIDs,
				StartedAt: now,
				ExpiresAt: now.Add(dur),
				Reason:    req.Reason,
				StartedBy: req.StartedBy,
			}
			if err := e.SetMaintenance(win); err != nil {
				// B24: storage layer returns ErrSplitBrain when cluster has
				// lost quorum. Translate to 503 so clients retry.
				if errors.Is(err, storage.ErrSplitBrain) {
					w.Header().Set("Retry-After", "10")
					w.WriteHeader(http.StatusServiceUnavailable)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"error": "cluster lost quorum; writes paused until peers recover",
					})
					return
				}
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}

			// Note: prior to B24 we also called clusterMgr.BroadcastMaintenanceSet
			// here. That is no longer needed — the storage layer broadcasts the
			// upsert automatically through gossip.ChangeBroadcaster. The old
			// MaintenanceBroadcast path remains in the cluster package for
			// backward compatibility with peers still running pre-B24 code, but
			// new writes flow exclusively through storage.

			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         win.ID,
				"expires_at": win.ExpiresAt,
				"targets":    win.TargetIDs,
			})

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "use GET or PUT"})
		}
	})

	// DELETE /cluster/maintenance/{id}
	mux.HandleFunc("/cluster/maintenance/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "use DELETE"})
			return
		}
		if !checkAdminAuth(e, w, r) {
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/cluster/maintenance/")
		if id == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "maintenance ID required"})
			return
		}
		if err := e.CancelMaintenance(id); err != nil {
			// B24: storage layer returns ErrSplitBrain when quorum lost.
			if errors.Is(err, storage.ErrSplitBrain) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "10")
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "cluster lost quorum; writes paused until peers recover",
				})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		// B24: storage layer broadcasts the tombstone automatically through
		// gossip.ChangeBroadcaster. The old cluster.BroadcastMaintenanceCancel
		// call is no longer needed.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"cancelled": true})
	})

	// ── Silences endpoints (B24.5) ────────────────────────────────────────────
	// GET    /cluster/silences      — list active silences (no auth)
	// PUT    /cluster/silences      — create new silence    (auth required)
	// DELETE /cluster/silences/{id} — cancel silence        (auth required)
	mux.HandleFunc("/cluster/silences", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet, "":
			_ = json.NewEncoder(w).Encode(e.Silences())

		case http.MethodPut:
			if !checkAdminAuth(e, w, r) {
				return
			}
			var req struct {
				Matchers  []engine.SilenceMatcher `json:"matchers"`
				Duration  string                  `json:"duration"`
				Comment   string                  `json:"comment,omitempty"`
				CreatedBy string                  `json:"created_by,omitempty"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON: " + err.Error()})
				return
			}
			if len(req.Matchers) == 0 {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "matchers must not be empty"})
				return
			}
			dur, err := time.ParseDuration(req.Duration)
			if err != nil || dur <= 0 {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "duration must be a valid Go duration (e.g. 30m, 2h)"})
				return
			}

			now := time.Now().UTC()
			sil := engine.Silence{
				ID:        engine.GenerateSilenceID(),
				Matchers:  req.Matchers,
				StartedAt: now,
				ExpiresAt: now.Add(dur),
				Comment:   req.Comment,
				CreatedBy: req.CreatedBy,
			}
			if err := e.SetSilence(sil); err != nil {
				if errors.Is(err, storage.ErrSplitBrain) {
					w.Header().Set("Retry-After", "10")
					w.WriteHeader(http.StatusServiceUnavailable)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"error": "cluster lost quorum; writes paused until peers recover",
					})
					return
				}
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         sil.ID,
				"expires_at": sil.ExpiresAt,
				"matchers":   sil.Matchers,
			})

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "use GET or PUT"})
		}
	})

	// DELETE /cluster/silences/{id}
	mux.HandleFunc("/cluster/silences/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "use DELETE"})
			return
		}
		if !checkAdminAuth(e, w, r) {
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/cluster/silences/")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "silence ID required"})
			return
		}
		if err := e.CancelSilence(id); err != nil {
			if errors.Is(err, storage.ErrSplitBrain) {
				w.Header().Set("Retry-After", "10")
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "cluster lost quorum; writes paused until peers recover",
				})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"cancelled": true})
	})

	// GET  /cluster/config — P1.5 config-sync snapshot (hash + per-peer drift).
	// PUT  /cluster/config — distribute a partial SharedConfig to all nodes.
	// PUT body: JSON or YAML (Content-Type: application/json or application/x-yaml).
	// Only shared fields are applied; node-specific fields are always ignored.
	// PUT requires admin.token auth if configured.
	mux.HandleFunc("/cluster/config", func(w http.ResponseWriter, r *http.Request) {
		mgr := e.ClusterManager()
		// GET: drift snapshot (open for read).
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
		if r.Method != http.MethodPut {
			http.Error(w, `{"error":"use GET or PUT"}`, http.StatusMethodNotAllowed)
			return
		}
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

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB max
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "read body: " + err.Error()})
			return
		}

		// Accept JSON or YAML depending on Content-Type.
		scJSON, err := parseSharedConfigBody(body, r.Header.Get("Content-Type"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Apply to self.
		applyErr := e.ApplySharedConfigJSON(scJSON)
		appliedLocally := applyErr == nil
		if applyErr != nil {
			slog.Warn("config push: local apply failed", "err", applyErr)
		}

		// Broadcast to all peers.
		peerResults := mgr.BroadcastConfigPush(scJSON)
		broadcastTo := make([]string, 0, len(peerResults))
		failedNodes := make(map[string]string)
		for node, err := range peerResults {
			broadcastTo = append(broadcastTo, node)
			if err != nil {
				failedNodes[node] = err.Error()
			}
		}

		// Parse SharedConfig for fields_applied report.
		var sc engine.SharedConfig
		_ = json.Unmarshal(scJSON, &sc)

		_ = json.NewEncoder(w).Encode(engine.ConfigPushResult{
			AppliedLocally: appliedLocally,
			BroadcastTo:    broadcastTo,
			FailedNodes:    failedNodes,
			FieldsApplied:  engine.AppliedFields(sc),
			PushedAt:       time.Now(),
		})
	})

	// POST /cluster/config/sync — take this node's shared fields and push to peers.
	// No body required. Requires admin.token auth if configured.
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
		for node, err := range peerResults {
			broadcastTo = append(broadcastTo, node)
			if err != nil {
				failedNodes[node] = err.Error()
			}
		}

		_ = json.NewEncoder(w).Encode(engine.ConfigPushResult{
			AppliedLocally: false, // sync pushes to peers only, self is already up-to-date
			BroadcastTo:    broadcastTo,
			FailedNodes:    failedNodes,
			FieldsApplied:  engine.AppliedFields(sc),
			PushedAt:       time.Now(),
		})
	})

	port := e.Port()
	slog.Info(engine.BinaryName+" listening", "port", port, "alias", e.NodeAlias(), "host", hostname)

	// Cluster startup banner — printed once, after memberlist has chosen its
	// advertise address. Helps operators copy-paste the join command for new nodes.
	if mgr := e.ClusterManager(); mgr != nil {
		printJoinBanner(e)
	}

	// CORS middleware — wraps the entire mux so the Nuxt UI can call this backend
	// from a different origin (e.g. http://localhost:3000 during development or a
	// separate subdomain in production).
	//
	// The allowed origin is read from cfg.CORSOrigin; when empty, "*" is used
	// (permissive default — suitable for internal/trusted deployments). For
	// production use, set cors_origin: "https://netwatch.yourcompany.local" in
	// config.yaml.
	corsOrigin := e.CORSOrigin()
	if corsOrigin == "" {
		corsOrigin = "*"
	}
	withCORS := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", corsOrigin)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		mux.ServeHTTP(w, r)
	})

	if err := http.ListenAndServe(":"+port, withCORS); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}

// ── Subcommands ───────────────────────────────────────────────────────────────

// cmdInit generates a config skeleton, a credentials file, and a systemd unit.
//
// Flags:
//
//	--config-dir DIR  destination directory (default /etc/netwatch)
//	--cluster         enable cluster mode in the generated config, auto-generate
//	                  a random AES-256 keyring, and print a `netwatch join`
//	                  command that other nodes can copy-paste to join.
//	--bind-port N     gossip port for cluster mode (default 7946)
//
// If config.yaml already exists at the destination, an interactive prompt
// asks whether to overwrite. The default is "no" — pass --force to skip the
// prompt and overwrite unconditionally.
func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configDir := fs.String("config-dir", filepath.Join("/etc", engine.BinaryName), "directory to write config files")
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
	unitDir := "/etc/systemd/system"
	unitFile := filepath.Join(unitDir, engine.BinaryName+".service")

	// Overwrite confirmation for the main config file.
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

	// Write config (always overwrites here; we already prompted above).
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

	// Write the systemd unit only when the systemd directory exists.
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
	fmt.Printf("  1. Edit %s (add targets, notifications, etc.)\n", configFile)
	if systemdAvailable {
		fmt.Printf("  2. sudo systemctl daemon-reload\n")
		fmt.Printf("  3. sudo systemctl enable --now %s\n", engine.BinaryName)
	} else {
		fmt.Printf("  2. Copy the .service file to your init system's unit directory\n")
		fmt.Printf("  3. Enable and start the service\n")
	}

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
		fmt.Printf("    %s join \\\n", engine.BinaryName)
		fmt.Printf("      --keyring %s \\\n", keyringKey)
		fmt.Printf("      --addr %s\n", joinAddr)
		fmt.Println("─────────────────────────────────────────────────────────")
	}
}

// defaultAdvertiseAddr returns a reasonable advertise IP for the join command
// printed by `init --cluster`. Prefers a non-loopback IPv4 address.
func defaultAdvertiseAddr() string {
	addrs, err := netInterfaceAddrs()
	if err != nil {
		return "<your-ip>"
	}
	for _, a := range addrs {
		if a != "" && !strings.HasPrefix(a, "127.") && !strings.HasPrefix(a, "169.254.") {
			return a
		}
	}
	return "<your-ip>"
}

// promptYesNo asks the user a yes/no question. Returns true on "y"/"yes",
// false on anything else (including empty input). The default is appended
// to the prompt in [Y/n] / [y/N] form.
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

node_alias: "{{.BinaryName}}-agent"
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

// ── Text format helpers ────────────────────────────────────────────────────────

// fleetStatusText renders a FleetSnapshot as a human-readable ASCII table.
// Accessible via GET /fleet/status?format=text.
func fleetStatusText(snap engine.FleetSnapshot) string {
	var b strings.Builder

	// ── Cluster header ──
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

	// ── Summary ──
	s := snap.Summary
	fmt.Fprintf(&b, "TARGETS  %d total  |  %d UP  |  %d SOFT  |  %d DOWN  |  %d UNKNOWN\n\n",
		s.Total, s.Up, s.SoftDown, s.HardDown, s.Unknown)

	// ── Active incidents ──
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

	// ── Per-target table ──
	if len(snap.Targets) == 0 {
		fmt.Fprintln(&b, "(no targets)")
		return b.String()
	}

	const colFmt = "%-28s  %-9s  %-10s  %-20s  %-5s\n"
	fmt.Fprintf(&b, colFmt, "NAME", "STATE", "SCOPE", "CLASSIFICATION", "CONF")
	fmt.Fprintln(&b, strings.Repeat("-", 80))

	// Sort by state (DOWN first) then name.
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

// sloText renders an SLOSnapshot as a human-readable ASCII table.
// Accessible via GET /slo?format=text.
func sloText(snap *engine.SLOSnapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Computed at: %s\n\n", snap.ComputedAt.Format(time.RFC3339))

	if len(snap.Targets) == 0 {
		fmt.Fprintln(&b, "(no SLO targets configured)")
		return b.String()
	}

	const colFmt = "%-24s  %-6s  %-8s  %-8s  %-10s  %s\n"
	fmt.Fprintf(&b, colFmt, "TARGET", "WINDOW", "TARGET", "ACTUAL", "STATUS", "BUDGET REMAINING")
	fmt.Fprintln(&b, strings.Repeat("-", 80))

	// Sort alphabetically.
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

// formatBudget converts a signed second count to a readable string like "+1h05m30s".
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

// ── Admin auth ────────────────────────────────────────────────────────────────

// checkAdminAuth validates the Authorization: Bearer <token> header against
// the configured admin.token. Returns true when the request is authorised.
// When admin.token is empty, all requests are allowed (no auth required).
//
// Future extension: replace this function with a middleware chain that checks
// a user/role map once user management is added to AdminConfig.
func checkAdminAuth(e *engine.Engine, w http.ResponseWriter, r *http.Request) bool {
	token := e.AdminToken()
	if token == "" {
		return true // no token configured → unrestricted
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
	if auth[len(prefix):] != token {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid token"})
		return false
	}
	return true
}

// parseSharedConfigBody parses a JSON or YAML body into a json.RawMessage
// that represents an engine.SharedConfig. Content-Type determines the format:
// "application/x-yaml" or "text/yaml" → YAML; everything else → JSON.
func parseSharedConfigBody(body []byte, contentType string) (json.RawMessage, error) {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if ct != "" {
		// Strip charset / params: "application/json; charset=utf-8" → "application/json"
		if idx := strings.IndexByte(ct, ';'); idx >= 0 {
			ct = strings.TrimSpace(ct[:idx])
		}
	}
	switch ct {
	case "application/x-yaml", "text/yaml", "application/yaml":
		// Convert YAML → JSON so we have a canonical representation.
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
		// Validate that the body is valid JSON.
		if !json.Valid(body) {
			return nil, fmt.Errorf("invalid JSON body")
		}
		return json.RawMessage(body), nil
	}
}

// ── join / keyring subcommands ────────────────────────────────────────────────

// cmdJoin writes a cluster-enabled config to disk so this node joins the
// cluster identified by the given keyring + seed address. Flags:
//
//	--keyring K       base64 AES key shared by the cluster (required)
//	--addr  H:P       any existing peer's bind address    (required)
//	--config PATH     destination config.yaml             (default: auto-detect)
//	--bind-port N     this node's gossip port             (default 7946)
//	--node-name NAME  cluster identity for this node      (default: hostname)
//
// Behaviour:
//   - If the config file does not exist, a minimal skeleton is created.
//   - If it exists, only the cluster.* fields are overwritten — targets,
//     notifications, slo, etc. are preserved.
//   - The agent is NOT started here. If one is already running, its hot-reload
//     loop picks up the new config within reload_interval_sec (default 30 s).
func cmdJoin(args []string) {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	keyring := fs.String("keyring", "", "base64 AES key (required)")
	addr := fs.String("addr", "", "any peer's bind address as host:port (required)")
	cfgPath := fs.String("config", "", "destination config.yaml (default: auto-detect)")
	bindPort := fs.Int("bind-port", 7946, "this node's gossip bind port")
	nodeName := fs.String("node-name", "", "cluster identity for this node (default: hostname)")
	_ = fs.Parse(args)

	// Validate inputs.
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

	// Resolve config path.
	path := *cfgPath
	if path == "" {
		path = filepath.Join("/etc", engine.BinaryName, "config.yaml")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating config dir: %v\n", err)
		os.Exit(1)
	}

	// Resolve node name.
	name := *nodeName
	if name == "" {
		h, _ := os.Hostname()
		if h == "" {
			h = engine.BinaryName + "-node"
		}
		name = h
	}

	// Load existing config if present (preserve targets, notifications, etc.).
	var m map[string]interface{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = sigs_yaml.Unmarshal(raw, &m)
	}
	if m == nil {
		// Fresh skeleton. Note: credentials_file deliberately omitted —
		// adding it would require the operator to create the file before the
		// agent could start. Operators can add `credentials_file:` to the
		// config later if they need ${VAR} substitution.
		m = map[string]interface{}{
			"port":              "10240",
			"state_file":        filepath.Join(filepath.Dir(path), "state.json"),
			"log_path":          filepath.Join(filepath.Dir(path), "agent.log"),
			"timeout":           5,
			"max_retries":       2,
			"retry_interval_sec": 30,
			"probe_interval_sec": 60,
			"ticker_interval_sec": 5,
			"reload_interval_sec": 30,
			"notifications":      map[string]interface{}{},
			"default_notify":     []string{},
			"targets":            []interface{}{},
		}
	}

	// Overwrite cluster section.
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
	fmt.Printf("  → hot-reload picks this up within reload_interval_sec (~30s)\n")
	fmt.Println()
	fmt.Println("Otherwise start it:")
	fmt.Printf("  sudo systemctl start %s\n", engine.BinaryName)
	fmt.Printf("    (or: %s --config %s)\n", engine.BinaryName, path)
}

// cmdKeyring dispatches `keyring` subcommands. Currently only `generate`.
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

// validKeyringKey checks that s is base64-encoded and decodes to 16/24/32 bytes.
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

// netInterfaceAddrs returns non-loopback IPv4 addresses on this host.
func netInterfaceAddrs() ([]string, error) {
	ifaces, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range ifaces {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		if ip4 := ipnet.IP.To4(); ip4 != nil {
			out = append(out, ip4.String())
		}
	}
	return out, nil
}

// printUsage prints the CLI help summary.
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
	fmt.Fprintf(w, "  %s leave [--port PORT]              tell a running agent to leave the cluster\n", bn)
	fmt.Fprintf(w, "  %s uninstall                        stop service, remove unit, optionally delete config\n", bn)
}

// printJoinBanner writes the operator-facing cluster banner to stdout.
// Called once at agent startup when cluster mode is enabled.
func printJoinBanner(e *engine.Engine) {
	addr := e.LocalClusterAddr()
	key := e.ClusterPrimaryKey()
	if addr == "" || key == "" {
		// Cluster manager exists but advertise address / keyring unavailable
		// (e.g. encryption disabled). Skip the banner — the join command would
		// be incomplete or misleading.
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
	fmt.Printf("    %s join \\\n", engine.BinaryName)
	fmt.Printf("      --keyring %s \\\n", key)
	fmt.Printf("      --addr %s\n", addr)
	fmt.Println()
	fmt.Println("=========================================================")
}

// clusterConfigSkeleton returns a config.yaml with cluster.enabled=true and
// a pre-generated keyring. Used by `init --cluster`.
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
