package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
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
		default:
			fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\nUsage:\n", os.Args[1])
			fmt.Fprintf(os.Stderr, "  %s [--config FILE]              start the monitoring agent\n", engine.BinaryName)
			fmt.Fprintf(os.Stderr, "  %s service install [--config F] register as a Windows Service\n", engine.BinaryName)
			fmt.Fprintf(os.Stderr, "  %s service remove               unregister the Windows Service\n", engine.BinaryName)
			fmt.Fprintf(os.Stderr, "  %s leave [--port PORT]          tell a running agent to leave the cluster\n", engine.BinaryName)
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
	runAgent(*configPath)
}

// agentService implements the Windows Service control interface.
type agentService struct{ configPath string }

func (s *agentService) Execute(args []string, changeReq <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		runAgent(s.configPath)
		cancel()
	}()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case req := <-changeReq:
			switch req.Cmd {
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				time.Sleep(2 * time.Second)
				return false, 0
			}
		case <-ctx.Done():
			return false, 0
		}
	}
}

func runAgent(configPath string) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	e := engine.New(hostname, engine.PowerShellRunner, configPath)
	if err := e.Init(); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}

	reg := prometheus.NewRegistry()
	engine.RegisterMetrics(reg)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
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

// ── Windows Service helpers ───────────────────────────────────────────────────

// installService registers the binary as a Windows Service.
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

// removeService unregisters the Windows Service.
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
