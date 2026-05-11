package engine

// BinaryName is the canonical name of this monitoring agent binary.
//
// It is used for:
//   - Windows service registration (service name and display name)
//   - Linux systemd unit file generation  (netwatch.service)
//   - Startup / shutdown log messages
//   - Generated config-directory paths  (/etc/netwatch/)
//   - ICMP echo payload tag             (netwatch-ping)
//
// To rebrand without touching source, override at link time:
//
//	go build -ldflags "-X github.com/saidtaylan/netwatch/internal/engine.BinaryName=myagent"
var BinaryName = "netwatch"
